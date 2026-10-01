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

// capacityLocked reports whether b can admit one more request: its request
// count is below rpmLimit and its recorded tokens are below tpmLimit. When it
// cannot, wait is how long until capacity may free up. Caller holds b.mu.
func (b *rateBucket) capacityLocked() (ok bool, wait time.Duration) {
	if b.rpmLimit > 0 {
		var count int
		switch b.windowStrategy {
		case SlidingWindow:
			count = b.slidingRPMCount()
		case FixedWindow:
			count = b.fixedRPMCountVal()
		}
		if count >= b.rpmLimit {
			return false, b.rpmWaitDuration()
		}
	}
	if b.tpmLimit > 0 {
		if b.tokenCountLocked() >= b.tpmLimit {
			return false, b.tpmWaitDuration()
		}
	}
	return true, 0
}

// tokenCountLocked returns the tokens recorded in the current token window.
// Caller holds b.mu.
func (b *rateBucket) tokenCountLocked() int {
	switch b.windowStrategy {
	case FixedWindow:
		return b.fixedTPMCountVal()
	default:
		return b.slidingTPMCount()
	}
}

// chargeRequestLocked records one admitted request. Buckets without a request
// limit keep no request history. Caller holds b.mu.
func (b *rateBucket) chargeRequestLocked() {
	now := b.now()
	b.lastAccess = now
	if b.rpmLimit <= 0 {
		return
	}
	switch b.windowStrategy {
	case SlidingWindow:
		b.rpmEvents = append(b.rpmEvents, now)
	case FixedWindow:
		b.maybeResetFixedWindow()
		b.fixedRPMCount++
	}
}

// recordLocked records actual token usage. Buckets without a token limit keep
// no token history. Caller holds b.mu.
func (b *rateBucket) recordLocked(tokens int) {
	now := b.now()
	b.lastAccess = now
	if b.tpmLimit <= 0 {
		return
	}
	switch b.windowStrategy {
	case SlidingWindow:
		b.tpmEvents = append(b.tpmEvents, tokenEvent{at: now, tokens: tokens})
	case FixedWindow:
		b.maybeResetFixedTokenWindow()
		b.fixedTPMCount += tokens
	}
}

// lockBuckets locks buckets in slice order. Callers always pass the per-key
// bucket before the global bucket, which gives a consistent lock order.
func lockBuckets(buckets []*rateBucket) {
	for _, b := range buckets {
		b.mu.Lock()
	}
}

func unlockBuckets(buckets []*rateBucket) {
	for i := len(buckets) - 1; i >= 0; i-- {
		buckets[i].mu.Unlock()
	}
}

// reserveBuckets admits one request on every bucket or on none. All buckets
// are checked and charged while all their locks are held, so a rejection by
// one bucket (for example the global one) never consumes another bucket's
// budget. In BlockMode it waits for the longest required wait and retries.
func reserveBuckets(ctx context.Context, behavior OverflowBehavior, buckets []*rateBucket) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		lockBuckets(buckets)
		admit := true
		var wait time.Duration
		for _, b := range buckets {
			if ok, w := b.capacityLocked(); !ok {
				admit = false
				if w > wait {
					wait = w
				}
			}
		}
		if admit {
			for _, b := range buckets {
				b.chargeRequestLocked()
			}
			unlockBuckets(buckets)
			return nil
		}
		unlockBuckets(buckets)
		if behavior == FailFastMode {
			return ErrRateLimitExceeded
		}
		if wait < time.Millisecond {
			wait = time.Millisecond
		}
		if err := waitFor(ctx, wait); err != nil {
			return err
		}
	}
}

// recordBuckets records tokens on every bucket in one critical section.
func recordBuckets(buckets []*rateBucket, tokens int) {
	lockBuckets(buckets)
	for _, b := range buckets {
		b.recordLocked(tokens)
	}
	unlockBuckets(buckets)
}

// waitFor sleeps for d or until ctx is done.
func waitFor(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// rateConfig holds a count + window pair for configurable limits.
type rateConfig struct {
	Count         int
	WindowSeconds int
}

func (c *rateConfig) window() time.Duration {
	return time.Duration(c.WindowSeconds) * time.Second
}

// concurrencySem is a per-key counting semaphore backed by a buffered
// channel: a send occupies a slot, a receive frees one. Waiters select on the
// channel and their context, so no helper goroutine is needed per waiter.
type concurrencySem struct {
	slots chan struct{}
	// refs counts callers that currently hold or are waiting for a slot. It
	// is guarded by RateLimiter.mu and prevents Purge or the stale sweep from
	// discarding a semaphore that is in use, which would let a fresh
	// semaphore for the same key exceed MaxConcurrent.
	refs int
}

// newConcurrencySem creates a concurrencySem with the given capacity.
func newConcurrencySem(max int) *concurrencySem {
	return &concurrencySem{slots: make(chan struct{}, max)}
}

// Acquire takes a slot. In FailFastMode it returns ErrRateLimitExceeded when
// none is free; in BlockMode it waits until a slot frees or ctx is done.
func (s *concurrencySem) Acquire(ctx context.Context, behavior OverflowBehavior) error {
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
		// A slot and cancellation can become ready together; never hand out
		// a slot to an already-cancelled caller.
		if err := ctx.Err(); err != nil {
			s.Release()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Release frees one slot. Releasing an idle semaphore is a no-op.
func (s *concurrencySem) Release() {
	select {
	case <-s.slots:
	default:
	}
}

// inflight returns the number of occupied slots.
func (s *concurrencySem) inflight() int { return len(s.slots) }

// capacity returns the semaphore's maximum concurrency.
func (s *concurrencySem) capacity() int { return cap(s.slots) }

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

	windowStrategy   WindowStrategy
	overflowBehavior OverflowBehavior

	buckets    map[string]*rateBucket
	lastSweep  time.Time     // last time stale buckets were evicted
	staleAfter time.Duration // idle time after which a per-key bucket holds no in-window state

	// Clock abstraction for testing.
	now func() time.Time

	// Configurable rate limits (additive constraints).
	requestRateLimit *rateConfig // nil = no per-key request limit
	tokenRateLimit   *rateConfig // nil = no per-key token limit

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
	tokenEstimator    TokenEstimator // nil = no pre-flight check
	preFlightDisabled bool           // true = explicitly disabled by WithoutPreFlight
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

// WithoutPreFlight disables the automatic pre-flight token budget check.
// By default, when a TPM limit is configured, the RateLimiter uses CharEstimator
// to reject requests that would obviously exceed the remaining budget. Use this
// option to disable that behavior and rely solely on post-hoc token accounting.
func WithoutPreFlight() RateLimiterOption {
	return func(rl *RateLimiter) {
		rl.preFlightDisabled = true
		rl.tokenEstimator = nil
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

	// Default: enable pre-flight token estimation when a TPM limit is configured
	// and no explicit estimator was set. This prevents obviously-over-budget calls
	// from consuming API quota. Use WithoutPreFlight() to opt out.
	hasTPM := rl.tokenRateLimit != nil || rl.globalTokenLimit != nil
	if hasTPM && rl.tokenEstimator == nil && !rl.preFlightDisabled {
		rl.tokenEstimator = CharEstimator{}
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

	rl.staleAfter = staleAfterFor(rl)

	return rl, nil
}

// Store keys used by the RateLimiter. Per-key counters are namespaced so a
// caller key such as "global" can never collide with the global counters.
const (
	storeGlobalKey   = "global"
	storePerKeyScope = "key:"
)

func storeKey(key string) string { return storePerKeyScope + key }

// storePollInterval is how often BlockMode retries against a RateLimitStore.
const storePollInterval = time.Second

// accountingTimeout bounds token-accounting store calls. Accounting runs after
// the provider already consumed tokens, so it survives caller cancellation
// but must not hang indefinitely.
const accountingTimeout = 5 * time.Second

// staleSweepInterval is how often idle buckets and semaphores are evicted.
const staleSweepInterval = 10 * time.Second

// bucket returns the rateBucket for key, creating one if needed, and marks it
// as recently used. It also evicts idle buckets and unused semaphores at most
// once per staleSweepInterval.
func (rl *RateLimiter) bucket(key string) *rateBucket {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.now()
	rl.sweepLocked(now)

	if b, ok := rl.buckets[key]; ok {
		// Touch under rl.mu so a concurrent sweep cannot evict a bucket that
		// is about to be used.
		b.mu.Lock()
		b.lastAccess = now
		b.mu.Unlock()
		return b
	}

	var rpmLimit, tpmLimit int
	var windowDuration, tokenWindowDuration time.Duration
	if rl.requestRateLimit != nil {
		rpmLimit = rl.requestRateLimit.Count
		windowDuration = rl.requestRateLimit.window()
	}
	if rl.tokenRateLimit != nil {
		tpmLimit = rl.tokenRateLimit.Count
		tokenWindowDuration = rl.tokenRateLimit.window()
	}
	b := &rateBucket{
		rpmLimit:            rpmLimit,
		tpmLimit:            tpmLimit,
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

// sweepLocked evicts per-key buckets idle for at least staleAfter (so every
// recorded event has left its window and eviction cannot reset a limit) and
// semaphores no caller holds or waits on. Caller holds rl.mu.
func (rl *RateLimiter) sweepLocked(now time.Time) {
	if now.Sub(rl.lastSweep) < staleSweepInterval {
		return
	}
	rl.lastSweep = now
	for k, b := range rl.buckets {
		if k == "" {
			continue
		}
		b.mu.Lock()
		idle := now.Sub(b.lastAccess)
		b.mu.Unlock()
		if idle >= rl.staleAfter {
			delete(rl.buckets, k)
		}
	}
	for k, s := range rl.semaphores {
		if s.refs == 0 {
			delete(rl.semaphores, k)
		}
	}
}

// staleAfterFor returns the minimum idle time after which a per-key bucket
// holds no in-window state: the largest configured per-key window, and never
// less than 60s.
func staleAfterFor(rl *RateLimiter) time.Duration {
	d := 60 * time.Second
	for _, c := range []*rateConfig{rl.requestRateLimit, rl.tokenRateLimit} {
		if c != nil && c.window() > d {
			d = c.window()
		}
	}
	return d
}

// leaseSem returns the semaphore for key, creating it if needed, and
// registers the caller as a user so it cannot be discarded while in use.
func (rl *RateLimiter) leaseSem(key string) *concurrencySem {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	s, ok := rl.semaphores[key]
	if !ok {
		s = newConcurrencySem(rl.maxConcurrent)
		rl.semaphores[key] = s
	}
	s.refs++
	return s
}

// unleaseSem drops a caller registered by leaseSem.
func (rl *RateLimiter) unleaseSem(s *concurrencySem) {
	rl.mu.Lock()
	s.refs--
	rl.mu.Unlock()
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

// Acquire admits one provider call for key. Use an empty string for shared
// (non-keyed) rate limiting; each distinct key is limited independently.
//
// Acquire first takes a concurrency slot (when MaxConcurrent is configured),
// then reserves one request on the per-key and global request budgets as a
// single all-or-nothing reservation. A failed Acquire — rejection, context
// cancellation, or store error — leaves request accounting exactly as if it
// never happened and holds no slot. In BlockMode the slot is held while
// waiting for request capacity.
//
// On success it returns a non-nil ReleaseFunc that the caller MUST invoke once
// the in-flight operation completes. On error the ReleaseFunc is nil.
func (rl *RateLimiter) Acquire(ctx context.Context, key string) (ReleaseFunc, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	release := ReleaseFunc(noopRelease)
	if rl.maxConcurrent > 0 {
		sem := rl.leaseSem(key)
		if err := sem.Acquire(ctx, rl.overflowBehavior); err != nil {
			rl.unleaseSem(sem)
			return nil, err
		}
		var once sync.Once
		release = func() {
			once.Do(func() {
				sem.Release()
				rl.unleaseSem(sem)
			})
		}
	}

	var err error
	if rl.store != nil {
		err = rl.reserveWithStore(ctx, key)
	} else {
		err = rl.reserveInMemory(ctx, key)
	}
	if err != nil {
		release()
		return nil, err
	}
	return release, nil
}

// limitBuckets returns the in-memory buckets that apply to key, per-key first.
func (rl *RateLimiter) limitBuckets(key string) []*rateBucket {
	buckets := []*rateBucket{rl.bucket(key)}
	if rl.globalBucket != nil {
		buckets = append(buckets, rl.globalBucket)
	}
	return buckets
}

func (rl *RateLimiter) reserveInMemory(ctx context.Context, key string) error {
	return reserveBuckets(ctx, rl.overflowBehavior, rl.limitBuckets(key))
}

// requestReservations returns the store request counters that apply to key.
func (rl *RateLimiter) requestReservations(key string) []RequestReservation {
	var rs []RequestReservation
	if rl.requestRateLimit != nil {
		rs = append(rs, RequestReservation{Key: storeKey(key), Limit: rl.requestRateLimit.Count, Window: rl.requestRateLimit.window()})
	}
	if rl.globalRequestLimit != nil {
		rs = append(rs, RequestReservation{Key: storeGlobalKey, Limit: rl.globalRequestLimit.Count, Window: rl.globalRequestLimit.window()})
	}
	return rs
}

// tokenCounters returns the store token counters that apply to key.
func (rl *RateLimiter) tokenCounters(key string) []TokenCounter {
	var cs []TokenCounter
	if rl.tokenRateLimit != nil {
		cs = append(cs, TokenCounter{Key: storeKey(key), Window: rl.tokenRateLimit.window()})
	}
	if rl.globalTokenLimit != nil {
		cs = append(cs, TokenCounter{Key: storeGlobalKey, Window: rl.globalTokenLimit.window()})
	}
	return cs
}

// storeTokensAvailable reports whether recorded token usage is below every
// applicable token limit. Errors are returned to the caller of Acquire.
func (rl *RateLimiter) storeTokensAvailable(ctx context.Context, key string) (bool, error) {
	if rl.tokenRateLimit != nil {
		n, err := rl.store.GetTokenCount(ctx, storeKey(key), rl.tokenRateLimit.window())
		if err != nil {
			return false, err
		}
		if n >= rl.tokenRateLimit.Count {
			return false, nil
		}
	}
	if rl.globalTokenLimit != nil {
		n, err := rl.store.GetTokenCount(ctx, storeGlobalKey, rl.globalTokenLimit.window())
		if err != nil {
			return false, err
		}
		if n >= rl.globalTokenLimit.Count {
			return false, nil
		}
	}
	return true, nil
}

// reserveWithStore admits one request through the store's atomic
// all-or-nothing reservation, polling in BlockMode.
func (rl *RateLimiter) reserveWithStore(ctx context.Context, key string) error {
	reservations := rl.requestReservations(key)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		ok, err := rl.storeTokensAvailable(ctx, key)
		if err != nil {
			return err
		}
		if ok {
			if len(reservations) == 0 {
				return nil
			}
			ok, err = rl.store.ReserveRequests(ctx, reservations)
			if err != nil {
				return err
			}
			if ok {
				return nil
			}
		}
		if rl.overflowBehavior == FailFastMode {
			return ErrRateLimitExceeded
		}
		if err := waitFor(ctx, storePollInterval); err != nil {
			return err
		}
	}
}

// Record records actual token usage for key after a successful provider call.
// Use an empty string for shared (non-keyed) rate limiting.
//
// Usage is recorded on the per-key and global token counters together:
// in memory under one critical section, with a store through one
// all-or-nothing RecordTokens call. Store calls use a context derived from
// ctx without its cancellation and bounded by a short timeout, so usage the
// provider already consumed is still recorded when the caller goes away.
//
// Record does NOT release the concurrency slot; that is the ReleaseFunc's job.
func (rl *RateLimiter) Record(ctx context.Context, key string, usage TokenUsage) error {
	tokens := usage.Total()
	if tokens <= 0 {
		return nil
	}
	if rl.store == nil {
		recordBuckets(rl.limitBuckets(key), tokens)
		return nil
	}
	counters := rl.tokenCounters(key)
	if len(counters) == 0 {
		return nil
	}
	accCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), accountingTimeout)
	defer cancel()
	return rl.store.RecordTokens(accCtx, counters, tokens)
}

// Purge removes the bucket for key, freeing its resources, and its
// concurrency semaphore when no call holds or waits for a slot. A semaphore
// still in use is kept so MaxConcurrent stays enforced for in-flight calls;
// the stale sweep removes it once idle. Call Purge when a conversation ends.
func (rl *RateLimiter) Purge(key string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	delete(rl.buckets, key)
	if s, ok := rl.semaphores[key]; ok && s.refs == 0 {
		delete(rl.semaphores, key)
	}
}

// Len returns the number of active key buckets.
func (rl *RateLimiter) Len() int {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return len(rl.buckets)
}

// PreFlightCheck estimates the input tokens of req and rejects the call with
// ErrRateLimitExceeded when the estimate exceeds the remaining per-key token
// budget or the remaining global token budget. Both must have room when both
// are configured.
//
// It fails open: it returns nil when no TokenEstimator is configured, when the
// estimator errors, or when a store read errors.
func (rl *RateLimiter) PreFlightCheck(ctx context.Context, key string, req ModelRequest) error {
	if rl.tokenEstimator == nil || (rl.tokenRateLimit == nil && rl.globalTokenLimit == nil) {
		return nil
	}
	estimate, err := rl.tokenEstimator.EstimateTokens(ctx, req)
	if err != nil {
		return nil
	}

	if rl.tokenRateLimit != nil {
		used, ok := rl.usedTokens(ctx, storeKey(key), rl.tokenRateLimit, func() *rateBucket { return rl.bucket(key) })
		if ok && estimate > rl.tokenRateLimit.Count-used {
			return ErrRateLimitExceeded
		}
	}
	if rl.globalTokenLimit != nil {
		used, ok := rl.usedTokens(ctx, storeGlobalKey, rl.globalTokenLimit, func() *rateBucket { return rl.globalBucket })
		if ok && estimate > rl.globalTokenLimit.Count-used {
			return ErrRateLimitExceeded
		}
	}
	return nil
}

// usedTokens returns recorded token usage from the store (storeKey) or from
// the in-memory bucket. ok is false when a store read fails (fail-open).
func (rl *RateLimiter) usedTokens(ctx context.Context, storeKey string, limit *rateConfig, bucket func() *rateBucket) (int, bool) {
	if rl.store != nil {
		n, err := rl.store.GetTokenCount(ctx, storeKey, limit.window())
		return n, err == nil
	}
	b := bucket()
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.tokenCountLocked(), true
}
