package ratelimit

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agent "github.com/camilbinas/gude-agents/agent"
)

func TestMemoryRPMOnlyTerminalRetention(t *testing.T) {
	store := memoryStore(t)
	now := time.Unix(0, 0)
	store.now = func() time.Time { return now }
	reservation := Reservation{ID: "rpm-commit", Requests: []RequestCounter{{Key: "customer", Limit: 10, Window: time.Minute, Strategy: SlidingWindow}}}
	ok, err := store.Reserve(context.Background(), reservation)
	if err != nil || !ok {
		t.Fatalf("Reserve = %v, %v", ok, err)
	}
	if err := store.Commit(context.Background(), reservation.ID, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.Commit(context.Background(), reservation.ID, 0); !errors.Is(err, agent.ErrRateLimitLeaseTerminal) {
		t.Fatalf("duplicate RPM-only Commit = %v, want terminal", err)
	}

	reservation.ID = "rpm-release"
	ok, err = store.Reserve(context.Background(), reservation)
	if err != nil || !ok {
		t.Fatalf("second Reserve = %v, %v", ok, err)
	}
	if err := store.Release(context.Background(), reservation.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Release(context.Background(), reservation.ID); !errors.Is(err, agent.ErrRateLimitLeaseTerminal) {
		t.Fatalf("duplicate RPM-only Release = %v, want terminal", err)
	}
}

func TestMemoryTerminalRetentionUsesLongestReservationWindow(t *testing.T) {
	store := memoryStore(t)
	now := time.Unix(0, 0)
	store.now = func() time.Time { return now }
	reservation := Reservation{
		ID:       "mixed-window",
		Requests: []RequestCounter{{Key: "customer", Limit: 10, Window: 2 * time.Minute, Strategy: SlidingWindow}},
		Tokens:   []TokenReservation{{Key: "customer", Limit: 100, Window: time.Minute, Strategy: SlidingWindow, Amount: 10}},
	}
	ok, err := store.Reserve(context.Background(), reservation)
	if err != nil || !ok {
		t.Fatalf("Reserve = %v, %v", ok, err)
	}
	if err := store.Commit(context.Background(), reservation.ID, 5); err != nil {
		t.Fatal(err)
	}
	now = now.Add(90 * time.Second)
	if err := store.Commit(context.Background(), reservation.ID, 5); !errors.Is(err, agent.ErrRateLimitLeaseTerminal) {
		t.Fatalf("terminal record expired with RPM window still live: %v", err)
	}
	now = now.Add(31 * time.Second)
	_, _ = store.Reserve(context.Background(), Reservation{ID: "sweep", Requests: []RequestCounter{{Key: "other", Limit: 10, Window: time.Minute, Strategy: SlidingWindow}}})
	if err := store.Commit(context.Background(), reservation.ID, 5); !errors.Is(err, agent.ErrRateLimitLeaseUnknown) {
		t.Fatalf("terminal record retained beyond longest window: %v", err)
	}
}

func TestLeaseOnlyConcurrentTokenAdmission(t *testing.T) {
	limiter := limiterForTest(t, TPM(100), WithTokenEstimator(fixedEstimator{n: 50}), WithFailFast())
	var admitted atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			lease, err := limiter.Reserve(context.Background(), agent.RateLimitRequest{Key: "customer"})
			if err == nil {
				admitted.Add(1)
				_ = limiter.Release(context.Background(), lease)
				return
			}
			if !errors.Is(err, agent.ErrRateLimitExceeded) {
				t.Errorf("Reserve = %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if got := admitted.Load(); got != 2 {
		t.Fatalf("admitted = %d, want 2", got)
	}
}

func TestLeaseOnlyBlockModeHonorsCancellation(t *testing.T) {
	limiter := limiterForTest(t, RPM(1), WithBlock())
	first, err := limiter.Reserve(context.Background(), agent.RateLimitRequest{Key: "customer"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := limiter.Reserve(ctx, agent.RateLimitRequest{Key: "customer"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked Reserve = %v, want deadline", err)
	}
	if err := limiter.Release(context.Background(), first); err != nil {
		t.Fatal(err)
	}
}

func TestLeaseOnlyFixedWindowResetsAdmission(t *testing.T) {
	store := memoryStore(t)
	now := time.Unix(0, 0)
	store.now = func() time.Time { return now }
	reservation := Reservation{ID: "one", Requests: []RequestCounter{{Key: "customer", Limit: 1, Window: time.Minute, Strategy: FixedWindow}}}
	ok, err := store.Reserve(context.Background(), reservation)
	if err != nil || !ok {
		t.Fatalf("first Reserve = %v, %v", ok, err)
	}
	reservation.ID = "two"
	if ok, err = store.Reserve(context.Background(), reservation); err != nil || ok {
		t.Fatalf("same fixed window Reserve = %v, %v", ok, err)
	}
	now = now.Add(time.Minute + time.Nanosecond)
	if ok, err = store.Reserve(context.Background(), reservation); err != nil || !ok {
		t.Fatalf("next fixed window Reserve = %v, %v", ok, err)
	}
}

func TestMemoryRPMOnlyTerminalRetentionExpiresAtRequestWindow(t *testing.T) {
	store := memoryStore(t)
	now := time.Unix(0, 0)
	store.now = func() time.Time { return now }
	reservation := Reservation{ID: "rpm", Requests: []RequestCounter{{Key: "customer", Limit: 10, Window: time.Minute, Strategy: SlidingWindow}}}
	ok, err := store.Reserve(context.Background(), reservation)
	if err != nil || !ok {
		t.Fatalf("Reserve = %v, %v", ok, err)
	}
	if err := store.Commit(context.Background(), reservation.ID, 0); err != nil {
		t.Fatal(err)
	}
	now = now.Add(59 * time.Second)
	_, _ = store.Reserve(context.Background(), Reservation{ID: "trigger-before", Requests: []RequestCounter{{Key: "other", Limit: 10, Window: time.Minute, Strategy: SlidingWindow}}})
	if err := store.Commit(context.Background(), reservation.ID, 0); !errors.Is(err, agent.ErrRateLimitLeaseTerminal) {
		t.Fatalf("RPM terminal lease expired early: %v", err)
	}
	now = now.Add(2 * time.Second)
	_, _ = store.Reserve(context.Background(), Reservation{ID: "trigger-after", Requests: []RequestCounter{{Key: "other", Limit: 10, Window: time.Minute, Strategy: SlidingWindow}}})
	if err := store.Commit(context.Background(), reservation.ID, 0); !errors.Is(err, agent.ErrRateLimitLeaseUnknown) {
		t.Fatalf("RPM terminal lease retained too long: %v", err)
	}
}

func TestLeaseGlobalRequestRejectionDoesNotChargePerKey(t *testing.T) {
	store := memoryStore(t)
	global := Reservation{ID: "fill-global", Requests: []RequestCounter{{Key: "global", Limit: 1, Window: time.Minute, Strategy: SlidingWindow}}}
	if ok, err := store.Reserve(context.Background(), global); err != nil || !ok {
		t.Fatalf("fill global = %v, %v", ok, err)
	}
	rejected := Reservation{ID: "rejected", Requests: []RequestCounter{
		{Key: "key:a", Limit: 10, Window: time.Minute, Strategy: SlidingWindow},
		{Key: "global", Limit: 1, Window: time.Minute, Strategy: SlidingWindow},
	}}
	if ok, err := store.Reserve(context.Background(), rejected); err != nil || ok {
		t.Fatalf("global rejection = %v, %v", ok, err)
	}
	store.mu.Lock()
	counter := store.counters["r:key:a"]
	count := 0
	if counter != nil {
		count = counter.requestCount()
	}
	store.mu.Unlock()
	if count != 0 {
		t.Fatalf("per-key RPM was charged on global rejection: %d", count)
	}
}

func TestLeaseBlockModeUnblocksAfterWindowExpiry(t *testing.T) {
	limiter := limiterForTest(t, RequestRateLimit(1, 1), WithBlock())
	first, err := limiter.Reserve(context.Background(), agent.RateLimitRequest{Key: "customer"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = limiter.Release(context.Background(), first) }()
	done := make(chan error, 1)
	go func() {
		_, err := limiter.Reserve(context.Background(), agent.RateLimitRequest{Key: "customer"})
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("blocked reservation returned early: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("blocked reservation did not unblock after window expiry")
	}
}

func TestLeaseFixedWindowTokensAndGlobalLimits(t *testing.T) {
	store := memoryStore(t)
	now := time.Unix(0, 0)
	store.now = func() time.Time { return now }
	reservation := Reservation{ID: "one", Tokens: []TokenReservation{
		{Key: "key:a", Limit: 10, Window: time.Minute, Strategy: FixedWindow, Amount: 6},
		{Key: "global", Limit: 10, Window: time.Minute, Strategy: FixedWindow, Amount: 6},
	}}
	if ok, err := store.Reserve(context.Background(), reservation); err != nil || !ok {
		t.Fatalf("first reserve = %v, %v", ok, err)
	}
	reservation.ID = "two"
	if ok, err := store.Reserve(context.Background(), reservation); err != nil || ok {
		t.Fatalf("fixed global token rejection = %v, %v", ok, err)
	}
	now = now.Add(time.Minute + time.Nanosecond)
	if ok, err := store.Reserve(context.Background(), reservation); err != nil || !ok {
		t.Fatalf("fixed token reserve after reset = %v, %v", ok, err)
	}
}
