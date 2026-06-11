package agent

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// WindowStrategy determines how the rate limiter tracks time windows.
type WindowStrategy int

const (
	// SlidingWindow tracks consumption over a continuously advancing 60-second window.
	SlidingWindow WindowStrategy = iota
	// FixedWindow resets consumption counters at fixed 60-second intervals.
	FixedWindow
)

// OverflowBehavior determines what happens when a limit is exceeded.
type OverflowBehavior int

const (
	// BlockMode waits until the current window resets and capacity becomes available.
	BlockMode OverflowBehavior = iota
	// FailFastMode returns ErrRateLimitExceeded immediately when a limit is exceeded.
	FailFastMode
)

// tokenEvent records a token consumption at a point in time.
type tokenEvent struct {
	at     time.Time
	tokens int
}

// rateBucket holds the counters for a single rate limit bucket.
type rateBucket struct {
	mu sync.Mutex

	rpmLimit int
	tpmLimit int

	windowStrategy   WindowStrategy
	overflowBehavior OverflowBehavior

	// windowDuration is the configurable window size for request limits.
	// When zero, defaults to 60s for backward compatibility.
	windowDuration time.Duration

	// tokenWindowDuration is the configurable window size for token limits.
	// When zero, falls back to windowDuration (and ultimately to 60s).
	tokenWindowDuration time.Duration

	// Sliding window: list of timestamped events
	rpmEvents []time.Time
	tpmEvents []tokenEvent

	// Fixed window: counters with reset time
	fixedWindowStart      time.Time
	fixedRPMCount         int
	fixedTPMCount         int
	fixedTokenWindowStart time.Time // separate fixed window tracking for tokens

	// lastAccess is updated on every acquire/record for TTL eviction.
	lastAccess time.Time

	// Clock abstraction for testing
	now func() time.Time
}

// effectiveWindow returns the configured window duration, defaulting to 60s
// when windowDuration is zero (backward compatibility).
func (b *rateBucket) effectiveWindow() time.Duration {
	if b.windowDuration > 0 {
		return b.windowDuration
	}
	return 60 * time.Second
}

// effectiveTokenWindow returns the configured token window duration.
// Falls back to tokenWindowDuration, then windowDuration, then 60s.
func (b *rateBucket) effectiveTokenWindow() time.Duration {
	if b.tokenWindowDuration > 0 {
		return b.tokenWindowDuration
	}
	return b.effectiveWindow()
}

// maybeResetFixedWindow resets the fixed window counters if the current window
// has expired (windowDuration elapsed since window start).
func (b *rateBucket) maybeResetFixedWindow() {
	now := b.now()
	if b.fixedWindowStart.IsZero() {
		b.fixedWindowStart = now
		return
	}
	if now.Sub(b.fixedWindowStart) >= b.effectiveWindow() {
		b.fixedWindowStart = now
		b.fixedRPMCount = 0
	}
}

// maybeResetFixedTokenWindow resets the fixed token window counter if the
// current token window has expired.
func (b *rateBucket) maybeResetFixedTokenWindow() {
	now := b.now()
	// Use fixedTokenWindowStart if set, otherwise fall back to fixedWindowStart.
	start := b.fixedTokenWindowStart
	if start.IsZero() {
		start = b.fixedWindowStart
	}
	if start.IsZero() {
		b.fixedTokenWindowStart = now
		return
	}
	if now.Sub(start) >= b.effectiveTokenWindow() {
		b.fixedTokenWindowStart = now
		b.fixedTPMCount = 0
	}
}

func (b *rateBucket) fixedRPMCountVal() int {
	b.maybeResetFixedWindow()
	return b.fixedRPMCount
}

func (b *rateBucket) fixedTPMCountVal() int {
	b.maybeResetFixedTokenWindow()
	return b.fixedTPMCount
}

func (b *rateBucket) slidingRPMCount() int {
	cutoff := b.now().Add(-b.effectiveWindow())
	i := sort.Search(len(b.rpmEvents), func(j int) bool {
		return !b.rpmEvents[j].Before(cutoff)
	})
	b.rpmEvents = b.rpmEvents[i:]
	return len(b.rpmEvents)
}

func (b *rateBucket) slidingTPMCount() int {
	cutoff := b.now().Add(-b.effectiveTokenWindow())
	i := sort.Search(len(b.tpmEvents), func(j int) bool {
		return !b.tpmEvents[j].at.Before(cutoff)
	})
	b.tpmEvents = b.tpmEvents[i:]
	total := 0
	for _, e := range b.tpmEvents {
		total += e.tokens
	}
	return total
}

func (b *rateBucket) rpmWaitDuration() time.Duration {
	now := b.now()
	window := b.effectiveWindow()
	switch b.windowStrategy {
	case SlidingWindow:
		if len(b.rpmEvents) > 0 {
			oldest := b.rpmEvents[0]
			return oldest.Add(window).Sub(now)
		}
	case FixedWindow:
		return b.fixedWindowStart.Add(window).Sub(now)
	}
	return time.Second
}

func (b *rateBucket) tpmWaitDuration() time.Duration {
	now := b.now()
	window := b.effectiveTokenWindow()
	switch b.windowStrategy {
	case SlidingWindow:
		if len(b.tpmEvents) > 0 {
			oldest := b.tpmEvents[0]
			return oldest.at.Add(window).Sub(now)
		}
	case FixedWindow:
		start := b.fixedTokenWindowStart
		if start.IsZero() {
			start = b.fixedWindowStart
		}
		return start.Add(window).Sub(now)
	}
	return time.Second
}

func (b *rateBucket) waitForCapacity(ctx context.Context, waitDuration time.Duration) error {
	timer := time.NewTimer(waitDuration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// acquire checks rate limits and blocks or fails fast depending on config.
func (b *rateBucket) acquire(ctx context.Context) error {
	b.mu.Lock()
	for {
		if b.rpmLimit > 0 {
			var count int
			switch b.windowStrategy {
			case SlidingWindow:
				count = b.slidingRPMCount()
			case FixedWindow:
				count = b.fixedRPMCountVal()
			}
			if count >= b.rpmLimit {
				if b.overflowBehavior == FailFastMode {
					b.mu.Unlock()
					return ErrRateLimitExceeded
				}
				waitDuration := b.rpmWaitDuration()
				b.mu.Unlock()
				if err := b.waitForCapacity(ctx, waitDuration); err != nil {
					return err
				}
				b.mu.Lock()
				continue
			}
		}

		if b.tpmLimit > 0 {
			var count int
			switch b.windowStrategy {
			case SlidingWindow:
				count = b.slidingTPMCount()
			case FixedWindow:
				count = b.fixedTPMCountVal()
			}
			if count >= b.tpmLimit {
				if b.overflowBehavior == FailFastMode {
					b.mu.Unlock()
					return ErrRateLimitExceeded
				}
				waitDuration := b.tpmWaitDuration()
				b.mu.Unlock()
				if err := b.waitForCapacity(ctx, waitDuration); err != nil {
					return err
				}
				b.mu.Lock()
				continue
			}
		}

		switch b.windowStrategy {
		case SlidingWindow:
			b.rpmEvents = append(b.rpmEvents, b.now())
		case FixedWindow:
			b.maybeResetFixedWindow()
			b.fixedRPMCount++
		}

		b.lastAccess = b.now()
		b.mu.Unlock()
		return nil
	}
}

// record records token consumption after a successful provider call.
func (b *rateBucket) record(usage TokenUsage) {
	b.mu.Lock()
	defer b.mu.Unlock()

	tokens := usage.Total()
	if tokens <= 0 {
		return
	}

	b.lastAccess = b.now()

	switch b.windowStrategy {
	case SlidingWindow:
		b.tpmEvents = append(b.tpmEvents, tokenEvent{at: b.now(), tokens: tokens})
	case FixedWindow:
		b.maybeResetFixedTokenWindow()
		b.fixedTPMCount += tokens
	}
}

// rateConfig holds a count + window pair for configurable limits.
type rateConfig struct {
	Count         int
	WindowSeconds int
}

// concurrencySem is a per-key semaphore for limiting concurrent in-flight calls.
// It uses sync.Cond for BlockMode waiting with context cancellation support.
type concurrencySem struct {
	mu       sync.Mutex
	cond     *sync.Cond
	inflight int
	max      int
}

// newConcurrencySem creates a concurrencySem with the given maximum concurrency.
func newConcurrencySem(max int) *concurrencySem {
	s := &concurrencySem{max: max}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// Acquire blocks (BlockMode) or fails (FailFastMode) if at capacity.
// In BlockMode, it waits until a slot is released or the context is cancelled.
func (s *concurrencySem) Acquire(ctx context.Context, behavior OverflowBehavior) error {
	s.mu.Lock()

	for s.inflight >= s.max {
		if behavior == FailFastMode {
			s.mu.Unlock()
			return ErrRateLimitExceeded
		}

		// BlockMode: wait with context awareness.
		// Use a goroutine to monitor context cancellation while we wait on the cond.
		// The cond.Wait() atomically unlocks and waits, then re-locks on wake.
		ctxDone := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				// Context cancelled — broadcast to wake all waiters so we can check.
				s.cond.Broadcast()
			case <-ctxDone:
				// Normal wake — context monitor is no longer needed.
			}
		}()

		s.cond.Wait()
		close(ctxDone)

		// Check if we woke because the context was cancelled.
		if ctx.Err() != nil {
			s.mu.Unlock()
			return ctx.Err()
		}
	}
	s.inflight++
	s.mu.Unlock()
	return nil
}

// Release decrements the inflight count and signals one waiter.
func (s *concurrencySem) Release() {
	s.mu.Lock()
	if s.inflight > 0 {
		s.inflight--
	}
	s.cond.Signal()
	s.mu.Unlock()
}

// RateLimiter enforces RPM and TPM limits on provider calls.
// It supports both shared (single-bucket) and per-key (multi-bucket) modes.
//
// In shared mode, all calls compete for the same budget regardless of
// conversation ID. In per-key mode, each conversation ID gets its own
// independent budget. The mode is determined automatically: when a conversation
// ID is present, the limiter uses per-key buckets; when absent, it uses a
// shared default bucket.
//
// It is safe for concurrent use by multiple goroutines and agents.
type RateLimiter struct {
	mu sync.Mutex

	rpmLimit int
	tpmLimit int

	windowStrategy   WindowStrategy
	overflowBehavior OverflowBehavior

	buckets   map[string]*rateBucket
	lastSweep time.Time // last time stale buckets were evicted

	// Clock abstraction for testing.
	now func() time.Time

	// Configurable rate limits (additive constraints).
	requestRateLimit *rateConfig // nil = use legacy rpmLimit
	tokenRateLimit   *rateConfig // nil = use legacy tpmLimit

	// Global limits (shared across all keys).
	globalRequestLimit *rateConfig // nil = no global request limit
	globalTokenLimit   *rateConfig // nil = no global token limit
	globalBucket       *rateBucket // lazily created when global limits configured

	// Concurrency limiting.
	maxConcurrent    int                        // 0 = unlimited
	maxConcurrentSet bool                       // true if MaxConcurrent option was explicitly called
	semaphores       map[string]*concurrencySem // per-key semaphores

	// Pluggable store.
	store RateLimitStore // nil = use in-memory (default)

	// Token estimation for pre-flight checks.
	tokenEstimator TokenEstimator // nil = no pre-flight check
}

// RateLimiterOption configures the RateLimiter.
type RateLimiterOption func(*RateLimiter)

// WithFixedWindow configures the RateLimiter to use fixed 60-second windows.
func WithFixedWindow() RateLimiterOption {
	return func(rl *RateLimiter) {
		rl.windowStrategy = FixedWindow
	}
}

// WithSlidingWindow configures the RateLimiter to use a sliding 60-second window.
// This is the default and is provided for explicitness.
func WithSlidingWindow() RateLimiterOption {
	return func(rl *RateLimiter) {
		rl.windowStrategy = SlidingWindow
	}
}

// WithFailFast configures the RateLimiter to return ErrRateLimitExceeded
// immediately when a limit is exceeded. This is the default.
func WithFailFast() RateLimiterOption {
	return func(rl *RateLimiter) {
		rl.overflowBehavior = FailFastMode
	}
}

// WithBlock configures the RateLimiter to wait until capacity is available.
// Useful for background batch processing where throughput matters more than latency.
func WithBlock() RateLimiterOption {
	return func(rl *RateLimiter) {
		rl.overflowBehavior = BlockMode
	}
}

// RequestRateLimit configures a request rate limit with a custom time window.
// The limiter will enforce at most count requests within windowSeconds seconds per key.
func RequestRateLimit(count, windowSeconds int) RateLimiterOption {
	return func(rl *RateLimiter) {
		rl.requestRateLimit = &rateConfig{Count: count, WindowSeconds: windowSeconds}
	}
}

// TokenRateLimit configures a token rate limit with a custom time window.
// The limiter will enforce at most count tokens within windowSeconds seconds per key.
func TokenRateLimit(count, windowSeconds int) RateLimiterOption {
	return func(rl *RateLimiter) {
		rl.tokenRateLimit = &rateConfig{Count: count, WindowSeconds: windowSeconds}
	}
}

// RPM is an alias for RequestRateLimit(count, 60).
func RPM(count int) RateLimiterOption {
	return RequestRateLimit(count, 60)
}

// TPM is an alias for TokenRateLimit(count, 60).
func TPM(count int) RateLimiterOption {
	return TokenRateLimit(count, 60)
}

// MaxConcurrent limits the number of in-flight calls per key.
// When n calls are in-flight for a key, subsequent Acquire calls will either
// block (BlockMode) or return ErrRateLimitExceeded (FailFastMode).
func MaxConcurrent(n int) RateLimiterOption {
	return func(rl *RateLimiter) {
		rl.maxConcurrent = n
		rl.maxConcurrentSet = true
	}
}

// WithGlobalRequestLimit sets a global (cross-key) request rate limit.
// The limiter will enforce at most count requests within windowSeconds seconds
// across all keys combined.
func WithGlobalRequestLimit(count, windowSeconds int) RateLimiterOption {
	return func(rl *RateLimiter) {
		rl.globalRequestLimit = &rateConfig{Count: count, WindowSeconds: windowSeconds}
	}
}

// WithGlobalTokenLimit sets a global (cross-key) token rate limit.
// The limiter will enforce at most count tokens within windowSeconds seconds
// across all keys combined.
func WithGlobalTokenLimit(count, windowSeconds int) RateLimiterOption {
	return func(rl *RateLimiter) {
		rl.globalTokenLimit = &rateConfig{Count: count, WindowSeconds: windowSeconds}
	}
}

// WithGlobalRPM is an alias for WithGlobalRequestLimit(count, 60).
func WithGlobalRPM(count int) RateLimiterOption {
	return WithGlobalRequestLimit(count, 60)
}

// WithGlobalTPM is an alias for WithGlobalTokenLimit(count, 60).
func WithGlobalTPM(count int) RateLimiterOption {
	return WithGlobalTokenLimit(count, 60)
}

// WithStore configures a pluggable RateLimitStore backend.
// When set, all counter operations are delegated to the provided store
// instead of the default in-memory bucket logic.
func WithStore(store RateLimitStore) RateLimiterOption {
	return func(rl *RateLimiter) {
		rl.store = store
	}
}

// WithTokenEstimator configures a TokenEstimator for pre-flight token budget
// checks. If estimator is nil, CharEstimator{} is used as the default.
func WithTokenEstimator(estimator TokenEstimator) RateLimiterOption {
	return func(rl *RateLimiter) {
		if estimator == nil {
			rl.tokenEstimator = CharEstimator{}
		} else {
			rl.tokenEstimator = estimator
		}
	}
}

// NewRateLimiter creates a RateLimiter configured entirely via functional options.
// At least one rate limit option (RPM, TPM, RequestRateLimit, TokenRateLimit,
// WithGlobalRequestLimit, WithGlobalTokenLimit, MaxConcurrent) must be provided.
// Defaults: SlidingWindow strategy, FailFast overflow behavior.
//
// When used without conversation IDs (or with a single shared agent), all calls
// share one budget. When conversation IDs are present, each ID gets its own
// independent budget with the same limits.
func NewRateLimiter(opts ...RateLimiterOption) (*RateLimiter, error) {
	rl := &RateLimiter{
		rpmLimit:         0,
		tpmLimit:         0,
		windowStrategy:   SlidingWindow,
		overflowBehavior: FailFastMode,
		buckets:          make(map[string]*rateBucket),
		now:              time.Now,
	}

	// Apply options first so we can validate their fields.
	for _, opt := range opts {
		opt(rl)
	}

	// Validate configurable rate limit options.
	if rl.requestRateLimit != nil {
		if rl.requestRateLimit.WindowSeconds <= 0 {
			return nil, fmt.Errorf("RequestRateLimit windowSeconds must be > 0, got %d", rl.requestRateLimit.WindowSeconds)
		}
		if rl.requestRateLimit.Count <= 0 {
			return nil, fmt.Errorf("RequestRateLimit count must be > 0, got %d", rl.requestRateLimit.Count)
		}
	}
	if rl.tokenRateLimit != nil {
		if rl.tokenRateLimit.WindowSeconds <= 0 {
			return nil, fmt.Errorf("TokenRateLimit windowSeconds must be > 0, got %d", rl.tokenRateLimit.WindowSeconds)
		}
		if rl.tokenRateLimit.Count <= 0 {
			return nil, fmt.Errorf("TokenRateLimit count must be > 0, got %d", rl.tokenRateLimit.Count)
		}
	}
	if rl.globalRequestLimit != nil {
		if rl.globalRequestLimit.WindowSeconds <= 0 {
			return nil, fmt.Errorf("WithGlobalRequestLimit windowSeconds must be > 0, got %d", rl.globalRequestLimit.WindowSeconds)
		}
		if rl.globalRequestLimit.Count <= 0 {
			return nil, fmt.Errorf("WithGlobalRequestLimit count must be > 0, got %d", rl.globalRequestLimit.Count)
		}
	}
	if rl.globalTokenLimit != nil {
		if rl.globalTokenLimit.WindowSeconds <= 0 {
			return nil, fmt.Errorf("WithGlobalTokenLimit windowSeconds must be > 0, got %d", rl.globalTokenLimit.WindowSeconds)
		}
		if rl.globalTokenLimit.Count <= 0 {
			return nil, fmt.Errorf("WithGlobalTokenLimit count must be > 0, got %d", rl.globalTokenLimit.Count)
		}
	}
	if rl.maxConcurrentSet && rl.maxConcurrent <= 0 {
		return nil, fmt.Errorf("MaxConcurrent must be > 0, got %d", rl.maxConcurrent)
	}

	// Determine whether at least one limit is configured.
	hasOptionLimit := rl.requestRateLimit != nil || rl.tokenRateLimit != nil ||
		rl.globalRequestLimit != nil || rl.globalTokenLimit != nil ||
		rl.maxConcurrent > 0

	if !hasOptionLimit {
		return nil, fmt.Errorf("at least one rate limit option must be provided")
	}

	// Lazily initialize globalBucket only when global limit options are provided.
	if rl.globalRequestLimit != nil || rl.globalTokenLimit != nil {
		globalRPMLimit := 0
		globalTPMLimit := 0
		var globalWindowDuration time.Duration
		var globalTokenWindowDuration time.Duration

		if rl.globalRequestLimit != nil {
			globalRPMLimit = rl.globalRequestLimit.Count
			globalWindowDuration = time.Duration(rl.globalRequestLimit.WindowSeconds) * time.Second
		}
		if rl.globalTokenLimit != nil {
			globalTPMLimit = rl.globalTokenLimit.Count
			globalTokenWindowDuration = time.Duration(rl.globalTokenLimit.WindowSeconds) * time.Second
		}

		rl.globalBucket = &rateBucket{
			rpmLimit:            globalRPMLimit,
			tpmLimit:            globalTPMLimit,
			windowStrategy:      rl.windowStrategy,
			overflowBehavior:    rl.overflowBehavior,
			windowDuration:      globalWindowDuration,
			tokenWindowDuration: globalTokenWindowDuration,
			lastAccess:          rl.now(),
			now:                 rl.now,
		}
	}

	// Lazily initialize semaphores map only when MaxConcurrent is configured.
	if rl.maxConcurrent > 0 {
		rl.semaphores = make(map[string]*concurrencySem)
	}

	return rl, nil
}

// bucket returns the rateBucket for the given key, creating one if needed.
// Lazily evicts stale buckets (idle > 60s) at most once per 10 seconds.
func (rl *RateLimiter) bucket(key string) *rateBucket {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.now()

	// Lazy sweep: evict stale buckets at most once per 10 seconds.
	if now.Sub(rl.lastSweep) >= 10*time.Second {
		rl.lastSweep = now
		for k, b := range rl.buckets {
			b.mu.Lock()
			idle := now.Sub(b.lastAccess)
			b.mu.Unlock()
			if idle >= 60*time.Second && k != "" {
				delete(rl.buckets, k)
			}
		}
	}

	if b, ok := rl.buckets[key]; ok {
		return b
	}

	// Determine effective RPM/TPM limits and window durations for the new bucket.
	effectiveRPM := rl.rpmLimit
	effectiveTPM := rl.tpmLimit
	var windowDuration time.Duration
	var tokenWindowDuration time.Duration

	if rl.requestRateLimit != nil {
		effectiveRPM = rl.requestRateLimit.Count
		windowDuration = time.Duration(rl.requestRateLimit.WindowSeconds) * time.Second
	}
	if rl.tokenRateLimit != nil {
		effectiveTPM = rl.tokenRateLimit.Count
		tokenWindowDuration = time.Duration(rl.tokenRateLimit.WindowSeconds) * time.Second
	}

	b := &rateBucket{
		rpmLimit:            effectiveRPM,
		tpmLimit:            effectiveTPM,
		windowStrategy:      rl.windowStrategy,
		overflowBehavior:    rl.overflowBehavior,
		windowDuration:      windowDuration,
		tokenWindowDuration: tokenWindowDuration,
		lastAccess:          now,
		now:                 rl.now,
	}
	rl.buckets[key] = b
	return b
}

// ReleaseFunc releases the per-key concurrency slot held by a successful
// Acquire when MaxConcurrent is configured. It is always non-nil on a
// successful Acquire (a no-op when MaxConcurrent is not configured) and is safe
// to call multiple times — only the first call releases the slot.
//
// Callers MUST call the returned ReleaseFunc exactly once after the in-flight
// operation completes, regardless of whether it succeeded or failed. The
// idiomatic pattern is to defer it immediately after a successful Acquire:
//
//	release, err := rl.Acquire(ctx, key)
//	if err != nil {
//	    return err
//	}
//	defer release()
//
// Releasing is independent of Record: Record updates rate counters, while
// ReleaseFunc frees the concurrency slot. This separation ensures the slot is
// freed even when the operation fails before Record is called.
type ReleaseFunc func()

// noopRelease is returned when MaxConcurrent is not configured.
func noopRelease() {}

// Acquire checks rate limits for the given key before a provider call.
// Use an empty string for shared (non-keyed) rate limiting.
// Each distinct key is rate-limited independently.
//
// On success it returns a non-nil ReleaseFunc that the caller MUST invoke once
// the in-flight operation completes (success or failure). When MaxConcurrent is
// configured, the ReleaseFunc frees the concurrency slot; otherwise it is a
// no-op. On error the returned ReleaseFunc is nil.
//
// When global limits are configured, Acquire also checks the global bucket
// after per-key checks pass. Both per-key AND global limits must have capacity.
//
// When MaxConcurrent is configured, Acquire also acquires a concurrency slot
// after all rate limit checks pass. The semaphore acquire is performed outside the
// main mutex to avoid deadlock when blocking.
//
// When a RateLimitStore is configured (via WithStore), all counter operations are
// delegated to the store instead of the in-memory bucket map.
func (rl *RateLimiter) Acquire(ctx context.Context, key string) (ReleaseFunc, error) {
	// When a store is configured, delegate all counter operations to it.
	if rl.store != nil {
		return rl.acquireWithStore(ctx, key)
	}

	// Step 1: Check per-key rate limits (RPM/TPM).
	if err := rl.bucket(key).acquire(ctx); err != nil {
		return nil, err
	}

	// Step 2: Check global rate limits (if configured).
	// Both per-key AND global limits must have capacity for Acquire to succeed.
	// The globalBucket.acquire() handles both RPM and TPM checks, respects
	// OverflowBehavior (FailFast/Block), and increments the global request
	// counter on success.
	if rl.globalBucket != nil {
		if err := rl.globalBucket.acquire(ctx); err != nil {
			return nil, err
		}
	}

	// Step 3: If concurrency limiting is not configured, we're done.
	if rl.maxConcurrent <= 0 {
		return noopRelease, nil
	}

	// Step 4: Get or lazily create the per-key semaphore (under lock).
	rl.mu.Lock()
	sem, ok := rl.semaphores[key]
	if !ok {
		sem = newConcurrencySem(rl.maxConcurrent)
		rl.semaphores[key] = sem
	}
	rl.mu.Unlock()

	// Step 5: Acquire semaphore slot OUTSIDE the main lock to avoid deadlock.
	if err := sem.Acquire(ctx, rl.overflowBehavior); err != nil {
		return nil, err
	}

	// Return an idempotent release that frees exactly one slot.
	var once sync.Once
	return func() { once.Do(sem.Release) }, nil
}

// acquireWithStore implements the Acquire logic using a RateLimitStore for counter
// operations. It checks per-key request limits, per-key token limits, global request
// limits, global token limits, then acquires a concurrency slot if configured.
func (rl *RateLimiter) acquireWithStore(ctx context.Context, key string) (ReleaseFunc, error) {
	// Determine per-key request limit and window.
	reqLimit := rl.rpmLimit
	reqWindow := 60 * time.Second
	if rl.requestRateLimit != nil {
		reqLimit = rl.requestRateLimit.Count
		reqWindow = time.Duration(rl.requestRateLimit.WindowSeconds) * time.Second
	}

	// Determine per-key token limit and window.
	tokLimit := rl.tpmLimit
	tokWindow := 60 * time.Second
	if rl.tokenRateLimit != nil {
		tokLimit = rl.tokenRateLimit.Count
		tokWindow = time.Duration(rl.tokenRateLimit.WindowSeconds) * time.Second
	}

	// Step 1: Check per-key request limit.
	if reqLimit > 0 {
		count, err := rl.store.GetRequestCount(ctx, key, reqWindow)
		if err != nil {
			return nil, err
		}
		if count >= reqLimit {
			if rl.overflowBehavior == FailFastMode {
				return nil, ErrRateLimitExceeded
			}
			// BlockMode: poll until capacity or context cancellation.
			for count >= reqLimit {
				timer := time.NewTimer(time.Second)
				select {
				case <-timer.C:
				case <-ctx.Done():
					timer.Stop()
					return nil, ctx.Err()
				}
				count, err = rl.store.GetRequestCount(ctx, key, reqWindow)
				if err != nil {
					return nil, err
				}
			}
		}
	}

	// Step 2: Check per-key token limit.
	if tokLimit > 0 {
		count, err := rl.store.GetTokenCount(ctx, key, tokWindow)
		if err != nil {
			return nil, err
		}
		if count >= tokLimit {
			if rl.overflowBehavior == FailFastMode {
				return nil, ErrRateLimitExceeded
			}
			// BlockMode: poll until capacity or context cancellation.
			for count >= tokLimit {
				timer := time.NewTimer(time.Second)
				select {
				case <-timer.C:
				case <-ctx.Done():
					timer.Stop()
					return nil, ctx.Err()
				}
				count, err = rl.store.GetTokenCount(ctx, key, tokWindow)
				if err != nil {
					return nil, err
				}
			}
		}
	}

	// Step 3: Check global request limit (if configured).
	if rl.globalRequestLimit != nil {
		globalReqWindow := time.Duration(rl.globalRequestLimit.WindowSeconds) * time.Second
		count, err := rl.store.GetRequestCount(ctx, "global:requests", globalReqWindow)
		if err != nil {
			return nil, err
		}
		if count >= rl.globalRequestLimit.Count {
			if rl.overflowBehavior == FailFastMode {
				return nil, ErrRateLimitExceeded
			}
			for count >= rl.globalRequestLimit.Count {
				timer := time.NewTimer(time.Second)
				select {
				case <-timer.C:
				case <-ctx.Done():
					timer.Stop()
					return nil, ctx.Err()
				}
				count, err = rl.store.GetRequestCount(ctx, "global:requests", globalReqWindow)
				if err != nil {
					return nil, err
				}
			}
		}
	}

	// Step 4: Check global token limit (if configured).
	if rl.globalTokenLimit != nil {
		globalTokWindow := time.Duration(rl.globalTokenLimit.WindowSeconds) * time.Second
		count, err := rl.store.GetTokenCount(ctx, "global:tokens", globalTokWindow)
		if err != nil {
			return nil, err
		}
		if count >= rl.globalTokenLimit.Count {
			if rl.overflowBehavior == FailFastMode {
				return nil, ErrRateLimitExceeded
			}
			for count >= rl.globalTokenLimit.Count {
				timer := time.NewTimer(time.Second)
				select {
				case <-timer.C:
				case <-ctx.Done():
					timer.Stop()
					return nil, ctx.Err()
				}
				count, err = rl.store.GetTokenCount(ctx, "global:tokens", globalTokWindow)
				if err != nil {
					return nil, err
				}
			}
		}
	}

	// Step 5: Increment per-key request counter on success.
	if reqLimit > 0 {
		if _, err := rl.store.IncrementRequests(ctx, key, reqWindow); err != nil {
			return nil, err
		}
	}

	// Step 6: Increment global request counter (if configured).
	if rl.globalRequestLimit != nil {
		globalReqWindow := time.Duration(rl.globalRequestLimit.WindowSeconds) * time.Second
		if _, err := rl.store.IncrementRequests(ctx, "global:requests", globalReqWindow); err != nil {
			return nil, err
		}
	}

	// Step 7: Acquire concurrency slot if configured.
	if rl.maxConcurrent <= 0 {
		return noopRelease, nil
	}

	rl.mu.Lock()
	sem, ok := rl.semaphores[key]
	if !ok {
		sem = newConcurrencySem(rl.maxConcurrent)
		rl.semaphores[key] = sem
	}
	rl.mu.Unlock()

	if err := sem.Acquire(ctx, rl.overflowBehavior); err != nil {
		return nil, err
	}

	var once sync.Once
	return func() { once.Do(sem.Release) }, nil
}

// Record records token consumption for the given key after a successful provider call.
// Use an empty string for shared (non-keyed) rate limiting.
//
// When a global token limit is configured, Record updates both the per-key and
// global token counters.
//
// Record does NOT release the concurrency slot — that is handled by the
// ReleaseFunc returned from Acquire. This separation ensures the slot is freed
// even when the operation fails before Record is called.
//
// When a RateLimitStore is configured (via WithStore), token recording is delegated
// to the store. Any store errors are propagated to the caller.
func (rl *RateLimiter) Record(key string, usage TokenUsage) error {
	// When a store is configured, delegate token recording to it.
	if rl.store != nil {
		return rl.recordWithStore(key, usage)
	}

	// Step 1: Record token usage in the per-key bucket.
	rl.bucket(key).record(usage)

	// Step 2: Record token usage in the global bucket (if global token limit configured).
	if rl.globalBucket != nil {
		rl.globalBucket.record(usage)
	}

	return nil
}

// recordWithStore implements the Record logic using a RateLimitStore for token
// counter operations. It increments both per-key and global token counters
// via the store. Concurrency slot release is handled by the ReleaseFunc from
// Acquire, not here.
func (rl *RateLimiter) recordWithStore(key string, usage TokenUsage) error {
	tokens := usage.Total()
	if tokens <= 0 {
		return nil
	}

	// Determine per-key token window.
	tokWindow := 60 * time.Second
	if rl.tokenRateLimit != nil {
		tokWindow = time.Duration(rl.tokenRateLimit.WindowSeconds) * time.Second
	}

	// Step 1: Increment per-key token counter via store.
	ctx := context.Background()
	if _, err := rl.store.IncrementTokens(ctx, key, tokWindow, tokens); err != nil {
		return err
	}

	// Step 2: Increment global token counter (if configured).
	if rl.globalTokenLimit != nil {
		globalTokWindow := time.Duration(rl.globalTokenLimit.WindowSeconds) * time.Second
		if _, err := rl.store.IncrementTokens(ctx, "global:tokens", globalTokWindow, tokens); err != nil {
			return err
		}
	}

	return nil
}

// Purge removes the bucket for the given key, freeing its resources.
// Call this when a conversation ends and no further calls are expected for that key.
// Has no effect on shared (empty-key) usage.
func (rl *RateLimiter) Purge(key string) {
	rl.mu.Lock()
	delete(rl.buckets, key)
	rl.mu.Unlock()
}

// Len returns the number of active key buckets.
func (rl *RateLimiter) Len() int {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return len(rl.buckets)
}

// PreFlightCheck estimates token usage for the given params and checks whether
// the estimated input tokens fit within the remaining TPM budget for key.
// Returns ErrRateLimitExceeded if the estimate exceeds remaining capacity.
// Returns nil (allows the call) if no TokenEstimator is configured, or if the
// estimator returns an error (fail-open).
func (rl *RateLimiter) PreFlightCheck(ctx context.Context, key string, params ConverseParams) error {
	if rl.tokenEstimator == nil {
		return nil
	}

	estimate, err := rl.tokenEstimator.EstimateTokens(ctx, params)
	if err != nil {
		// Fail-open: allow the call when estimation fails.
		return nil
	}

	// Determine the effective TPM limit and window.
	tpmLimit := rl.tpmLimit
	tokenWindow := 60 * time.Second
	if rl.tokenRateLimit != nil {
		tpmLimit = rl.tokenRateLimit.Count
		tokenWindow = time.Duration(rl.tokenRateLimit.WindowSeconds) * time.Second
	}

	// No token limit configured — nothing to check.
	if tpmLimit <= 0 {
		return nil
	}

	// Get current token usage for this key.
	var currentUsage int
	if rl.store != nil {
		currentUsage, err = rl.store.GetTokenCount(ctx, key, tokenWindow)
		if err != nil {
			// Fail-open on store errors.
			return nil
		}
	} else {
		b := rl.bucket(key)
		b.mu.Lock()
		switch b.windowStrategy {
		case SlidingWindow:
			currentUsage = b.slidingTPMCount()
		case FixedWindow:
			currentUsage = b.fixedTPMCountVal()
		}
		b.mu.Unlock()
	}

	remaining := tpmLimit - currentUsage
	if estimate > remaining {
		return ErrRateLimitExceeded
	}

	return nil
}
