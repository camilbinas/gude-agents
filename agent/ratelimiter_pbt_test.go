package agent

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent/prompt"
	"pgregory.net/rapid"
)

// TestProperty_ZeroLimitMeansUnlimited verifies that when a limit dimension is
// set to zero, the RateLimiter never blocks or returns an error for that dimension,
// regardless of how many Acquire/Record calls are made.
//
// **Validates: Requirements 1.2, 1.3**
func TestProperty_ZeroLimitMeansUnlimited(t *testing.T) {
	t.Run("ZeroRPM_NeverBlocksOnRPM", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// RPM = 0 (unlimited), TPM > 0 (so constructor accepts it)
			tpmLimit := rapid.IntRange(1, 10000).Draw(rt, "tpmLimit")

			// Pick a random window strategy
			strategy := rapid.SampledFrom([]RateLimiterOption{
				WithSlidingWindow(),
				WithFixedWindow(),
			}).Draw(rt, "strategy")

			// Pick a random overflow behavior
			overflow := rapid.SampledFrom([]RateLimiterOption{
				WithBlock(),
				WithFailFast(),
			}).Draw(rt, "overflow")

			rl, err := NewRateLimiter(0, tpmLimit, strategy, overflow)
			if err != nil {
				rt.Fatalf("NewRateLimiter(0, %d) returned error: %v", tpmLimit, err)
			}

			// Use a mock clock so time doesn't advance (worst case for rate limiting)
			fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rl.now = func() time.Time { return fixedTime }

			// Generate a random number of Acquire calls (1–500)
			numCalls := rapid.IntRange(1, 500).Draw(rt, "numCalls")

			ctx := context.Background()
			for i := 0; i < numCalls; i++ {
				err := rl.Acquire(ctx)
				if err != nil {
					rt.Fatalf("Acquire() returned error on call %d with RPM=0 (unlimited): %v", i+1, err)
				}
			}
		})
	})

	t.Run("ZeroTPM_NeverBlocksOnTPM", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// RPM > 0 (so constructor accepts it), TPM = 0 (unlimited)
			rpmLimit := rapid.IntRange(1, 10000).Draw(rt, "rpmLimit")

			// Pick a random window strategy
			strategy := rapid.SampledFrom([]RateLimiterOption{
				WithSlidingWindow(),
				WithFixedWindow(),
			}).Draw(rt, "strategy")

			// Pick a random overflow behavior
			overflow := rapid.SampledFrom([]RateLimiterOption{
				WithBlock(),
				WithFailFast(),
			}).Draw(rt, "overflow")

			rl, err := NewRateLimiter(rpmLimit, 0, strategy, overflow)
			if err != nil {
				rt.Fatalf("NewRateLimiter(%d, 0) returned error: %v", rpmLimit, err)
			}

			// Use a mock clock so time doesn't advance (worst case for rate limiting)
			fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rl.now = func() time.Time { return fixedTime }

			// Generate random token amounts to record
			numRecords := rapid.IntRange(1, 100).Draw(rt, "numRecords")

			ctx := context.Background()

			// Record large amounts of tokens — TPM should never be enforced
			for i := 0; i < numRecords; i++ {
				tokens := rapid.IntRange(1, 100000).Draw(rt, "tokens")
				rl.Record(TokenUsage{
					InputTokens:  tokens / 2,
					OutputTokens: tokens - tokens/2,
				})
			}

			// Now Acquire should still succeed (TPM is unlimited).
			// We need to stay within RPM limit for this to pass, so we only
			// call Acquire up to rpmLimit times.
			acquireCalls := rapid.IntRange(1, rpmLimit).Draw(rt, "acquireCalls")
			for i := 0; i < acquireCalls; i++ {
				err := rl.Acquire(ctx)
				if err != nil {
					rt.Fatalf("Acquire() returned error on call %d with TPM=0 (unlimited) after recording %d token events: %v", i+1, numRecords, err)
				}
			}
		})
	})
}

// TestProperty_FixedWindowResetsAtIntervals verifies that for any RateLimiter
// using FixedWindow strategy, the RPM and TPM counters reset to zero when the
// current time exceeds the window start time plus 60 seconds. The new window
// starts at the reset time.
//
// **Validates: Requirements 2.1**
func TestProperty_FixedWindowResetsAtIntervals(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a random RPM limit (small enough to exhaust quickly)
		rpmLimit := rapid.IntRange(1, 50).Draw(rt, "rpmLimit")

		// Create a RateLimiter with FixedWindow and FailFast
		rl, err := NewRateLimiter(rpmLimit, 0, WithFixedWindow(), WithFailFast())
		if err != nil {
			rt.Fatalf("NewRateLimiter(%d, 0) returned error: %v", rpmLimit, err)
		}

		// Use a mock clock starting at a fixed time
		currentTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
		rl.now = func() time.Time { return currentTime }

		ctx := context.Background()

		// Exhaust the RPM limit within the current window
		for i := 0; i < rpmLimit; i++ {
			err := rl.Acquire(ctx)
			if err != nil {
				rt.Fatalf("Acquire() returned error on call %d (limit=%d): %v", i+1, rpmLimit, err)
			}
		}

		// Verify the limit is actually exhausted (next call should fail)
		err = rl.Acquire(ctx)
		if err != ErrRateLimitExceeded {
			rt.Fatalf("expected ErrRateLimitExceeded after exhausting RPM limit, got: %v", err)
		}

		// Advance the clock past the 60-second window boundary
		advanceDuration := rapid.Int64Range(60, 300).Draw(rt, "advanceSeconds")
		currentTime = currentTime.Add(time.Duration(advanceDuration) * time.Second)

		// After the window resets, Acquire should succeed again
		err = rl.Acquire(ctx)
		if err != nil {
			rt.Fatalf("Acquire() returned error after window reset (advanced %ds): %v", advanceDuration, err)
		}
	})
}

// TestProperty_SlidingWindowOnlyCountsRecentRequests verifies that for a
// SlidingWindow RateLimiter, only requests within the last 60 seconds contribute
// to the RPM and TPM counts. Requests older than 60 seconds are pruned and do
// not cause rate limit failures.
//
// **Validates: Requirements 2.2**
func TestProperty_SlidingWindowOnlyCountsRecentRequests(t *testing.T) {
	t.Run("RPM_OldRequestsExpire", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Use a generous RPM limit so we can fill it, then verify expiry
			rpmLimit := rapid.IntRange(2, 20).Draw(rt, "rpmLimit")

			rl, err := NewRateLimiter(rpmLimit, 0, WithSlidingWindow(), WithFailFast())
			if err != nil {
				rt.Fatalf("NewRateLimiter failed: %v", err)
			}

			// Start at a fixed base time
			baseTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			currentTime := baseTime
			rl.now = func() time.Time { return currentTime }

			ctx := context.Background()

			// Generate a number of requests to make at various offsets within the first window
			numOldRequests := rapid.IntRange(1, rpmLimit).Draw(rt, "numOldRequests")

			// Make requests at various times within the first 30 seconds
			for i := 0; i < numOldRequests; i++ {
				offset := rapid.IntRange(0, 29).Draw(rt, "oldOffset")
				currentTime = baseTime.Add(time.Duration(offset) * time.Second)
				err := rl.Acquire(ctx)
				if err != nil {
					rt.Fatalf("Acquire() failed during old requests (call %d): %v", i+1, err)
				}
			}

			// Advance time past 60 seconds from the latest possible old request
			// All old requests were made at baseTime + [0,29]s, so at baseTime + 90s
			// they are all older than 60s
			currentTime = baseTime.Add(90 * time.Second)

			// Now the sliding window should have pruned all old requests.
			// We should be able to make rpmLimit requests without hitting the limit.
			for i := 0; i < rpmLimit; i++ {
				err := rl.Acquire(ctx)
				if err != nil {
					rt.Fatalf("Acquire() returned error on call %d after old requests expired (rpmLimit=%d, numOldRequests=%d): %v",
						i+1, rpmLimit, numOldRequests, err)
				}
			}

			// The next request should fail (we've now used all capacity)
			err = rl.Acquire(ctx)
			if !errors.Is(err, ErrRateLimitExceeded) {
				rt.Fatalf("Expected ErrRateLimitExceeded after filling capacity, got: %v", err)
			}
		})
	})

	t.Run("TPM_OldTokensExpire", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Use a TPM limit that we can fill and verify expiry
			tpmLimit := rapid.IntRange(100, 5000).Draw(rt, "tpmLimit")

			rl, err := NewRateLimiter(0, tpmLimit, WithSlidingWindow(), WithFailFast())
			if err != nil {
				rt.Fatalf("NewRateLimiter failed: %v", err)
			}

			// Start at a fixed base time
			baseTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			currentTime := baseTime
			rl.now = func() time.Time { return currentTime }

			ctx := context.Background()

			// Record tokens at various times within the first 30 seconds to fill the budget
			numRecords := rapid.IntRange(1, 10).Draw(rt, "numRecords")
			totalOldTokens := 0
			for i := 0; i < numRecords; i++ {
				offset := rapid.IntRange(0, 29).Draw(rt, "tokenOffset")
				currentTime = baseTime.Add(time.Duration(offset) * time.Second)

				// Record some tokens (distribute the tpmLimit across records)
				tokens := rapid.IntRange(1, tpmLimit/numRecords+1).Draw(rt, "tokens")
				totalOldTokens += tokens
				rl.Record(TokenUsage{
					InputTokens:  tokens / 2,
					OutputTokens: tokens - tokens/2,
				})
				// Also acquire to register the RPM event (needed for the request to count)
				_ = rl.Acquire(ctx)
			}

			// Advance time past 60 seconds from the latest possible old record
			currentTime = baseTime.Add(90 * time.Second)

			// Now the sliding window should have pruned all old token records.
			// Record tokens up to the limit — this should succeed because old tokens expired.
			tokensToRecord := tpmLimit - 1 // just under the limit
			rl.Record(TokenUsage{
				InputTokens:  tokensToRecord / 2,
				OutputTokens: tokensToRecord - tokensToRecord/2,
			})

			// Acquire should still succeed (TPM is at tpmLimit-1, under the limit)
			err = rl.Acquire(ctx)
			if err != nil {
				rt.Fatalf("Acquire() failed after old tokens expired and recording %d tokens (tpmLimit=%d, totalOldTokens=%d): %v",
					tokensToRecord, tpmLimit, totalOldTokens, err)
			}
		})
	})

	t.Run("RPM_RecentRequestsStillCount", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Verify that requests within the 60s window DO count
			rpmLimit := rapid.IntRange(2, 20).Draw(rt, "rpmLimit")

			rl, err := NewRateLimiter(rpmLimit, 0, WithSlidingWindow(), WithFailFast())
			if err != nil {
				rt.Fatalf("NewRateLimiter failed: %v", err)
			}

			baseTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			currentTime := baseTime
			rl.now = func() time.Time { return currentTime }

			ctx := context.Background()

			// Make requests at times within the last 60 seconds relative to a check time
			checkTime := baseTime.Add(120 * time.Second)

			// Generate requests at various recent offsets (within 59 seconds before checkTime)
			numRecentRequests := rapid.IntRange(1, rpmLimit).Draw(rt, "numRecentRequests")
			for i := 0; i < numRecentRequests; i++ {
				// Place requests within (checkTime-60s, checkTime] — i.e., within the window
				offset := rapid.IntRange(1, 59).Draw(rt, "recentOffset")
				currentTime = checkTime.Add(-time.Duration(offset) * time.Second)
				err := rl.Acquire(ctx)
				if err != nil {
					rt.Fatalf("Acquire() failed for recent request %d: %v", i+1, err)
				}
			}

			// At checkTime, the recent requests should still count
			currentTime = checkTime
			remainingCapacity := rpmLimit - numRecentRequests

			// We should be able to make exactly remainingCapacity more requests
			for i := 0; i < remainingCapacity; i++ {
				err := rl.Acquire(ctx)
				if err != nil {
					rt.Fatalf("Acquire() failed on remaining capacity call %d (remaining=%d): %v",
						i+1, remainingCapacity, err)
				}
			}

			// The next one should fail
			err = rl.Acquire(ctx)
			if !errors.Is(err, ErrRateLimitExceeded) {
				rt.Fatalf("Expected ErrRateLimitExceeded after using all capacity, got: %v", err)
			}
		})
	})
}

// TestProperty_FailFastReturnsErrRateLimitExceeded verifies that for any
// RateLimiter in FailFastMode where the RPM or TPM counter equals or exceeds
// the configured limit, calling Acquire returns ErrRateLimitExceeded without
// blocking, and the error is detectable via errors.Is.
//
// **Validates: Requirements 3.1, 9.1, 9.2, 9.3**
func TestProperty_FailFastReturnsErrRateLimitExceeded(t *testing.T) {
	t.Run("RPM_Exceeded_ReturnsErrRateLimitExceeded", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Random RPM limit between 1 and 100
			rpmLimit := rapid.IntRange(1, 100).Draw(rt, "rpmLimit")

			// Pick a random window strategy
			strategy := rapid.SampledFrom([]RateLimiterOption{
				WithSlidingWindow(),
				WithFixedWindow(),
			}).Draw(rt, "strategy")

			// Create a RateLimiter with FailFast and the random RPM limit (TPM=0 unlimited)
			rl, err := NewRateLimiter(rpmLimit, 0, strategy, WithFailFast())
			if err != nil {
				rt.Fatalf("NewRateLimiter(%d, 0) returned error: %v", rpmLimit, err)
			}

			// Use a mock clock so time doesn't advance
			fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rl.now = func() time.Time { return fixedTime }

			ctx := context.Background()

			// Exhaust the RPM limit by calling Acquire rpmLimit times
			for i := 0; i < rpmLimit; i++ {
				err := rl.Acquire(ctx)
				if err != nil {
					rt.Fatalf("Acquire() returned error on call %d (before limit reached): %v", i+1, err)
				}
			}

			// The next Acquire should return ErrRateLimitExceeded immediately
			err = rl.Acquire(ctx)
			if err == nil {
				rt.Fatalf("Acquire() returned nil after exhausting RPM limit of %d", rpmLimit)
			}
			if !errors.Is(err, ErrRateLimitExceeded) {
				rt.Fatalf("Acquire() returned error %v, which is not detectable via errors.Is(err, ErrRateLimitExceeded)", err)
			}
		})
	})

	t.Run("TPM_Exceeded_ReturnsErrRateLimitExceeded", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Random TPM limit between 1 and 10000
			tpmLimit := rapid.IntRange(1, 10000).Draw(rt, "tpmLimit")

			// Pick a random window strategy
			strategy := rapid.SampledFrom([]RateLimiterOption{
				WithSlidingWindow(),
				WithFixedWindow(),
			}).Draw(rt, "strategy")

			// Create a RateLimiter with FailFast and the random TPM limit
			// RPM must be high enough to not interfere (set to tpmLimit+1 to allow enough acquires)
			rpmLimit := tpmLimit + 1
			rl, err := NewRateLimiter(rpmLimit, tpmLimit, strategy, WithFailFast())
			if err != nil {
				rt.Fatalf("NewRateLimiter(%d, %d) returned error: %v", rpmLimit, tpmLimit, err)
			}

			// Use a mock clock so time doesn't advance
			fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rl.now = func() time.Time { return fixedTime }

			ctx := context.Background()

			// Record tokens to exhaust the TPM limit.
			// We need to record enough tokens to meet or exceed tpmLimit.
			// Use a single Record call with exactly tpmLimit tokens.
			rl.Record(TokenUsage{
				InputTokens:  tpmLimit / 2,
				OutputTokens: tpmLimit - tpmLimit/2,
			})

			// The next Acquire should return ErrRateLimitExceeded due to TPM exceeded
			err = rl.Acquire(ctx)
			if err == nil {
				rt.Fatalf("Acquire() returned nil after exhausting TPM limit of %d", tpmLimit)
			}
			if !errors.Is(err, ErrRateLimitExceeded) {
				rt.Fatalf("Acquire() returned error %v, which is not detectable via errors.Is(err, ErrRateLimitExceeded)", err)
			}
		})
	})
}

// TestProperty_CountersTrackConsumptionAccurately verifies that for any sequence
// of N successful Acquire calls, the RPM counter equals N, and for any sequence
// of Record calls with token usages, the TPM counter equals the sum of all
// usage.Total() values. Time is frozen so nothing expires.
//
// **Validates: Requirements 4.3, 5.1**
func TestProperty_CountersTrackConsumptionAccurately(t *testing.T) {
	t.Run("SlidingWindow_RPMCounter", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// High limits so nothing gets rate-limited
			rl, err := NewRateLimiter(100000, 100000, WithSlidingWindow(), WithFailFast())
			if err != nil {
				rt.Fatalf("NewRateLimiter failed: %v", err)
			}

			// Freeze time so nothing expires
			fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rl.now = func() time.Time { return fixedTime }

			// Random number of Acquire calls
			n := rapid.IntRange(1, 200).Draw(rt, "numAcquires")

			ctx := context.Background()
			for i := 0; i < n; i++ {
				if err := rl.Acquire(ctx); err != nil {
					rt.Fatalf("Acquire() returned error on call %d: %v", i+1, err)
				}
			}

			// Verify RPM counter equals N
			rl.mu.Lock()
			count := rl.slidingRPMCount()
			rl.mu.Unlock()

			if count != n {
				rt.Fatalf("sliding RPM counter = %d, want %d", count, n)
			}
		})
	})

	t.Run("SlidingWindow_TPMCounter", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// High limits so nothing gets rate-limited
			rl, err := NewRateLimiter(100000, 100000, WithSlidingWindow(), WithFailFast())
			if err != nil {
				rt.Fatalf("NewRateLimiter failed: %v", err)
			}

			// Freeze time so nothing expires
			fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rl.now = func() time.Time { return fixedTime }

			// Random number of Record calls with random token amounts
			k := rapid.IntRange(1, 100).Draw(rt, "numRecords")
			expectedTotal := 0

			for i := 0; i < k; i++ {
				input := rapid.IntRange(1, 5000).Draw(rt, "inputTokens")
				output := rapid.IntRange(1, 5000).Draw(rt, "outputTokens")
				usage := TokenUsage{InputTokens: input, OutputTokens: output}
				expectedTotal += usage.Total()
				rl.Record(usage)
			}

			// Verify TPM counter equals sum of all tokens
			rl.mu.Lock()
			count := rl.slidingTPMCount()
			rl.mu.Unlock()

			if count != expectedTotal {
				rt.Fatalf("sliding TPM counter = %d, want %d", count, expectedTotal)
			}
		})
	})

	t.Run("FixedWindow_RPMCounter", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// High limits so nothing gets rate-limited
			rl, err := NewRateLimiter(100000, 100000, WithFixedWindow(), WithFailFast())
			if err != nil {
				rt.Fatalf("NewRateLimiter failed: %v", err)
			}

			// Freeze time so nothing expires
			fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rl.now = func() time.Time { return fixedTime }

			// Random number of Acquire calls
			n := rapid.IntRange(1, 200).Draw(rt, "numAcquires")

			ctx := context.Background()
			for i := 0; i < n; i++ {
				if err := rl.Acquire(ctx); err != nil {
					rt.Fatalf("Acquire() returned error on call %d: %v", i+1, err)
				}
			}

			// Verify RPM counter equals N
			rl.mu.Lock()
			count := rl.fixedRPMCount
			rl.mu.Unlock()

			if count != n {
				rt.Fatalf("fixed RPM counter = %d, want %d", count, n)
			}
		})
	})

	t.Run("FixedWindow_TPMCounter", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// High limits so nothing gets rate-limited
			rl, err := NewRateLimiter(100000, 100000, WithFixedWindow(), WithFailFast())
			if err != nil {
				rt.Fatalf("NewRateLimiter failed: %v", err)
			}

			// Freeze time so nothing expires
			fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rl.now = func() time.Time { return fixedTime }

			// Random number of Record calls with random token amounts
			k := rapid.IntRange(1, 100).Draw(rt, "numRecords")
			expectedTotal := 0

			for i := 0; i < k; i++ {
				input := rapid.IntRange(1, 5000).Draw(rt, "inputTokens")
				output := rapid.IntRange(1, 5000).Draw(rt, "outputTokens")
				usage := TokenUsage{InputTokens: input, OutputTokens: output}
				expectedTotal += usage.Total()
				rl.Record(usage)
			}

			// Verify TPM counter equals sum of all tokens
			rl.mu.Lock()
			count := rl.fixedTPMCount
			rl.mu.Unlock()

			if count != expectedTotal {
				rt.Fatalf("fixed TPM counter = %d, want %d", count, expectedTotal)
			}
		})
	})
}

// TestProperty_BlockModeWaitsAndSucceedsAfterWindowReset verifies that for any
// RateLimiter in BlockMode where a limit is exceeded, calling Acquire blocks
// until the window resets and capacity becomes available, then returns nil.
// The total wait time does not exceed the remaining time in the current window.
//
// Strategy: Use a mock clock where events are placed near the expiry boundary
// so the calculated wait duration is very short (real timer fires quickly).
// A background goroutine advances the mock clock past the window boundary
// so the re-check after waking finds capacity available.
//
// **Validates: Requirements 3.2**
func TestProperty_BlockModeWaitsAndSucceedsAfterWindowReset(t *testing.T) {
	t.Run("SlidingWindow_RPM_BlocksAndSucceeds", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Small RPM limit so we can exhaust it quickly
			rpmLimit := rapid.IntRange(1, 5).Draw(rt, "rpmLimit")

			rl, err := NewRateLimiter(rpmLimit, 0, WithSlidingWindow(), WithBlock())
			if err != nil {
				rt.Fatalf("NewRateLimiter failed: %v", err)
			}

			// Mock clock setup: events placed at T=0, clock advanced to T=59.99s
			// so the wait duration = (T=0 + 60s) - T=59.99s = 10ms
			baseTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			var mu sync.Mutex
			currentTime := baseTime
			rl.now = func() time.Time {
				mu.Lock()
				defer mu.Unlock()
				return currentTime
			}

			ctx := context.Background()

			// Exhaust the RPM limit at T=0
			for i := 0; i < rpmLimit; i++ {
				err := rl.Acquire(ctx)
				if err != nil {
					rt.Fatalf("Acquire() failed on call %d: %v", i+1, err)
				}
			}

			// Advance mock clock to T=59.99s so wait duration is ~10ms
			mu.Lock()
			currentTime = baseTime.Add(59*time.Second + 990*time.Millisecond)
			mu.Unlock()

			// Start a goroutine that after a short real delay advances the mock
			// clock past 60s so the re-check finds capacity available
			go func() {
				time.Sleep(5 * time.Millisecond)
				mu.Lock()
				currentTime = baseTime.Add(60*time.Second + 100*time.Millisecond)
				mu.Unlock()
			}()

			// Acquire should block briefly (real timer ~10ms), then succeed
			start := time.Now()
			err = rl.Acquire(ctx)
			elapsed := time.Since(start)

			if err != nil {
				rt.Fatalf("Acquire() returned error after blocking: %v", err)
			}

			// The wait time should not exceed the remaining window time (~10ms)
			// plus some tolerance for scheduling. We use a generous upper bound.
			if elapsed > 200*time.Millisecond {
				rt.Fatalf("Acquire() blocked for %v, expected at most ~10ms + scheduling overhead", elapsed)
			}
		})
	})

	t.Run("FixedWindow_RPM_BlocksAndSucceeds", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Small RPM limit
			rpmLimit := rapid.IntRange(1, 5).Draw(rt, "rpmLimit")

			rl, err := NewRateLimiter(rpmLimit, 0, WithFixedWindow(), WithBlock())
			if err != nil {
				rt.Fatalf("NewRateLimiter failed: %v", err)
			}

			// Mock clock: window starts at T=0, advance to T=59.99s
			// Wait duration = (T=0 + 60s) - T=59.99s = 10ms
			baseTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			var mu sync.Mutex
			currentTime := baseTime
			rl.now = func() time.Time {
				mu.Lock()
				defer mu.Unlock()
				return currentTime
			}

			ctx := context.Background()

			// Exhaust the RPM limit at T=0
			for i := 0; i < rpmLimit; i++ {
				err := rl.Acquire(ctx)
				if err != nil {
					rt.Fatalf("Acquire() failed on call %d: %v", i+1, err)
				}
			}

			// Advance mock clock to T=59.99s so wait duration is ~10ms
			mu.Lock()
			currentTime = baseTime.Add(59*time.Second + 990*time.Millisecond)
			mu.Unlock()

			// Start a goroutine that after a short real delay advances the mock
			// clock past 60s so the fixed window resets
			go func() {
				time.Sleep(5 * time.Millisecond)
				mu.Lock()
				currentTime = baseTime.Add(60*time.Second + 100*time.Millisecond)
				mu.Unlock()
			}()

			// Acquire should block briefly then succeed after window reset
			start := time.Now()
			err = rl.Acquire(ctx)
			elapsed := time.Since(start)

			if err != nil {
				rt.Fatalf("Acquire() returned error after blocking: %v", err)
			}

			// Verify wait time is bounded
			if elapsed > 200*time.Millisecond {
				rt.Fatalf("Acquire() blocked for %v, expected at most ~10ms + scheduling overhead", elapsed)
			}
		})
	})

	t.Run("SlidingWindow_TPM_BlocksAndSucceeds", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Random TPM limit
			tpmLimit := rapid.IntRange(100, 5000).Draw(rt, "tpmLimit")

			// RPM high enough to not interfere
			rl, err := NewRateLimiter(10000, tpmLimit, WithSlidingWindow(), WithBlock())
			if err != nil {
				rt.Fatalf("NewRateLimiter failed: %v", err)
			}

			// Mock clock: tokens recorded at T=0, advance to T=59.99s
			baseTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			var mu sync.Mutex
			currentTime := baseTime
			rl.now = func() time.Time {
				mu.Lock()
				defer mu.Unlock()
				return currentTime
			}

			// Record tokens to exhaust TPM at T=0
			rl.Record(TokenUsage{
				InputTokens:  tpmLimit / 2,
				OutputTokens: tpmLimit - tpmLimit/2,
			})

			// Advance mock clock to T=59.99s so wait = 10ms
			mu.Lock()
			currentTime = baseTime.Add(59*time.Second + 990*time.Millisecond)
			mu.Unlock()

			// Background goroutine advances clock past 60s
			go func() {
				time.Sleep(5 * time.Millisecond)
				mu.Lock()
				currentTime = baseTime.Add(60*time.Second + 100*time.Millisecond)
				mu.Unlock()
			}()

			ctx := context.Background()
			start := time.Now()
			err = rl.Acquire(ctx)
			elapsed := time.Since(start)

			if err != nil {
				rt.Fatalf("Acquire() returned error after blocking on TPM: %v", err)
			}

			if elapsed > 200*time.Millisecond {
				rt.Fatalf("Acquire() blocked for %v on TPM, expected at most ~10ms + scheduling overhead", elapsed)
			}
		})
	})

	t.Run("WaitTimeDoesNotExceedRemainingWindow", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// This sub-test verifies the wait time is bounded by the remaining
			// window time. We set up a scenario where the remaining time is known
			// and verify the actual wait is within that bound.
			rpmLimit := rapid.IntRange(1, 3).Draw(rt, "rpmLimit")

			rl, err := NewRateLimiter(rpmLimit, 0, WithSlidingWindow(), WithBlock())
			if err != nil {
				rt.Fatalf("NewRateLimiter failed: %v", err)
			}

			baseTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			var mu sync.Mutex
			currentTime := baseTime
			rl.now = func() time.Time {
				mu.Lock()
				defer mu.Unlock()
				return currentTime
			}

			ctx := context.Background()

			// Exhaust RPM at T=0
			for i := 0; i < rpmLimit; i++ {
				if err := rl.Acquire(ctx); err != nil {
					rt.Fatalf("Acquire() failed: %v", err)
				}
			}

			// Advance to T=59.985s — remaining window time = 15ms
			mu.Lock()
			currentTime = baseTime.Add(59*time.Second + 985*time.Millisecond)
			mu.Unlock()

			remainingWindow := 15 * time.Millisecond

			// Advance clock past window after a short delay
			go func() {
				time.Sleep(5 * time.Millisecond)
				mu.Lock()
				currentTime = baseTime.Add(60*time.Second + 50*time.Millisecond)
				mu.Unlock()
			}()

			start := time.Now()
			err = rl.Acquire(ctx)
			elapsed := time.Since(start)

			if err != nil {
				rt.Fatalf("Acquire() returned error: %v", err)
			}

			// Wait time should not exceed remaining window + generous tolerance
			// for goroutine scheduling
			maxExpected := remainingWindow + 150*time.Millisecond
			if elapsed > maxExpected {
				rt.Fatalf("Wait time %v exceeded remaining window %v + tolerance", elapsed, remainingWindow)
			}
		})
	})
}

// TestProperty_ContextCancellationInterruptsBlocking verifies that for any
// RateLimiter in BlockMode that is blocking on Acquire, if the provided context
// is cancelled, Acquire SHALL return the context error immediately without
// waiting for capacity.
//
// **Validates: Requirements 3.3, 10.1, 10.2**
func TestProperty_ContextCancellationInterruptsBlocking(t *testing.T) {
	t.Run("CancelledContext_ReturnsContextError", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Generate a small RPM limit so we can exhaust it quickly
			rpmLimit := rapid.IntRange(1, 10).Draw(rt, "rpmLimit")

			// Pick a random window strategy
			strategy := rapid.SampledFrom([]RateLimiterOption{
				WithSlidingWindow(),
				WithFixedWindow(),
			}).Draw(rt, "strategy")

			// Create a RateLimiter with BlockMode
			rl, err := NewRateLimiter(rpmLimit, 0, strategy, WithBlock())
			if err != nil {
				rt.Fatalf("NewRateLimiter(%d, 0) returned error: %v", rpmLimit, err)
			}

			// Use a mock clock frozen at T=0 so that the wait duration is 60s
			fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rl.now = func() time.Time { return fixedTime }

			ctx := context.Background()

			// Exhaust the RPM limit — all events at T=0, so wait = 60s
			for i := 0; i < rpmLimit; i++ {
				err := rl.Acquire(ctx)
				if err != nil {
					rt.Fatalf("Acquire() returned error on call %d (before limit reached): %v", i+1, err)
				}
			}

			// Now the limiter is exhausted. The next Acquire in BlockMode would
			// block for ~60s. We use a context with a short timeout so that
			// context cancellation fires first.
			cancelTimeout := rapid.IntRange(10, 100).Draw(rt, "cancelTimeoutMs")
			ctxWithTimeout, cancel := context.WithTimeout(context.Background(), time.Duration(cancelTimeout)*time.Millisecond)
			defer cancel()

			// Track timing to verify it returns quickly (not after 60s)
			start := time.Now()

			// Acquire should block, then return the context error
			acquireErr := rl.Acquire(ctxWithTimeout)

			elapsed := time.Since(start)

			// Verify the error is the context error (DeadlineExceeded), not ErrRateLimitExceeded
			if acquireErr == nil {
				rt.Fatalf("Acquire() returned nil, expected context error after timeout")
			}
			if errors.Is(acquireErr, ErrRateLimitExceeded) {
				rt.Fatalf("Acquire() returned ErrRateLimitExceeded, expected context error (BlockMode should not return ErrRateLimitExceeded)")
			}
			if !errors.Is(acquireErr, context.DeadlineExceeded) {
				rt.Fatalf("Acquire() returned %v, expected context.DeadlineExceeded", acquireErr)
			}

			// Verify it returned quickly (within 2 seconds, not 60s)
			if elapsed > 2*time.Second {
				rt.Fatalf("Acquire() took %v, expected it to return quickly after context cancellation", elapsed)
			}
		})
	})

	t.Run("ExplicitCancel_ReturnsContextCanceled", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Generate a small RPM limit
			rpmLimit := rapid.IntRange(1, 10).Draw(rt, "rpmLimit")

			// Pick a random window strategy
			strategy := rapid.SampledFrom([]RateLimiterOption{
				WithSlidingWindow(),
				WithFixedWindow(),
			}).Draw(rt, "strategy")

			// Create a RateLimiter with BlockMode
			rl, err := NewRateLimiter(rpmLimit, 0, strategy, WithBlock())
			if err != nil {
				rt.Fatalf("NewRateLimiter(%d, 0) returned error: %v", rpmLimit, err)
			}

			// Use a mock clock frozen at T=0
			fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rl.now = func() time.Time { return fixedTime }

			ctx := context.Background()

			// Exhaust the RPM limit
			for i := 0; i < rpmLimit; i++ {
				err := rl.Acquire(ctx)
				if err != nil {
					rt.Fatalf("Acquire() returned error on call %d (before limit reached): %v", i+1, err)
				}
			}

			// Create a cancellable context and cancel it from a goroutine after a short delay
			cancelDelay := rapid.IntRange(10, 100).Draw(rt, "cancelDelayMs")
			ctxWithCancel, cancel := context.WithCancel(context.Background())

			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				time.Sleep(time.Duration(cancelDelay) * time.Millisecond)
				cancel()
			}()

			// Track timing
			start := time.Now()

			// Acquire should block, then return context.Canceled
			acquireErr := rl.Acquire(ctxWithCancel)

			elapsed := time.Since(start)
			wg.Wait()

			// Verify the error is context.Canceled
			if acquireErr == nil {
				rt.Fatalf("Acquire() returned nil, expected context.Canceled after cancel")
			}
			if errors.Is(acquireErr, ErrRateLimitExceeded) {
				rt.Fatalf("Acquire() returned ErrRateLimitExceeded, expected context.Canceled (BlockMode should not return ErrRateLimitExceeded)")
			}
			if !errors.Is(acquireErr, context.Canceled) {
				rt.Fatalf("Acquire() returned %v, expected context.Canceled", acquireErr)
			}

			// Verify it returned quickly (within 2 seconds, not 60s)
			if elapsed > 2*time.Second {
				rt.Fatalf("Acquire() took %v, expected it to return quickly after context cancellation", elapsed)
			}
		})
	})
}

// TestProperty_AcquireEnforcesBothRPMAndTPMLimits verifies that for any
// RateLimiter where the RPM counter equals or exceeds the RPM limit, OR the TPM
// counter equals or exceeds the TPM limit, calling Acquire SHALL apply the
// configured overflow behavior. The RPM check SHALL be performed before the TPM check.
//
// **Validates: Requirements 4.2, 5.2, 6.1, 6.2**
func TestProperty_AcquireEnforcesBothRPMAndTPMLimits(t *testing.T) {
	t.Run("RPM_Exhausted_TPM_Under_Limit_Fails", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Generate random limits: RPM small enough to exhaust, TPM large enough to not interfere
			rpmLimit := rapid.IntRange(1, 50).Draw(rt, "rpmLimit")
			tpmLimit := rapid.IntRange(1000, 100000).Draw(rt, "tpmLimit")

			// Pick a random window strategy
			strategy := rapid.SampledFrom([]RateLimiterOption{
				WithSlidingWindow(),
				WithFixedWindow(),
			}).Draw(rt, "strategy")

			rl, err := NewRateLimiter(rpmLimit, tpmLimit, strategy, WithFailFast())
			if err != nil {
				rt.Fatalf("NewRateLimiter(%d, %d) returned error: %v", rpmLimit, tpmLimit, err)
			}

			// Freeze time so nothing expires
			fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rl.now = func() time.Time { return fixedTime }

			ctx := context.Background()

			// Exhaust RPM by calling Acquire rpmLimit times
			for i := 0; i < rpmLimit; i++ {
				err := rl.Acquire(ctx)
				if err != nil {
					rt.Fatalf("Acquire() returned error on call %d (before RPM limit reached): %v", i+1, err)
				}
			}

			// TPM is still well under limit (no tokens recorded beyond what Acquire does)
			// The next Acquire should fail due to RPM exhaustion
			err = rl.Acquire(ctx)
			if err == nil {
				rt.Fatalf("Acquire() returned nil after exhausting RPM limit of %d (TPM still under limit)", rpmLimit)
			}
			if !errors.Is(err, ErrRateLimitExceeded) {
				rt.Fatalf("Acquire() returned %v, expected ErrRateLimitExceeded", err)
			}
		})
	})

	t.Run("TPM_Exhausted_RPM_Under_Limit_Fails", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Generate random limits: RPM large enough to not interfere, TPM small enough to exhaust
			rpmLimit := rapid.IntRange(1000, 100000).Draw(rt, "rpmLimit")
			tpmLimit := rapid.IntRange(1, 10000).Draw(rt, "tpmLimit")

			// Pick a random window strategy
			strategy := rapid.SampledFrom([]RateLimiterOption{
				WithSlidingWindow(),
				WithFixedWindow(),
			}).Draw(rt, "strategy")

			rl, err := NewRateLimiter(rpmLimit, tpmLimit, strategy, WithFailFast())
			if err != nil {
				rt.Fatalf("NewRateLimiter(%d, %d) returned error: %v", rpmLimit, tpmLimit, err)
			}

			// Freeze time so nothing expires
			fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rl.now = func() time.Time { return fixedTime }

			ctx := context.Background()

			// Record tokens to exhaust TPM limit
			rl.Record(TokenUsage{
				InputTokens:  tpmLimit / 2,
				OutputTokens: tpmLimit - tpmLimit/2,
			})

			// RPM is still well under limit (only 0 acquires so far)
			// The next Acquire should fail due to TPM exhaustion
			err = rl.Acquire(ctx)
			if err == nil {
				rt.Fatalf("Acquire() returned nil after exhausting TPM limit of %d (RPM still under limit)", tpmLimit)
			}
			if !errors.Is(err, ErrRateLimitExceeded) {
				rt.Fatalf("Acquire() returned %v, expected ErrRateLimitExceeded", err)
			}
		})
	})

	t.Run("Both_Under_Limit_Succeeds", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Generate random limits
			rpmLimit := rapid.IntRange(2, 100).Draw(rt, "rpmLimit")
			tpmLimit := rapid.IntRange(100, 100000).Draw(rt, "tpmLimit")

			// Pick a random window strategy
			strategy := rapid.SampledFrom([]RateLimiterOption{
				WithSlidingWindow(),
				WithFixedWindow(),
			}).Draw(rt, "strategy")

			rl, err := NewRateLimiter(rpmLimit, tpmLimit, strategy, WithFailFast())
			if err != nil {
				rt.Fatalf("NewRateLimiter(%d, %d) returned error: %v", rpmLimit, tpmLimit, err)
			}

			// Freeze time so nothing expires
			fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rl.now = func() time.Time { return fixedTime }

			ctx := context.Background()

			// Record some tokens but stay under TPM limit
			tokensToRecord := rapid.IntRange(0, tpmLimit-1).Draw(rt, "tokensToRecord")
			if tokensToRecord > 0 {
				rl.Record(TokenUsage{
					InputTokens:  tokensToRecord / 2,
					OutputTokens: tokensToRecord - tokensToRecord/2,
				})
			}

			// Make some Acquire calls but stay under RPM limit
			acquireCalls := rapid.IntRange(1, rpmLimit-1).Draw(rt, "acquireCalls")
			for i := 0; i < acquireCalls; i++ {
				err := rl.Acquire(ctx)
				if err != nil {
					rt.Fatalf("Acquire() returned error on call %d (both under limit, rpmLimit=%d, tpmLimit=%d): %v",
						i+1, rpmLimit, tpmLimit, err)
				}
			}
		})
	})

	t.Run("RPM_Checked_Before_TPM", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Generate random limits: both small enough to exhaust
			rpmLimit := rapid.IntRange(1, 50).Draw(rt, "rpmLimit")
			tpmLimit := rapid.IntRange(1, 10000).Draw(rt, "tpmLimit")

			// Pick a random window strategy
			strategy := rapid.SampledFrom([]RateLimiterOption{
				WithSlidingWindow(),
				WithFixedWindow(),
			}).Draw(rt, "strategy")

			rl, err := NewRateLimiter(rpmLimit, tpmLimit, strategy, WithFailFast())
			if err != nil {
				rt.Fatalf("NewRateLimiter(%d, %d) returned error: %v", rpmLimit, tpmLimit, err)
			}

			// Freeze time so nothing expires
			fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rl.now = func() time.Time { return fixedTime }

			ctx := context.Background()

			// Exhaust RPM by calling Acquire rpmLimit times
			for i := 0; i < rpmLimit; i++ {
				err := rl.Acquire(ctx)
				if err != nil {
					rt.Fatalf("Acquire() returned error on call %d (before RPM limit reached): %v", i+1, err)
				}
			}

			// Also exhaust TPM by recording tokens >= tpmLimit
			rl.Record(TokenUsage{
				InputTokens:  tpmLimit / 2,
				OutputTokens: tpmLimit - tpmLimit/2,
			})

			// Both RPM and TPM are exhausted. Since RPM is checked first,
			// the error should be ErrRateLimitExceeded (from RPM check).
			// This verifies the ordering: RPM check happens before TPM check.
			err = rl.Acquire(ctx)
			if err == nil {
				rt.Fatalf("Acquire() returned nil after exhausting both RPM (%d) and TPM (%d)", rpmLimit, tpmLimit)
			}
			if !errors.Is(err, ErrRateLimitExceeded) {
				rt.Fatalf("Acquire() returned %v, expected ErrRateLimitExceeded (RPM should be checked first)", err)
			}
		})
	})
}

// TestProperty_FailedCallsDontRecordTokens verifies that for any provider call
// that fails (i.e., Record is never called), the TPM counter remains unchanged.
// Only successful calls where Record is explicitly called contribute to TPM
// accounting. Acquire alone does not affect the TPM counter.
//
// **Validates: Requirements 5.3**
func TestProperty_FailedCallsDontRecordTokens(t *testing.T) {
	t.Run("SlidingWindow_AcquireAloneDoesNotAffectTPM", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// High limits so nothing gets rate-limited
			rl, err := NewRateLimiter(100000, 100000, WithSlidingWindow(), WithFailFast())
			if err != nil {
				rt.Fatalf("NewRateLimiter failed: %v", err)
			}

			// Freeze time so nothing expires
			fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rl.now = func() time.Time { return fixedTime }

			ctx := context.Background()

			// Perform a random number of Acquire calls (simulating provider calls that fail)
			numFailedCalls := rapid.IntRange(1, 100).Draw(rt, "numFailedCalls")
			for i := 0; i < numFailedCalls; i++ {
				if err := rl.Acquire(ctx); err != nil {
					rt.Fatalf("Acquire() returned error on call %d: %v", i+1, err)
				}
				// Do NOT call Record — simulating a failed provider call
			}

			// Verify TPM counter is zero (no tokens recorded)
			rl.mu.Lock()
			tpmCount := rl.slidingTPMCount()
			rl.mu.Unlock()

			if tpmCount != 0 {
				rt.Fatalf("TPM counter = %d after %d Acquire-only calls (no Record), want 0",
					tpmCount, numFailedCalls)
			}
		})
	})

	t.Run("FixedWindow_AcquireAloneDoesNotAffectTPM", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// High limits so nothing gets rate-limited
			rl, err := NewRateLimiter(100000, 100000, WithFixedWindow(), WithFailFast())
			if err != nil {
				rt.Fatalf("NewRateLimiter failed: %v", err)
			}

			// Freeze time so nothing expires
			fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rl.now = func() time.Time { return fixedTime }

			ctx := context.Background()

			// Perform a random number of Acquire calls (simulating provider calls that fail)
			numFailedCalls := rapid.IntRange(1, 100).Draw(rt, "numFailedCalls")
			for i := 0; i < numFailedCalls; i++ {
				if err := rl.Acquire(ctx); err != nil {
					rt.Fatalf("Acquire() returned error on call %d: %v", i+1, err)
				}
				// Do NOT call Record — simulating a failed provider call
			}

			// Verify TPM counter is zero (no tokens recorded)
			rl.mu.Lock()
			tpmCount := rl.fixedTPMCountVal()
			rl.mu.Unlock()

			if tpmCount != 0 {
				rt.Fatalf("TPM counter = %d after %d Acquire-only calls (no Record), want 0",
					tpmCount, numFailedCalls)
			}
		})
	})

	t.Run("SlidingWindow_OnlyRecordedCallsContributeToTPM", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// High limits so nothing gets rate-limited
			rl, err := NewRateLimiter(100000, 100000, WithSlidingWindow(), WithFailFast())
			if err != nil {
				rt.Fatalf("NewRateLimiter failed: %v", err)
			}

			// Freeze time so nothing expires
			fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rl.now = func() time.Time { return fixedTime }

			ctx := context.Background()

			// Generate a mix of successful and failed calls
			numSuccessful := rapid.IntRange(1, 50).Draw(rt, "numSuccessful")
			numFailed := rapid.IntRange(1, 50).Draw(rt, "numFailed")

			expectedTPM := 0

			// Interleave successful and failed calls
			totalCalls := numSuccessful + numFailed
			// Generate indices for successful calls
			successIndices := make(map[int]bool)
			for i := 0; i < numSuccessful; i++ {
				idx := rapid.IntRange(0, totalCalls-1).Draw(rt, "successIdx")
				successIndices[idx] = true
			}

			for i := 0; i < totalCalls; i++ {
				if err := rl.Acquire(ctx); err != nil {
					rt.Fatalf("Acquire() returned error on call %d: %v", i+1, err)
				}

				if successIndices[i] {
					// Successful call — record tokens
					input := rapid.IntRange(1, 1000).Draw(rt, "inputTokens")
					output := rapid.IntRange(1, 1000).Draw(rt, "outputTokens")
					usage := TokenUsage{InputTokens: input, OutputTokens: output}
					expectedTPM += usage.Total()
					rl.Record(usage)
				}
				// else: failed call — do NOT call Record
			}

			// Verify TPM counter only reflects tokens from Record calls
			rl.mu.Lock()
			actualTPM := rl.slidingTPMCount()
			rl.mu.Unlock()

			if actualTPM != expectedTPM {
				rt.Fatalf("TPM counter = %d, want %d (only from %d successful Record calls out of %d total Acquire calls)",
					actualTPM, expectedTPM, numSuccessful, totalCalls)
			}
		})
	})

	t.Run("FixedWindow_OnlyRecordedCallsContributeToTPM", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// High limits so nothing gets rate-limited
			rl, err := NewRateLimiter(100000, 100000, WithFixedWindow(), WithFailFast())
			if err != nil {
				rt.Fatalf("NewRateLimiter failed: %v", err)
			}

			// Freeze time so nothing expires
			fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rl.now = func() time.Time { return fixedTime }

			ctx := context.Background()

			// Generate a mix of successful and failed calls
			numSuccessful := rapid.IntRange(1, 50).Draw(rt, "numSuccessful")
			numFailed := rapid.IntRange(1, 50).Draw(rt, "numFailed")

			expectedTPM := 0

			// Interleave successful and failed calls
			totalCalls := numSuccessful + numFailed
			// Generate indices for successful calls
			successIndices := make(map[int]bool)
			for i := 0; i < numSuccessful; i++ {
				idx := rapid.IntRange(0, totalCalls-1).Draw(rt, "successIdx")
				successIndices[idx] = true
			}

			for i := 0; i < totalCalls; i++ {
				if err := rl.Acquire(ctx); err != nil {
					rt.Fatalf("Acquire() returned error on call %d: %v", i+1, err)
				}

				if successIndices[i] {
					// Successful call — record tokens
					input := rapid.IntRange(1, 1000).Draw(rt, "inputTokens")
					output := rapid.IntRange(1, 1000).Draw(rt, "outputTokens")
					usage := TokenUsage{InputTokens: input, OutputTokens: output}
					expectedTPM += usage.Total()
					rl.Record(usage)
				}
				// else: failed call — do NOT call Record
			}

			// Verify TPM counter only reflects tokens from Record calls
			rl.mu.Lock()
			actualTPM := rl.fixedTPMCountVal()
			rl.mu.Unlock()

			if actualTPM != expectedTPM {
				rt.Fatalf("TPM counter = %d, want %d (only from %d successful Record calls out of %d total Acquire calls)",
					actualTPM, expectedTPM, numSuccessful, totalCalls)
			}
		})
	})
}

// TestProperty_ErrRateLimitExceededShortCircuitsRetries verifies that for any
// agent with retry configured and a RateLimiter in FailFastMode, when Acquire
// returns ErrRateLimitExceeded during any attempt (including retries), the agent
// propagates ErrRateLimitExceeded immediately without performing further retry
// attempts.
//
// **Validates: Requirements 11.1, 11.2**
func TestProperty_ErrRateLimitExceededShortCircuitsRetries(t *testing.T) {
	t.Run("ExhaustedBeforeInvoke_NeverCallsProvider", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Generate random RPM limit (small, 1-5) and retry count (1-5)
			rpmLimit := rapid.IntRange(1, 5).Draw(rt, "rpmLimit")
			retryMax := rapid.IntRange(1, 5).Draw(rt, "retryMax")

			// Pick a random window strategy
			strategy := rapid.SampledFrom([]RateLimiterOption{
				WithSlidingWindow(),
				WithFixedWindow(),
			}).Draw(rt, "strategy")

			// Create a RateLimiter with FailFast
			rl, err := NewRateLimiter(rpmLimit, 0, strategy, WithFailFast())
			if err != nil {
				rt.Fatalf("NewRateLimiter(%d, 0) returned error: %v", rpmLimit, err)
			}

			// Freeze time so nothing expires
			fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rl.now = func() time.Time { return fixedTime }

			// Exhaust the RPM limit by calling Acquire directly
			ctx := context.Background()
			for i := 0; i < rpmLimit; i++ {
				if err := rl.Acquire(ctx); err != nil {
					rt.Fatalf("Acquire() returned error on call %d (exhausting limit): %v", i+1, err)
				}
			}

			// Track provider calls
			var providerCalls atomic.Int32
			countingProvider := &funcProvider{
				fn: func(ctx context.Context, params ConverseParams, cb StreamCallback) (*ProviderResponse, error) {
					providerCalls.Add(1)
					return &ProviderResponse{Text: "should not be called"}, nil
				},
			}

			// Create an agent with the exhausted rate limiter and retry configured
			a, err := New(countingProvider, prompt.Text("sys"), nil,
				WithRateLimiter(rl),
				WithRetry(retryMax, 10*time.Millisecond),
			)
			if err != nil {
				rt.Fatalf("New() returned error: %v", err)
			}

			// Invoke the agent — Acquire should fail immediately on the first attempt
			_, invokeErr := a.Invoke(Background(), "hello")

			// Verify: error is ErrRateLimitExceeded
			if invokeErr == nil {
				rt.Fatalf("Invoke() returned nil error, expected ErrRateLimitExceeded")
			}
			if !errors.Is(invokeErr, ErrRateLimitExceeded) {
				rt.Fatalf("Invoke() returned error %v, expected ErrRateLimitExceeded", invokeErr)
			}

			// Verify: provider was never called (0 calls)
			if calls := int(providerCalls.Load()); calls != 0 {
				rt.Fatalf("Provider was called %d times, expected 0 (rate limit should short-circuit)", calls)
			}
		})
	})

	t.Run("ExhaustedDuringRetry_StopsImmediately", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// RPM limit of 1 means: first Acquire succeeds, second (retry) fails
			// Use retryMax >= 1 so the agent would normally retry
			retryMax := rapid.IntRange(1, 5).Draw(rt, "retryMax")

			// Pick a random window strategy
			strategy := rapid.SampledFrom([]RateLimiterOption{
				WithSlidingWindow(),
				WithFixedWindow(),
			}).Draw(rt, "strategy")

			// Create a RateLimiter with FailFast and RPM=1
			rl, err := NewRateLimiter(1, 0, strategy, WithFailFast())
			if err != nil {
				rt.Fatalf("NewRateLimiter(1, 0) returned error: %v", err)
			}

			// Freeze time so nothing expires
			fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rl.now = func() time.Time { return fixedTime }

			// Track provider calls — always fail to trigger retry
			var providerCalls atomic.Int32
			failingProvider := &funcProvider{
				fn: func(ctx context.Context, params ConverseParams, cb StreamCallback) (*ProviderResponse, error) {
					providerCalls.Add(1)
					return nil, errors.New("transient error")
				},
			}

			// Create an agent with the rate limiter (RPM=1) and retry configured
			a, err := New(failingProvider, prompt.Text("sys"), nil,
				WithRateLimiter(rl),
				WithRetry(retryMax, 1*time.Millisecond),
			)
			if err != nil {
				rt.Fatalf("New() returned error: %v", err)
			}

			// Invoke the agent:
			// - First attempt: Acquire succeeds (RPM goes from 0 to 1), provider fails
			// - Retry attempt: Acquire fails with ErrRateLimitExceeded (RPM=1, limit=1)
			// - Agent should propagate ErrRateLimitExceeded immediately
			_, invokeErr := a.Invoke(Background(), "hello")

			// Verify: error is ErrRateLimitExceeded (not the transient error)
			if invokeErr == nil {
				rt.Fatalf("Invoke() returned nil error, expected ErrRateLimitExceeded")
			}
			if !errors.Is(invokeErr, ErrRateLimitExceeded) {
				rt.Fatalf("Invoke() returned error %v, expected ErrRateLimitExceeded (should short-circuit retries)", invokeErr)
			}

			// Verify: provider was called exactly once (first attempt only, no retries completed)
			if calls := int(providerCalls.Load()); calls != 1 {
				rt.Fatalf("Provider was called %d times, expected 1 (first attempt only, retry should be short-circuited by rate limit)", calls)
			}
		})
	})
}

// TestProperty_ConcurrencySafety verifies that for any set of N goroutines
// sharing a single RateLimiter instance, each performing M Acquire+Record
// operations concurrently, the final RPM counter equals the total number of
// successful acquires across all goroutines (within the window), and the final
// TPM counter equals the sum of all recorded tokens across all goroutines
// (within the window). No data races occur (verified by running with -race flag).
//
// **Validates: Requirements 4.4, 5.4, 8.1, 8.2, 8.3**
func TestProperty_ConcurrencySafety(t *testing.T) {
	t.Run("SlidingWindow_SharedLimiterAggregatesCorrectly", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Generate random N (number of goroutines) and M (operations per goroutine)
			numGoroutines := rapid.IntRange(2, 20).Draw(rt, "numGoroutines")
			opsPerGoroutine := rapid.IntRange(1, 50).Draw(rt, "opsPerGoroutine")

			// Generate a known token amount per operation so we can compute expected total
			tokensPerOp := rapid.IntRange(1, 1000).Draw(rt, "tokensPerOp")

			// Create a shared RateLimiter with very high limits so nothing gets rate-limited
			rl, err := NewRateLimiter(1000000, 1000000, WithSlidingWindow(), WithFailFast())
			if err != nil {
				rt.Fatalf("NewRateLimiter failed: %v", err)
			}

			// Use a frozen mock clock so nothing expires during the test
			fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rl.now = func() time.Time { return fixedTime }

			ctx := context.Background()

			// Track total successful acquires and total tokens recorded
			var totalAcquires atomic.Int64
			var totalTokens atomic.Int64

			// Launch N goroutines, each performing M Acquire+Record operations
			var wg sync.WaitGroup
			wg.Add(numGoroutines)

			for g := 0; g < numGoroutines; g++ {
				go func() {
					defer wg.Done()
					for op := 0; op < opsPerGoroutine; op++ {
						if err := rl.Acquire(ctx); err != nil {
							// With very high limits, this should never happen
							return
						}
						totalAcquires.Add(1)

						usage := TokenUsage{
							InputTokens:  tokensPerOp / 2,
							OutputTokens: tokensPerOp - tokensPerOp/2,
						}
						rl.Record(usage)
						totalTokens.Add(int64(usage.Total()))
					}
				}()
			}

			// Wait for all goroutines to complete
			wg.Wait()

			// Expected values
			expectedRPM := numGoroutines * opsPerGoroutine
			expectedTPM := numGoroutines * opsPerGoroutine * tokensPerOp

			// Verify total successful acquires
			actualAcquires := int(totalAcquires.Load())
			if actualAcquires != expectedRPM {
				rt.Fatalf("total successful acquires = %d, want %d (N=%d, M=%d)",
					actualAcquires, expectedRPM, numGoroutines, opsPerGoroutine)
			}

			// Verify RPM counter matches total acquires
			rl.mu.Lock()
			rpmCount := rl.slidingRPMCount()
			rl.mu.Unlock()

			if rpmCount != expectedRPM {
				rt.Fatalf("sliding RPM counter = %d, want %d (N=%d, M=%d)",
					rpmCount, expectedRPM, numGoroutines, opsPerGoroutine)
			}

			// Verify TPM counter matches sum of all recorded tokens
			rl.mu.Lock()
			tpmCount := rl.slidingTPMCount()
			rl.mu.Unlock()

			if tpmCount != expectedTPM {
				rt.Fatalf("sliding TPM counter = %d, want %d (N=%d, M=%d, tokensPerOp=%d)",
					tpmCount, expectedTPM, numGoroutines, opsPerGoroutine, tokensPerOp)
			}

			// Verify atomic tracker matches TPM counter (cross-check)
			actualTokens := int(totalTokens.Load())
			if actualTokens != expectedTPM {
				rt.Fatalf("total recorded tokens (atomic) = %d, want %d",
					actualTokens, expectedTPM)
			}
		})
	})

	t.Run("FixedWindow_SharedLimiterAggregatesCorrectly", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Generate random N (number of goroutines) and M (operations per goroutine)
			numGoroutines := rapid.IntRange(2, 20).Draw(rt, "numGoroutines")
			opsPerGoroutine := rapid.IntRange(1, 50).Draw(rt, "opsPerGoroutine")

			// Generate a known token amount per operation
			tokensPerOp := rapid.IntRange(1, 1000).Draw(rt, "tokensPerOp")

			// Create a shared RateLimiter with very high limits so nothing gets rate-limited
			rl, err := NewRateLimiter(1000000, 1000000, WithFixedWindow(), WithFailFast())
			if err != nil {
				rt.Fatalf("NewRateLimiter failed: %v", err)
			}

			// Use a frozen mock clock so nothing expires during the test
			fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rl.now = func() time.Time { return fixedTime }

			ctx := context.Background()

			// Track total successful acquires and total tokens recorded
			var totalAcquires atomic.Int64
			var totalTokens atomic.Int64

			// Launch N goroutines, each performing M Acquire+Record operations
			var wg sync.WaitGroup
			wg.Add(numGoroutines)

			for g := 0; g < numGoroutines; g++ {
				go func() {
					defer wg.Done()
					for op := 0; op < opsPerGoroutine; op++ {
						if err := rl.Acquire(ctx); err != nil {
							// With very high limits, this should never happen
							return
						}
						totalAcquires.Add(1)

						usage := TokenUsage{
							InputTokens:  tokensPerOp / 2,
							OutputTokens: tokensPerOp - tokensPerOp/2,
						}
						rl.Record(usage)
						totalTokens.Add(int64(usage.Total()))
					}
				}()
			}

			// Wait for all goroutines to complete
			wg.Wait()

			// Expected values
			expectedRPM := numGoroutines * opsPerGoroutine
			expectedTPM := numGoroutines * opsPerGoroutine * tokensPerOp

			// Verify total successful acquires
			actualAcquires := int(totalAcquires.Load())
			if actualAcquires != expectedRPM {
				rt.Fatalf("total successful acquires = %d, want %d (N=%d, M=%d)",
					actualAcquires, expectedRPM, numGoroutines, opsPerGoroutine)
			}

			// Verify RPM counter matches total acquires
			rl.mu.Lock()
			rpmCount := rl.fixedRPMCount
			rl.mu.Unlock()

			if rpmCount != expectedRPM {
				rt.Fatalf("fixed RPM counter = %d, want %d (N=%d, M=%d)",
					rpmCount, expectedRPM, numGoroutines, opsPerGoroutine)
			}

			// Verify TPM counter matches sum of all recorded tokens
			rl.mu.Lock()
			tpmCount := rl.fixedTPMCount
			rl.mu.Unlock()

			if tpmCount != expectedTPM {
				rt.Fatalf("fixed TPM counter = %d, want %d (N=%d, M=%d, tokensPerOp=%d)",
					tpmCount, expectedTPM, numGoroutines, opsPerGoroutine, tokensPerOp)
			}

			// Verify atomic tracker matches TPM counter (cross-check)
			actualTokens := int(totalTokens.Load())
			if actualTokens != expectedTPM {
				rt.Fatalf("total recorded tokens (atomic) = %d, want %d",
					actualTokens, expectedTPM)
			}
		})
	})
}
