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

	// Sliding window: list of timestamped events
	rpmEvents []time.Time
	tpmEvents []tokenEvent

	// Fixed window: counters with reset time
	fixedWindowStart time.Time
	fixedRPMCount    int
	fixedTPMCount    int

	// lastAccess is updated on every acquire/record for TTL eviction.
	lastAccess time.Time

	// Clock abstraction for testing
	now func() time.Time
}

// maybeResetFixedWindow resets the fixed window counters if the current window
// has expired (60 seconds elapsed since window start).
func (b *rateBucket) maybeResetFixedWindow() {
	now := b.now()
	if b.fixedWindowStart.IsZero() {
		b.fixedWindowStart = now
		return
	}
	if now.Sub(b.fixedWindowStart) >= 60*time.Second {
		b.fixedWindowStart = now
		b.fixedRPMCount = 0
		b.fixedTPMCount = 0
	}
}

func (b *rateBucket) fixedRPMCountVal() int {
	b.maybeResetFixedWindow()
	return b.fixedRPMCount
}

func (b *rateBucket) fixedTPMCountVal() int {
	b.maybeResetFixedWindow()
	return b.fixedTPMCount
}

func (b *rateBucket) slidingRPMCount() int {
	cutoff := b.now().Add(-60 * time.Second)
	i := sort.Search(len(b.rpmEvents), func(j int) bool {
		return !b.rpmEvents[j].Before(cutoff)
	})
	b.rpmEvents = b.rpmEvents[i:]
	return len(b.rpmEvents)
}

func (b *rateBucket) slidingTPMCount() int {
	cutoff := b.now().Add(-60 * time.Second)
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
	switch b.windowStrategy {
	case SlidingWindow:
		if len(b.rpmEvents) > 0 {
			oldest := b.rpmEvents[0]
			return oldest.Add(60 * time.Second).Sub(now)
		}
	case FixedWindow:
		return b.fixedWindowStart.Add(60 * time.Second).Sub(now)
	}
	return time.Second
}

func (b *rateBucket) tpmWaitDuration() time.Duration {
	now := b.now()
	switch b.windowStrategy {
	case SlidingWindow:
		if len(b.tpmEvents) > 0 {
			oldest := b.tpmEvents[0]
			return oldest.at.Add(60 * time.Second).Sub(now)
		}
	case FixedWindow:
		return b.fixedWindowStart.Add(60 * time.Second).Sub(now)
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
		b.maybeResetFixedWindow()
		b.fixedTPMCount += tokens
	}
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

// NewRateLimiter creates a RateLimiter with the given RPM and TPM limits.
// At least one of rpmLimit or tpmLimit must be > 0.
// Defaults: SlidingWindow strategy, FailFast overflow behavior.
//
// When used without conversation IDs (or with a single shared agent), all calls
// share one budget. When conversation IDs are present, each ID gets its own
// independent budget with the same limits.
func NewRateLimiter(rpmLimit, tpmLimit int, opts ...RateLimiterOption) (*RateLimiter, error) {
	if rpmLimit == 0 && tpmLimit == 0 {
		return nil, fmt.Errorf("at least one of rpmLimit or tpmLimit must be > 0")
	}
	rl := &RateLimiter{
		rpmLimit:         rpmLimit,
		tpmLimit:         tpmLimit,
		windowStrategy:   SlidingWindow,
		overflowBehavior: FailFastMode,
		buckets:          make(map[string]*rateBucket),
		now:              time.Now,
	}
	for _, opt := range opts {
		opt(rl)
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
	b := &rateBucket{
		rpmLimit:         rl.rpmLimit,
		tpmLimit:         rl.tpmLimit,
		windowStrategy:   rl.windowStrategy,
		overflowBehavior: rl.overflowBehavior,
		lastAccess:       now,
		now:              rl.now,
	}
	rl.buckets[key] = b
	return b
}

// Acquire checks rate limits for the given key before a provider call.
// Use an empty string for shared (non-keyed) rate limiting.
// Each distinct key is rate-limited independently.
func (rl *RateLimiter) Acquire(ctx context.Context, key string) error {
	return rl.bucket(key).acquire(ctx)
}

// Record records token consumption for the given key after a successful provider call.
// Use an empty string for shared (non-keyed) rate limiting.
func (rl *RateLimiter) Record(key string, usage TokenUsage) {
	rl.bucket(key).record(usage)
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
