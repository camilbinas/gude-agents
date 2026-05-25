package agent

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent/prompt"
)

// waitForGoroutineCount polls runtime.NumGoroutine until it reaches target
// or the deadline expires. Goroutine cleanup after channel close is not
// instantaneous, so a tight equality check would be flaky.
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

// TestInvokeEventStream_NoGoroutineLeak runs InvokeEventStream many times
// in sequence and asserts that the number of goroutines returns to (close to)
// baseline. Catches the "leaks one goroutine per failed run" class of bug,
// which is exactly what the recover() + close(ch) machinery is supposed to
// prevent.
func TestInvokeEventStream_NoGoroutineLeak(t *testing.T) {
	p := streamingTextProvider("hello world", 4)
	a, err := New(p, prompt.Text("sys"), nil)
	if err != nil {
		t.Fatal(err)
	}

	// Warm up — the first few runs allocate runtime structures that don't
	// belong to our leak budget.
	for i := 0; i < 5; i++ {
		for range a.InvokeEventStream(Background(), "hi") {
		}
	}
	runtime.GC()
	baseline := runtime.NumGoroutine()

	const iterations = 200
	for i := 0; i < iterations; i++ {
		for range a.InvokeEventStream(Background(), "hi") {
		}
	}
	runtime.GC()

	got := waitForGoroutineCount(baseline+2, time.Now().Add(2*time.Second))
	// Allow a small slack — Go runtime parks/unparks worker goroutines.
	if got > baseline+2 {
		t.Errorf("goroutine leak: started=%d, after %d runs=%d (slack=2)",
			baseline, iterations, got)
	}
}

// TestInvokeEventStream_ConcurrentSafe stresses the per-call clone in
// InvokeEventStream by running many streams concurrently against the same
// shared *Context. Without the clone, the fan-in EventHook would race on the
// shared context's eventHook field. Run with -race for the strongest signal.
func TestInvokeEventStream_ConcurrentSafe(t *testing.T) {
	p := streamingTextProvider("hello world", 4)
	a, err := New(p, prompt.Text("sys"), nil)
	if err != nil {
		t.Fatal(err)
	}

	const workers = 16
	const runsPerWorker = 25

	// All workers share one *Context — exactly the pattern that broke under
	// the original (mutating) implementation.
	shared := Background()

	var wg sync.WaitGroup
	wg.Add(workers)
	errs := make(chan error, workers*runsPerWorker)

	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			for i := 0; i < runsPerWorker; i++ {
				var lastType EventType
				for ev := range a.InvokeEventStream(shared, "hi") {
					lastType = ev.Type
				}
				if lastType != EventInvokeEnd {
					errs <- &mismatchedTerminalEventError{got: string(lastType)}
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent stream worker: %v", err)
	}

	// Caller's context must still be usable for follow-up plain Invokes —
	// proves the clone didn't leak a stale hook back into shared.
	if _, err := a.Invoke(shared, "after concurrent streams"); err != nil {
		t.Fatalf("post-stress Invoke failed: %v", err)
	}
}

type mismatchedTerminalEventError struct{ got string }

func (e *mismatchedTerminalEventError) Error() string {
	return "expected EventInvokeEnd as last event, got " + e.got
}

// TestInvokeEventStream_ContextCancelTerminatesStream verifies that the
// stream goroutine exits when the caller cancels its context, even if the
// consumer stops reading. Without proper cancellation propagation this
// would deadlock the agent loop and leak the goroutine.
func TestInvokeEventStream_ContextCancelTerminatesStream(t *testing.T) {
	// Provider that streams a lot of chunks slowly so we have time to cancel.
	p := streamingTextProvider("the quick brown fox jumps over the lazy dog and runs away into the night", 64)
	a, err := New(p, prompt.Text("sys"), nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	c := NewContext(ctx)

	events := a.InvokeEventStream(c, "hi", WithEventStreamBuffer(1))

	// Drain the InvokeStart so the goroutine is past setup, then cancel and
	// drain to completion. Stream must close in bounded time without deadlock.
	first, ok := <-events
	if !ok {
		t.Fatal("channel closed before any event")
	}
	if first.Type != EventInvokeStart {
		t.Fatalf("first event = %s, want %s", first.Type, EventInvokeStart)
	}
	cancel()

	done := make(chan struct{})
	go func() {
		for range events {
		}
		close(done)
	}()

	select {
	case <-done:
		// Good — channel closed.
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not close within 2s after context cancel")
	}
}
