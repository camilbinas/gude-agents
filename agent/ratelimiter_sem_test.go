package agent

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewConcurrencySem(t *testing.T) {
	s := newConcurrencySem(5)
	if s.max != 5 {
		t.Errorf("expected max=5, got %d", s.max)
	}
	if s.inflight != 0 {
		t.Errorf("expected inflight=0, got %d", s.inflight)
	}
	if s.cond == nil {
		t.Error("expected cond to be initialized")
	}
}

func TestConcurrencySem_Acquire_FailFast(t *testing.T) {
	s := newConcurrencySem(2)
	ctx := context.Background()

	// Acquire two slots successfully.
	if err := s.Acquire(ctx, FailFastMode); err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	if err := s.Acquire(ctx, FailFastMode); err != nil {
		t.Fatalf("second acquire failed: %v", err)
	}

	// Third acquire should fail fast.
	err := s.Acquire(ctx, FailFastMode)
	if err != ErrRateLimitExceeded {
		t.Errorf("expected ErrRateLimitExceeded, got %v", err)
	}
}

func TestConcurrencySem_Release(t *testing.T) {
	s := newConcurrencySem(1)
	ctx := context.Background()

	if err := s.Acquire(ctx, FailFastMode); err != nil {
		t.Fatalf("acquire failed: %v", err)
	}

	// At capacity — should fail.
	if err := s.Acquire(ctx, FailFastMode); err != ErrRateLimitExceeded {
		t.Fatalf("expected ErrRateLimitExceeded, got %v", err)
	}

	// Release and try again.
	s.Release()

	if err := s.Acquire(ctx, FailFastMode); err != nil {
		t.Fatalf("acquire after release failed: %v", err)
	}
}

func TestConcurrencySem_Acquire_BlockMode(t *testing.T) {
	s := newConcurrencySem(1)
	ctx := context.Background()

	if err := s.Acquire(ctx, BlockMode); err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}

	// Second acquire in BlockMode should block until release.
	done := make(chan error, 1)
	go func() {
		done <- s.Acquire(ctx, BlockMode)
	}()

	// Give the goroutine time to block.
	time.Sleep(20 * time.Millisecond)

	select {
	case <-done:
		t.Fatal("acquire should have blocked")
	default:
	}

	// Release the slot — the blocked goroutine should proceed.
	s.Release()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("blocked acquire returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for blocked acquire to complete")
	}
}

func TestConcurrencySem_Acquire_ContextCancellation(t *testing.T) {
	s := newConcurrencySem(1)
	ctx := context.Background()

	if err := s.Acquire(ctx, BlockMode); err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}

	// Second acquire with a context that will be cancelled.
	cancelCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- s.Acquire(cancelCtx, BlockMode)
	}()

	// Give goroutine time to block.
	time.Sleep(20 * time.Millisecond)

	// Cancel the context.
	cancel()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for context cancellation")
	}
}

func TestConcurrencySem_ConcurrentAccess(t *testing.T) {
	const maxConcurrent = 3
	const numGoroutines = 10

	s := newConcurrencySem(maxConcurrent)
	ctx := context.Background()

	var peak atomic.Int32
	var wg sync.WaitGroup

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Acquire(ctx, BlockMode); err != nil {
				t.Errorf("acquire failed: %v", err)
				return
			}
			cur := peak.Add(1)
			if cur > maxConcurrent {
				t.Errorf("inflight %d exceeded max %d", cur, maxConcurrent)
			}
			time.Sleep(5 * time.Millisecond)
			peak.Add(-1)
			s.Release()
		}()
	}

	wg.Wait()
}

func TestConcurrencySem_Release_NoUnderflow(t *testing.T) {
	s := newConcurrencySem(5)

	// Calling Release without any Acquire should not underflow.
	s.Release()

	if s.inflight != 0 {
		t.Errorf("expected inflight=0 after spurious release, got %d", s.inflight)
	}
}
