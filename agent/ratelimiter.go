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

// RateLimiter enforces RPM and TPM limits on provider calls.
// It is safe for concurrent use by multiple goroutines and agents.
type RateLimiter struct {
	mu sync.Mutex

	rpmLimit int // 0 = unlimited
	tpmLimit int // 0 = unlimited

	windowStrategy   WindowStrategy
	overflowBehavior OverflowBehavior

	// Sliding window: list of timestamped events
	rpmEvents []time.Time
	tpmEvents []tokenEvent

	// Fixed window: counters with reset time
	fixedWindowStart time.Time
	fixedRPMCount    int
	fixedTPMCount    int

	// Clock abstraction for testing
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
// immediately when a limit is exceeded.
func WithFailFast() RateLimiterOption {
	return func(rl *RateLimiter) {
		rl.overflowBehavior = FailFastMode
	}
}

// WithBlock configures the RateLimiter to wait until capacity is available.
// This is the default and is provided for explicitness.
func WithBlock() RateLimiterOption {
	return func(rl *RateLimiter) {
		rl.overflowBehavior = BlockMode
	}
}

// maybeResetFixedWindow resets the fixed window counters if the current window
// has expired (60 seconds elapsed since window start). If the window has not
// been initialized yet, it sets the start to now.
func (rl *RateLimiter) maybeResetFixedWindow() {
	now := rl.now()
	if rl.fixedWindowStart.IsZero() {
		rl.fixedWindowStart = now
		return
	}
	if now.Sub(rl.fixedWindowStart) >= 60*time.Second {
		rl.fixedWindowStart = now
		rl.fixedRPMCount = 0
		rl.fixedTPMCount = 0
	}
}

// fixedRPMCountVal returns the current RPM count for the fixed window,
// resetting the window first if it has expired.
func (rl *RateLimiter) fixedRPMCountVal() int {
	rl.maybeResetFixedWindow()
	return rl.fixedRPMCount
}

// fixedTPMCountVal returns the current TPM count for the fixed window,
// resetting the window first if it has expired.
func (rl *RateLimiter) fixedTPMCountVal() int {
	rl.maybeResetFixedWindow()
	return rl.fixedTPMCount
}

// NewRateLimiter creates a RateLimiter with the given RPM and TPM limits.
// At least one of rpmLimit or tpmLimit must be > 0.
// Defaults: SlidingWindow strategy, BlockMode overflow behavior.
func NewRateLimiter(rpmLimit, tpmLimit int, opts ...RateLimiterOption) (*RateLimiter, error) {
	if rpmLimit == 0 && tpmLimit == 0 {
		return nil, fmt.Errorf("at least one of rpmLimit or tpmLimit must be > 0")
	}
	rl := &RateLimiter{
		rpmLimit:         rpmLimit,
		tpmLimit:         tpmLimit,
		windowStrategy:   SlidingWindow,
		overflowBehavior: BlockMode,
		now:              time.Now,
	}
	for _, opt := range opts {
		opt(rl)
	}
	return rl, nil
}

// slidingRPMCount prunes rpmEvents older than 60 seconds and returns the count
// of remaining events. Must be called with rl.mu held.
func (rl *RateLimiter) slidingRPMCount() int {
	cutoff := rl.now().Add(-60 * time.Second)
	// Use sort.Search to find the first event that is not before the cutoff.
	i := sort.Search(len(rl.rpmEvents), func(j int) bool {
		return !rl.rpmEvents[j].Before(cutoff)
	})
	rl.rpmEvents = rl.rpmEvents[i:]
	return len(rl.rpmEvents)
}

// slidingTPMCount prunes tpmEvents older than 60 seconds and returns the sum
// of remaining token counts. Must be called with rl.mu held.
func (rl *RateLimiter) slidingTPMCount() int {
	cutoff := rl.now().Add(-60 * time.Second)
	i := sort.Search(len(rl.tpmEvents), func(j int) bool {
		return !rl.tpmEvents[j].at.Before(cutoff)
	})
	rl.tpmEvents = rl.tpmEvents[i:]
	total := 0
	for _, e := range rl.tpmEvents {
		total += e.tokens
	}
	return total
}

// Acquire checks rate limits before a provider call.
// It checks RPM first, then TPM. Depending on overflow behavior:
// - FailFastMode: returns ErrRateLimitExceeded immediately if a limit is exceeded
// - BlockMode: waits until the window resets and capacity is available
// The context is monitored for cancellation during blocking.
func (rl *RateLimiter) Acquire(ctx context.Context) error {
	rl.mu.Lock()
	for {
		// Check RPM limit (if configured, i.e. > 0)
		if rl.rpmLimit > 0 {
			var count int
			switch rl.windowStrategy {
			case SlidingWindow:
				count = rl.slidingRPMCount()
			case FixedWindow:
				count = rl.fixedRPMCountVal()
			}
			if count >= rl.rpmLimit {
				if rl.overflowBehavior == FailFastMode {
					rl.mu.Unlock()
					return ErrRateLimitExceeded
				}
				// BlockMode: calculate wait duration, release lock, wait, re-acquire
				waitDuration := rl.rpmWaitDuration()
				rl.mu.Unlock()
				if err := rl.waitForCapacity(ctx, waitDuration); err != nil {
					return err
				}
				rl.mu.Lock()
				continue
			}
		}

		// Check TPM limit (if configured, i.e. > 0)
		if rl.tpmLimit > 0 {
			var count int
			switch rl.windowStrategy {
			case SlidingWindow:
				count = rl.slidingTPMCount()
			case FixedWindow:
				count = rl.fixedTPMCountVal()
			}
			if count >= rl.tpmLimit {
				if rl.overflowBehavior == FailFastMode {
					rl.mu.Unlock()
					return ErrRateLimitExceeded
				}
				// BlockMode: calculate wait duration, release lock, wait, re-acquire
				waitDuration := rl.tpmWaitDuration()
				rl.mu.Unlock()
				if err := rl.waitForCapacity(ctx, waitDuration); err != nil {
					return err
				}
				rl.mu.Lock()
				continue
			}
		}

		// Both checks passed: increment RPM counter and return
		switch rl.windowStrategy {
		case SlidingWindow:
			rl.rpmEvents = append(rl.rpmEvents, rl.now())
		case FixedWindow:
			rl.maybeResetFixedWindow()
			rl.fixedRPMCount++
		}

		rl.mu.Unlock()
		return nil
	}
}

// rpmWaitDuration calculates how long to wait until RPM capacity becomes available.
// Must be called with rl.mu held.
func (rl *RateLimiter) rpmWaitDuration() time.Duration {
	now := rl.now()
	switch rl.windowStrategy {
	case SlidingWindow:
		if len(rl.rpmEvents) > 0 {
			oldest := rl.rpmEvents[0]
			return oldest.Add(60 * time.Second).Sub(now)
		}
	case FixedWindow:
		return rl.fixedWindowStart.Add(60 * time.Second).Sub(now)
	}
	return time.Second // fallback, should not happen
}

// tpmWaitDuration calculates how long to wait until TPM capacity becomes available.
// Must be called with rl.mu held.
func (rl *RateLimiter) tpmWaitDuration() time.Duration {
	now := rl.now()
	switch rl.windowStrategy {
	case SlidingWindow:
		if len(rl.tpmEvents) > 0 {
			oldest := rl.tpmEvents[0]
			return oldest.at.Add(60 * time.Second).Sub(now)
		}
	case FixedWindow:
		return rl.fixedWindowStart.Add(60 * time.Second).Sub(now)
	}
	return time.Second // fallback, should not happen
}

// waitForCapacity waits for the specified duration or until the context is cancelled.
// It does NOT hold the mutex — it is called after releasing the lock.
// Returns nil when the timer fires (capacity may be available), or ctx.Err() if
// the context is cancelled before the timer fires.
func (rl *RateLimiter) waitForCapacity(ctx context.Context, waitDuration time.Duration) error {
	timer := time.NewTimer(waitDuration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil // capacity available, retry check
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Record records token consumption after a successful provider call.
// Only call this after a successful provider response.
func (rl *RateLimiter) Record(usage TokenUsage) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	tokens := usage.Total()
	if tokens <= 0 {
		return
	}

	switch rl.windowStrategy {
	case SlidingWindow:
		rl.tpmEvents = append(rl.tpmEvents, tokenEvent{at: rl.now(), tokens: tokens})
	case FixedWindow:
		rl.maybeResetFixedWindow()
		rl.fixedTPMCount += tokens
	}
}
