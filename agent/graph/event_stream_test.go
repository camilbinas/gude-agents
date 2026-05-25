package graph

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func drainGraphEvents(s *EventStream[State]) []GraphEvent {
	var out []GraphEvent
	for ev := range s.Events() {
		out = append(out, ev)
	}
	return out
}

func graphEventTypes(events []GraphEvent) []EventType {
	out := make([]EventType, len(events))
	for i, e := range events {
		out[i] = e.Type
	}
	return out
}

func countEventType(events []GraphEvent, t EventType) int {
	n := 0
	for _, e := range events {
		if e.Type == t {
			n++
		}
	}
	return n
}

// TestRunEventStream_BasicLifecycle verifies the event channel emits the
// expected start/node/end sequence and the typed Result is returned.
func TestRunEventStream_BasicLifecycle(t *testing.T) {
	g := mustGraph(t)
	mustAddNodeWithKeys(t, g, "step", setter("output", "done"), []string{"output"}, []string{})

	stream := g.RunEventStream(context.Background(), State{})
	events := drainGraphEvents(stream)

	if len(events) == 0 {
		t.Fatal("expected events, got none")
	}
	if events[0].Type != EventGraphStarted {
		t.Errorf("first event = %s, want %s", events[0].Type, EventGraphStarted)
	}
	last := events[len(events)-1]
	if last.Type != EventGraphCompleted {
		t.Errorf("last event = %s, want %s", last.Type, EventGraphCompleted)
	}
	if last.Error != nil {
		t.Errorf("last event Error = %v, want nil", last.Error)
	}
	if countEventType(events, EventNodeStarted) != 1 || countEventType(events, EventNodeCompleted) != 1 {
		t.Errorf("expected 1 NodeStarted + 1 NodeCompleted, got sequence: %v", graphEventTypes(events))
	}

	res, err := stream.Result()
	if err != nil {
		t.Fatalf("Result err = %v, want nil", err)
	}
	if res.State["output"] != "done" {
		t.Errorf("State[output] = %v, want %q", res.State["output"], "done")
	}
}

// TestRunEventStream_NodeError surfaces an error via Result and a final
// EventGraphCompleted with Error populated.
func TestRunEventStream_NodeError(t *testing.T) {
	g := mustGraph(t)
	wantErr := errors.New("boom")
	mustAddNodeWithKeys(t, g, "fail",
		func(_ context.Context, _ State) (State, error) { return nil, wantErr },
		[]string{"output"}, []string{})

	stream := g.RunEventStream(context.Background(), State{})
	events := drainGraphEvents(stream)

	if last := events[len(events)-1]; last.Type != EventGraphCompleted {
		t.Fatalf("last event = %s, want %s", last.Type, EventGraphCompleted)
	}
	if last := events[len(events)-1]; !errors.Is(last.Error, wantErr) {
		t.Errorf("last event Error = %v, want it to wrap %v", last.Error, wantErr)
	}
	if _, err := stream.Result(); !errors.Is(err, wantErr) {
		t.Errorf("Result err = %v, want it to wrap %v", err, wantErr)
	}
}

// TestRunEventStream_PreservesUpstreamHook proves that an existing
// graph-level GraphEventHook still receives events when RunEventStream is used.
func TestRunEventStream_PreservesUpstreamHook(t *testing.T) {
	upstream := &concurrentRecordingHook{}
	g, err := New[State](WithEventHook(upstream))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mustAddNodeWithKeys(t, g, "step", setter("output", "ok"), []string{"output"}, []string{})

	stream := g.RunEventStream(context.Background(), State{})
	events := drainGraphEvents(stream)

	if len(events) == 0 {
		t.Fatal("expected stream events")
	}
	if upstream.count() == 0 {
		t.Fatal("upstream hook received no events; channel hook clobbered it")
	}
	if upstream.count() != len(events) {
		t.Errorf("upstream got %d events, channel got %d — counts should match", upstream.count(), len(events))
	}
	if _, err := stream.Result(); err != nil {
		t.Fatalf("Result err = %v, want nil", err)
	}
}

// TestRunEventStream_ConcurrentRuns confirms two RunEventStream calls on the
// same graph don't clobber each other's hooks. Without per-run hook injection
// this would race on g.eventHook.
func TestRunEventStream_ConcurrentRuns(t *testing.T) {
	g := mustGraph(t)
	mustAddNodeWithKeys(t, g, "step", setter("output", "ok"), []string{"output"}, []string{})

	var wg sync.WaitGroup
	wg.Add(2)

	var aGraphCompleted, bGraphCompleted atomic.Int32
	for i := 0; i < 2; i++ {
		i := i
		go func() {
			defer wg.Done()
			stream := g.RunEventStream(context.Background(), State{})
			for ev := range stream.Events() {
				if ev.Type == EventGraphCompleted {
					if i == 0 {
						aGraphCompleted.Add(1)
					} else {
						bGraphCompleted.Add(1)
					}
				}
			}
			if _, err := stream.Result(); err != nil {
				t.Errorf("worker %d: Result err = %v", i, err)
			}
		}()
	}
	wg.Wait()

	if aGraphCompleted.Load() != 1 || bGraphCompleted.Load() != 1 {
		t.Errorf("expected each run to see exactly 1 GraphCompleted, got a=%d b=%d",
			aGraphCompleted.Load(), bGraphCompleted.Load())
	}
}

// TestRunEventStream_WithRunOption ensures RunOption forwarding works
// (smoke-tested via WithThreadID, which is harmless without a checkpointer).
func TestRunEventStream_WithRunOption(t *testing.T) {
	g := mustGraph(t)
	mustAddNodeWithKeys(t, g, "step", setter("output", "ok"), []string{"output"}, []string{})

	stream := g.RunEventStream(context.Background(), State{},
		WithRunOption(WithThreadID("t-1")),
	)
	events := drainGraphEvents(stream)
	completed := events[len(events)-1]
	if completed.Type != EventGraphCompleted {
		t.Fatalf("last = %s, want GraphCompleted", completed.Type)
	}
	if completed.ThreadID != "t-1" {
		t.Errorf("GraphCompleted.ThreadID = %q, want %q", completed.ThreadID, "t-1")
	}
}

// TestRunEventStream_BufferOption sanity-checks small + zero/negative buffers.
func TestRunEventStream_BufferOption(t *testing.T) {
	g := mustGraph(t)
	mustAddNodeWithKeys(t, g, "step", setter("output", "ok"), []string{"output"}, []string{})

	for _, buf := range []int{1, 0, -10} {
		stream := g.RunEventStream(context.Background(), State{},
			WithEventStreamBuffer(buf),
		)
		events := drainGraphEvents(stream)
		if last := events[len(events)-1]; last.Type != EventGraphCompleted || last.Error != nil {
			t.Errorf("buffer=%d: bad terminal event %+v", buf, last)
		}
		if _, err := stream.Result(); err != nil {
			t.Errorf("buffer=%d: Result err = %v", buf, err)
		}
	}
}

// concurrentRecordingHook is a goroutine-safe GraphEventHook used by tests
// that may run concurrent graph executions.
type concurrentRecordingHook struct {
	mu     sync.Mutex
	events []GraphEvent
}

func (h *concurrentRecordingHook) OnEvent(e GraphEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, e)
}

func (h *concurrentRecordingHook) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.events)
}
