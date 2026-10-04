package ratelimit

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fixedEstimator struct {
	n   int
	err error
}

func (e fixedEstimator) EstimateTokens(context.Context, ModelRequest) (int, error) { return e.n, e.err }

func TestAcquireLeaseConcurrentTPMReservation(t *testing.T) {
	for _, useStore := range []bool{false, true} {
		t.Run(map[bool]string{false: "in-memory", true: "memory-store"}[useStore], func(t *testing.T) {
			opts := []RateLimiterOption{TPM(1000), WithTokenEstimator(fixedEstimator{n: 500})}
			if useStore {
				opts = append(opts, WithStore(NewMemoryStore()))
			}
			rl := mustLimiter(t, opts...)
			var admitted atomic.Int32
			var wg sync.WaitGroup
			start := make(chan struct{})
			for range 10 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					lease, err := rl.AcquireLease(context.Background(), "key", ModelRequest{})
					if err == nil {
						admitted.Add(1)
						lease.Release()
						return
					}
					if !errors.Is(err, ErrRateLimitExceeded) {
						t.Errorf("AcquireLease: %v", err)
					}
				}()
			}
			close(start)
			wg.Wait()
			if got := admitted.Load(); got != 2 {
				t.Fatalf("admitted = %d, want 2", got)
			}
		})
	}
}

func TestAcquireLeaseGlobalTokenRejectionLeavesNoPerKeyReservation(t *testing.T) {
	for _, useStore := range []bool{false, true} {
		t.Run(map[bool]string{false: "in-memory", true: "memory-store"}[useStore], func(t *testing.T) {
			opts := []RateLimiterOption{TPM(500), WithGlobalTPM(200), WithTokenEstimator(fixedEstimator{n: 300})}
			if useStore {
				opts = append(opts, WithStore(NewMemoryStore()))
			}
			rl := mustLimiter(t, opts...)
			if _, err := rl.AcquireLease(context.Background(), "a", ModelRequest{}); !errors.Is(err, ErrRateLimitExceeded) {
				t.Fatalf("AcquireLease = %v, want ErrRateLimitExceeded", err)
			}
			if useStore {
				ms := rl.store.(*MemoryStore)
				ms.mu.Lock()
				defer ms.mu.Unlock()
				if c := ms.counters["tok:"+storeKey("a")]; c != nil && c.reserved != 0 {
					t.Fatalf("per-key reservation = %d, want 0", c.reserved)
				}
			} else {
				bucket := rl.bucket("a")
				bucket.mu.Lock()
				reserved := bucket.reservedTPM
				bucket.mu.Unlock()
				if reserved != 0 {
					t.Fatalf("per-key reservation = %d, want 0", reserved)
				}
			}
		})
	}
}

func TestRateLimitLeaseReconcilesActualUsage(t *testing.T) {
	for _, useStore := range []bool{false, true} {
		t.Run(map[bool]string{false: "in-memory", true: "memory-store"}[useStore], func(t *testing.T) {
			opts := []RateLimiterOption{TPM(1000), WithTokenEstimator(fixedEstimator{n: 500})}
			if useStore {
				opts = append(opts, WithStore(NewMemoryStore()))
			}
			rl := mustLimiter(t, opts...)
			lease, err := rl.AcquireLease(context.Background(), "key", ModelRequest{})
			if err != nil {
				t.Fatal(err)
			}
			if err := lease.Commit(context.Background(), TokenUsage{InputTokens: 300}); err != nil {
				t.Fatal(err)
			}
			lease.Release()

			rl.tokenEstimator = fixedEstimator{n: 700}
			lease, err = rl.AcquireLease(context.Background(), "key", ModelRequest{})
			if err != nil {
				t.Fatalf("second admission = %v", err)
			}
			if err := lease.Commit(context.Background(), TokenUsage{InputTokens: 700}); err != nil {
				t.Fatal(err)
			}
			lease.Release()
			rl.tokenEstimator = fixedEstimator{n: 1}
			if _, err := rl.AcquireLease(context.Background(), "key", ModelRequest{}); !errors.Is(err, ErrRateLimitExceeded) {
				t.Fatalf("post-reconciliation admission = %v, want ErrRateLimitExceeded", err)
			}
		})
	}
}

func TestRateLimitLeaseActualOverEstimateAndFailureRemainCharged(t *testing.T) {
	rl := mustLimiter(t, TPM(1000), WithTokenEstimator(fixedEstimator{n: 500}))
	lease, err := rl.AcquireLease(context.Background(), "key", ModelRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Commit(context.Background(), TokenUsage{InputTokens: 700}); err != nil {
		t.Fatal(err)
	}
	lease.Release()
	if _, err := rl.AcquireLease(context.Background(), "key", ModelRequest{}); !errors.Is(err, ErrRateLimitExceeded) {
		t.Fatalf("actual over-estimate was not retained: %v", err)
	}

	rl = mustLimiter(t, TPM(1000), WithTokenEstimator(fixedEstimator{n: 500}))
	lease, err = rl.AcquireLease(context.Background(), "key", ModelRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Fail(context.Background()); err != nil {
		t.Fatal(err)
	}
	lease.Release()
	rl.tokenEstimator = fixedEstimator{n: 600}
	if _, err := rl.AcquireLease(context.Background(), "key", ModelRequest{}); !errors.Is(err, ErrRateLimitExceeded) {
		t.Fatalf("failed attempt reservation was refunded: %v", err)
	}
}

func TestRateLimitLeaseTerminalMethodsAreIdempotent(t *testing.T) {
	rl := mustLimiter(t, TPM(1000), MaxConcurrent(1), WithTokenEstimator(fixedEstimator{n: 500}))
	lease, err := rl.AcquireLease(context.Background(), "key", ModelRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Commit(context.Background(), TokenUsage{InputTokens: 300}); err != nil {
		t.Fatal(err)
	}
	if err := lease.Commit(context.Background(), TokenUsage{InputTokens: 900}); err != nil {
		t.Fatal(err)
	}
	if err := lease.Fail(context.Background()); err != nil {
		t.Fatal(err)
	}
	lease.Release()
	lease.Release()
	rl.tokenEstimator = fixedEstimator{n: 800}
	if _, err := rl.AcquireLease(context.Background(), "key", ModelRequest{}); !errors.Is(err, ErrRateLimitExceeded) {
		t.Fatalf("double terminal transition corrupted token accounting: %v", err)
	}
}

func TestMemoryStoreLeaseFixedWindow(t *testing.T) {
	ms := NewMemoryStore()
	ms.now = func() time.Time { return time.Unix(0, 0) }
	reservation := RateLimitReservation{ID: "one", Tokens: []TokenReservation{{Key: "key", Limit: 1, Window: time.Minute, Strategy: FixedWindow, Amount: 1}}}
	ok, err := ms.ReserveLease(context.Background(), reservation)
	if err != nil || !ok {
		t.Fatalf("ReserveLease = %v, %v", ok, err)
	}
	ok, err = ms.ReserveLease(context.Background(), RateLimitReservation{ID: "two", Tokens: reservation.Tokens})
	if err != nil || ok {
		t.Fatalf("fixed second reservation = %v, %v", ok, err)
	}
	ms.now = func() time.Time { return time.Unix(61, 0) }
	ok, err = ms.ReserveLease(context.Background(), RateLimitReservation{ID: "three", Tokens: reservation.Tokens})
	if err != nil || !ok {
		t.Fatalf("fixed reservation after reset = %v, %v", ok, err)
	}
}
