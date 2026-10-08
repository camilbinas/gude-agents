package ratelimit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	agent "github.com/camilbinas/gude-agents/agent"
)

// WindowStrategy determines counter expiry behavior.
type WindowStrategy int

const (
	SlidingWindow WindowStrategy = iota
	FixedWindow
)

// OverflowBehavior controls admission when a counter has no capacity.
type OverflowBehavior int

const (
	BlockMode OverflowBehavior = iota
	FailFastMode
)

type rateConfig struct {
	Count         int
	WindowSeconds int
}

func (c *rateConfig) window() time.Duration { return time.Duration(c.WindowSeconds) * time.Second }

// RateLimiter enforces per-key and global provider-attempt limits.
type RateLimiter struct {
	windowStrategy                       WindowStrategy
	overflowBehavior                     OverflowBehavior
	requestRateLimit, tokenRateLimit     *rateConfig
	globalRequestLimit, globalTokenLimit *rateConfig
	maxConcurrent                        int
	maxConcurrentSet                     bool
	tokenEstimator                       TokenEstimator
	tokenReservationDisabled             bool
	outputReservationFallback            int
	store                                Store
	mu                                   sync.Mutex
	semaphores                           map[string]*concurrencySem
}

// RateLimiterOption configures a RateLimiter.
type RateLimiterOption func(*RateLimiter)

func WithFixedWindow() RateLimiterOption {
	return func(r *RateLimiter) { r.windowStrategy = FixedWindow }
}
func WithSlidingWindow() RateLimiterOption {
	return func(r *RateLimiter) { r.windowStrategy = SlidingWindow }
}
func WithFailFast() RateLimiterOption {
	return func(r *RateLimiter) { r.overflowBehavior = FailFastMode }
}
func WithBlock() RateLimiterOption { return func(r *RateLimiter) { r.overflowBehavior = BlockMode } }
func RequestRateLimit(count, windowSeconds int) RateLimiterOption {
	return func(r *RateLimiter) { r.requestRateLimit = &rateConfig{count, windowSeconds} }
}
func TokenRateLimit(count, windowSeconds int) RateLimiterOption {
	return func(r *RateLimiter) { r.tokenRateLimit = &rateConfig{count, windowSeconds} }
}
func RPM(count int) RateLimiterOption { return RequestRateLimit(count, 60) }
func TPM(count int) RateLimiterOption { return TokenRateLimit(count, 60) }
func MaxConcurrent(n int) RateLimiterOption {
	return func(r *RateLimiter) { r.maxConcurrent, r.maxConcurrentSet = n, true }
}
func WithGlobalRequestLimit(count, windowSeconds int) RateLimiterOption {
	return func(r *RateLimiter) { r.globalRequestLimit = &rateConfig{count, windowSeconds} }
}
func WithGlobalTokenLimit(count, windowSeconds int) RateLimiterOption {
	return func(r *RateLimiter) { r.globalTokenLimit = &rateConfig{count, windowSeconds} }
}
func WithGlobalRPM(count int) RateLimiterOption { return WithGlobalRequestLimit(count, 60) }
func WithGlobalTPM(count int) RateLimiterOption { return WithGlobalTokenLimit(count, 60) }

// WithStore uses one lease-only backend for distributed RPM and TPM accounting.
// MaxConcurrent remains process-local.
func WithStore(store Store) RateLimiterOption { return func(r *RateLimiter) { r.store = store } }
func WithTokenEstimator(estimator TokenEstimator) RateLimiterOption {
	return func(r *RateLimiter) {
		if estimator == nil {
			r.tokenEstimator = CharEstimator{}
		} else {
			r.tokenEstimator = estimator
		}
	}
}
func WithoutTokenReservation() RateLimiterOption {
	return func(r *RateLimiter) { r.tokenReservationDisabled, r.tokenEstimator = true, nil }
}

// WithOutputReservationFallback reserves this many output tokens when the
// provider request has no MaxTokens. Zero (the default) means output capacity
// is unknown and only the request estimator is reserved.
func WithOutputReservationFallback(tokens int) RateLimiterOption {
	return func(r *RateLimiter) { r.outputReservationFallback = tokens }
}

// NewRateLimiter constructs a lease-only limiter.
func NewRateLimiter(opts ...RateLimiterOption) (*RateLimiter, error) {
	r := &RateLimiter{windowStrategy: SlidingWindow, overflowBehavior: FailFastMode}
	for _, opt := range opts {
		opt(r)
	}
	for name, c := range map[string]*rateConfig{"request": r.requestRateLimit, "token": r.tokenRateLimit, "global request": r.globalRequestLimit, "global token": r.globalTokenLimit} {
		if c != nil && (c.Count <= 0 || c.WindowSeconds <= 0) {
			return nil, fmt.Errorf("%s rate limit count and window must be > 0", name)
		}
	}
	if r.maxConcurrentSet && r.maxConcurrent <= 0 {
		return nil, fmt.Errorf("MaxConcurrent must be > 0")
	}
	if r.outputReservationFallback < 0 {
		return nil, fmt.Errorf("output reservation fallback must be >= 0")
	}
	if r.requestRateLimit == nil && r.tokenRateLimit == nil && r.globalRequestLimit == nil && r.globalTokenLimit == nil && r.maxConcurrent == 0 {
		return nil, fmt.Errorf("at least one rate limit option must be provided")
	}
	if !r.tokenReservationDisabled && (r.tokenRateLimit != nil || r.globalTokenLimit != nil) && r.tokenEstimator == nil {
		r.tokenEstimator = CharEstimator{}
	}
	if r.store == nil {
		store, err := NewMemoryStore()
		if err != nil {
			return nil, err
		}
		r.store = store
	}
	if r.maxConcurrent > 0 {
		r.semaphores = make(map[string]*concurrencySem)
	}
	return r, nil
}

const (
	storeGlobalKey    = "global"
	storePerKeyScope  = "key:"
	storePollInterval = 20 * time.Millisecond
)

func storeKey(key string) string { return storePerKeyScope + key }

type concurrencySem struct{ slots chan struct{} }

func newConcurrencySem(n int) *concurrencySem { return &concurrencySem{slots: make(chan struct{}, n)} }
func (s *concurrencySem) take(ctx context.Context, behavior OverflowBehavior) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if behavior == FailFastMode {
		select {
		case s.slots <- struct{}{}:
			return nil
		default:
			return ErrRateLimitExceeded
		}
	}
	select {
	case s.slots <- struct{}{}:
		if err := ctx.Err(); err != nil {
			s.free()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *concurrencySem) free() {
	select {
	case <-s.slots:
	default:
	}
}
func (r *RateLimiter) semaphore(key string) *concurrencySem {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.semaphores[key]
	if s == nil {
		s = newConcurrencySem(r.maxConcurrent)
		r.semaphores[key] = s
	}
	return s
}

// Lease is opaque to callers and identifies exactly one admitted attempt.
type Lease struct {
	limiter *RateLimiter
	id      string
	done    sync.Once
	free    func()
}

func (*Lease) RateLimitLease() {}

// Reserve admits exactly one provider attempt and reserves its full known cost.
func (r *RateLimiter) Reserve(ctx context.Context, request agent.RateLimitRequest) (agent.RateLimitLease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	id, err := leaseID()
	if err != nil {
		return nil, err
	}
	lease := &Lease{limiter: r, id: id, free: func() {}}
	if r.maxConcurrent > 0 {
		s := r.semaphore(request.Key)
		if err := s.take(ctx, r.overflowBehavior); err != nil {
			return nil, err
		}
		lease.free = func() { lease.done.Do(s.free) }
	}
	reservation := r.reservation(id, request)
	for {
		ok, err := r.store.Reserve(ctx, reservation)
		if err != nil {
			lease.free()
			return nil, err
		}
		if ok {
			return lease, nil
		}
		if r.overflowBehavior == FailFastMode {
			lease.free()
			return nil, ErrRateLimitExceeded
		}
		if err := wait(ctx, storePollInterval); err != nil {
			lease.free()
			return nil, err
		}
	}
}

// Commit records actual confirmed provider usage and terminally frees the lease.
func (r *RateLimiter) Commit(ctx context.Context, opaque agent.RateLimitLease, usage TokenUsage) error {
	lease, err := r.ownedLease(opaque)
	if err != nil {
		return err
	}
	accounting, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	err = r.store.Commit(accounting, lease.id, max(usage.Total(), 0))
	if err == nil || isTerminal(err) {
		lease.free()
	}
	return err
}

// Release conservatively settles a provider attempt at its reserved estimate.
func (r *RateLimiter) Release(ctx context.Context, opaque agent.RateLimitLease) error {
	lease, err := r.ownedLease(opaque)
	if err != nil {
		return err
	}
	accounting, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	err = r.store.Release(accounting, lease.id)
	if err == nil || isTerminal(err) {
		lease.free()
	}
	return err
}
func (r *RateLimiter) ownedLease(opaque agent.RateLimitLease) (*Lease, error) {
	lease, ok := opaque.(*Lease)
	if !ok || lease.limiter != r {
		return nil, ErrLeaseUnknown
	}
	return lease, nil
}
func isTerminal(err error) bool {
	return err == ErrLeaseTerminal || err == ErrLeaseCrossTerminal || err == ErrLeaseExpired
}
func (r *RateLimiter) reservation(id string, request agent.RateLimitRequest) Reservation {
	estimate := r.outputReservationFallback
	if r.tokenEstimator != nil && !r.tokenReservationDisabled && (r.tokenRateLimit != nil || r.globalTokenLimit != nil) {
		if n, err := r.tokenEstimator.EstimateTokens(context.Background(), request.Request); err == nil && n > 0 {
			estimate += n
		}
	}
	if maxOutput := request.Request.InferenceConfig; maxOutput != nil && maxOutput.MaxTokens != nil && *maxOutput.MaxTokens > 0 {
		estimate += *maxOutput.MaxTokens
	}
	out := Reservation{ID: id}
	if r.requestRateLimit != nil {
		out.Requests = append(out.Requests, RequestCounter{Key: storeKey(request.Key), Limit: r.requestRateLimit.Count, Window: r.requestRateLimit.window(), Strategy: r.windowStrategy})
	}
	if r.globalRequestLimit != nil {
		out.Requests = append(out.Requests, RequestCounter{Key: storeGlobalKey, Limit: r.globalRequestLimit.Count, Window: r.globalRequestLimit.window(), Strategy: r.windowStrategy})
	}
	if r.tokenRateLimit != nil {
		out.Tokens = append(out.Tokens, TokenReservation{Key: storeKey(request.Key), Limit: r.tokenRateLimit.Count, Window: r.tokenRateLimit.window(), Strategy: r.windowStrategy, Amount: estimate})
	}
	if r.globalTokenLimit != nil {
		out.Tokens = append(out.Tokens, TokenReservation{Key: storeGlobalKey, Limit: r.globalTokenLimit.Count, Window: r.globalTokenLimit.window(), Strategy: r.windowStrategy, Amount: estimate})
	}
	return out
}
func leaseID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate rate limit lease ID: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
func wait(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
