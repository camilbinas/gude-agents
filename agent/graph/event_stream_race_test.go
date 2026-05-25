package graph

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"
)

// waitForGoroutineCount polls until NumGoroutine drops to target or deadline.
func waitForGoroutineCount(target int, deadline time.Time) int {
	for {
		got := runtime.NumGoroutine()
		if got <= target {
			return got
		}
		if time.Now().After(deadline) {
			return got
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestRunEventStream_NoGoroutineLeak runs RunEventStream many times and
// asserts the goroutine count returns to baseline. Catches leaks in the
// stream goroutine, the panic-recover deferred close, or the engine.
func TestRunEventStream_NoGoroutineLeak(t *testing.T) {
	g := mustGraph(t)
	mustAddNodeWithKeys(t, g, "step", setter("output", "ok"), []string{"output"}, []string{})

	for i := 0; i < 5; i++ {
		stream := g.RunEventStream(context.Background(), State{})
		for range stream.Events() {
		}
		if _, err := stream.Result(); err != nil {
			t.Fatalf("warm-up Result: %v", err)
		}
	}
	runtime.GC()
	baseline := runtime.NumGoroutine()

	const iterations = 200
	for i := 0; i < iterations; i++ {
		stream := g.RunEventStream(context.Background(), State{})
		for range stream.Events() {
		}
		if _, err := stream.Result(); err != nil {
			t.Fatalf("Result %d: %v", i, err)
		}
	}
	runtime.GC()

	got := waitForGoroutineCount(baseline+2, time.Now().Add(2*time.Second))
	if got > baseline+2 {
		t.Errorf("goroutine leak: started=%d, after %d runs=%d", baseline, iterations, got)
	}
}

// TestRunEventStream_ConcurrentStress runs many concurrent RunEventStream
// calls on the same graph. With per-call extraEventHook this is race-free;
// with the previous SetEventHook pattern it would have clobbered state.
// Best run with -race for the strongest signal.
func TestRunEventStream_ConcurrentStress(t *testing.T) {
	g := mustGraph(t)
	mustAddNodeWithKeys(t, g, "step", setter("output", "ok"), []string{"output"}, []string{})

	const workers = 16
	const runsPerWorker = 25

	var wg sync.WaitGroup
	wg.Add(workers)
	errCh := make(chan error, workers*runsPerWorker)

	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			for i := 0; i < runsPerWorker; i++ {
				stream := g.RunEventStream(context.Background(), State{})
				var sawCompleted bool
				for ev := range stream.Events() {
					if ev.Type == EventGraphCompleted {
						sawCompleted = true
					}
				}
				if !sawCompleted {
					errCh <- &graphCompletedMissingError{}
					return
				}
				if _, err := stream.Result(); err != nil {
					errCh <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent stream worker: %v", err)
	}
}

type graphCompletedMissingError struct{}

func (graphCompletedMissingError) Error() string {
	return "stream did not emit EventGraphCompleted"
}

// TestRunEventStream_ContextCancelTerminatesStream verifies that cancelling
// the run's context closes the stream and exits the spawned goroutine in
// bounded time. Slow node fn so we can cancel mid-flight.
func TestRunEventStream_ContextCancelTerminatesStream(t *testing.T) {
	g := mustGraph(t)
	mustAddNodeWithKeys(t, g, "slow",
		func(ctx context.Context, s State) (State, error) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(2 * time.Second):
				return s, nil
			}
		},
		[]string{"output"}, []string{})

	ctx, cancel := context.WithCancel(context.Background())
	stream := g.RunEventStream(ctx, State{}, WithEventStreamBuffer(1))

	// Drain the GraphStarted event then cancel.
	first, ok := <-stream.Events()
	if !ok {
		t.Fatal("channel closed before any event")
	}
	if first.Type != EventGraphStarted {
		t.Fatalf("first event = %s, want %s", first.Type, EventGraphStarted)
	}
	cancel()

	done := make(chan struct{})
	go func() {
		for range stream.Events() {
		}
		close(done)
	}()

	select {
	case <-done:
		// Channel closed in bounded time; goroutine cleanly exited.
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not close within 3s after context cancel")
	}
}
