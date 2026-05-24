package agentcore

import (
	"math"
	"testing"
	"time"
)

func TestBackoffDelay_ExponentialGrowth(t *testing.T) {
	cfg := backoffConfig{
		baseDelay:  1 * time.Second,
		maxDelay:   30 * time.Second,
		maxRetries: 0,
		jitterPct:  0, // no jitter for deterministic test
	}

	tests := []struct {
		attempt  int
		expected time.Duration
	}{
		{0, 1 * time.Second},  // 1s * 2^0 = 1s
		{1, 2 * time.Second},  // 1s * 2^1 = 2s
		{2, 4 * time.Second},  // 1s * 2^2 = 4s
		{3, 8 * time.Second},  // 1s * 2^3 = 8s
		{4, 16 * time.Second}, // 1s * 2^4 = 16s
		{5, 30 * time.Second}, // 1s * 2^5 = 32s, capped at 30s
	}

	for _, tt := range tests {
		got := cfg.delay(tt.attempt)
		if got != tt.expected {
			t.Errorf("delay(attempt=%d) = %v, want %v", tt.attempt, got, tt.expected)
		}
	}
}

func TestBackoffDelay_CapsAtMaxDelay(t *testing.T) {
	cfg := backoffConfig{
		baseDelay:  1 * time.Second,
		maxDelay:   10 * time.Second,
		maxRetries: 3,
		jitterPct:  0,
	}

	// attempt=10 would be 1024s without cap
	got := cfg.delay(10)
	if got != 10*time.Second {
		t.Errorf("delay(attempt=10) = %v, want %v (maxDelay)", got, 10*time.Second)
	}
}

func TestBackoffDelay_JitterBounds(t *testing.T) {
	cfg := backoffConfig{
		baseDelay:  1 * time.Second,
		maxDelay:   30 * time.Second,
		maxRetries: 0,
		jitterPct:  0.25,
	}

	// Run many iterations to check jitter stays within bounds
	for attempt := 0; attempt < 5; attempt++ {
		baseDelay := float64(cfg.baseDelay) * math.Pow(2, float64(attempt))
		if baseDelay > float64(cfg.maxDelay) {
			baseDelay = float64(cfg.maxDelay)
		}
		minExpected := time.Duration(baseDelay * (1 - cfg.jitterPct))
		maxExpected := time.Duration(baseDelay * (1 + cfg.jitterPct))

		for i := 0; i < 1000; i++ {
			got := cfg.delay(attempt)
			if got < minExpected || got > maxExpected {
				t.Errorf("delay(attempt=%d) = %v, want in [%v, %v]", attempt, got, minExpected, maxExpected)
				break
			}
		}
	}
}

func TestBackoffDelay_LargeAttemptNoOverflow(t *testing.T) {
	cfg := backoffConfig{
		baseDelay:  1 * time.Second,
		maxDelay:   30 * time.Second,
		maxRetries: 0,
		jitterPct:  0.25,
	}

	// Very large attempt should not panic or overflow, just cap at maxDelay
	got := cfg.delay(100)
	minExpected := time.Duration(float64(cfg.maxDelay) * (1 - cfg.jitterPct))
	maxExpected := time.Duration(float64(cfg.maxDelay) * (1 + cfg.jitterPct))

	if got < minExpected || got > maxExpected {
		t.Errorf("delay(attempt=100) = %v, want in [%v, %v]", got, minExpected, maxExpected)
	}
}

func TestBackoffDelay_NeverNegative(t *testing.T) {
	cfg := backoffConfig{
		baseDelay:  1 * time.Millisecond,
		maxDelay:   100 * time.Millisecond,
		maxRetries: 0,
		jitterPct:  0.99, // extreme jitter
	}

	for i := 0; i < 10000; i++ {
		got := cfg.delay(0)
		if got < 0 {
			t.Fatalf("delay returned negative duration: %v", got)
		}
	}
}

func TestDefaultBackoffConfigs(t *testing.T) {
	// Verify heartbeatBackoff
	if heartbeatBackoff.baseDelay != 1*time.Second {
		t.Errorf("heartbeatBackoff.baseDelay = %v, want 1s", heartbeatBackoff.baseDelay)
	}
	if heartbeatBackoff.maxDelay != 10*time.Second {
		t.Errorf("heartbeatBackoff.maxDelay = %v, want 10s", heartbeatBackoff.maxDelay)
	}
	if heartbeatBackoff.maxRetries != 3 {
		t.Errorf("heartbeatBackoff.maxRetries = %d, want 3", heartbeatBackoff.maxRetries)
	}
	if heartbeatBackoff.jitterPct != 0.25 {
		t.Errorf("heartbeatBackoff.jitterPct = %f, want 0.25", heartbeatBackoff.jitterPct)
	}

	// Verify pollBackoff
	if pollBackoff.baseDelay != 1*time.Second {
		t.Errorf("pollBackoff.baseDelay = %v, want 1s", pollBackoff.baseDelay)
	}
	if pollBackoff.maxDelay != 30*time.Second {
		t.Errorf("pollBackoff.maxDelay = %v, want 30s", pollBackoff.maxDelay)
	}
	if pollBackoff.maxRetries != 0 {
		t.Errorf("pollBackoff.maxRetries = %d, want 0 (infinite)", pollBackoff.maxRetries)
	}
	if pollBackoff.jitterPct != 0.25 {
		t.Errorf("pollBackoff.jitterPct = %f, want 0.25", pollBackoff.jitterPct)
	}

	// Verify submitBackoff
	if submitBackoff.baseDelay != 1*time.Second {
		t.Errorf("submitBackoff.baseDelay = %v, want 1s", submitBackoff.baseDelay)
	}
	if submitBackoff.maxDelay != 10*time.Second {
		t.Errorf("submitBackoff.maxDelay = %v, want 10s", submitBackoff.maxDelay)
	}
	if submitBackoff.maxRetries != 3 {
		t.Errorf("submitBackoff.maxRetries = %d, want 3", submitBackoff.maxRetries)
	}
	if submitBackoff.jitterPct != 0.25 {
		t.Errorf("submitBackoff.jitterPct = %f, want 0.25", submitBackoff.jitterPct)
	}
}
