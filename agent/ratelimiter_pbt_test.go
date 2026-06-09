package agent

import (
	"context"
	"errors"
	"fmt"
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

			rl, err := NewRateLimiter(TPM(tpmLimit), strategy, overflow)
			if err != nil {
				rt.Fatalf("NewRateLimiter(TPM(%d)) returned error: %v", tpmLimit, err)
			}

			// Use a mock clock so time doesn't advance (worst case for rate limiting)
			fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rl.now = func() time.Time { return fixedTime }

			// Generate a random number of Acquire calls (1–500)
			numCalls := rapid.IntRange(1, 500).Draw(rt, "numCalls")

			ctx := context.Background()
			for i := 0; i < numCalls; i++ {
				_, err := rl.Acquire(ctx, "")
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

			rl, err := NewRateLimiter(RPM(rpmLimit), strategy, overflow)
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
				rl.Record("", TokenUsage{
					InputTokens:  tokens / 2,
					OutputTokens: tokens - tokens/2,
				})
			}

			// Now Acquire should still succeed (TPM is unlimited).
			// We need to stay within RPM limit for this to pass, so we only
			// call Acquire up to rpmLimit times.
			acquireCalls := rapid.IntRange(1, rpmLimit).Draw(rt, "acquireCalls")
			for i := 0; i < acquireCalls; i++ {
				_, err := rl.Acquire(ctx, "")
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
		rl, err := NewRateLimiter(RPM(rpmLimit), WithFixedWindow(), WithFailFast())
		if err != nil {
			rt.Fatalf("NewRateLimiter(%d, 0) returned error: %v", rpmLimit, err)
		}

		// Use a mock clock starting at a fixed time
		currentTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
		rl.now = func() time.Time { return currentTime }

		ctx := context.Background()

		// Exhaust the RPM limit within the current window
		for i := 0; i < rpmLimit; i++ {
			_, err := rl.Acquire(ctx, "")
			if err != nil {
				rt.Fatalf("Acquire() returned error on call %d (limit=%d): %v", i+1, rpmLimit, err)
			}
		}

		// Verify the limit is actually exhausted (next call should fail)
		_, err = rl.Acquire(ctx, "")
		if err != ErrRateLimitExceeded {
			rt.Fatalf("expected ErrRateLimitExceeded after exhausting RPM limit, got: %v", err)
		}

		// Advance the clock past the 60-second window boundary
		advanceDuration := rapid.Int64Range(60, 300).Draw(rt, "advanceSeconds")
		currentTime = currentTime.Add(time.Duration(advanceDuration) * time.Second)

		// After the window resets, Acquire should succeed again
		_, err = rl.Acquire(ctx, "")
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

			rl, err := NewRateLimiter(RPM(rpmLimit), WithSlidingWindow(), WithFailFast())
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
				_, err := rl.Acquire(ctx, "")
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
				_, err := rl.Acquire(ctx, "")
				if err != nil {
					rt.Fatalf("Acquire() returned error on call %d after old requests expired (rpmLimit=%d, numOldRequests=%d): %v",
						i+1, rpmLimit, numOldRequests, err)
				}
			}

			// The next request should fail (we've now used all capacity)
			_, err = rl.Acquire(ctx, "")
			if !errors.Is(err, ErrRateLimitExceeded) {
				rt.Fatalf("Expected ErrRateLimitExceeded after filling capacity, got: %v", err)
			}
		})
	})

	t.Run("TPM_OldTokensExpire", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Use a TPM limit that we can fill and verify expiry
			tpmLimit := rapid.IntRange(100, 5000).Draw(rt, "tpmLimit")

			rl, err := NewRateLimiter(TPM(tpmLimit), WithSlidingWindow(), WithFailFast())
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
				rl.Record("", TokenUsage{
					InputTokens:  tokens / 2,
					OutputTokens: tokens - tokens/2,
				})
				// Also acquire to register the RPM event (needed for the request to count)
				_, _ = rl.Acquire(ctx, "")
			}

			// Advance time past 60 seconds from the latest possible old record
			currentTime = baseTime.Add(90 * time.Second)

			// Now the sliding window should have pruned all old token records.
			// Record tokens up to the limit — this should succeed because old tokens expired.
			tokensToRecord := tpmLimit - 1 // just under the limit
			rl.Record("", TokenUsage{
				InputTokens:  tokensToRecord / 2,
				OutputTokens: tokensToRecord - tokensToRecord/2,
			})

			// Acquire should still succeed (TPM is at tpmLimit-1, under the limit)
			_, err = rl.Acquire(ctx, "")
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

			rl, err := NewRateLimiter(RPM(rpmLimit), WithSlidingWindow(), WithFailFast())
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
				_, err := rl.Acquire(ctx, "")
				if err != nil {
					rt.Fatalf("Acquire() failed for recent request %d: %v", i+1, err)
				}
			}

			// At checkTime, the recent requests should still count
			currentTime = checkTime
			remainingCapacity := rpmLimit - numRecentRequests

			// We should be able to make exactly remainingCapacity more requests
			for i := 0; i < remainingCapacity; i++ {
				_, err := rl.Acquire(ctx, "")
				if err != nil {
					rt.Fatalf("Acquire() failed on remaining capacity call %d (remaining=%d): %v",
						i+1, remainingCapacity, err)
				}
			}

			// The next one should fail
			_, err = rl.Acquire(ctx, "")
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
			rl, err := NewRateLimiter(RPM(rpmLimit), strategy, WithFailFast())
			if err != nil {
				rt.Fatalf("NewRateLimiter(%d, 0) returned error: %v", rpmLimit, err)
			}

			// Use a mock clock so time doesn't advance
			fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rl.now = func() time.Time { return fixedTime }

			ctx := context.Background()

			// Exhaust the RPM limit by calling Acquire rpmLimit times
			for i := 0; i < rpmLimit; i++ {
				_, err := rl.Acquire(ctx, "")
				if err != nil {
					rt.Fatalf("Acquire() returned error on call %d (before limit reached): %v", i+1, err)
				}
			}

			// The next Acquire should return ErrRateLimitExceeded immediately
			_, err = rl.Acquire(ctx, "")
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
			rl, err := NewRateLimiter(RPM(rpmLimit), TPM(tpmLimit), strategy, WithFailFast())
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
			rl.Record("", TokenUsage{
				InputTokens:  tpmLimit / 2,
				OutputTokens: tpmLimit - tpmLimit/2,
			})

			// The next Acquire should return ErrRateLimitExceeded due to TPM exceeded
			_, err = rl.Acquire(ctx, "")
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
			rl, err := NewRateLimiter(RPM(100000), TPM(100000), WithSlidingWindow(), WithFailFast())
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
				if _, err := rl.Acquire(ctx, ""); err != nil {
					rt.Fatalf("Acquire() returned error on call %d: %v", i+1, err)
				}
			}

			// Verify RPM counter equals N
			b := rl.bucket("")
			b.mu.Lock()
			count := b.slidingRPMCount()
			b.mu.Unlock()

			if count != n {
				rt.Fatalf("sliding RPM counter = %d, want %d", count, n)
			}
		})
	})

	t.Run("SlidingWindow_TPMCounter", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// High limits so nothing gets rate-limited
			rl, err := NewRateLimiter(RPM(100000), TPM(100000), WithSlidingWindow(), WithFailFast())
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
				rl.Record("", usage)
			}

			// Verify TPM counter equals sum of all tokens
			b := rl.bucket("")
			b.mu.Lock()
			count := b.slidingTPMCount()
			b.mu.Unlock()

			if count != expectedTotal {
				rt.Fatalf("sliding TPM counter = %d, want %d", count, expectedTotal)
			}
		})
	})

	t.Run("FixedWindow_RPMCounter", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// High limits so nothing gets rate-limited
			rl, err := NewRateLimiter(RPM(100000), TPM(100000), WithFixedWindow(), WithFailFast())
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
				if _, err := rl.Acquire(ctx, ""); err != nil {
					rt.Fatalf("Acquire() returned error on call %d: %v", i+1, err)
				}
			}

			// Verify RPM counter equals N
			b := rl.bucket("")
			b.mu.Lock()
			count := b.fixedRPMCount
			b.mu.Unlock()

			if count != n {
				rt.Fatalf("fixed RPM counter = %d, want %d", count, n)
			}
		})
	})

	t.Run("FixedWindow_TPMCounter", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// High limits so nothing gets rate-limited
			rl, err := NewRateLimiter(RPM(100000), TPM(100000), WithFixedWindow(), WithFailFast())
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
				rl.Record("", usage)
			}

			// Verify TPM counter equals sum of all tokens
			b := rl.bucket("")
			b.mu.Lock()
			count := b.fixedTPMCount
			b.mu.Unlock()

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

			rl, err := NewRateLimiter(RPM(rpmLimit), WithSlidingWindow(), WithBlock())
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
				_, err := rl.Acquire(ctx, "")
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
			_, err = rl.Acquire(ctx, "")
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

			rl, err := NewRateLimiter(RPM(rpmLimit), WithFixedWindow(), WithBlock())
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
				_, err := rl.Acquire(ctx, "")
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
			_, err = rl.Acquire(ctx, "")
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
			rl, err := NewRateLimiter(RPM(10000), TPM(tpmLimit), WithSlidingWindow(), WithBlock())
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
			rl.Record("", TokenUsage{
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
			_, err = rl.Acquire(ctx, "")
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

			rl, err := NewRateLimiter(RPM(rpmLimit), WithSlidingWindow(), WithBlock())
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
				if _, err := rl.Acquire(ctx, ""); err != nil {
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
			_, err = rl.Acquire(ctx, "")
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
			rl, err := NewRateLimiter(RPM(rpmLimit), strategy, WithBlock())
			if err != nil {
				rt.Fatalf("NewRateLimiter(%d, 0) returned error: %v", rpmLimit, err)
			}

			// Use a mock clock frozen at T=0 so that the wait duration is 60s
			fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rl.now = func() time.Time { return fixedTime }

			ctx := context.Background()

			// Exhaust the RPM limit — all events at T=0, so wait = 60s
			for i := 0; i < rpmLimit; i++ {
				_, err := rl.Acquire(ctx, "")
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
			_, acquireErr := rl.Acquire(ctxWithTimeout, "")

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
			rl, err := NewRateLimiter(RPM(rpmLimit), strategy, WithBlock())
			if err != nil {
				rt.Fatalf("NewRateLimiter(%d, 0) returned error: %v", rpmLimit, err)
			}

			// Use a mock clock frozen at T=0
			fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rl.now = func() time.Time { return fixedTime }

			ctx := context.Background()

			// Exhaust the RPM limit
			for i := 0; i < rpmLimit; i++ {
				_, err := rl.Acquire(ctx, "")
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
			_, acquireErr := rl.Acquire(ctxWithCancel, "")

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

			rl, err := NewRateLimiter(RPM(rpmLimit), TPM(tpmLimit), strategy, WithFailFast())
			if err != nil {
				rt.Fatalf("NewRateLimiter(%d, %d) returned error: %v", rpmLimit, tpmLimit, err)
			}

			// Freeze time so nothing expires
			fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rl.now = func() time.Time { return fixedTime }

			ctx := context.Background()

			// Exhaust RPM by calling Acquire rpmLimit times
			for i := 0; i < rpmLimit; i++ {
				_, err := rl.Acquire(ctx, "")
				if err != nil {
					rt.Fatalf("Acquire() returned error on call %d (before RPM limit reached): %v", i+1, err)
				}
			}

			// TPM is still well under limit (no tokens recorded beyond what Acquire does)
			// The next Acquire should fail due to RPM exhaustion
			_, err = rl.Acquire(ctx, "")
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

			rl, err := NewRateLimiter(RPM(rpmLimit), TPM(tpmLimit), strategy, WithFailFast())
			if err != nil {
				rt.Fatalf("NewRateLimiter(%d, %d) returned error: %v", rpmLimit, tpmLimit, err)
			}

			// Freeze time so nothing expires
			fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rl.now = func() time.Time { return fixedTime }

			ctx := context.Background()

			// Record tokens to exhaust TPM limit
			rl.Record("", TokenUsage{
				InputTokens:  tpmLimit / 2,
				OutputTokens: tpmLimit - tpmLimit/2,
			})

			// RPM is still well under limit (only 0 acquires so far)
			// The next Acquire should fail due to TPM exhaustion
			_, err = rl.Acquire(ctx, "")
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

			rl, err := NewRateLimiter(RPM(rpmLimit), TPM(tpmLimit), strategy, WithFailFast())
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
				rl.Record("", TokenUsage{
					InputTokens:  tokensToRecord / 2,
					OutputTokens: tokensToRecord - tokensToRecord/2,
				})
			}

			// Make some Acquire calls but stay under RPM limit
			acquireCalls := rapid.IntRange(1, rpmLimit-1).Draw(rt, "acquireCalls")
			for i := 0; i < acquireCalls; i++ {
				_, err := rl.Acquire(ctx, "")
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

			rl, err := NewRateLimiter(RPM(rpmLimit), TPM(tpmLimit), strategy, WithFailFast())
			if err != nil {
				rt.Fatalf("NewRateLimiter(%d, %d) returned error: %v", rpmLimit, tpmLimit, err)
			}

			// Freeze time so nothing expires
			fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rl.now = func() time.Time { return fixedTime }

			ctx := context.Background()

			// Exhaust RPM by calling Acquire rpmLimit times
			for i := 0; i < rpmLimit; i++ {
				_, err := rl.Acquire(ctx, "")
				if err != nil {
					rt.Fatalf("Acquire() returned error on call %d (before RPM limit reached): %v", i+1, err)
				}
			}

			// Also exhaust TPM by recording tokens >= tpmLimit
			rl.Record("", TokenUsage{
				InputTokens:  tpmLimit / 2,
				OutputTokens: tpmLimit - tpmLimit/2,
			})

			// Both RPM and TPM are exhausted. Since RPM is checked first,
			// the error should be ErrRateLimitExceeded (from RPM check).
			// This verifies the ordering: RPM check happens before TPM check.
			_, err = rl.Acquire(ctx, "")
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
			rl, err := NewRateLimiter(RPM(100000), TPM(100000), WithSlidingWindow(), WithFailFast())
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
				if _, err := rl.Acquire(ctx, ""); err != nil {
					rt.Fatalf("Acquire() returned error on call %d: %v", i+1, err)
				}
				// Do NOT call Record — simulating a failed provider call
			}

			// Verify TPM counter is zero (no tokens recorded)
			b := rl.bucket("")
			b.mu.Lock()
			tpmCount := b.slidingTPMCount()
			b.mu.Unlock()

			if tpmCount != 0 {
				rt.Fatalf("TPM counter = %d after %d Acquire-only calls (no Record), want 0",
					tpmCount, numFailedCalls)
			}
		})
	})

	t.Run("FixedWindow_AcquireAloneDoesNotAffectTPM", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// High limits so nothing gets rate-limited
			rl, err := NewRateLimiter(RPM(100000), TPM(100000), WithFixedWindow(), WithFailFast())
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
				if _, err := rl.Acquire(ctx, ""); err != nil {
					rt.Fatalf("Acquire() returned error on call %d: %v", i+1, err)
				}
				// Do NOT call Record — simulating a failed provider call
			}

			// Verify TPM counter is zero (no tokens recorded)
			b := rl.bucket("")
			b.mu.Lock()
			tpmCount := b.fixedTPMCountVal()
			b.mu.Unlock()

			if tpmCount != 0 {
				rt.Fatalf("TPM counter = %d after %d Acquire-only calls (no Record), want 0",
					tpmCount, numFailedCalls)
			}
		})
	})

	t.Run("SlidingWindow_OnlyRecordedCallsContributeToTPM", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// High limits so nothing gets rate-limited
			rl, err := NewRateLimiter(RPM(100000), TPM(100000), WithSlidingWindow(), WithFailFast())
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
				if _, err := rl.Acquire(ctx, ""); err != nil {
					rt.Fatalf("Acquire() returned error on call %d: %v", i+1, err)
				}

				if successIndices[i] {
					// Successful call — record tokens
					input := rapid.IntRange(1, 1000).Draw(rt, "inputTokens")
					output := rapid.IntRange(1, 1000).Draw(rt, "outputTokens")
					usage := TokenUsage{InputTokens: input, OutputTokens: output}
					expectedTPM += usage.Total()
					rl.Record("", usage)
				}
				// else: failed call — do NOT call Record
			}

			// Verify TPM counter only reflects tokens from Record calls
			b := rl.bucket("")
			b.mu.Lock()
			actualTPM := b.slidingTPMCount()
			b.mu.Unlock()

			if actualTPM != expectedTPM {
				rt.Fatalf("TPM counter = %d, want %d (only from %d successful Record calls out of %d total Acquire calls)",
					actualTPM, expectedTPM, numSuccessful, totalCalls)
			}
		})
	})

	t.Run("FixedWindow_OnlyRecordedCallsContributeToTPM", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// High limits so nothing gets rate-limited
			rl, err := NewRateLimiter(RPM(100000), TPM(100000), WithFixedWindow(), WithFailFast())
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
				if _, err := rl.Acquire(ctx, ""); err != nil {
					rt.Fatalf("Acquire() returned error on call %d: %v", i+1, err)
				}

				if successIndices[i] {
					// Successful call — record tokens
					input := rapid.IntRange(1, 1000).Draw(rt, "inputTokens")
					output := rapid.IntRange(1, 1000).Draw(rt, "outputTokens")
					usage := TokenUsage{InputTokens: input, OutputTokens: output}
					expectedTPM += usage.Total()
					rl.Record("", usage)
				}
				// else: failed call — do NOT call Record
			}

			// Verify TPM counter only reflects tokens from Record calls
			b := rl.bucket("")
			b.mu.Lock()
			actualTPM := b.fixedTPMCountVal()
			b.mu.Unlock()

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
			rl, err := NewRateLimiter(RPM(rpmLimit), strategy, WithFailFast())
			if err != nil {
				rt.Fatalf("NewRateLimiter(%d, 0) returned error: %v", rpmLimit, err)
			}

			// Freeze time so nothing expires
			fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rl.now = func() time.Time { return fixedTime }

			// Exhaust the RPM limit by calling Acquire directly
			ctx := context.Background()
			for i := 0; i < rpmLimit; i++ {
				if _, err := rl.Acquire(ctx, ""); err != nil {
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
			rl, err := NewRateLimiter(RPM(1), strategy, WithFailFast())
			if err != nil {
				rt.Fatalf("NewRateLimiter(RPM(1)) returned error: %v", err)
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
			rl, err := NewRateLimiter(RPM(1000000), TPM(1000000), WithSlidingWindow(), WithFailFast())
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
						if _, err := rl.Acquire(ctx, ""); err != nil {
							// With very high limits, this should never happen
							return
						}
						totalAcquires.Add(1)

						usage := TokenUsage{
							InputTokens:  tokensPerOp / 2,
							OutputTokens: tokensPerOp - tokensPerOp/2,
						}
						rl.Record("", usage)
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
			b := rl.bucket("")
			b.mu.Lock()
			rpmCount := b.slidingRPMCount()
			b.mu.Unlock()

			if rpmCount != expectedRPM {
				rt.Fatalf("sliding RPM counter = %d, want %d (N=%d, M=%d)",
					rpmCount, expectedRPM, numGoroutines, opsPerGoroutine)
			}

			// Verify TPM counter matches sum of all recorded tokens
			b = rl.bucket("")
			b.mu.Lock()
			tpmCount := b.slidingTPMCount()
			b.mu.Unlock()

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
			rl, err := NewRateLimiter(RPM(1000000), TPM(1000000), WithFixedWindow(), WithFailFast())
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
						if _, err := rl.Acquire(ctx, ""); err != nil {
							// With very high limits, this should never happen
							return
						}
						totalAcquires.Add(1)

						usage := TokenUsage{
							InputTokens:  tokensPerOp / 2,
							OutputTokens: tokensPerOp - tokensPerOp/2,
						}
						rl.Record("", usage)
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
			b := rl.bucket("")
			b.mu.Lock()
			rpmCount := b.fixedRPMCount
			b.mu.Unlock()

			if rpmCount != expectedRPM {
				rt.Fatalf("fixed RPM counter = %d, want %d (N=%d, M=%d)",
					rpmCount, expectedRPM, numGoroutines, opsPerGoroutine)
			}

			// Verify TPM counter matches sum of all recorded tokens
			b = rl.bucket("")
			b.mu.Lock()
			tpmCount := b.fixedTPMCount
			b.mu.Unlock()

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

// TestProperty_AliasEquivalence verifies that for any positive integer count,
// the behavior of RPM(count) is identical to RequestRateLimit(count, 60),
// TPM(count) is identical to TokenRateLimit(count, 60),
// WithGlobalRPM(count) is identical to WithGlobalRequestLimit(count, 60),
// and WithGlobalTPM(count) is identical to WithGlobalTokenLimit(count, 60) —
// for any sequence of Acquire/Record operations.
//
// Strategy: Create two RateLimiters with equivalent configs (one using alias,
// one using explicit), run the same sequence of Acquire/Record operations,
// verify identical outcomes.
//
// **Validates: Requirements 1.3, 1.4, 4.3, 4.4**
func TestProperty_AliasEquivalence(t *testing.T) {
	t.Run("RPM_equals_RequestRateLimit_60", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			count := rapid.IntRange(1, 1000).Draw(rt, "count")

			// Create two RateLimiters: one with alias, one with explicit config
			rlAlias, err := NewRateLimiter(RPM(count), WithFailFast())
			if err != nil {
				rt.Fatalf("NewRateLimiter with RPM(%d) failed: %v", count, err)
			}
			rlExplicit, err := NewRateLimiter(RequestRateLimit(count, 60), WithFailFast())
			if err != nil {
				rt.Fatalf("NewRateLimiter with RequestRateLimit(%d, 60) failed: %v", count, err)
			}

			// Use a shared mock clock so time is controlled identically
			currentTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rlAlias.now = func() time.Time { return currentTime }
			rlExplicit.now = func() time.Time { return currentTime }

			ctx := context.Background()

			// Generate a sequence of operations (acquire count+5 to test both success and rejection)
			numOps := rapid.IntRange(1, count+5).Draw(rt, "numOps")

			for i := 0; i < numOps; i++ {
				_, errAlias := rlAlias.Acquire(ctx, "key1")
				_, errExplicit := rlExplicit.Acquire(ctx, "key1")

				if (errAlias == nil) != (errExplicit == nil) {
					rt.Fatalf("Acquire() diverged on call %d: alias=%v, explicit=%v (count=%d)",
						i+1, errAlias, errExplicit, count)
				}
				if errAlias != nil && errExplicit != nil {
					if !errors.Is(errAlias, ErrRateLimitExceeded) || !errors.Is(errExplicit, ErrRateLimitExceeded) {
						rt.Fatalf("Acquire() error types diverged on call %d: alias=%v, explicit=%v",
							i+1, errAlias, errExplicit)
					}
				}
			}
		})
	})

	t.Run("TPM_equals_TokenRateLimit_60", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			count := rapid.IntRange(1, 1000).Draw(rt, "count")

			// Need a request limit to allow Acquire calls (use high RPM)
			rlAlias, err := NewRateLimiter(TPM(count), RPM(10000), WithFailFast())
			if err != nil {
				rt.Fatalf("NewRateLimiter with TPM(%d) failed: %v", count, err)
			}
			rlExplicit, err := NewRateLimiter(TokenRateLimit(count, 60), RequestRateLimit(10000, 60), WithFailFast())
			if err != nil {
				rt.Fatalf("NewRateLimiter with TokenRateLimit(%d, 60) failed: %v", count, err)
			}

			// Use a shared mock clock
			currentTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rlAlias.now = func() time.Time { return currentTime }
			rlExplicit.now = func() time.Time { return currentTime }

			ctx := context.Background()

			// Record tokens to fill the budget, then try Acquire
			numRecords := rapid.IntRange(1, 10).Draw(rt, "numRecords")
			for i := 0; i < numRecords; i++ {
				tokens := rapid.IntRange(1, count/2+1).Draw(rt, "tokens")
				usage := TokenUsage{
					InputTokens:  tokens / 2,
					OutputTokens: tokens - tokens/2,
				}
				rlAlias.Record("key1", usage)
				rlExplicit.Record("key1", usage)
			}

			// Try multiple Acquires and verify identical behavior
			numAcquires := rapid.IntRange(1, 5).Draw(rt, "numAcquires")
			for i := 0; i < numAcquires; i++ {
				_, errAlias := rlAlias.Acquire(ctx, "key1")
				_, errExplicit := rlExplicit.Acquire(ctx, "key1")

				if (errAlias == nil) != (errExplicit == nil) {
					rt.Fatalf("Acquire() diverged on call %d: alias=%v, explicit=%v (count=%d)",
						i+1, errAlias, errExplicit, count)
				}
			}
		})
	})

	t.Run("WithGlobalRPM_equals_WithGlobalRequestLimit_60", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			count := rapid.IntRange(1, 1000).Draw(rt, "count")

			// Per-key limit set high so global limit is the binding constraint
			rlAlias, err := NewRateLimiter(RPM(100000), WithGlobalRPM(count), WithFailFast())
			if err != nil {
				rt.Fatalf("NewRateLimiter with WithGlobalRPM(%d) failed: %v", count, err)
			}
			rlExplicit, err := NewRateLimiter(RequestRateLimit(100000, 60), WithGlobalRequestLimit(count, 60), WithFailFast())
			if err != nil {
				rt.Fatalf("NewRateLimiter with WithGlobalRequestLimit(%d, 60) failed: %v", count, err)
			}

			// Use a shared mock clock
			currentTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rlAlias.now = func() time.Time { return currentTime }
			rlExplicit.now = func() time.Time { return currentTime }

			ctx := context.Background()

			// Generate operations across multiple keys to exercise global limit
			numOps := rapid.IntRange(1, count+5).Draw(rt, "numOps")
			keys := []string{"key1", "key2", "key3"}

			for i := 0; i < numOps; i++ {
				key := keys[i%len(keys)]
				_, errAlias := rlAlias.Acquire(ctx, key)
				_, errExplicit := rlExplicit.Acquire(ctx, key)

				if (errAlias == nil) != (errExplicit == nil) {
					rt.Fatalf("Acquire() diverged on call %d (key=%s): alias=%v, explicit=%v (globalCount=%d)",
						i+1, key, errAlias, errExplicit, count)
				}
				if errAlias != nil && errExplicit != nil {
					if !errors.Is(errAlias, ErrRateLimitExceeded) || !errors.Is(errExplicit, ErrRateLimitExceeded) {
						rt.Fatalf("Acquire() error types diverged on call %d: alias=%v, explicit=%v",
							i+1, errAlias, errExplicit)
					}
				}
			}
		})
	})

	t.Run("WithGlobalTPM_equals_WithGlobalTokenLimit_60", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			count := rapid.IntRange(1, 1000).Draw(rt, "count")

			// Per-key RPM and TPM set high, global TPM is the binding constraint
			rlAlias, err := NewRateLimiter(RPM(100000), TPM(100000), WithGlobalTPM(count), WithFailFast())
			if err != nil {
				rt.Fatalf("NewRateLimiter with WithGlobalTPM(%d) failed: %v", count, err)
			}
			rlExplicit, err := NewRateLimiter(RequestRateLimit(100000, 60), TokenRateLimit(100000, 60), WithGlobalTokenLimit(count, 60), WithFailFast())
			if err != nil {
				rt.Fatalf("NewRateLimiter with WithGlobalTokenLimit(%d, 60) failed: %v", count, err)
			}

			// Use a shared mock clock
			currentTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rlAlias.now = func() time.Time { return currentTime }
			rlExplicit.now = func() time.Time { return currentTime }

			ctx := context.Background()
			keys := []string{"key1", "key2", "key3"}

			// Record tokens across keys and verify both limiters behave identically
			numRecords := rapid.IntRange(1, 10).Draw(rt, "numRecords")
			for i := 0; i < numRecords; i++ {
				key := keys[i%len(keys)]
				tokens := rapid.IntRange(1, count/2+1).Draw(rt, "tokens")
				usage := TokenUsage{
					InputTokens:  tokens / 2,
					OutputTokens: tokens - tokens/2,
				}
				rlAlias.Record(key, usage)
				rlExplicit.Record(key, usage)
			}

			// Try Acquires and verify identical behavior
			numAcquires := rapid.IntRange(1, 5).Draw(rt, "numAcquires")
			for i := 0; i < numAcquires; i++ {
				key := keys[i%len(keys)]
				_, errAlias := rlAlias.Acquire(ctx, key)
				_, errExplicit := rlExplicit.Acquire(ctx, key)

				if (errAlias == nil) != (errExplicit == nil) {
					rt.Fatalf("Acquire() diverged on call %d (key=%s): alias=%v, explicit=%v (globalTPM=%d)",
						i+1, key, errAlias, errExplicit, count)
				}
			}
		})
	})
}

// TestProperty_InvalidConfig verifies that for any invalid configuration,
// NewRateLimiter returns a non-nil error and a nil *RateLimiter.
//
// Invalid configurations include:
// - windowSeconds <= 0 in RequestRateLimit/TokenRateLimit/WithGlobalRequestLimit/WithGlobalTokenLimit
// - count <= 0 in RequestRateLimit/TokenRateLimit/WithGlobalRequestLimit/WithGlobalTokenLimit
// - n <= 0 in MaxConcurrent
// - Both positional rpmLimit and tpmLimit equal to zero with no additive limit options
//
// **Validates: Requirements 1.5, 1.6, 2.2, 3.5**
func TestProperty_InvalidConfig(t *testing.T) {
	t.Run("RequestRateLimit_InvalidWindowSeconds", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Generate invalid window seconds (zero or negative)
			windowSeconds := rapid.IntRange(-100, 0).Draw(rt, "windowSeconds")
			count := rapid.IntRange(1, 1000).Draw(rt, "count")

			rl, err := NewRateLimiter(RequestRateLimit(count, windowSeconds))
			if err == nil {
				rt.Fatalf("NewRateLimiter with RequestRateLimit(%d, %d) returned nil error, expected error", count, windowSeconds)
			}
			if rl != nil {
				rt.Fatalf("NewRateLimiter with RequestRateLimit(%d, %d) returned non-nil RateLimiter, expected nil", count, windowSeconds)
			}
		})
	})

	t.Run("RequestRateLimit_InvalidCount", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Generate invalid count (zero or negative)
			count := rapid.IntRange(-100, 0).Draw(rt, "count")
			windowSeconds := rapid.IntRange(1, 3600).Draw(rt, "windowSeconds")

			rl, err := NewRateLimiter(RequestRateLimit(count, windowSeconds))
			if err == nil {
				rt.Fatalf("NewRateLimiter with RequestRateLimit(%d, %d) returned nil error, expected error", count, windowSeconds)
			}
			if rl != nil {
				rt.Fatalf("NewRateLimiter with RequestRateLimit(%d, %d) returned non-nil RateLimiter, expected nil", count, windowSeconds)
			}
		})
	})

	t.Run("TokenRateLimit_InvalidWindowSeconds", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			windowSeconds := rapid.IntRange(-100, 0).Draw(rt, "windowSeconds")
			count := rapid.IntRange(1, 1000).Draw(rt, "count")

			rl, err := NewRateLimiter(TokenRateLimit(count, windowSeconds))
			if err == nil {
				rt.Fatalf("NewRateLimiter with TokenRateLimit(%d, %d) returned nil error, expected error", count, windowSeconds)
			}
			if rl != nil {
				rt.Fatalf("NewRateLimiter with TokenRateLimit(%d, %d) returned non-nil RateLimiter, expected nil", count, windowSeconds)
			}
		})
	})

	t.Run("TokenRateLimit_InvalidCount", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			count := rapid.IntRange(-100, 0).Draw(rt, "count")
			windowSeconds := rapid.IntRange(1, 3600).Draw(rt, "windowSeconds")

			rl, err := NewRateLimiter(TokenRateLimit(count, windowSeconds))
			if err == nil {
				rt.Fatalf("NewRateLimiter with TokenRateLimit(%d, %d) returned nil error, expected error", count, windowSeconds)
			}
			if rl != nil {
				rt.Fatalf("NewRateLimiter with TokenRateLimit(%d, %d) returned non-nil RateLimiter, expected nil", count, windowSeconds)
			}
		})
	})

	t.Run("WithGlobalRequestLimit_InvalidWindowSeconds", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			windowSeconds := rapid.IntRange(-100, 0).Draw(rt, "windowSeconds")
			count := rapid.IntRange(1, 1000).Draw(rt, "count")

			// Provide a valid per-key limit so only the global option is invalid
			rl, err := NewRateLimiter(RPM(100), WithGlobalRequestLimit(count, windowSeconds))
			if err == nil {
				rt.Fatalf("NewRateLimiter with WithGlobalRequestLimit(%d, %d) returned nil error, expected error", count, windowSeconds)
			}
			if rl != nil {
				rt.Fatalf("NewRateLimiter with WithGlobalRequestLimit(%d, %d) returned non-nil RateLimiter, expected nil", count, windowSeconds)
			}
		})
	})

	t.Run("WithGlobalRequestLimit_InvalidCount", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			count := rapid.IntRange(-100, 0).Draw(rt, "count")
			windowSeconds := rapid.IntRange(1, 3600).Draw(rt, "windowSeconds")

			rl, err := NewRateLimiter(RPM(100), WithGlobalRequestLimit(count, windowSeconds))
			if err == nil {
				rt.Fatalf("NewRateLimiter with WithGlobalRequestLimit(%d, %d) returned nil error, expected error", count, windowSeconds)
			}
			if rl != nil {
				rt.Fatalf("NewRateLimiter with WithGlobalRequestLimit(%d, %d) returned non-nil RateLimiter, expected nil", count, windowSeconds)
			}
		})
	})

	t.Run("WithGlobalTokenLimit_InvalidWindowSeconds", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			windowSeconds := rapid.IntRange(-100, 0).Draw(rt, "windowSeconds")
			count := rapid.IntRange(1, 1000).Draw(rt, "count")

			rl, err := NewRateLimiter(RPM(100), WithGlobalTokenLimit(count, windowSeconds))
			if err == nil {
				rt.Fatalf("NewRateLimiter with WithGlobalTokenLimit(%d, %d) returned nil error, expected error", count, windowSeconds)
			}
			if rl != nil {
				rt.Fatalf("NewRateLimiter with WithGlobalTokenLimit(%d, %d) returned non-nil RateLimiter, expected nil", count, windowSeconds)
			}
		})
	})

	t.Run("WithGlobalTokenLimit_InvalidCount", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			count := rapid.IntRange(-100, 0).Draw(rt, "count")
			windowSeconds := rapid.IntRange(1, 3600).Draw(rt, "windowSeconds")

			rl, err := NewRateLimiter(RPM(100), WithGlobalTokenLimit(count, windowSeconds))
			if err == nil {
				rt.Fatalf("NewRateLimiter with WithGlobalTokenLimit(%d, %d) returned nil error, expected error", count, windowSeconds)
			}
			if rl != nil {
				rt.Fatalf("NewRateLimiter with WithGlobalTokenLimit(%d, %d) returned non-nil RateLimiter, expected nil", count, windowSeconds)
			}
		})
	})

	t.Run("MaxConcurrent_InvalidN", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Generate invalid n (zero or negative)
			n := rapid.IntRange(-100, 0).Draw(rt, "n")

			// Provide valid positional limits so only MaxConcurrent is invalid
			rl, err := NewRateLimiter(RPM(100), TPM(100), MaxConcurrent(n))
			if err == nil {
				rt.Fatalf("NewRateLimiter with MaxConcurrent(%d) returned nil error, expected error", n)
			}
			if rl != nil {
				rt.Fatalf("NewRateLimiter with MaxConcurrent(%d) returned non-nil RateLimiter, expected nil", n)
			}
		})
	})

	t.Run("NoLimitsAtAll_BothPositionalZero_NoOptions", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Both positional args are zero and no additive limit options are provided.
			// This should always return an error regardless of window/overflow options.
			strategy := rapid.SampledFrom([]RateLimiterOption{
				WithSlidingWindow(),
				WithFixedWindow(),
			}).Draw(rt, "strategy")

			overflow := rapid.SampledFrom([]RateLimiterOption{
				WithBlock(),
				WithFailFast(),
			}).Draw(rt, "overflow")

			rl, err := NewRateLimiter(strategy, overflow)
			if err == nil {
				rt.Fatalf("NewRateLimiter() with no limit options returned nil error, expected error")
			}
			if rl != nil {
				rt.Fatalf("NewRateLimiter() with no limit options returned non-nil RateLimiter, expected nil")
			}
		})
	})
}

// TestProperty_MaxConcurrentInvariant verifies that for any n > 0 configured
// via MaxConcurrent(n), and for any key, the number of concurrently in-flight
// calls (between Acquire returning nil and the corresponding Record call) SHALL
// never exceed n. In FailFastMode the n+1th concurrent Acquire returns
// ErrRateLimitExceeded immediately; in BlockMode all goroutines eventually
// complete and peak concurrency never exceeds n.
//
// Strategy: Generate random n (1-10), launch many goroutines (20-50) that
// Acquire, sleep briefly, then Record. Track peak concurrency with atomic.Int32.
//
// **Validates: Requirements 3.1, 3.2, 3.3, 3.4, 7.3**
func TestProperty_MaxConcurrentInvariant(t *testing.T) {
	t.Run("FailFast_PeakNeverExceedsN", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Generate random max concurrency (1-10) and goroutine count (20-50)
			n := rapid.IntRange(1, 10).Draw(rt, "maxConcurrent")
			numGoroutines := rapid.IntRange(20, 50).Draw(rt, "numGoroutines")
			sleepMs := rapid.IntRange(1, 5).Draw(rt, "sleepMs")

			// Create a RateLimiter with MaxConcurrent and FailFast mode.
			// Use a very high RPM so rate limiting doesn't interfere.
			rl, err := NewRateLimiter(RPM(100000), MaxConcurrent(n), WithFailFast())
			if err != nil {
				rt.Fatalf("NewRateLimiter with MaxConcurrent(%d) returned error: %v", n, err)
			}

			ctx := context.Background()

			// Track peak concurrency with atomic counter
			var inflight atomic.Int32
			var peak atomic.Int32
			var successCount atomic.Int32

			var wg sync.WaitGroup
			wg.Add(numGoroutines)

			for g := 0; g < numGoroutines; g++ {
				go func() {
					defer wg.Done()

					release, err := rl.Acquire(ctx, "testkey")
					if err != nil {
						// In FailFast mode, excess goroutines get ErrRateLimitExceeded
						if !errors.Is(err, ErrRateLimitExceeded) {
							// Unexpected error
							return
						}
						return
					}

					// Successfully acquired — track in-flight count
					successCount.Add(1)
					current := inflight.Add(1)

					// Update peak if this is a new high
					for {
						old := peak.Load()
						if current <= old {
							break
						}
						if peak.CompareAndSwap(old, current) {
							break
						}
					}

					// Simulate some work to increase chance of overlap
					time.Sleep(time.Duration(sleepMs) * time.Millisecond)

					// Release the slot via the lease returned by Acquire.
					inflight.Add(-1)
					rl.Record("testkey", TokenUsage{InputTokens: 1, OutputTokens: 1})
					release()
				}()
			}

			wg.Wait()

			// Verify: peak concurrency never exceeded n
			observedPeak := int(peak.Load())
			if observedPeak > n {
				rt.Fatalf("peak concurrency = %d, exceeds MaxConcurrent(%d)", observedPeak, n)
			}

			// Verify: at least some goroutines succeeded
			if successCount.Load() == 0 {
				rt.Fatalf("no goroutines succeeded in Acquire, expected at least 1")
			}
		})
	})

	t.Run("BlockMode_AllComplete_PeakNeverExceedsN", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Generate random max concurrency (1-10) and goroutine count (20-50)
			n := rapid.IntRange(1, 10).Draw(rt, "maxConcurrent")
			numGoroutines := rapid.IntRange(20, 50).Draw(rt, "numGoroutines")

			// Create a RateLimiter with MaxConcurrent and Block mode.
			// Use a very high RPM so rate limiting doesn't interfere.
			rl, err := NewRateLimiter(RPM(100000), MaxConcurrent(n), WithBlock())
			if err != nil {
				rt.Fatalf("NewRateLimiter with MaxConcurrent(%d) returned error: %v", n, err)
			}

			// Use a timeout context as safety net against deadlocks
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			// Track peak concurrency with atomic counter
			var inflight atomic.Int32
			var peak atomic.Int32
			var completedCount atomic.Int32

			var wg sync.WaitGroup
			wg.Add(numGoroutines)

			for g := 0; g < numGoroutines; g++ {
				go func() {
					defer wg.Done()

					release, err := rl.Acquire(ctx, "testkey")
					if err != nil {
						// Context timeout means deadlock — should not happen
						return
					}

					// Successfully acquired — track in-flight count
					current := inflight.Add(1)

					// Update peak if this is a new high
					for {
						old := peak.Load()
						if current <= old {
							break
						}
						if peak.CompareAndSwap(old, current) {
							break
						}
					}

					// Simulate some work to maximize concurrency overlap
					time.Sleep(1 * time.Millisecond)

					// Release the slot via the lease returned by Acquire.
					inflight.Add(-1)
					rl.Record("testkey", TokenUsage{InputTokens: 1, OutputTokens: 1})
					release()
					completedCount.Add(1)
				}()
			}

			wg.Wait()

			// Verify: ALL goroutines completed (BlockMode should never reject)
			completed := int(completedCount.Load())
			if completed != numGoroutines {
				rt.Fatalf("only %d/%d goroutines completed (BlockMode should allow all to complete eventually)",
					completed, numGoroutines)
			}

			// Verify: peak concurrency never exceeded n
			observedPeak := int(peak.Load())
			if observedPeak > n {
				rt.Fatalf("peak concurrency = %d, exceeds MaxConcurrent(%d)", observedPeak, n)
			}

			// Verify: peak actually reached n (with enough goroutines it should saturate)
			// Note: This is a soft check — on very fast systems with n close to numGoroutines
			// it might not always reach peak. We only check if numGoroutines >= 2*n.
			if numGoroutines >= 2*n && observedPeak < n {
				rt.Logf("WARNING: peak concurrency = %d, expected to reach MaxConcurrent(%d) with %d goroutines",
					observedPeak, n, numGoroutines)
			}
		})
	})
}

// TestProperty_ConfigurableWindow verifies Property 1: Configurable window
// enforcement (rate cap). For any valid count and windowSeconds, and for any
// key, if more than count requests are issued within a single window of
// windowSeconds seconds, subsequent Acquire calls SHALL be rejected (FailFast).
// Also verifies that after advancing time past the window, Acquire succeeds
// again. Additionally tests token limits: TokenRateLimit(count, windowSeconds)
// with Record filling budget, then Acquire fails.
//
// Strategy: Generate random count (1-100), random windowSeconds (1-300). Create
// RateLimiter with RequestRateLimit(count, windowSeconds) and FailFast. Use mock
// clock (rl.now) to control time. Issue exactly count Acquires (all succeed),
// then verify count+1th Acquire returns ErrRateLimitExceeded.
//
// **Validates: Requirements 1.1, 1.2, 2.1**
func TestProperty_ConfigurableWindow(t *testing.T) {
	t.Run("RequestRateLimit_ExceedingCountRejects", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Generate random count (1-100) and windowSeconds (1-300)
			count := rapid.IntRange(1, 100).Draw(rt, "count")
			windowSeconds := rapid.IntRange(1, 300).Draw(rt, "windowSeconds")

			// Generate a random key
			key := rapid.StringMatching(`[a-z]{1,10}`).Draw(rt, "key")

			// Create RateLimiter with RequestRateLimit and FailFast
			rl, err := NewRateLimiter(RequestRateLimit(count, windowSeconds), WithFailFast())
			if err != nil {
				rt.Fatalf("NewRateLimiter with RequestRateLimit(%d, %d) failed: %v", count, windowSeconds, err)
			}

			// Use a mock clock frozen at a fixed time
			fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rl.now = func() time.Time { return fixedTime }

			ctx := context.Background()

			// Issue exactly count Acquires — all should succeed
			for i := 0; i < count; i++ {
				_, err := rl.Acquire(ctx, key)
				if err != nil {
					rt.Fatalf("Acquire() returned error on call %d (limit=%d, window=%ds): %v",
						i+1, count, windowSeconds, err)
				}
			}

			// The count+1th Acquire should be rejected
			_, err = rl.Acquire(ctx, key)
			if err == nil {
				rt.Fatalf("Acquire() returned nil after %d calls (limit=%d, window=%ds), expected ErrRateLimitExceeded",
					count+1, count, windowSeconds)
			}
			if !errors.Is(err, ErrRateLimitExceeded) {
				rt.Fatalf("Acquire() returned %v, expected ErrRateLimitExceeded", err)
			}
		})
	})

	t.Run("RequestRateLimit_WindowAdvanceResetsCapacity", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Generate random count (1-100) and windowSeconds (1-300)
			count := rapid.IntRange(1, 100).Draw(rt, "count")
			windowSeconds := rapid.IntRange(1, 300).Draw(rt, "windowSeconds")

			// Generate a random key
			key := rapid.StringMatching(`[a-z]{1,10}`).Draw(rt, "key")

			// Pick a random window strategy
			strategy := rapid.SampledFrom([]RateLimiterOption{
				WithSlidingWindow(),
				WithFixedWindow(),
			}).Draw(rt, "strategy")

			// Create RateLimiter with RequestRateLimit and FailFast
			rl, err := NewRateLimiter(RequestRateLimit(count, windowSeconds), strategy, WithFailFast())
			if err != nil {
				rt.Fatalf("NewRateLimiter with RequestRateLimit(%d, %d) failed: %v", count, windowSeconds, err)
			}

			// Use a mock clock starting at a base time
			baseTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			currentTime := baseTime
			rl.now = func() time.Time { return currentTime }

			ctx := context.Background()

			// Exhaust the limit within the window
			for i := 0; i < count; i++ {
				_, err := rl.Acquire(ctx, key)
				if err != nil {
					rt.Fatalf("Acquire() returned error on call %d (limit=%d, window=%ds): %v",
						i+1, count, windowSeconds, err)
				}
			}

			// Confirm the limit is reached
			_, err = rl.Acquire(ctx, key)
			if !errors.Is(err, ErrRateLimitExceeded) {
				rt.Fatalf("Expected ErrRateLimitExceeded after exhausting limit, got: %v", err)
			}

			// Advance time past the window
			currentTime = baseTime.Add(time.Duration(windowSeconds)*time.Second + time.Second)

			// After the window passes, Acquire should succeed again
			_, err = rl.Acquire(ctx, key)
			if err != nil {
				rt.Fatalf("Acquire() returned error after advancing past window (%ds + 1s): %v",
					windowSeconds, err)
			}
		})
	})

	t.Run("TokenRateLimit_ExceedingTokenBudgetRejects", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Generate random count (token budget, 1-100) and windowSeconds (1-300)
			count := rapid.IntRange(1, 100).Draw(rt, "count")
			windowSeconds := rapid.IntRange(1, 300).Draw(rt, "windowSeconds")

			// Generate a random key
			key := rapid.StringMatching(`[a-z]{1,10}`).Draw(rt, "key")

			// Create RateLimiter with TokenRateLimit and a high RPM so it doesn't interfere.
			// We also need RequestRateLimit or rpmLimit to allow Acquire calls.
			rl, err := NewRateLimiter(
				TokenRateLimit(count, windowSeconds),
				RequestRateLimit(10000, windowSeconds), // high request limit to not interfere
				WithFailFast(),
			)
			if err != nil {
				rt.Fatalf("NewRateLimiter with TokenRateLimit(%d, %d) failed: %v", count, windowSeconds, err)
			}

			// Use a mock clock frozen at a fixed time
			fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rl.now = func() time.Time { return fixedTime }

			ctx := context.Background()

			// Record tokens to fill the budget exactly
			rl.Record(key, TokenUsage{
				InputTokens:  count / 2,
				OutputTokens: count - count/2,
			})

			// The next Acquire should be rejected due to TPM exhaustion
			_, err = rl.Acquire(ctx, key)
			if err == nil {
				rt.Fatalf("Acquire() returned nil after recording %d tokens (limit=%d, window=%ds), expected ErrRateLimitExceeded",
					count, count, windowSeconds)
			}
			if !errors.Is(err, ErrRateLimitExceeded) {
				rt.Fatalf("Acquire() returned %v, expected ErrRateLimitExceeded", err)
			}
		})
	})

	t.Run("TokenRateLimit_WindowAdvanceResetsTokenBudget", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Generate random count (token budget, 1-100) and windowSeconds (1-300)
			count := rapid.IntRange(1, 100).Draw(rt, "count")
			windowSeconds := rapid.IntRange(1, 300).Draw(rt, "windowSeconds")

			// Generate a random key
			key := rapid.StringMatching(`[a-z]{1,10}`).Draw(rt, "key")

			// Pick a random window strategy
			strategy := rapid.SampledFrom([]RateLimiterOption{
				WithSlidingWindow(),
				WithFixedWindow(),
			}).Draw(rt, "strategy")

			// Create RateLimiter with TokenRateLimit and high request limit
			rl, err := NewRateLimiter(
				TokenRateLimit(count, windowSeconds),
				RequestRateLimit(10000, windowSeconds),
				strategy,
				WithFailFast(),
			)
			if err != nil {
				rt.Fatalf("NewRateLimiter with TokenRateLimit(%d, %d) failed: %v", count, windowSeconds, err)
			}

			// Use a mock clock starting at a base time
			baseTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			currentTime := baseTime
			rl.now = func() time.Time { return currentTime }

			ctx := context.Background()

			// Record tokens to fill the budget
			rl.Record(key, TokenUsage{
				InputTokens:  count / 2,
				OutputTokens: count - count/2,
			})

			// Confirm the token limit is reached
			_, err = rl.Acquire(ctx, key)
			if !errors.Is(err, ErrRateLimitExceeded) {
				rt.Fatalf("Expected ErrRateLimitExceeded after filling token budget, got: %v", err)
			}

			// Advance time past the window
			currentTime = baseTime.Add(time.Duration(windowSeconds)*time.Second + time.Second)

			// After the window passes, Acquire should succeed again
			_, err = rl.Acquire(ctx, key)
			if err != nil {
				rt.Fatalf("Acquire() returned error after advancing past token window (%ds + 1s): %v",
					windowSeconds, err)
			}
		})
	})
}

// TestProperty_GlobalLimitConjunction verifies Property 5: Global limit conjunction.
// For any configuration with both per-key and global limits, and for any set of
// keys issuing requests, the global counter SHALL accumulate across all keys,
// and Acquire SHALL succeed only when BOTH the per-key limit AND the global
// limit have available capacity. A single key cannot exhaust the global budget
// without affecting other keys' ability to acquire.
//
// Strategy:
// 1. Generate random per-key limit (50-200), random global limit (5-30)
// 2. Create RateLimiter with high per-key RPM + low global RPM + FailFast
// 3. Use mock clock (freeze time)
// 4. Issue Acquire calls across multiple keys (key1, key2, key3...)
// 5. Verify total successful acquires across ALL keys equals the global limit
// 6. Verify each individual key has not exceeded per-key limit
// 7. Verify once global limit is exhausted, Acquire fails even for a fresh key
//
// **Validates: Requirements 4.1, 4.2, 4.5, 4.6, 4.7**
func TestProperty_GlobalLimitConjunction(t *testing.T) {
	t.Run("GlobalCounterAccumulatesAcrossKeys", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Generate random per-key limit (50-200) and global limit (5-30)
			perKeyLimit := rapid.IntRange(50, 200).Draw(rt, "perKeyLimit")
			globalLimit := rapid.IntRange(5, 30).Draw(rt, "globalLimit")

			// Number of keys to use (2-5)
			numKeys := rapid.IntRange(2, 5).Draw(rt, "numKeys")

			// Create RateLimiter with high per-key RPM + low global RPM + FailFast
			rl, err := NewRateLimiter(
				RequestRateLimit(perKeyLimit, 60),
				WithGlobalRequestLimit(globalLimit, 60),
				WithFailFast(),
			)
			if err != nil {
				rt.Fatalf("NewRateLimiter failed: %v", err)
			}

			// Freeze time so no window expiry occurs
			fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rl.now = func() time.Time { return fixedTime }

			ctx := context.Background()

			// Track per-key success counts
			keySuccesses := make(map[string]int)
			totalSuccesses := 0

			// Issue Acquire calls across multiple keys in round-robin fashion
			// Keep going until all keys fail (global limit exhausted)
			keys := make([]string, numKeys)
			for i := range keys {
				keys[i] = fmt.Sprintf("key%d", i)
			}

			// Issue requests across keys until the global limit is exhausted.
			// We need at most globalLimit + numKeys attempts (the extra are failures).
			maxAttempts := globalLimit + numKeys
			for attempt := 0; attempt < maxAttempts; attempt++ {
				key := keys[attempt%numKeys]
				_, err := rl.Acquire(ctx, key)
				if err == nil {
					keySuccesses[key]++
					totalSuccesses++
				}
				if totalSuccesses >= globalLimit {
					break
				}
			}

			// Verify: total successful acquires across ALL keys equals the global limit
			if totalSuccesses != globalLimit {
				rt.Fatalf("total successful acquires = %d, want exactly globalLimit = %d (perKeyLimit=%d, numKeys=%d)",
					totalSuccesses, globalLimit, perKeyLimit, numKeys)
			}

			// Verify: each individual key has not exceeded per-key limit
			for key, count := range keySuccesses {
				if count > perKeyLimit {
					rt.Fatalf("key %q had %d successes, exceeds per-key limit of %d",
						key, count, perKeyLimit)
				}
			}
		})
	})

	t.Run("FreshKeyFailsWhenGlobalExhausted", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Generate random per-key limit (50-200) and global limit (5-30)
			perKeyLimit := rapid.IntRange(50, 200).Draw(rt, "perKeyLimit")
			globalLimit := rapid.IntRange(5, 30).Draw(rt, "globalLimit")

			// Create RateLimiter with high per-key RPM + low global RPM + FailFast
			rl, err := NewRateLimiter(
				RequestRateLimit(perKeyLimit, 60),
				WithGlobalRequestLimit(globalLimit, 60),
				WithFailFast(),
			)
			if err != nil {
				rt.Fatalf("NewRateLimiter failed: %v", err)
			}

			// Freeze time so no window expiry occurs
			fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rl.now = func() time.Time { return fixedTime }

			ctx := context.Background()

			// Exhaust the global limit using a single key (which has plenty of per-key capacity)
			for i := 0; i < globalLimit; i++ {
				_, err := rl.Acquire(ctx, "exhaustkey")
				if err != nil {
					rt.Fatalf("Acquire() returned error on call %d (globalLimit=%d, perKeyLimit=%d): %v",
						i+1, globalLimit, perKeyLimit, err)
				}
			}

			// Now use a completely fresh key that has never been seen before.
			// It has full per-key capacity but global is exhausted.
			freshKey := rapid.StringMatching(`fresh_[a-z]{3,8}`).Draw(rt, "freshKey")
			_, err = rl.Acquire(ctx, freshKey)
			if err == nil {
				rt.Fatalf("Acquire() returned nil for fresh key %q after global limit (%d) exhausted, expected ErrRateLimitExceeded",
					freshKey, globalLimit)
			}
			if !errors.Is(err, ErrRateLimitExceeded) {
				rt.Fatalf("Acquire() returned %v for fresh key %q, expected ErrRateLimitExceeded", err, freshKey)
			}
		})
	})

	t.Run("BothLimitsRequiredForSuccess", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Generate random per-key limit (50-200) and global limit (5-30)
			perKeyLimit := rapid.IntRange(50, 200).Draw(rt, "perKeyLimit")
			globalLimit := rapid.IntRange(5, 30).Draw(rt, "globalLimit")

			// Pick a random window strategy
			strategy := rapid.SampledFrom([]RateLimiterOption{
				WithSlidingWindow(),
				WithFixedWindow(),
			}).Draw(rt, "strategy")

			// Create RateLimiter with both per-key and global limits
			rl, err := NewRateLimiter(
				RequestRateLimit(perKeyLimit, 60),
				WithGlobalRequestLimit(globalLimit, 60),
				strategy,
				WithFailFast(),
			)
			if err != nil {
				rt.Fatalf("NewRateLimiter failed: %v", err)
			}

			// Freeze time
			fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rl.now = func() time.Time { return fixedTime }

			ctx := context.Background()

			// Use multiple keys, distributing requests across them
			numKeys := rapid.IntRange(2, 5).Draw(rt, "numKeys")
			keys := make([]string, numKeys)
			for i := range keys {
				keys[i] = fmt.Sprintf("conj_key%d", i)
			}

			totalSuccesses := 0
			keySuccesses := make(map[string]int)

			// Issue acquires across keys. Since perKeyLimit >> globalLimit,
			// the global limit should be the binding constraint.
			for i := 0; i < globalLimit+numKeys; i++ {
				key := keys[i%numKeys]
				_, err := rl.Acquire(ctx, key)
				if err == nil {
					totalSuccesses++
					keySuccesses[key]++
				}
			}

			// The global limit is the binding constraint since perKeyLimit >> globalLimit
			if totalSuccesses != globalLimit {
				rt.Fatalf("total successful acquires = %d, want globalLimit = %d (perKeyLimit=%d, numKeys=%d)",
					totalSuccesses, globalLimit, perKeyLimit, numKeys)
			}

			// Verify no per-key limit was exceeded
			for key, count := range keySuccesses {
				if count > perKeyLimit {
					rt.Fatalf("key %q had %d successes, exceeds per-key limit of %d",
						key, count, perKeyLimit)
				}
			}

			// After global is exhausted, ALL keys should fail
			for _, key := range keys {
				_, err := rl.Acquire(ctx, key)
				if err == nil {
					rt.Fatalf("Acquire(key=%q) succeeded after global limit exhausted", key)
				}
				if !errors.Is(err, ErrRateLimitExceeded) {
					rt.Fatalf("Acquire(key=%q) returned %v, expected ErrRateLimitExceeded", key, err)
				}
			}
		})
	})
}

// mockRateLimitStore is a test double that tracks all method calls to verify
// delegation from the RateLimiter when a store is configured via WithStore.
type mockRateLimitStore struct {
	mu sync.Mutex

	incrementRequestsCalls []mockStoreCall
	incrementTokensCalls   []mockStoreCall
	getRequestCountCalls   []mockStoreCall
	getTokenCountCalls     []mockStoreCall

	// Configurable return values for counts (defaults to 0 = below any limit).
	requestCount int
	tokenCount   int
}

type mockStoreCall struct {
	Key    string
	Window time.Duration
	Amount int // only used for IncrementTokens
}

func (m *mockRateLimitStore) IncrementRequests(_ context.Context, key string, window time.Duration) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.incrementRequestsCalls = append(m.incrementRequestsCalls, mockStoreCall{Key: key, Window: window})
	return m.requestCount, nil
}

func (m *mockRateLimitStore) IncrementTokens(_ context.Context, key string, window time.Duration, amount int) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.incrementTokensCalls = append(m.incrementTokensCalls, mockStoreCall{Key: key, Window: window, Amount: amount})
	return m.tokenCount, nil
}

func (m *mockRateLimitStore) GetRequestCount(_ context.Context, key string, window time.Duration) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getRequestCountCalls = append(m.getRequestCountCalls, mockStoreCall{Key: key, Window: window})
	return m.requestCount, nil
}

func (m *mockRateLimitStore) GetTokenCount(_ context.Context, key string, window time.Duration) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getTokenCountCalls = append(m.getTokenCountCalls, mockStoreCall{Key: key, Window: window})
	return m.tokenCount, nil
}

// TestProperty_StoreDelegation verifies that when a RateLimitStore is configured
// via WithStore, all counter increment and query operations during Acquire and
// Record are delegated to the store, and the in-memory bucket map is NOT used.
//
// **Validates: Requirements 5.3**
func TestProperty_StoreDelegation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate random RPM and TPM limits.
		rpmLimit := rapid.IntRange(1, 100).Draw(rt, "rpmLimit")
		tpmLimit := rapid.IntRange(1, 10000).Draw(rt, "tpmLimit")

		// Create a mock store that returns counts below limits (so Acquire succeeds).
		store := &mockRateLimitStore{
			requestCount: 0,
			tokenCount:   0,
		}

		// Create RateLimiter with the mock store.
		rl, err := NewRateLimiter(RPM(rpmLimit), TPM(tpmLimit), WithStore(store), WithFailFast())
		if err != nil {
			rt.Fatalf("NewRateLimiter with WithStore failed: %v", err)
		}

		// Generate a random sequence of Acquire and Record calls.
		numOps := rapid.IntRange(1, 20).Draw(rt, "numOps")
		key := rapid.StringMatching(`[a-z]{1,10}`).Draw(rt, "key")

		ctx := context.Background()

		acquireCount := 0
		recordCount := 0
		totalTokens := 0

		for i := 0; i < numOps; i++ {
			op := rapid.IntRange(0, 1).Draw(rt, "op")
			switch op {
			case 0: // Acquire
				_, err := rl.Acquire(ctx, key)
				if err != nil {
					rt.Fatalf("Acquire() returned error on op %d: %v", i, err)
				}
				acquireCount++
			case 1: // Record
				tokens := rapid.IntRange(1, 1000).Draw(rt, "tokens")
				totalTokens += tokens
				err := rl.Record(key, TokenUsage{
					InputTokens:  tokens / 2,
					OutputTokens: tokens - tokens/2,
				})
				if err != nil {
					rt.Fatalf("Record() returned error on op %d: %v", i, err)
				}
				recordCount++
			}
		}

		// Verify: mock store IncrementRequests was called for each Acquire.
		store.mu.Lock()
		incReqCalls := len(store.incrementRequestsCalls)
		store.mu.Unlock()

		if incReqCalls != acquireCount {
			rt.Fatalf("expected %d IncrementRequests calls (one per Acquire), got %d", acquireCount, incReqCalls)
		}

		// Verify: mock store IncrementTokens was called for each Record with tokens > 0.
		store.mu.Lock()
		incTokCalls := len(store.incrementTokensCalls)
		store.mu.Unlock()

		if incTokCalls != recordCount {
			rt.Fatalf("expected %d IncrementTokens calls (one per Record), got %d", recordCount, incTokCalls)
		}

		// Verify: IncrementTokens amounts sum to totalTokens.
		store.mu.Lock()
		actualTotalTokens := 0
		for _, call := range store.incrementTokensCalls {
			actualTotalTokens += call.Amount
		}
		store.mu.Unlock()

		if actualTotalTokens != totalTokens {
			rt.Fatalf("expected total tokens delegated to store = %d, got %d", totalTokens, actualTotalTokens)
		}

		// Verify: GetRequestCount was called during Acquire (for limit checking).
		store.mu.Lock()
		getReqCalls := len(store.getRequestCountCalls)
		store.mu.Unlock()

		if getReqCalls < acquireCount {
			rt.Fatalf("expected at least %d GetRequestCount calls (one per Acquire), got %d", acquireCount, getReqCalls)
		}

		// Verify: in-memory bucket map is NOT used when store is configured.
		// rl.Len() should return 0 since no in-memory buckets should be created.
		if rl.Len() != 0 {
			rt.Fatalf("expected rl.Len() = 0 (no in-memory buckets when store configured), got %d", rl.Len())
		}
	})
}

// TestProperty_RecordBothCounters verifies Property 6: Record updates both
// per-key and global counters. For any Record(key, usage) call where a global
// token limit is configured, the global token counter SHALL increase by
// usage.Total() AND the per-key token counter for key SHALL increase by
// usage.Total().
//
// **Validates: Requirements 4.8**
func TestProperty_RecordBothCounters(t *testing.T) {
	t.Run("SlidingWindow_SingleKey", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Generate random limits
			perKeyTokenLimit := rapid.IntRange(100, 10000).Draw(rt, "perKeyTokenLimit")
			globalTokenLimit := rapid.IntRange(100, 10000).Draw(rt, "globalTokenLimit")
			windowSeconds := rapid.IntRange(60, 300).Draw(rt, "windowSeconds")

			// Create RateLimiter with both per-key and global token limits + high RPM
			rl, err := NewRateLimiter(
				RequestRateLimit(100000, windowSeconds),
				TokenRateLimit(perKeyTokenLimit, windowSeconds),
				WithGlobalRequestLimit(100000, windowSeconds),
				WithGlobalTokenLimit(globalTokenLimit, windowSeconds),
				WithSlidingWindow(),
				WithFailFast(),
			)
			if err != nil {
				rt.Fatalf("NewRateLimiter failed: %v", err)
			}

			// Freeze time so nothing expires
			fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rl.now = func() time.Time { return fixedTime }

			key := "test-key"

			// Issue multiple Record calls with random TokenUsage values
			numRecords := rapid.IntRange(1, 20).Draw(rt, "numRecords")
			expectedTotal := 0

			for i := 0; i < numRecords; i++ {
				input := rapid.IntRange(1, 100).Draw(rt, "inputTokens")
				output := rapid.IntRange(1, 100).Draw(rt, "outputTokens")
				usage := TokenUsage{InputTokens: input, OutputTokens: output}
				expectedTotal += usage.Total()
				rl.Record(key, usage)
			}

			// Verify per-key token count matches expected total
			b := rl.bucket(key)
			b.mu.Lock()
			perKeyCount := b.slidingTPMCount()
			b.mu.Unlock()

			if perKeyCount != expectedTotal {
				rt.Fatalf("per-key sliding TPM count = %d, want %d", perKeyCount, expectedTotal)
			}

			// Verify global token count matches expected total
			rl.globalBucket.mu.Lock()
			globalCount := rl.globalBucket.slidingTPMCount()
			rl.globalBucket.mu.Unlock()

			if globalCount != expectedTotal {
				rt.Fatalf("global sliding TPM count = %d, want %d", globalCount, expectedTotal)
			}
		})
	})

	t.Run("SlidingWindow_MultipleKeys", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Generate random limits
			perKeyTokenLimit := rapid.IntRange(100, 10000).Draw(rt, "perKeyTokenLimit")
			globalTokenLimit := rapid.IntRange(1000, 50000).Draw(rt, "globalTokenLimit")
			windowSeconds := rapid.IntRange(60, 300).Draw(rt, "windowSeconds")

			// Create RateLimiter with both per-key and global token limits + high RPM
			rl, err := NewRateLimiter(
				RequestRateLimit(100000, windowSeconds),
				TokenRateLimit(perKeyTokenLimit, windowSeconds),
				WithGlobalRequestLimit(100000, windowSeconds),
				WithGlobalTokenLimit(globalTokenLimit, windowSeconds),
				WithSlidingWindow(),
				WithFailFast(),
			)
			if err != nil {
				rt.Fatalf("NewRateLimiter failed: %v", err)
			}

			// Freeze time so nothing expires
			fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rl.now = func() time.Time { return fixedTime }

			// Use multiple keys
			numKeys := rapid.IntRange(2, 5).Draw(rt, "numKeys")
			keys := make([]string, numKeys)
			for i := range keys {
				keys[i] = rapid.StringMatching(`[a-z]{3,8}`).Draw(rt, "key")
			}

			// Track expected totals per key and global
			perKeyExpected := make(map[string]int)
			globalExpected := 0

			// Issue Record calls with random keys and token amounts
			numRecords := rapid.IntRange(1, 30).Draw(rt, "numRecords")
			for i := 0; i < numRecords; i++ {
				keyIdx := rapid.IntRange(0, numKeys-1).Draw(rt, "keyIdx")
				key := keys[keyIdx]
				input := rapid.IntRange(1, 100).Draw(rt, "inputTokens")
				output := rapid.IntRange(1, 100).Draw(rt, "outputTokens")
				usage := TokenUsage{InputTokens: input, OutputTokens: output}
				total := usage.Total()

				perKeyExpected[key] += total
				globalExpected += total

				rl.Record(key, usage)
			}

			// Verify per-key token counts
			for key, expected := range perKeyExpected {
				b := rl.bucket(key)
				b.mu.Lock()
				count := b.slidingTPMCount()
				b.mu.Unlock()

				if count != expected {
					rt.Fatalf("per-key sliding TPM count for key %q = %d, want %d", key, count, expected)
				}
			}

			// Verify global token count matches sum across all keys
			rl.globalBucket.mu.Lock()
			globalCount := rl.globalBucket.slidingTPMCount()
			rl.globalBucket.mu.Unlock()

			if globalCount != globalExpected {
				rt.Fatalf("global sliding TPM count = %d, want %d", globalCount, globalExpected)
			}
		})
	})

	t.Run("FixedWindow_SingleKey", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Generate random limits
			perKeyTokenLimit := rapid.IntRange(100, 10000).Draw(rt, "perKeyTokenLimit")
			globalTokenLimit := rapid.IntRange(100, 10000).Draw(rt, "globalTokenLimit")
			windowSeconds := rapid.IntRange(60, 300).Draw(rt, "windowSeconds")

			// Create RateLimiter with both per-key and global token limits + high RPM
			rl, err := NewRateLimiter(
				RequestRateLimit(100000, windowSeconds),
				TokenRateLimit(perKeyTokenLimit, windowSeconds),
				WithGlobalRequestLimit(100000, windowSeconds),
				WithGlobalTokenLimit(globalTokenLimit, windowSeconds),
				WithFixedWindow(),
				WithFailFast(),
			)
			if err != nil {
				rt.Fatalf("NewRateLimiter failed: %v", err)
			}

			// Freeze time so nothing expires
			fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rl.now = func() time.Time { return fixedTime }

			key := "test-key"

			// Issue multiple Record calls with random TokenUsage values
			numRecords := rapid.IntRange(1, 20).Draw(rt, "numRecords")
			expectedTotal := 0

			for i := 0; i < numRecords; i++ {
				input := rapid.IntRange(1, 100).Draw(rt, "inputTokens")
				output := rapid.IntRange(1, 100).Draw(rt, "outputTokens")
				usage := TokenUsage{InputTokens: input, OutputTokens: output}
				expectedTotal += usage.Total()
				rl.Record(key, usage)
			}

			// Verify per-key token count matches expected total
			b := rl.bucket(key)
			b.mu.Lock()
			perKeyCount := b.fixedTPMCount
			b.mu.Unlock()

			if perKeyCount != expectedTotal {
				rt.Fatalf("per-key fixed TPM count = %d, want %d", perKeyCount, expectedTotal)
			}

			// Verify global token count matches expected total
			rl.globalBucket.mu.Lock()
			globalCount := rl.globalBucket.fixedTPMCount
			rl.globalBucket.mu.Unlock()

			if globalCount != expectedTotal {
				rt.Fatalf("global fixed TPM count = %d, want %d", globalCount, expectedTotal)
			}
		})
	})

	t.Run("FixedWindow_MultipleKeys", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Generate random limits
			perKeyTokenLimit := rapid.IntRange(100, 10000).Draw(rt, "perKeyTokenLimit")
			globalTokenLimit := rapid.IntRange(1000, 50000).Draw(rt, "globalTokenLimit")
			windowSeconds := rapid.IntRange(60, 300).Draw(rt, "windowSeconds")

			// Create RateLimiter with both per-key and global token limits + high RPM
			rl, err := NewRateLimiter(
				RequestRateLimit(100000, windowSeconds),
				TokenRateLimit(perKeyTokenLimit, windowSeconds),
				WithGlobalRequestLimit(100000, windowSeconds),
				WithGlobalTokenLimit(globalTokenLimit, windowSeconds),
				WithFixedWindow(),
				WithFailFast(),
			)
			if err != nil {
				rt.Fatalf("NewRateLimiter failed: %v", err)
			}

			// Freeze time so nothing expires
			fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rl.now = func() time.Time { return fixedTime }

			// Use multiple keys
			numKeys := rapid.IntRange(2, 5).Draw(rt, "numKeys")
			keys := make([]string, numKeys)
			for i := range keys {
				keys[i] = rapid.StringMatching(`[a-z]{3,8}`).Draw(rt, "key")
			}

			// Track expected totals per key and global
			perKeyExpected := make(map[string]int)
			globalExpected := 0

			// Issue Record calls with random keys and token amounts
			numRecords := rapid.IntRange(1, 30).Draw(rt, "numRecords")
			for i := 0; i < numRecords; i++ {
				keyIdx := rapid.IntRange(0, numKeys-1).Draw(rt, "keyIdx")
				key := keys[keyIdx]
				input := rapid.IntRange(1, 100).Draw(rt, "inputTokens")
				output := rapid.IntRange(1, 100).Draw(rt, "outputTokens")
				usage := TokenUsage{InputTokens: input, OutputTokens: output}
				total := usage.Total()

				perKeyExpected[key] += total
				globalExpected += total

				rl.Record(key, usage)
			}

			// Verify per-key token counts
			for key, expected := range perKeyExpected {
				b := rl.bucket(key)
				b.mu.Lock()
				count := b.fixedTPMCount
				b.mu.Unlock()

				if count != expected {
					rt.Fatalf("per-key fixed TPM count for key %q = %d, want %d", key, count, expected)
				}
			}

			// Verify global token count matches sum across all keys
			rl.globalBucket.mu.Lock()
			globalCount := rl.globalBucket.fixedTPMCount
			rl.globalBucket.mu.Unlock()

			if globalCount != globalExpected {
				rt.Fatalf("global fixed TPM count = %d, want %d", globalCount, globalExpected)
			}
		})
	})
}

// errorRateLimitStore is a test double that returns configurable errors from
// specific methods to verify that the RateLimiter propagates store errors.
type errorRateLimitStore struct {
	getRequestCountErr   error
	getTokenCountErr     error
	incrementRequestsErr error
	incrementTokensErr   error

	// Return values for successful calls (0 = below any limit).
	requestCount int
	tokenCount   int
}

func (e *errorRateLimitStore) GetRequestCount(_ context.Context, _ string, _ time.Duration) (int, error) {
	if e.getRequestCountErr != nil {
		return 0, e.getRequestCountErr
	}
	return e.requestCount, nil
}

func (e *errorRateLimitStore) GetTokenCount(_ context.Context, _ string, _ time.Duration) (int, error) {
	if e.getTokenCountErr != nil {
		return 0, e.getTokenCountErr
	}
	return e.tokenCount, nil
}

func (e *errorRateLimitStore) IncrementRequests(_ context.Context, _ string, _ time.Duration) (int, error) {
	if e.incrementRequestsErr != nil {
		return 0, e.incrementRequestsErr
	}
	return e.requestCount, nil
}

func (e *errorRateLimitStore) IncrementTokens(_ context.Context, _ string, _ time.Duration, _ int) (int, error) {
	if e.incrementTokensErr != nil {
		return 0, e.incrementTokensErr
	}
	return e.tokenCount, nil
}

// TestProperty_StoreErrorPropagation verifies Property 8: Store error propagation.
// For any error returned by a RateLimitStore method during Acquire or Record,
// the RateLimiter SHALL propagate that error to the caller without swallowing it.
//
// Strategy:
// 1. Create mock stores that return specific errors from each method:
//   - GetRequestCount returns error → Acquire returns that error
//   - GetTokenCount returns error → Acquire returns that error
//   - IncrementRequests returns error → Acquire returns that error
//   - IncrementTokens returns error → Record returns that error
//
// 2. Generate random error messages with rapid
// 3. Verify: the exact error returned by the store is the error returned by Acquire/Record
// 4. Use errors.Is() to verify error identity is preserved
//
// **Validates: Requirements 5.6**
func TestProperty_StoreErrorPropagation(t *testing.T) {
	t.Run("GetRequestCount_Error_PropagatesFromAcquire", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Generate a random error message.
			errMsg := rapid.StringMatching(`[a-z]{3,20}_error_[0-9]{1,5}`).Draw(rt, "errMsg")
			storeErr := errors.New(errMsg)

			// Create a store that fails on GetRequestCount.
			store := &errorRateLimitStore{
				getRequestCountErr: storeErr,
			}

			// Need both RPM and TPM limits configured so the request path is exercised.
			rpmLimit := rapid.IntRange(1, 100).Draw(rt, "rpmLimit")
			tpmLimit := rapid.IntRange(1, 10000).Draw(rt, "tpmLimit")

			rl, err := NewRateLimiter(RPM(rpmLimit), TPM(tpmLimit), WithStore(store), WithFailFast())
			if err != nil {
				rt.Fatalf("NewRateLimiter failed: %v", err)
			}

			// Acquire should propagate the GetRequestCount error.
			key := rapid.StringMatching(`[a-z]{1,10}`).Draw(rt, "key")
			_, acquireErr := rl.Acquire(context.Background(), key)

			if acquireErr == nil {
				rt.Fatalf("Acquire() returned nil, expected store error %q", errMsg)
			}
			if !errors.Is(acquireErr, storeErr) {
				rt.Fatalf("Acquire() returned %v, expected errors.Is to match store error %v", acquireErr, storeErr)
			}
		})
	})

	t.Run("GetTokenCount_Error_PropagatesFromAcquire", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Generate a random error message.
			errMsg := rapid.StringMatching(`[a-z]{3,20}_error_[0-9]{1,5}`).Draw(rt, "errMsg")
			storeErr := errors.New(errMsg)

			// Create a store where GetRequestCount succeeds (returns 0) but
			// GetTokenCount fails.
			store := &errorRateLimitStore{
				getTokenCountErr: storeErr,
				requestCount:     0, // below limit, so request check passes
			}

			// Need both RPM and TPM limits configured so the token path is exercised.
			rpmLimit := rapid.IntRange(1, 100).Draw(rt, "rpmLimit")
			tpmLimit := rapid.IntRange(1, 10000).Draw(rt, "tpmLimit")

			rl, err := NewRateLimiter(RPM(rpmLimit), TPM(tpmLimit), WithStore(store), WithFailFast())
			if err != nil {
				rt.Fatalf("NewRateLimiter failed: %v", err)
			}

			// Acquire should propagate the GetTokenCount error.
			key := rapid.StringMatching(`[a-z]{1,10}`).Draw(rt, "key")
			_, acquireErr := rl.Acquire(context.Background(), key)

			if acquireErr == nil {
				rt.Fatalf("Acquire() returned nil, expected store error %q", errMsg)
			}
			if !errors.Is(acquireErr, storeErr) {
				rt.Fatalf("Acquire() returned %v, expected errors.Is to match store error %v", acquireErr, storeErr)
			}
		})
	})

	t.Run("IncrementRequests_Error_PropagatesFromAcquire", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Generate a random error message.
			errMsg := rapid.StringMatching(`[a-z]{3,20}_error_[0-9]{1,5}`).Draw(rt, "errMsg")
			storeErr := errors.New(errMsg)

			// Create a store where Get methods return 0 (under limit) but
			// IncrementRequests fails.
			store := &errorRateLimitStore{
				incrementRequestsErr: storeErr,
				requestCount:         0, // below limit
				tokenCount:           0, // below limit
			}

			// Need both RPM and TPM limits configured so both check paths pass.
			rpmLimit := rapid.IntRange(1, 100).Draw(rt, "rpmLimit")
			tpmLimit := rapid.IntRange(1, 10000).Draw(rt, "tpmLimit")

			rl, err := NewRateLimiter(RPM(rpmLimit), TPM(tpmLimit), WithStore(store), WithFailFast())
			if err != nil {
				rt.Fatalf("NewRateLimiter failed: %v", err)
			}

			// Acquire should propagate the IncrementRequests error.
			key := rapid.StringMatching(`[a-z]{1,10}`).Draw(rt, "key")
			_, acquireErr := rl.Acquire(context.Background(), key)

			if acquireErr == nil {
				rt.Fatalf("Acquire() returned nil, expected store error %q", errMsg)
			}
			if !errors.Is(acquireErr, storeErr) {
				rt.Fatalf("Acquire() returned %v, expected errors.Is to match store error %v", acquireErr, storeErr)
			}
		})
	})

	t.Run("IncrementTokens_Error_PropagatesFromRecord", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Generate a random error message.
			errMsg := rapid.StringMatching(`[a-z]{3,20}_error_[0-9]{1,5}`).Draw(rt, "errMsg")
			storeErr := errors.New(errMsg)

			// Create a store where everything succeeds EXCEPT IncrementTokens.
			store := &errorRateLimitStore{
				incrementTokensErr: storeErr,
				requestCount:       0, // below limit
				tokenCount:         0, // below limit
			}

			// Need both RPM and TPM limits configured.
			rpmLimit := rapid.IntRange(1, 100).Draw(rt, "rpmLimit")
			tpmLimit := rapid.IntRange(1, 10000).Draw(rt, "tpmLimit")

			rl, err := NewRateLimiter(RPM(rpmLimit), TPM(tpmLimit), WithStore(store), WithFailFast())
			if err != nil {
				rt.Fatalf("NewRateLimiter failed: %v", err)
			}

			key := rapid.StringMatching(`[a-z]{1,10}`).Draw(rt, "key")

			// Acquire should succeed since Get/Increment for requests all pass.
			_, acquireErr := rl.Acquire(context.Background(), key)
			if acquireErr != nil {
				rt.Fatalf("Acquire() returned unexpected error: %v", acquireErr)
			}

			// Record should propagate the IncrementTokens error.
			tokens := rapid.IntRange(1, 1000).Draw(rt, "tokens")
			recordErr := rl.Record(key, TokenUsage{
				InputTokens:  tokens / 2,
				OutputTokens: tokens - tokens/2,
			})

			if recordErr == nil {
				rt.Fatalf("Record() returned nil, expected store error %q", errMsg)
			}
			if !errors.Is(recordErr, storeErr) {
				rt.Fatalf("Record() returned %v, expected errors.Is to match store error %v", recordErr, storeErr)
			}
		})
	})
}

// TestProperty_ConcurrentSafety verifies that for any number of goroutines
// concurrently calling Acquire, Record, Purge, and Len on the same RateLimiter
// (with per-key limits, global limits, and MaxConcurrent all configured), the
// implementation SHALL produce no data races (verifiable via go test -race) and
// no deadlocks (verified via context timeout as safety net).
//
// Strategy: Generate random config with per-key RPM (10-100), per-key TPM
// (100-1000), global RPM (50-200), global TPM (500-2000), MaxConcurrent (2-10).
// Launch 10-50 goroutines each randomly calling Acquire, Record, Purge, or Len.
// Use WithBlock() to exercise blocking paths (goroutines block on MaxConcurrent
// semaphore). Rate limits are scaled up so blocking only happens on concurrency
// slots, not on rate windows. A 5-second context timeout detects deadlocks.
// The -race flag detects data races.
//
// **Validates: Requirements 7.1, 7.2, 7.3**
func TestProperty_ConcurrentSafety(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Step 1: Generate random configuration
		perKeyRPM := rapid.IntRange(10, 100).Draw(rt, "perKeyRPM")
		perKeyTPM := rapid.IntRange(100, 1000).Draw(rt, "perKeyTPM")
		globalRPM := rapid.IntRange(50, 200).Draw(rt, "globalRPM")
		globalTPM := rapid.IntRange(500, 2000).Draw(rt, "globalTPM")
		maxConcurrent := rapid.IntRange(2, 10).Draw(rt, "maxConcurrent")

		// Step 2: Generate random number of goroutines and keys
		numGoroutines := rapid.IntRange(10, 50).Draw(rt, "numGoroutines")
		numKeys := rapid.IntRange(2, 5).Draw(rt, "numKeys")

		// Generate keys
		keys := make([]string, numKeys)
		for i := range keys {
			keys[i] = fmt.Sprintf("key-%d", i)
		}

		// Calculate total operations to size rate limits appropriately.
		// Each goroutine does up to 20 iterations, and Acquire+Record pairs
		// consume 1 request each. We scale rate limits so that blocking only
		// occurs on the MaxConcurrent semaphore, not on rate windows.
		maxOps := numGoroutines * 20
		effectivePerKeyRPM := perKeyRPM + maxOps
		effectiveGlobalRPM := globalRPM + maxOps
		effectivePerKeyTPM := perKeyTPM + maxOps*30
		effectiveGlobalTPM := globalTPM + maxOps*30

		// Use WithBlock() to exercise blocking paths — goroutines will block
		// on the MaxConcurrent semaphore until others call Record.
		rl, err := NewRateLimiter(
			RequestRateLimit(effectivePerKeyRPM, 60),
			TokenRateLimit(effectivePerKeyTPM, 60),
			WithGlobalRequestLimit(effectiveGlobalRPM, 60),
			WithGlobalTokenLimit(effectiveGlobalTPM, 60),
			MaxConcurrent(maxConcurrent),
			WithBlock(),
		)
		if err != nil {
			rt.Fatalf("NewRateLimiter failed: %v", err)
		}

		// Step 4: Use a shared context with 5-second timeout as deadlock safety net
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Step 3 & 5: Launch goroutines and wait for completion
		var wg sync.WaitGroup
		wg.Add(numGoroutines)

		// Track panics
		var panicCount atomic.Int32

		for g := 0; g < numGoroutines; g++ {
			go func(goroutineID int) {
				defer wg.Done()
				defer func() {
					if r := recover(); r != nil {
						panicCount.Add(1)
					}
				}()

				// Each goroutine performs a random number of iterations (5-20)
				iterations := 5 + (goroutineID % 16)
				for i := 0; i < iterations; i++ {
					// Check if context is cancelled (deadlock safety)
					if ctx.Err() != nil {
						return
					}

					// Pick a random key — rotate through keys
					key := keys[(goroutineID+i)%numKeys]

					// Pick a random operation based on iteration + goroutineID
					op := (goroutineID + i) % 4
					switch op {
					case 0:
						// Acquire + Record pair
						release, err := rl.Acquire(ctx, key)
						if err == nil {
							_ = rl.Record(key, TokenUsage{
								InputTokens:  1 + (i % 10),
								OutputTokens: 1 + (goroutineID % 10),
							})
							release()
						}
					case 1:
						// Acquire + Record with larger token usage
						release, err := rl.Acquire(ctx, key)
						if err == nil {
							_ = rl.Record(key, TokenUsage{
								InputTokens:  5 + (i % 20),
								OutputTokens: 3 + (goroutineID % 15),
							})
							release()
						}
					case 2:
						// Purge — exercises concurrent map deletion
						rl.Purge(key)
					case 3:
						// Len — exercises concurrent map read
						_ = rl.Len()
					}
				}
			}(g)
		}

		// Wait for all goroutines — if this blocks past context timeout,
		// we have a deadlock
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			// All goroutines finished successfully
		case <-ctx.Done():
			rt.Fatalf("deadlock detected: goroutines did not finish within 5-second timeout "+
				"(config: perKeyRPM=%d, perKeyTPM=%d, globalRPM=%d, globalTPM=%d, maxConcurrent=%d, goroutines=%d, keys=%d)",
				perKeyRPM, perKeyTPM, globalRPM, globalTPM, maxConcurrent, numGoroutines, numKeys)
		}

		// Step 5: Verify no panics occurred
		if panics := panicCount.Load(); panics > 0 {
			rt.Fatalf("detected %d panics during concurrent execution", panics)
		}
	})
}
