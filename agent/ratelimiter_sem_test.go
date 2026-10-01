package agent

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewConcurrencySem(t *testing.T) {
	s := newConcurrencySem(5)
	if s.capacity() != 5 {
		t.Errorf("capacity = %d, want 5", s.capacity())
	}
	if s.inflight() != 0 {
		t.Errorf("inflight = %d, want 0", s.inflight())
	}
}

func TestConcurrencySem_Acquire_FailFast(t *testing.T) {
	s := newConcurrencySem(2)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := s.Acquire(ctx, FailFastMode); err != nil {
			t.Fatalf("Acquire #%d: %v", i, err)
		}
	}
	if err := s.Acquire(ctx, FailFastMode); !errors.Is(err, ErrRateLimitExceeded) {
		t.Fatalf("Acquire at capacity = %v, want ErrRateLimitExceeded", err)
	}
	if s.inflight() != 2 {
		t.Fatalf("inflight = %d, want 2", s.inflight())
	}
}

func TestConcurrencySem_Release(t *testing.T) {
	s := newConcurrencySem(1)
	ctx := context.Background()
	if err := s.Acquire(ctx, FailFastMode); err != nil {
		t.Fatal(err)
	}
	s.Release()
	if err := s.Acquire(ctx, FailFastMode); err != nil {
		t.Fatalf("Acquire after Release: %v", err)
	}
}

func TestConcurrencySem_Acquire_BlockModeWakeup(t *testing.T) {
	s := newConcurrencySem(1)
	if err := s.Acquire(context.Background(), BlockMode); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Acquire(context.Background(), BlockMode) }()

	select {
	case err := <-done:
		t.Fatalf("Acquire returned early: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	s.Release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("woken Acquire: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("blocked Acquire was not woken by Release")
	}
}

func TestConcurrencySem_Acquire_ContextCancellation(t *testing.T) {
	s := newConcurrencySem(1)
	if err := s.Acquire(context.Background(), BlockMode); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Acquire(ctx, BlockMode) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation did not unblock Acquire")
	}
	if s.inflight() != 1 {
		t.Fatalf("inflight = %d, want 1 (cancelled waiter must not take a slot)", s.inflight())
	}
}

func TestConcurrencySem_AlreadyCancelledNeverTakesSlot(t *testing.T) {
	s := newConcurrencySem(1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, mode := range []OverflowBehavior{BlockMode, FailFastMode} {
		if err := s.Acquire(ctx, mode); !errors.Is(err, context.Canceled) {
			t.Fatalf("mode %v: err = %v, want context.Canceled", mode, err)
		}
	}
	if s.inflight() != 0 {
		t.Fatalf("inflight = %d, want 0", s.inflight())
	}
}

func TestConcurrencySem_Release_NoUnderflow(t *testing.T) {
	s := newConcurrencySem(5)
	s.Release()
	s.Release()
	if s.inflight() != 0 {
		t.Fatalf("inflight = %d after spurious releases, want 0", s.inflight())
	}
	for i := 0; i < 5; i++ {
		if err := s.Acquire(context.Background(), FailFastMode); err != nil {
			t.Fatalf("Acquire #%d: %v", i, err)
		}
	}
	if err := s.Acquire(context.Background(), FailFastMode); !errors.Is(err, ErrRateLimitExceeded) {
		t.Fatal("spurious releases must not raise capacity")
	}
}

// TestConcurrencySem_StressManyWaiters runs many blocked waiters with random
// cancellation and concurrent releases, checking capacity is never exceeded
// and no waiter goroutines leak. Run with -race.
func TestConcurrencySem_StressManyWaiters(t *testing.T) {
	const (
		capacity = 4
		waiters  = 200
	)
	s := newConcurrencySem(capacity)
	before := runtime.NumGoroutine()

	var inside, maxInside, acquired, cancelled atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < waiters; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx := context.Background()
			var cancel context.CancelFunc = func() {}
			if i%3 == 0 {
				ctx, cancel = context.WithTimeout(ctx, time.Duration(i%7)*time.Millisecond)
			}
			defer cancel()
			if err := s.Acquire(ctx, BlockMode); err != nil {
				cancelled.Add(1)
				return
			}
			n := inside.Add(1)
			for {
				m := maxInside.Load()
				if n <= m || maxInside.CompareAndSwap(m, n) {
					break
				}
			}
			time.Sleep(time.Millisecond)
			inside.Add(-1)
			acquired.Add(1)
			s.Release()
		}(i)
	}
	wg.Wait()

	if maxInside.Load() > capacity {
		t.Fatalf("max concurrent holders = %d, capacity %d", maxInside.Load(), capacity)
	}
	if acquired.Load()+cancelled.Load() != waiters {
		t.Fatalf("acquired %d + cancelled %d != %d", acquired.Load(), cancelled.Load(), waiters)
	}
	if s.inflight() != 0 {
		t.Fatalf("inflight = %d after all releases, want 0", s.inflight())
	}
	// The channel semaphore spawns no helper goroutines per waiter.
	time.Sleep(20 * time.Millisecond)
	if after := runtime.NumGoroutine(); after > before+5 {
		t.Fatalf("goroutines grew from %d to %d", before, after)
	}
}

// TestConcurrencySem_FailFastAtCapacityConcurrent verifies exactly capacity
// concurrent FailFast acquisitions succeed.
func TestConcurrencySem_FailFastAtCapacityConcurrent(t *testing.T) {
	const capacity = 3
	s := newConcurrencySem(capacity)
	var ok, rejected atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := s.Acquire(context.Background(), FailFastMode); err == nil {
				ok.Add(1)
			} else if errors.Is(err, ErrRateLimitExceeded) {
				rejected.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if ok.Load() != capacity || rejected.Load() != 100-capacity {
		t.Fatalf("ok=%d rejected=%d, want %d and %d", ok.Load(), rejected.Load(), capacity, 100-capacity)
	}
}
