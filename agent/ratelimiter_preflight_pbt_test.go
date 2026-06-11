package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// Feature: token-estimation, Property 5: Pre-flight budget check correctness
// **Validates: Requirements 5.4, 5.5**

// TestProperty_PreFlightBudgetCheck verifies that for any ConverseParams and any
// remaining TPM capacity, the RateLimiter.PreFlightCheck returns ErrRateLimitExceeded
// if and only if the estimated input tokens exceed the remaining capacity. When
// estimated tokens are within capacity, it returns nil.
func TestProperty_PreFlightBudgetCheck(t *testing.T) {
	t.Run("RejectsWhenEstimateExceedsCapacity", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Generate a random TPM capacity (1–100000).
			tpmCapacity := rapid.IntRange(1, 100000).Draw(rt, "tpmCapacity")

			// Generate random prior token usage (0 to tpmCapacity-1 so there's some capacity left,
			// or 0 to tpmCapacity to allow full exhaustion).
			priorUsage := rapid.IntRange(0, tpmCapacity).Draw(rt, "priorUsage")

			// Generate random ConverseParams.
			params := drawConverseParams(rt)

			// Use CharEstimator (deterministic, no errors) to compute expected estimate.
			estimator := CharEstimator{}
			expectedEstimate, _ := estimator.EstimateTokens(context.Background(), params)

			// Create a RateLimiter with the generated TPM capacity and CharEstimator.
			rl, err := NewRateLimiter(
				TPM(tpmCapacity),
				RPM(1000000), // high RPM to avoid interference
				WithTokenEstimator(estimator),
				WithSlidingWindow(),
				WithFailFast(),
			)
			if err != nil {
				rt.Fatalf("NewRateLimiter failed: %v", err)
			}

			// Freeze time so nothing expires.
			fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rl.now = func() time.Time { return fixedTime }

			// Record prior usage to simulate consumed tokens.
			if priorUsage > 0 {
				rl.Record("test-key", TokenUsage{
					InputTokens:  priorUsage / 2,
					OutputTokens: priorUsage - priorUsage/2,
				})
			}

			// Compute expected remaining capacity.
			remaining := tpmCapacity - priorUsage

			// Perform the pre-flight check.
			checkErr := rl.PreFlightCheck(context.Background(), "test-key", params)

			// Assert: returns ErrRateLimitExceeded iff estimate > remaining.
			if expectedEstimate > remaining {
				if !errors.Is(checkErr, ErrRateLimitExceeded) {
					rt.Fatalf("expected ErrRateLimitExceeded when estimate=%d > remaining=%d (tpm=%d, priorUsage=%d), got: %v",
						expectedEstimate, remaining, tpmCapacity, priorUsage, checkErr)
				}
			} else {
				if checkErr != nil {
					rt.Fatalf("expected nil when estimate=%d <= remaining=%d (tpm=%d, priorUsage=%d), got: %v",
						expectedEstimate, remaining, tpmCapacity, priorUsage, checkErr)
				}
			}
		})
	})

	t.Run("AllowsWhenEstimateWithinCapacity", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Generate a random TPM capacity (1–100000).
			tpmCapacity := rapid.IntRange(1, 100000).Draw(rt, "tpmCapacity")

			// Generate random ConverseParams.
			params := drawConverseParams(rt)

			// Use CharEstimator to compute expected estimate.
			estimator := CharEstimator{}
			expectedEstimate, _ := estimator.EstimateTokens(context.Background(), params)

			// Ensure prior usage leaves enough room so that estimate <= remaining.
			// remaining = tpmCapacity - priorUsage >= expectedEstimate
			// => priorUsage <= tpmCapacity - expectedEstimate
			maxPriorUsage := tpmCapacity - expectedEstimate
			if maxPriorUsage < 0 {
				// The estimate alone exceeds the full capacity — skip this case
				// as it's covered by the other sub-test.
				return
			}

			priorUsage := rapid.IntRange(0, maxPriorUsage).Draw(rt, "priorUsage")

			// Create a RateLimiter.
			rl, err := NewRateLimiter(
				TPM(tpmCapacity),
				RPM(1000000),
				WithTokenEstimator(estimator),
				WithSlidingWindow(),
				WithFailFast(),
			)
			if err != nil {
				rt.Fatalf("NewRateLimiter failed: %v", err)
			}

			// Freeze time.
			fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rl.now = func() time.Time { return fixedTime }

			// Record prior usage.
			if priorUsage > 0 {
				rl.Record("test-key", TokenUsage{
					InputTokens:  priorUsage / 2,
					OutputTokens: priorUsage - priorUsage/2,
				})
			}

			// Pre-flight check should return nil (allow).
			checkErr := rl.PreFlightCheck(context.Background(), "test-key", params)
			if checkErr != nil {
				remaining := tpmCapacity - priorUsage
				rt.Fatalf("expected nil when estimate=%d <= remaining=%d (tpm=%d, priorUsage=%d), got: %v",
					expectedEstimate, remaining, tpmCapacity, priorUsage, checkErr)
			}
		})
	})

	t.Run("RejectsWhenExactlyExceedsByOne", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Generate a random TPM capacity (1–50000).
			tpmCapacity := rapid.IntRange(1, 50000).Draw(rt, "tpmCapacity")

			// Generate random ConverseParams.
			params := drawConverseParams(rt)

			// Use CharEstimator to compute expected estimate.
			estimator := CharEstimator{}
			expectedEstimate, _ := estimator.EstimateTokens(context.Background(), params)

			if expectedEstimate == 0 {
				// Zero estimate always fits — skip.
				return
			}

			// Set prior usage so remaining = expectedEstimate - 1 (one token short).
			priorUsage := tpmCapacity - expectedEstimate + 1
			if priorUsage < 0 || priorUsage > tpmCapacity {
				// Can't construct this scenario with current capacity — skip.
				return
			}

			// Create a RateLimiter.
			rl, err := NewRateLimiter(
				TPM(tpmCapacity),
				RPM(1000000),
				WithTokenEstimator(estimator),
				WithSlidingWindow(),
				WithFailFast(),
			)
			if err != nil {
				rt.Fatalf("NewRateLimiter failed: %v", err)
			}

			// Freeze time.
			fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rl.now = func() time.Time { return fixedTime }

			// Record prior usage.
			if priorUsage > 0 {
				rl.Record("test-key", TokenUsage{
					InputTokens:  priorUsage / 2,
					OutputTokens: priorUsage - priorUsage/2,
				})
			}

			// remaining = tpmCapacity - priorUsage = expectedEstimate - 1
			// So estimate > remaining → should reject.
			checkErr := rl.PreFlightCheck(context.Background(), "test-key", params)
			if !errors.Is(checkErr, ErrRateLimitExceeded) {
				remaining := tpmCapacity - priorUsage
				rt.Fatalf("expected ErrRateLimitExceeded when estimate=%d > remaining=%d, got: %v",
					expectedEstimate, remaining, checkErr)
			}
		})
	})

	t.Run("AllowsWhenEstimateExactlyEqualsRemaining", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Generate a random TPM capacity (1–50000).
			tpmCapacity := rapid.IntRange(1, 50000).Draw(rt, "tpmCapacity")

			// Generate random ConverseParams.
			params := drawConverseParams(rt)

			// Use CharEstimator to compute expected estimate.
			estimator := CharEstimator{}
			expectedEstimate, _ := estimator.EstimateTokens(context.Background(), params)

			// Set prior usage so remaining = expectedEstimate exactly.
			priorUsage := tpmCapacity - expectedEstimate
			if priorUsage < 0 || priorUsage > tpmCapacity {
				// Can't construct this scenario — skip.
				return
			}

			// Create a RateLimiter.
			rl, err := NewRateLimiter(
				TPM(tpmCapacity),
				RPM(1000000),
				WithTokenEstimator(estimator),
				WithSlidingWindow(),
				WithFailFast(),
			)
			if err != nil {
				rt.Fatalf("NewRateLimiter failed: %v", err)
			}

			// Freeze time.
			fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			rl.now = func() time.Time { return fixedTime }

			// Record prior usage.
			if priorUsage > 0 {
				rl.Record("test-key", TokenUsage{
					InputTokens:  priorUsage / 2,
					OutputTokens: priorUsage - priorUsage/2,
				})
			}

			// remaining = expectedEstimate, so estimate <= remaining → should allow.
			checkErr := rl.PreFlightCheck(context.Background(), "test-key", params)
			if checkErr != nil {
				remaining := tpmCapacity - priorUsage
				rt.Fatalf("expected nil when estimate=%d == remaining=%d, got: %v",
					expectedEstimate, remaining, checkErr)
			}
		})
	})
}
