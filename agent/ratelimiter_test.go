package agent

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestNewRateLimiter_NoOptionsReturnsError(t *testing.T) {
	rl, err := NewRateLimiter()
	if err == nil {
		t.Fatal("expected error when no options provided, got nil")
	}
	if rl != nil {
		t.Fatal("expected nil RateLimiter when no options provided")
	}
}

func TestNewRateLimiter_RPMOnlySucceeds(t *testing.T) {
	rl, err := NewRateLimiter(RPM(10))
	if err != nil {
		t.Fatalf("unexpected error for RPM-only limiter: %v", err)
	}
	if rl == nil {
		t.Fatal("expected non-nil RateLimiter")
	}
	if rl.requestRateLimit == nil || rl.requestRateLimit.Count != 10 {
		t.Errorf("expected requestRateLimit.Count=10, got %v", rl.requestRateLimit)
	}
}

func TestNewRateLimiter_TPMOnlySucceeds(t *testing.T) {
	rl, err := NewRateLimiter(TPM(1000))
	if err != nil {
		t.Fatalf("unexpected error for TPM-only limiter: %v", err)
	}
	if rl == nil {
		t.Fatal("expected non-nil RateLimiter")
	}
	if rl.tokenRateLimit == nil || rl.tokenRateLimit.Count != 1000 {
		t.Errorf("expected tokenRateLimit.Count=1000, got %v", rl.tokenRateLimit)
	}
}

func TestNewRateLimiter_BothLimitsSucceeds(t *testing.T) {
	rl, err := NewRateLimiter(RPM(10), TPM(1000))
	if err != nil {
		t.Fatalf("unexpected error for both-limits limiter: %v", err)
	}
	if rl == nil {
		t.Fatal("expected non-nil RateLimiter")
	}
	if rl.requestRateLimit == nil || rl.requestRateLimit.Count != 10 {
		t.Errorf("expected requestRateLimit.Count=10, got %v", rl.requestRateLimit)
	}
	if rl.tokenRateLimit == nil || rl.tokenRateLimit.Count != 1000 {
		t.Errorf("expected tokenRateLimit.Count=1000, got %v", rl.tokenRateLimit)
	}
}

func TestNewRateLimiter_DefaultStrategySlidingWindow(t *testing.T) {
	rl, err := NewRateLimiter(RPM(10), TPM(1000))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rl.windowStrategy != SlidingWindow {
		t.Errorf("expected default windowStrategy=SlidingWindow, got %d", rl.windowStrategy)
	}
}

func TestNewRateLimiter_DefaultOverflowBlockMode(t *testing.T) {
	rl, err := NewRateLimiter(RPM(10), TPM(1000))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rl.overflowBehavior != FailFastMode {
		t.Errorf("expected default overflowBehavior=FailFastMode, got %d", rl.overflowBehavior)
	}
}

func TestNewRateLimiter_WithFixedWindowOption(t *testing.T) {
	rl, err := NewRateLimiter(RPM(10), TPM(1000), WithFixedWindow())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rl.windowStrategy != FixedWindow {
		t.Errorf("expected windowStrategy=FixedWindow, got %d", rl.windowStrategy)
	}
}

func TestNewRateLimiter_WithFailFastOption(t *testing.T) {
	rl, err := NewRateLimiter(RPM(10), TPM(1000), WithFailFast())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rl.overflowBehavior != FailFastMode {
		t.Errorf("expected overflowBehavior=FailFastMode, got %d", rl.overflowBehavior)
	}
}

// ── Shared mode (empty key) ──────────────────────────────────────────────────

func TestRateLimiter_SharedMode_RPM(t *testing.T) {
	rl, _ := NewRateLimiter(RPM(2), WithFailFast())

	ctx := context.Background()
	if _, err := rl.Acquire(ctx, ""); err != nil {
		t.Fatalf("call 1: %v", err)
	}
	if _, err := rl.Acquire(ctx, ""); err != nil {
		t.Fatalf("call 2: %v", err)
	}
	if _, err := rl.Acquire(ctx, ""); err != ErrRateLimitExceeded {
		t.Fatalf("call 3: expected ErrRateLimitExceeded, got %v", err)
	}
}

func TestRateLimiter_SharedMode_TPM(t *testing.T) {
	rl, _ := NewRateLimiter(TPM(100), WithFailFast())

	ctx := context.Background()
	if _, err := rl.Acquire(ctx, ""); err != nil {
		t.Fatalf("acquire 1: %v", err)
	}
	rl.Record("", TokenUsage{InputTokens: 60, OutputTokens: 0})

	if _, err := rl.Acquire(ctx, ""); err != nil {
		t.Fatalf("acquire 2: %v", err)
	}
	rl.Record("", TokenUsage{InputTokens: 50, OutputTokens: 0})

	if _, err := rl.Acquire(ctx, ""); err != ErrRateLimitExceeded {
		t.Fatalf("expected ErrRateLimitExceeded, got %v", err)
	}
}

// ── Per-key mode ─────────────────────────────────────────────────────────────

func TestRateLimiter_PerKey_IndependentBudgets(t *testing.T) {
	rl, _ := NewRateLimiter(RPM(2), WithFailFast())

	ctx := context.Background()

	// Key "a" can acquire twice.
	if _, err := rl.Acquire(ctx, "a"); err != nil {
		t.Fatalf("key a, call 1: %v", err)
	}
	if _, err := rl.Acquire(ctx, "a"); err != nil {
		t.Fatalf("key a, call 2: %v", err)
	}
	if _, err := rl.Acquire(ctx, "a"); err != ErrRateLimitExceeded {
		t.Fatalf("key a, call 3: expected ErrRateLimitExceeded, got %v", err)
	}

	// Key "b" is independent.
	if _, err := rl.Acquire(ctx, "b"); err != nil {
		t.Fatalf("key b, call 1: %v", err)
	}
	if _, err := rl.Acquire(ctx, "b"); err != nil {
		t.Fatalf("key b, call 2: %v", err)
	}
	if _, err := rl.Acquire(ctx, "b"); err != ErrRateLimitExceeded {
		t.Fatalf("key b, call 3: expected ErrRateLimitExceeded, got %v", err)
	}
}

func TestRateLimiter_PerKey_Record(t *testing.T) {
	rl, _ := NewRateLimiter(TPM(100), WithFailFast())

	ctx := context.Background()

	_, _ = rl.Acquire(ctx, "a")
	rl.Record("a", TokenUsage{InputTokens: 30, OutputTokens: 30})
	_, _ = rl.Acquire(ctx, "a")
	rl.Record("a", TokenUsage{InputTokens: 25, OutputTokens: 25})

	if _, err := rl.Acquire(ctx, "a"); err != ErrRateLimitExceeded {
		t.Fatalf("expected ErrRateLimitExceeded, got %v", err)
	}

	// Key "b" unaffected.
	if _, err := rl.Acquire(ctx, "b"); err != nil {
		t.Fatalf("key b should not be limited: %v", err)
	}
}

func TestRateLimiter_Purge(t *testing.T) {
	rl, _ := NewRateLimiter(RPM(2), WithFailFast())

	ctx := context.Background()
	_, _ = rl.Acquire(ctx, "a")
	_, _ = rl.Acquire(ctx, "a")
	if _, err := rl.Acquire(ctx, "a"); err != ErrRateLimitExceeded {
		t.Fatal("expected rate limit exceeded before purge")
	}

	rl.Purge("a")

	if _, err := rl.Acquire(ctx, "a"); err != nil {
		t.Fatalf("after purge, expected success, got: %v", err)
	}
}

func TestRateLimiter_Len(t *testing.T) {
	rl, _ := NewRateLimiter(RPM(5))

	ctx := context.Background()
	_, _ = rl.Acquire(ctx, "a")
	_, _ = rl.Acquire(ctx, "b")
	_, _ = rl.Acquire(ctx, "c")

	if got := rl.Len(); got != 3 {
		t.Errorf("expected 3 buckets, got %d", got)
	}

	rl.Purge("b")
	if got := rl.Len(); got != 2 {
		t.Errorf("expected 2 buckets after purge, got %d", got)
	}
}

func TestRateLimiter_TTLEviction(t *testing.T) {
	rl, _ := NewRateLimiter(RPM(5), WithFailFast())

	// Use a mock clock.
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	rl.now = func() time.Time { return now }

	ctx := context.Background()

	// Create buckets for "a" and "b".
	_, _ = rl.Acquire(ctx, "a")
	_, _ = rl.Acquire(ctx, "b")
	if got := rl.Len(); got != 2 {
		t.Fatalf("expected 2 buckets, got %d", got)
	}

	// Advance time past 60s (bucket TTL) and past 10s (sweep interval).
	now = now.Add(61 * time.Second)

	// Touch "a" so it stays alive.
	_, _ = rl.Acquire(ctx, "a")

	// "b" should be evicted on next bucket() call (sweep triggered).
	// Force a sweep by accessing any key after the sweep interval.
	now = now.Add(11 * time.Second)
	_, _ = rl.Acquire(ctx, "a")

	if got := rl.Len(); got != 1 {
		t.Errorf("expected 1 bucket after TTL eviction, got %d", got)
	}
}

func TestRateLimiter_ConcurrentKeys(t *testing.T) {
	rl, _ := NewRateLimiter(RPM(1), WithFailFast())

	ctx := context.Background()
	var wg sync.WaitGroup
	results := make([]error, 20)

	for i := range 10 {
		key := string(rune('a' + i))
		wg.Add(2)
		go func(idx int, k string) {
			defer wg.Done()
			_, results[idx*2] = rl.Acquire(ctx, k)
		}(i, key)
		go func(idx int, k string) {
			defer wg.Done()
			time.Sleep(time.Millisecond)
			_, results[idx*2+1] = rl.Acquire(ctx, k)
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

// ── Constructor validation with new options ──────────────────────────────────

func TestNewRateLimiter_RequestRateLimit_NegativeWindow(t *testing.T) {
	rl, err := NewRateLimiter(RequestRateLimit(100, -1))
	if err == nil {
		t.Fatal("expected error for negative windowSeconds")
	}
	if rl != nil {
		t.Fatal("expected nil RateLimiter")
	}
}

func TestNewRateLimiter_RequestRateLimit_ZeroWindow(t *testing.T) {
	rl, err := NewRateLimiter(RequestRateLimit(100, 0))
	if err == nil {
		t.Fatal("expected error for zero windowSeconds")
	}
	if rl != nil {
		t.Fatal("expected nil RateLimiter")
	}
}

func TestNewRateLimiter_RequestRateLimit_ZeroCount(t *testing.T) {
	rl, err := NewRateLimiter(RequestRateLimit(0, 60))
	if err == nil {
		t.Fatal("expected error for zero count")
	}
	if rl != nil {
		t.Fatal("expected nil RateLimiter")
	}
}

func TestNewRateLimiter_RequestRateLimit_NegativeCount(t *testing.T) {
	rl, err := NewRateLimiter(RequestRateLimit(-5, 60))
	if err == nil {
		t.Fatal("expected error for negative count")
	}
	if rl != nil {
		t.Fatal("expected nil RateLimiter")
	}
}

func TestNewRateLimiter_TokenRateLimit_NegativeWindow(t *testing.T) {
	rl, err := NewRateLimiter(TokenRateLimit(1000, -10))
	if err == nil {
		t.Fatal("expected error for negative windowSeconds")
	}
	if rl != nil {
		t.Fatal("expected nil RateLimiter")
	}
}

func TestNewRateLimiter_TokenRateLimit_ZeroCount(t *testing.T) {
	rl, err := NewRateLimiter(TokenRateLimit(0, 60))
	if err == nil {
		t.Fatal("expected error for zero count")
	}
	if rl != nil {
		t.Fatal("expected nil RateLimiter")
	}
}

func TestNewRateLimiter_WithGlobalRequestLimit_ZeroWindow(t *testing.T) {
	rl, err := NewRateLimiter(RPM(10), WithGlobalRequestLimit(100, 0))
	if err == nil {
		t.Fatal("expected error for zero windowSeconds")
	}
	if rl != nil {
		t.Fatal("expected nil RateLimiter")
	}
}

func TestNewRateLimiter_WithGlobalRequestLimit_NegativeCount(t *testing.T) {
	rl, err := NewRateLimiter(RPM(10), WithGlobalRequestLimit(-1, 60))
	if err == nil {
		t.Fatal("expected error for negative count")
	}
	if rl != nil {
		t.Fatal("expected nil RateLimiter")
	}
}

func TestNewRateLimiter_WithGlobalTokenLimit_ZeroWindow(t *testing.T) {
	rl, err := NewRateLimiter(RPM(10), WithGlobalTokenLimit(1000, 0))
	if err == nil {
		t.Fatal("expected error for zero windowSeconds")
	}
	if rl != nil {
		t.Fatal("expected nil RateLimiter")
	}
}

func TestNewRateLimiter_WithGlobalTokenLimit_NegativeCount(t *testing.T) {
	rl, err := NewRateLimiter(RPM(10), WithGlobalTokenLimit(-5, 60))
	if err == nil {
		t.Fatal("expected error for negative count")
	}
	if rl != nil {
		t.Fatal("expected nil RateLimiter")
	}
}

func TestNewRateLimiter_MaxConcurrent_Zero(t *testing.T) {
	rl, err := NewRateLimiter(RPM(10), MaxConcurrent(0))
	if err == nil {
		t.Fatal("expected error for MaxConcurrent(0)")
	}
	if rl != nil {
		t.Fatal("expected nil RateLimiter")
	}
}

func TestNewRateLimiter_MaxConcurrent_Negative(t *testing.T) {
	rl, err := NewRateLimiter(RPM(10), MaxConcurrent(-3))
	if err == nil {
		t.Fatal("expected error for MaxConcurrent(-3)")
	}
	if rl != nil {
		t.Fatal("expected nil RateLimiter")
	}
}

func TestNewRateLimiter_RequestRateLimitProvided(t *testing.T) {
	rl, err := NewRateLimiter(RequestRateLimit(100, 30))
	if err != nil {
		t.Fatalf("expected success when RequestRateLimit provided, got: %v", err)
	}
	if rl == nil {
		t.Fatal("expected non-nil RateLimiter")
	}
	if rl.requestRateLimit == nil || rl.requestRateLimit.Count != 100 || rl.requestRateLimit.WindowSeconds != 30 {
		t.Error("requestRateLimit not set correctly")
	}
}

func TestNewRateLimiter_TokenRateLimitProvided(t *testing.T) {
	rl, err := NewRateLimiter(TokenRateLimit(5000, 120))
	if err != nil {
		t.Fatalf("expected success when TokenRateLimit provided, got: %v", err)
	}
	if rl == nil {
		t.Fatal("expected non-nil RateLimiter")
	}
}

func TestNewRateLimiter_GlobalRequestLimitProvided(t *testing.T) {
	rl, err := NewRateLimiter(WithGlobalRequestLimit(200, 60))
	if err != nil {
		t.Fatalf("expected success when WithGlobalRequestLimit provided, got: %v", err)
	}
	if rl == nil {
		t.Fatal("expected non-nil RateLimiter")
	}
}

func TestNewRateLimiter_MaxConcurrentProvided(t *testing.T) {
	rl, err := NewRateLimiter(MaxConcurrent(5))
	if err != nil {
		t.Fatalf("expected success when MaxConcurrent provided, got: %v", err)
	}
	if rl == nil {
		t.Fatal("expected non-nil RateLimiter")
	}
}

func TestNewRateLimiter_GlobalBucket_LazyInit_NotCreatedWithoutGlobalOptions(t *testing.T) {
	rl, err := NewRateLimiter(RPM(10), TPM(100))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rl.globalBucket != nil {
		t.Error("globalBucket should be nil when no global options are provided")
	}
}

func TestNewRateLimiter_GlobalBucket_LazyInit_CreatedWithGlobalRequestLimit(t *testing.T) {
	rl, err := NewRateLimiter(RPM(10), WithGlobalRequestLimit(100, 30))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rl.globalBucket == nil {
		t.Fatal("globalBucket should be created when WithGlobalRequestLimit is provided")
	}
	if rl.globalBucket.rpmLimit != 100 {
		t.Errorf("expected globalBucket.rpmLimit=100, got %d", rl.globalBucket.rpmLimit)
	}
}

func TestNewRateLimiter_GlobalBucket_LazyInit_CreatedWithGlobalTokenLimit(t *testing.T) {
	rl, err := NewRateLimiter(RPM(10), WithGlobalTokenLimit(5000, 60))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rl.globalBucket == nil {
		t.Fatal("globalBucket should be created when WithGlobalTokenLimit is provided")
	}
	if rl.globalBucket.tpmLimit != 5000 {
		t.Errorf("expected globalBucket.tpmLimit=5000, got %d", rl.globalBucket.tpmLimit)
	}
}

func TestNewRateLimiter_Semaphores_LazyInit_NotCreatedWithoutMaxConcurrent(t *testing.T) {
	rl, err := NewRateLimiter(RPM(10), TPM(100))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rl.semaphores != nil {
		t.Error("semaphores should be nil when MaxConcurrent is not configured")
	}
}

func TestNewRateLimiter_Semaphores_LazyInit_CreatedWithMaxConcurrent(t *testing.T) {
	rl, err := NewRateLimiter(RPM(10), MaxConcurrent(5))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rl.semaphores == nil {
		t.Fatal("semaphores should be initialized when MaxConcurrent is configured")
	}
	if rl.maxConcurrent != 5 {
		t.Errorf("expected maxConcurrent=5, got %d", rl.maxConcurrent)
	}
}

// ── Concurrency slot lease/release semantics ─────────────────────────────────
//
// These tests cover the original lease-handle design: the slot returned by
// Acquire is freed exclusively by calling the returned ReleaseFunc, never by
// Record. This decoupling prevents slot leaks when a provider call fails
// between Acquire and Record, which would otherwise permanently exhaust the
// per-key MaxConcurrent budget.

func TestRateLimiter_MaxConcurrent_ReleaseFreesSlot(t *testing.T) {
	rl, err := NewRateLimiter(RPM(100), MaxConcurrent(2), WithFailFast())
	if err != nil {
		t.Fatalf("NewRateLimiter failed: %v", err)
	}

	ctx := context.Background()

	// Acquire two slots — capacity is exhausted.
	r1, err := rl.Acquire(ctx, "k")
	if err != nil {
		t.Fatalf("acquire 1: %v", err)
	}
	r2, err := rl.Acquire(ctx, "k")
	if err != nil {
		t.Fatalf("acquire 2: %v", err)
	}

	// Third acquire fails (FailFast) since both slots are held.
	if _, err := rl.Acquire(ctx, "k"); err != ErrRateLimitExceeded {
		t.Fatalf("acquire 3: expected ErrRateLimitExceeded, got %v", err)
	}

	// Releasing one slot via the ReleaseFunc allows the next acquire to succeed.
	r1()
	r3, err := rl.Acquire(ctx, "k")
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	r2()
	r3()
}

func TestRateLimiter_MaxConcurrent_ReleaseIsIdempotent(t *testing.T) {
	rl, err := NewRateLimiter(RPM(100), MaxConcurrent(1), WithFailFast())
	if err != nil {
		t.Fatalf("NewRateLimiter failed: %v", err)
	}

	ctx := context.Background()
	release, err := rl.Acquire(ctx, "k")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	// Calling release twice must only free one slot (idempotency guard).
	release()
	release()

	// Acquire two more — only one should succeed if release is idempotent.
	r2, err := rl.Acquire(ctx, "k")
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	if _, err := rl.Acquire(ctx, "k"); err != ErrRateLimitExceeded {
		t.Fatalf("third acquire: expected ErrRateLimitExceeded (release must be idempotent), got %v", err)
	}
	r2()
}

// TestRateLimiter_MaxConcurrent_FailedCallsDoNotLeakSlots is the regression
// test for the original lease bug: when a caller acquires a slot but the
// downstream operation fails (so Record is never called), the slot must still
// be freed by the ReleaseFunc. Without this, every retry leaks one slot until
// MaxConcurrent failures permanently deadlock the key.
func TestRateLimiter_MaxConcurrent_FailedCallsDoNotLeakSlots(t *testing.T) {
	const n = 3
	rl, err := NewRateLimiter(RPM(100000), MaxConcurrent(n), WithFailFast())
	if err != nil {
		t.Fatalf("NewRateLimiter failed: %v", err)
	}

	ctx := context.Background()

	// Simulate many "failed" provider calls: Acquire then immediately release
	// without calling Record (the path that previously leaked slots).
	for i := 0; i < 100; i++ {
		release, err := rl.Acquire(ctx, "k")
		if err != nil {
			t.Fatalf("iteration %d: Acquire failed (slot leak suspected): %v", i, err)
		}
		release()
	}

	// After 100 acquire/release cycles all slots should still be available.
	holders := make([]ReleaseFunc, 0, n)
	for i := 0; i < n; i++ {
		release, err := rl.Acquire(ctx, "k")
		if err != nil {
			t.Fatalf("post-loop acquire %d: %v", i+1, err)
		}
		holders = append(holders, release)
	}
	for _, r := range holders {
		r()
	}
}
