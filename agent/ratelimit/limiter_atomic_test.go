package ratelimit

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// requestCount returns the in-window request count of an in-memory bucket.
func requestCount(b *rateBucket) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.windowStrategy == FixedWindow {
		return b.fixedRPMCountVal()
	}
	return b.slidingRPMCount()
}

func storeRequestCount(ms *MemoryStore, key string, window time.Duration) int {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	return len(pruneRequests(ms.requests[key], ms.now().Add(-window)))
}

func mustLimiter(t *testing.T, opts ...RateLimiterOption) *RateLimiter {
	t.Helper()
	rl, err := NewRateLimiter(opts...)
	if err != nil {
		t.Fatal(err)
	}
	return rl
}

func TestAcquire_GlobalRejectionDoesNotConsumePerKey(t *testing.T) {
	t.Run("memory", func(t *testing.T) {
		rl := mustLimiter(t, RPM(5), WithGlobalRPM(1))
		if _, err := rl.Acquire(context.Background(), "a"); err != nil {
			t.Fatal(err)
		}
		if _, err := rl.Acquire(context.Background(), "b"); !errors.Is(err, ErrRateLimitExceeded) {
			t.Fatalf("err = %v, want ErrRateLimitExceeded", err)
		}
		if n := requestCount(rl.bucket("b")); n != 0 {
			t.Fatalf("per-key count for b = %d, want 0", n)
		}
	})
	t.Run("store", func(t *testing.T) {
		ms := NewMemoryStore()
		rl := mustLimiter(t, RPM(5), WithGlobalRPM(1), WithStore(ms))
		if _, err := rl.Acquire(context.Background(), "a"); err != nil {
			t.Fatal(err)
		}
		if _, err := rl.Acquire(context.Background(), "b"); !errors.Is(err, ErrRateLimitExceeded) {
			t.Fatalf("err = %v, want ErrRateLimitExceeded", err)
		}
		if n := storeRequestCount(ms, storeKey("b"), time.Minute); n != 0 {
			t.Fatalf("per-key count for b = %d, want 0", n)
		}
	})
}

func TestAcquire_PerKeyRejectionDoesNotConsumeGlobal(t *testing.T) {
	t.Run("memory", func(t *testing.T) {
		rl := mustLimiter(t, RPM(1), WithGlobalRPM(10))
		_, _ = rl.Acquire(context.Background(), "a")
		if _, err := rl.Acquire(context.Background(), "a"); !errors.Is(err, ErrRateLimitExceeded) {
			t.Fatalf("err = %v, want ErrRateLimitExceeded", err)
		}
		if n := requestCount(rl.globalBucket); n != 1 {
			t.Fatalf("global count = %d, want 1", n)
		}
	})
	t.Run("store", func(t *testing.T) {
		ms := NewMemoryStore()
		rl := mustLimiter(t, RPM(1), WithGlobalRPM(10), WithStore(ms))
		_, _ = rl.Acquire(context.Background(), "a")
		if _, err := rl.Acquire(context.Background(), "a"); !errors.Is(err, ErrRateLimitExceeded) {
			t.Fatalf("err = %v, want ErrRateLimitExceeded", err)
		}
		if n := storeRequestCount(ms, storeGlobalKey, time.Minute); n != 1 {
			t.Fatalf("global count = %d, want 1", n)
		}
	})
}

func TestAcquire_ConcurrencyRejectionDoesNotConsumeRPM(t *testing.T) {
	for _, withStore := range []bool{false, true} {
		ms := NewMemoryStore()
		opts := []RateLimiterOption{RPM(10), WithGlobalRPM(10), MaxConcurrent(1)}
		if withStore {
			opts = append(opts, WithStore(ms))
		}
		rl := mustLimiter(t, opts...)
		release, err := rl.Acquire(context.Background(), "a")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := rl.Acquire(context.Background(), "a"); !errors.Is(err, ErrRateLimitExceeded) {
			t.Fatalf("store=%v: err = %v, want ErrRateLimitExceeded", withStore, err)
		}
		release()
		got := 0
		if withStore {
			got = storeRequestCount(ms, storeKey("a"), time.Minute)
		} else {
			got = requestCount(rl.bucket("a"))
		}
		if got != 1 {
			t.Fatalf("store=%v: request count = %d, want 1", withStore, got)
		}
	}
}

func TestAcquire_CancellationWhileAcquiringDoesNotConsumeRPM(t *testing.T) {
	t.Run("blocked on concurrency", func(t *testing.T) {
		rl := mustLimiter(t, RPM(10), MaxConcurrent(1), WithBlock())
		release, err := rl.Acquire(context.Background(), "a")
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		if _, err := rl.Acquire(ctx, "a"); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want DeadlineExceeded", err)
		}
		if n := requestCount(rl.bucket("a")); n != 1 {
			t.Fatalf("request count = %d, want 1", n)
		}
	})
	t.Run("blocked on rate", func(t *testing.T) {
		rl := mustLimiter(t, RPM(1), WithGlobalRPM(10), MaxConcurrent(5), WithBlock())
		r1, err := rl.Acquire(context.Background(), "a")
		if err != nil {
			t.Fatal(err)
		}
		r1()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		if _, err := rl.Acquire(ctx, "a"); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want DeadlineExceeded", err)
		}
		if n := requestCount(rl.globalBucket); n != 1 {
			t.Fatalf("global count = %d, want 1", n)
		}
		rl.mu.Lock()
		inflight := rl.semaphores["a"].inflight()
		rl.mu.Unlock()
		if inflight != 0 {
			t.Fatalf("cancelled Acquire still holds %d slot(s)", inflight)
		}
	})
}

func TestAcquire_ConcurrentReservationsNeverExceedLimit(t *testing.T) {
	const limit, callers = 10, 100
	run := func(t *testing.T, rl *RateLimiter, key func(i int) string) {
		var ok, rejected atomic.Int32
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < callers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				release, err := rl.Acquire(context.Background(), key(i))
				switch {
				case err == nil:
					ok.Add(1)
					release()
				case errors.Is(err, ErrRateLimitExceeded):
					rejected.Add(1)
				default:
					t.Errorf("unexpected error: %v", err)
				}
			}(i)
		}
		close(start)
		wg.Wait()
		if ok.Load() != limit || rejected.Load() != callers-limit {
			t.Fatalf("ok=%d rejected=%d, want %d and %d", ok.Load(), rejected.Load(), limit, callers-limit)
		}
	}
	same := func(int) string { return "k" }
	distinct := func(i int) string { return string(rune('a'+i%26)) + string(rune('a'+i/26)) }

	t.Run("memory per-key", func(t *testing.T) { run(t, mustLimiter(t, RPM(limit)), same) })
	t.Run("memory global across keys", func(t *testing.T) {
		rl := mustLimiter(t, RPM(1000), WithGlobalRPM(limit))
		run(t, rl, distinct)
		total := 0
		for i := 0; i < callers; i++ {
			total += requestCount(rl.bucket(distinct(i)))
		}
		if total != limit {
			t.Fatalf("sum of per-key counts = %d, want %d (per-key and global must be charged together)", total, limit)
		}
	})
	t.Run("store per-key", func(t *testing.T) { run(t, mustLimiter(t, RPM(limit), WithStore(NewMemoryStore())), same) })
	t.Run("store global across keys", func(t *testing.T) {
		ms := NewMemoryStore()
		run(t, mustLimiter(t, RPM(1000), WithGlobalRPM(limit), WithStore(ms)), distinct)
		total := 0
		for i := 0; i < callers; i++ {
			total += storeRequestCount(ms, storeKey(distinct(i)), time.Minute)
		}
		if total != limit {
			t.Fatalf("sum of per-key counts = %d, want %d", total, limit)
		}
	})
}

func TestMemoryStore_ReserveRequestsAllOrNothing(t *testing.T) {
	ms := NewMemoryStore()
	ctx := context.Background()
	full := RequestReservation{Key: "full", Limit: 1, Window: time.Minute}
	free := RequestReservation{Key: "free", Limit: 5, Window: time.Minute}
	if ok, err := ms.ReserveRequests(ctx, []RequestReservation{full}); !ok || err != nil {
		t.Fatalf("first reservation = %v, %v", ok, err)
	}
	if ok, err := ms.ReserveRequests(ctx, []RequestReservation{free, full}); ok || err != nil {
		t.Fatalf("reservation over limit = %v, %v, want false, nil", ok, err)
	}
	if n := storeRequestCount(ms, "free", time.Minute); n != 0 {
		t.Fatalf("rejected reservation charged free counter %d time(s)", n)
	}

	var ok atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if allowed, _ := ms.ReserveRequests(ctx, []RequestReservation{{Key: "race", Limit: 10, Window: time.Minute}}); allowed {
				ok.Add(1)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 10 {
		t.Fatalf("concurrent reservations allowed = %d, want 10", ok.Load())
	}
}

func TestRecord_UpdatesPerKeyAndGlobalTogether(t *testing.T) {
	ctx := context.Background()
	t.Run("memory", func(t *testing.T) {
		rl := mustLimiter(t, TPM(1000), WithGlobalTPM(1000))
		if err := rl.Record(ctx, "a", TokenUsage{InputTokens: 30, OutputTokens: 12}); err != nil {
			t.Fatal(err)
		}
		for name, b := range map[string]*rateBucket{"per-key": rl.bucket("a"), "global": rl.globalBucket} {
			b.mu.Lock()
			n := b.tokenCountLocked()
			b.mu.Unlock()
			if n != 42 {
				t.Fatalf("%s tokens = %d, want 42", name, n)
			}
		}
	})
	t.Run("store", func(t *testing.T) {
		ms := NewMemoryStore()
		rl := mustLimiter(t, TPM(1000), WithGlobalTPM(1000), WithStore(ms))
		if err := rl.Record(ctx, "a", TokenUsage{InputTokens: 30, OutputTokens: 12}); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{storeKey("a"), storeGlobalKey} {
			if n, _ := ms.GetTokenCount(ctx, key, time.Minute); n != 42 {
				t.Fatalf("%s tokens = %d, want 42", key, n)
			}
		}
	})
}

// TestRecord_SurvivesCallerCancellation verifies accounting is not erased
// when the caller's context is already cancelled.
func TestRecord_SurvivesCallerCancellation(t *testing.T) {
	ms := NewMemoryStore()
	rl := mustLimiter(t, TPM(1000), WithStore(ms))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := rl.Record(ctx, "a", TokenUsage{InputTokens: 7}); err != nil {
		t.Fatal(err)
	}
	if n, _ := ms.GetTokenCount(context.Background(), storeKey("a"), time.Minute); n != 7 {
		t.Fatalf("tokens = %d, want 7", n)
	}
}

func TestPreFlightCheck_EnforcesGlobalTPM(t *testing.T) {
	ctx := context.Background()
	for _, withStore := range []bool{false, true} {
		opts := []RateLimiterOption{TPM(10_000), WithGlobalTPM(100), WithTokenEstimator(&mockEstimator{estimate: 150})}
		if withStore {
			opts = append(opts, WithStore(NewMemoryStore()))
		}
		rl := mustLimiter(t, opts...)
		if err := rl.PreFlightCheck(ctx, "a", ModelRequest{}); !errors.Is(err, ErrRateLimitExceeded) {
			t.Fatalf("store=%v: err = %v, want ErrRateLimitExceeded (estimate fits per-key but not global)", withStore, err)
		}
	}

	// Global-only TPM enables the default estimator too.
	rl := mustLimiter(t, WithGlobalTPM(1))
	if rl.tokenEstimator == nil {
		t.Fatal("global TPM should enable the default pre-flight estimator")
	}
	// Remaining global budget is consumed by recorded usage.
	rl = mustLimiter(t, WithGlobalTPM(100), WithTokenEstimator(&mockEstimator{estimate: 50}))
	_ = rl.Record(ctx, "x", TokenUsage{InputTokens: 60})
	if err := rl.PreFlightCheck(ctx, "y", ModelRequest{}); !errors.Is(err, ErrRateLimitExceeded) {
		t.Fatalf("err = %v, want ErrRateLimitExceeded after global usage", err)
	}
}

func TestRateLimiter_LongWindowSurvivesStaleSweep(t *testing.T) {
	rl := mustLimiter(t, RequestRateLimit(1, 300))
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := base
	rl.now = func() time.Time { return now }

	if _, err := rl.Acquire(context.Background(), "k"); err != nil {
		t.Fatal(err)
	}
	// Idle well past the old hardcoded 60s threshold; a sweep runs.
	now = base.Add(120 * time.Second)
	_ = rl.bucket("other")
	if _, err := rl.Acquire(context.Background(), "k"); !errors.Is(err, ErrRateLimitExceeded) {
		t.Fatalf("err = %v, want ErrRateLimitExceeded inside a 300s window", err)
	}
	now = base.Add(301 * time.Second)
	if _, err := rl.Acquire(context.Background(), "k"); err != nil {
		t.Fatalf("Acquire after the window: %v", err)
	}
	if rl.staleAfter != 300*time.Second {
		t.Fatalf("staleAfter = %v, want 300s", rl.staleAfter)
	}
}

func TestRateLimiter_PurgeSemaphores(t *testing.T) {
	semCount := func(rl *RateLimiter) int {
		rl.mu.Lock()
		defer rl.mu.Unlock()
		return len(rl.semaphores)
	}

	t.Run("idle semaphore is removed", func(t *testing.T) {
		rl := mustLimiter(t, MaxConcurrent(1))
		release, err := rl.Acquire(context.Background(), "k")
		if err != nil {
			t.Fatal(err)
		}
		release()
		rl.Purge("k")
		if n := semCount(rl); n != 0 {
			t.Fatalf("semaphores = %d, want 0", n)
		}
	})

	t.Run("active semaphore is kept", func(t *testing.T) {
		rl := mustLimiter(t, MaxConcurrent(1))
		release, err := rl.Acquire(context.Background(), "k")
		if err != nil {
			t.Fatal(err)
		}
		rl.Purge("k")
		if _, err := rl.Acquire(context.Background(), "k"); !errors.Is(err, ErrRateLimitExceeded) {
			t.Fatalf("err = %v, want ErrRateLimitExceeded (purge must not reset in-flight concurrency)", err)
		}
		release()
		release2, err := rl.Acquire(context.Background(), "k")
		if err != nil {
			t.Fatalf("Acquire after release: %v", err)
		}
		release2()
	})

	t.Run("stale sweep removes idle semaphores", func(t *testing.T) {
		rl := mustLimiter(t, MaxConcurrent(1))
		base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		now := base
		rl.now = func() time.Time { return now }
		release, _ := rl.Acquire(context.Background(), "idle")
		release()
		held, _ := rl.Acquire(context.Background(), "held")
		defer held()
		now = base.Add(staleSweepInterval)
		_ = rl.bucket("trigger")
		rl.mu.Lock()
		_, idleKept := rl.semaphores["idle"]
		_, heldKept := rl.semaphores["held"]
		rl.mu.Unlock()
		if idleKept || !heldKept {
			t.Fatalf("idle kept=%v held kept=%v, want false/true", idleKept, heldKept)
		}
	})
}

func TestAcquire_BlockModeCancellationReleasesSlot(t *testing.T) {
	rl := mustLimiter(t, MaxConcurrent(1), WithBlock())
	held, err := rl.Acquire(context.Background(), "k")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
			defer cancel()
			if _, err := rl.Acquire(ctx, "k"); !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("err = %v, want DeadlineExceeded", err)
			}
		}()
	}
	wg.Wait()
	held()
	rl.mu.Lock()
	refs := rl.semaphores["k"].refs
	rl.mu.Unlock()
	if refs != 0 {
		t.Fatalf("semaphore refs = %d after all callers finished, want 0", refs)
	}
}
