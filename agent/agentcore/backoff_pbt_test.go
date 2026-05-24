package agentcore

import (
	"math"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// Feature: agentcore-runtime, Property 3: Exponential backoff with jitter bounds

// TestProperty_ExponentialBackoffWithJitterBounds verifies that for any attempt number N
// (0–20) and backoff configuration (baseDelay, maxDelay, jitterPct), the computed retry
// delay is within [delay*(1-jitterPct), delay*(1+jitterPct)] where
// delay = min(baseDelay * 2^N, maxDelay).
//
// **Validates: Requirements 2.3, 10.1, 10.2, 10.6**
func TestProperty_ExponentialBackoffWithJitterBounds(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a random backoff config with reasonable ranges.
		baseDelayMs := rapid.IntRange(1, 10000).Draw(rt, "baseDelayMs") // 1ms to 10s
		baseDelay := time.Duration(baseDelayMs) * time.Millisecond

		// maxDelay must be >= baseDelay, up to 60s
		maxDelayMs := rapid.IntRange(baseDelayMs, 60000).Draw(rt, "maxDelayMs")
		maxDelay := time.Duration(maxDelayMs) * time.Millisecond

		// jitterPct in [0, 0.5] — generate as integer 0–500 then divide by 1000
		jitterPctInt := rapid.IntRange(0, 500).Draw(rt, "jitterPctInt")
		jitterPct := float64(jitterPctInt) / 1000.0

		cfg := backoffConfig{
			baseDelay:  baseDelay,
			maxDelay:   maxDelay,
			maxRetries: 0,
			jitterPct:  jitterPct,
		}

		// Generate a random attempt number 0–20.
		attempt := rapid.IntRange(0, 20).Draw(rt, "attempt")

		// Compute the expected base delay (before jitter).
		expectedBase := float64(baseDelay) * math.Pow(2, float64(attempt))
		if expectedBase > float64(maxDelay) || math.IsInf(expectedBase, 1) || math.IsNaN(expectedBase) {
			expectedBase = float64(maxDelay)
		}

		// Compute the expected bounds.
		lowerBound := time.Duration(expectedBase * (1 - jitterPct))
		upperBound := time.Duration(expectedBase * (1 + jitterPct))

		// The delay function may clamp negative results to 0, so adjust lower bound.
		if lowerBound < 0 {
			lowerBound = 0
		}

		// Call the delay function.
		got := cfg.delay(attempt)

		// Verify the result is within bounds.
		if got < lowerBound || got > upperBound {
			rt.Fatalf("delay(attempt=%d) = %v, want in [%v, %v] (baseDelay=%v, maxDelay=%v, jitterPct=%.3f, expectedBase=%v)",
				attempt, got, lowerBound, upperBound, baseDelay, maxDelay, jitterPct, time.Duration(expectedBase))
		}
	})
}
