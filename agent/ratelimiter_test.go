package agent

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestNewRateLimiter_BothZeroReturnsError(t *testing.T) {
	rl, err := NewRateLimiter(0, 0)
	if err == nil {
		t.Fatal("expected error when both rpmLimit and tpmLimit are zero, got nil")
	}
	if rl != nil {
		t.Fatal("expected nil RateLimiter when both limits are zero")
	}
}

func TestNewRateLimiter_RPMOnlySucceeds(t *testing.T) {
	rl, err := NewRateLimiter(10, 0)
	if err != nil {
		t.Fatalf("unexpected error for RPM-only limiter: %v", err)
	}
	if rl == nil {
		t.Fatal("expected non-nil RateLimiter")
	}
	if rl.rpmLimit != 10 {
		t.Errorf("expected rpmLimit=10, got %d", rl.rpmLimit)
	}
	if rl.tpmLimit != 0 {
		t.Errorf("expected tpmLimit=0, got %d", rl.tpmLimit)
	}
}

func TestNewRateLimiter_TPMOnlySucceeds(t *testing.T) {
	rl, err := NewRateLimiter(0, 1000)
	if err != nil {
		t.Fatalf("unexpected error for TPM-only limiter: %v", err)
	}
	if rl == nil {
		t.Fatal("expected non-nil RateLimiter")
	}
	if rl.rpmLimit != 0 {
		t.Errorf("expected rpmLimit=0, got %d", rl.rpmLimit)
	}
	if rl.tpmLimit != 1000 {
		t.Errorf("expected tpmLimit=1000, got %d", rl.tpmLimit)
	}
}

func TestNewRateLimiter_BothLimitsSucceeds(t *testing.T) {
	rl, err := NewRateLimiter(10, 1000)
	if err != nil {
		t.Fatalf("unexpected error for both-limits limiter: %v", err)
	}
	if rl == nil {
		t.Fatal("expected non-nil RateLimiter")
	}
	if rl.rpmLimit != 10 {
		t.Errorf("expected rpmLimit=10, got %d", rl.rpmLimit)
	}
	if rl.tpmLimit != 1000 {
		t.Errorf("expected tpmLimit=1000, got %d", rl.tpmLimit)
	}
}

func TestNewRateLimiter_DefaultStrategySlidingWindow(t *testing.T) {
	rl, err := NewRateLimiter(10, 1000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rl.windowStrategy != SlidingWindow {
		t.Errorf("expected default windowStrategy=SlidingWindow, got %d", rl.windowStrategy)
	}
}

func TestNewRateLimiter_DefaultOverflowBlockMode(t *testing.T) {
	rl, err := NewRateLimiter(10, 1000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rl.overflowBehavior != FailFastMode {
		t.Errorf("expected default overflowBehavior=FailFastMode, got %d", rl.overflowBehavior)
	}
}

func TestNewRateLimiter_WithFixedWindowOption(t *testing.T) {
	rl, err := NewRateLimiter(10, 1000, WithFixedWindow())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rl.windowStrategy != FixedWindow {
		t.Errorf("expected windowStrategy=FixedWindow, got %d", rl.windowStrategy)
	}
}

func TestNewRateLimiter_WithFailFastOption(t *testing.T) {
	rl, err := NewRateLimiter(10, 1000, WithFailFast())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rl.overflowBehavior != FailFastMode {
		t.Errorf("expected overflowBehavior=FailFastMode, got %d", rl.overflowBehavior)
	}
}

// ── Shared mode (empty key) ──────────────────────────────────────────────────

func TestRateLimiter_SharedMode_RPM(t *testing.T) {
	rl, _ := NewRateLimiter(2, 0, WithFailFast())

	ctx := context.Background()
	if err := rl.Acquire(ctx, ""); err != nil {
		t.Fatalf("call 1: %v", err)
	}
	if err := rl.Acquire(ctx, ""); err != nil {
		t.Fatalf("call 2: %v", err)
	}
	if err := rl.Acquire(ctx, ""); err != ErrRateLimitExceeded {
		t.Fatalf("call 3: expected ErrRateLimitExceeded, got %v", err)
	}
}

func TestRateLimiter_SharedMode_TPM(t *testing.T) {
	rl, _ := NewRateLimiter(0, 100, WithFailFast())

	ctx := context.Background()
	if err := rl.Acquire(ctx, ""); err != nil {
		t.Fatalf("acquire 1: %v", err)
	}
	rl.Record("", TokenUsage{InputTokens: 60, OutputTokens: 0})

	if err := rl.Acquire(ctx, ""); err != nil {
		t.Fatalf("acquire 2: %v", err)
	}
	rl.Record("", TokenUsage{InputTokens: 50, OutputTokens: 0})

	if err := rl.Acquire(ctx, ""); err != ErrRateLimitExceeded {
		t.Fatalf("expected ErrRateLimitExceeded, got %v", err)
	}
}

// ── Per-key mode ─────────────────────────────────────────────────────────────

func TestRateLimiter_PerKey_IndependentBudgets(t *testing.T) {
	rl, _ := NewRateLimiter(2, 0, WithFailFast())

	ctx := context.Background()

	// Key "a" can acquire twice.
	if err := rl.Acquire(ctx, "a"); err != nil {
		t.Fatalf("key a, call 1: %v", err)
	}
	if err := rl.Acquire(ctx, "a"); err != nil {
		t.Fatalf("key a, call 2: %v", err)
	}
	if err := rl.Acquire(ctx, "a"); err != ErrRateLimitExceeded {
		t.Fatalf("key a, call 3: expected ErrRateLimitExceeded, got %v", err)
	}

	// Key "b" is independent.
	if err := rl.Acquire(ctx, "b"); err != nil {
		t.Fatalf("key b, call 1: %v", err)
	}
	if err := rl.Acquire(ctx, "b"); err != nil {
		t.Fatalf("key b, call 2: %v", err)
	}
	if err := rl.Acquire(ctx, "b"); err != ErrRateLimitExceeded {
		t.Fatalf("key b, call 3: expected ErrRateLimitExceeded, got %v", err)
	}
}

func TestRateLimiter_PerKey_Record(t *testing.T) {
	rl, _ := NewRateLimiter(0, 100, WithFailFast())

	ctx := context.Background()

	rl.Acquire(ctx, "a")
	rl.Record("a", TokenUsage{InputTokens: 30, OutputTokens: 30})
	rl.Acquire(ctx, "a")
	rl.Record("a", TokenUsage{InputTokens: 25, OutputTokens: 25})

	if err := rl.Acquire(ctx, "a"); err != ErrRateLimitExceeded {
		t.Fatalf("expected ErrRateLimitExceeded, got %v", err)
	}

	// Key "b" unaffected.
	if err := rl.Acquire(ctx, "b"); err != nil {
		t.Fatalf("key b should not be limited: %v", err)
	}
}

func TestRateLimiter_Purge(t *testing.T) {
	rl, _ := NewRateLimiter(2, 0, WithFailFast())

	ctx := context.Background()
	rl.Acquire(ctx, "a")
	rl.Acquire(ctx, "a")
	if err := rl.Acquire(ctx, "a"); err != ErrRateLimitExceeded {
		t.Fatal("expected rate limit exceeded before purge")
	}

	rl.Purge("a")

	if err := rl.Acquire(ctx, "a"); err != nil {
		t.Fatalf("after purge, expected success, got: %v", err)
	}
}

func TestRateLimiter_Len(t *testing.T) {
	rl, _ := NewRateLimiter(5, 0)

	ctx := context.Background()
	rl.Acquire(ctx, "a")
	rl.Acquire(ctx, "b")
	rl.Acquire(ctx, "c")

	if got := rl.Len(); got != 3 {
		t.Errorf("expected 3 buckets, got %d", got)
	}

	rl.Purge("b")
	if got := rl.Len(); got != 2 {
		t.Errorf("expected 2 buckets after purge, got %d", got)
	}
}

func TestRateLimiter_TTLEviction(t *testing.T) {
	rl, _ := NewRateLimiter(5, 0, WithFailFast())

	// Use a mock clock.
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	rl.now = func() time.Time { return now }

	ctx := context.Background()

	// Create buckets for "a" and "b".
	rl.Acquire(ctx, "a")
	rl.Acquire(ctx, "b")
	if got := rl.Len(); got != 2 {
		t.Fatalf("expected 2 buckets, got %d", got)
	}

	// Advance time past 60s (bucket TTL) and past 10s (sweep interval).
	now = now.Add(61 * time.Second)

	// Touch "a" so it stays alive.
	rl.Acquire(ctx, "a")

	// "b" should be evicted on next bucket() call (sweep triggered).
	// Force a sweep by accessing any key after the sweep interval.
	now = now.Add(11 * time.Second)
	rl.Acquire(ctx, "a")

	if got := rl.Len(); got != 1 {
		t.Errorf("expected 1 bucket after TTL eviction, got %d", got)
	}
}

func TestRateLimiter_ConcurrentKeys(t *testing.T) {
	rl, _ := NewRateLimiter(1, 0, WithFailFast())

	ctx := context.Background()
	var wg sync.WaitGroup
	results := make([]error, 20)

	for i := range 10 {
		key := string(rune('a' + i))
		wg.Add(2)
		go func(idx int, k string) {
			defer wg.Done()
			results[idx*2] = rl.Acquire(ctx, k)
		}(i, key)
		go func(idx int, k string) {
			defer wg.Done()
			time.Sleep(time.Millisecond)
			results[idx*2+1] = rl.Acquire(ctx, k)
		}(i, key)
	}
	wg.Wait()

	for i := range 10 {
		if results[i*2] != nil {
			t.Errorf("key %c, call 1: expected nil, got %v", 'a'+i, results[i*2])
		}
		if results[i*2+1] != ErrRateLimitExceeded {
			t.Errorf("key %c, call 2: expected ErrRateLimitExceeded, got %v", 'a'+i, results[i*2+1])
		}
	}
}
