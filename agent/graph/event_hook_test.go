package graph

import (
	"context"
	"errors"
	"testing"
)

// ── recording event hook for tests ───────────────────────────────────────────

type recordingHook struct {
	events []GraphEvent
}

func (h *recordingHook) OnEvent(event GraphEvent) {
	h.events = append(h.events, event)
}

// ── event hook unit tests ────────────────────────────────────────────────────

func TestEventHook_GraphStartedIsFirstEvent(t *testing.T) {
	hook := &recordingHook{}
	g := mustGraph(t, WithEventHook(hook))

	mustAddNodeWithKeys(t, g, "a", setter("a", "done_a"), []string{"a"}, []string{})
	mustAddNodeWithKeys(t, g, "b", setter("b", "done_b"), []string{"b"}, []string{"a"})
	g.Start("a")

	_, err := g.Run(context.Background(), State{"init": "yes"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(hook.events) == 0 {
		t.Fatal("expected at least one event, got none")
	}
	if hook.events[0].Type != EventGraphStarted {
		t.Errorf("expected first event to be GraphStarted, got %s", hook.events[0].Type)
	}
	// Verify initial state snapshot is present.
	if hook.events[0].StateSnapshot["init"] != "yes" {
		t.Errorf("expected initial state snapshot to contain init=yes, got %v", hook.events[0].StateSnapshot)
	}
}

func TestEventHook_GraphCompletedIsLastEvent(t *testing.T) {
	hook := &recordingHook{}
	g := mustGraph(t, WithEventHook(hook))

	mustAddNodeWithKeys(t, g, "a", setter("a", "done_a"), []string{"a"}, []string{})
	mustAddNodeWithKeys(t, g, "b", setter("b", "done_b"), []string{"b"}, []string{"a"})
	g.Start("a")

	_, err := g.Run(context.Background(), State{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(hook.events) == 0 {
		t.Fatal("expected at least one event, got none")
	}
	last := hook.events[len(hook.events)-1]
	if last.Type != EventGraphCompleted {
		t.Errorf("expected last event to be GraphCompleted, got %s", last.Type)
	}
	// Verify final state snapshot contains results from both nodes.
	if last.StateSnapshot["a"] != "done_a" {
		t.Errorf("expected final state[a]=done_a, got %v", last.StateSnapshot["a"])
	}
	if last.StateSnapshot["b"] != "done_b" {
		t.Errorf("expected final state[b]=done_b, got %v", last.StateSnapshot["b"])
	}
	// Verify error is nil on successful completion.
	if last.Error != nil {
		t.Errorf("expected nil error on GraphCompleted, got %v", last.Error)
	}
}

func TestEventHook_NodeStartedAndCompletedPairs(t *testing.T) {
	hook := &recordingHook{}
	g := mustGraph(t, WithEventHook(hook))

	mustAddNodeWithKeys(t, g, "a", setter("a", "done_a"), []string{"a"}, []string{})
	mustAddNodeWithKeys(t, g, "b", setter("b", "done_b"), []string{"b"}, []string{"a"})
	mustAddNodeWithKeys(t, g, "c", setter("c", "done_c"), []string{"c"}, []string{"b"})
	g.Start("a")

	_, err := g.Run(context.Background(), State{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Collect NodeStarted and NodeCompleted events.
	type nodePair struct {
		started   bool
		completed bool
	}
	pairs := make(map[string]*nodePair)

	for _, ev := range hook.events {
		switch ev.Type {
		case EventNodeStarted:
			if _, ok := pairs[ev.NodeName]; !ok {
				pairs[ev.NodeName] = &nodePair{}
			}
			pairs[ev.NodeName].started = true
		case EventNodeCompleted:
			if _, ok := pairs[ev.NodeName]; !ok {
				pairs[ev.NodeName] = &nodePair{}
			}
			pairs[ev.NodeName].completed = true
		}
	}

	// Verify each node has both started and completed events.
	for _, name := range []string{"a", "b", "c"} {
		p, ok := pairs[name]
		if !ok {
			t.Errorf("no events found for node %q", name)
			continue
		}
		if !p.started {
			t.Errorf("missing NodeStarted event for node %q", name)
		}
		if !p.completed {
			t.Errorf("missing NodeCompleted event for node %q", name)
		}
	}

	// Verify NodeStarted comes before NodeCompleted for each node.
	for _, name := range []string{"a", "b", "c"} {
		startIdx := -1
		completeIdx := -1
		for i, ev := range hook.events {
			if ev.NodeName == name && ev.Type == EventNodeStarted {
				startIdx = i
			}
			if ev.NodeName == name && ev.Type == EventNodeCompleted {
				completeIdx = i
			}
		}
		if startIdx >= completeIdx {
			t.Errorf("NodeStarted (idx=%d) should come before NodeCompleted (idx=%d) for node %q",
				startIdx, completeIdx, name)
		}
	}
}

func TestEventHook_CheckpointSavedEvents(t *testing.T) {
	hook := &recordingHook{}
	cp := &mockCheckpointer{}
	g := mustGraph(t, WithCheckpointer(cp), WithEventHook(hook))

	mustAddNodeWithKeys(t, g, "a", setter("a", "done_a"), []string{"a"}, []string{})
	mustAddNodeWithKeys(t, g, "b", setter("b", "done_b"), []string{"b"}, []string{"a"})
	g.Start("a")

	_, err := g.Run(context.Background(), State{}, WithThreadID("thread-cp-events"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Collect CheckpointSaved events.
	var cpEvents []GraphEvent
	for _, ev := range hook.events {
		if ev.Type == EventCheckpointSaved {
			cpEvents = append(cpEvents, ev)
		}
	}

	// Should have one checkpoint per node (a and b).
	if len(cpEvents) != 2 {
		t.Fatalf("expected 2 CheckpointSaved events, got %d", len(cpEvents))
	}

	// Verify versions are sequential.
	if cpEvents[0].Version != 1 {
		t.Errorf("expected first checkpoint version=1, got %d", cpEvents[0].Version)
	}
	if cpEvents[1].Version != 2 {
		t.Errorf("expected second checkpoint version=2, got %d", cpEvents[1].Version)
	}

	// Verify node names.
	if cpEvents[0].NodeName != "a" {
		t.Errorf("expected first checkpoint node='a', got %q", cpEvents[0].NodeName)
	}
	if cpEvents[1].NodeName != "b" {
		t.Errorf("expected second checkpoint node='b', got %q", cpEvents[1].NodeName)
	}
}

func TestEventHook_InterruptFiredEvent(t *testing.T) {
	hook := &recordingHook{}
	cp := &mockCheckpointer{}
	g := mustGraph(t, WithCheckpointer(cp), WithEventHook(hook))

	mustAddNodeWithKeys(t, g, "a", setter("a", "done_a"), []string{"a"}, []string{})
	mustAddNodeWithKeys(t, g, "b", setter("b", "done_b"), []string{"b"}, []string{"a"})
	mustAddNodeWithKeys(t, g, "c", setter("c", "done_c"), []string{"c"}, []string{"b"})
	g.Start("a")

	if err := g.InterruptBefore("b"); err != nil {
		t.Fatalf("InterruptBefore failed: %v", err)
	}

	_, err := g.Run(context.Background(), State{}, WithThreadID("thread-int-event"))
	var intErr *GraphInterruptError
	if !errors.As(err, &intErr) {
		t.Fatalf("expected GraphInterruptError, got %T: %v", err, err)
	}

	// Find InterruptFired event.
	var intEvents []GraphEvent
	for _, ev := range hook.events {
		if ev.Type == EventInterruptFired {
			intEvents = append(intEvents, ev)
		}
	}

	if len(intEvents) != 1 {
		t.Fatalf("expected 1 InterruptFired event, got %d", len(intEvents))
	}

	intEv := intEvents[0]
	if intEv.NodeName != "b" {
		t.Errorf("expected InterruptFired node='b', got %q", intEv.NodeName)
	}
	if intEv.InterruptType != InterruptTypeBefore {
		t.Errorf("expected InterruptType=before, got %q", intEv.InterruptType)
	}
	if intEv.Version == 0 {
		t.Error("expected non-zero version on InterruptFired event")
	}
}

func TestEventHook_InterruptAfterFiredEvent(t *testing.T) {
	hook := &recordingHook{}
	cp := &mockCheckpointer{}
	g := mustGraph(t, WithCheckpointer(cp), WithEventHook(hook))

	mustAddNodeWithKeys(t, g, "a", setter("a", "done_a"), []string{"a"}, []string{})
	mustAddNodeWithKeys(t, g, "b", setter("b", "done_b"), []string{"b"}, []string{"a"})
	mustAddNodeWithKeys(t, g, "c", setter("c", "done_c"), []string{"c"}, []string{"b"})
	g.Start("a")

	if err := g.InterruptAfter("b"); err != nil {
		t.Fatalf("InterruptAfter failed: %v", err)
	}

	_, err := g.Run(context.Background(), State{}, WithThreadID("thread-int-after-event"))
	var intErr *GraphInterruptError
	if !errors.As(err, &intErr) {
		t.Fatalf("expected GraphInterruptError, got %T: %v", err, err)
	}

	// Find InterruptFired event.
	var intEvents []GraphEvent
	for _, ev := range hook.events {
		if ev.Type == EventInterruptFired {
			intEvents = append(intEvents, ev)
		}
	}

	if len(intEvents) != 1 {
		t.Fatalf("expected 1 InterruptFired event, got %d", len(intEvents))
	}

	intEv := intEvents[0]
	if intEv.NodeName != "b" {
		t.Errorf("expected InterruptFired node='b', got %q", intEv.NodeName)
	}
	if intEv.InterruptType != InterruptTypeAfter {
		t.Errorf("expected InterruptType=after, got %q", intEv.InterruptType)
	}
}

func TestEventHook_NoEventsWithoutHook(t *testing.T) {
	// When no event hook is configured, execution should work normally.
	g := mustGraph(t) // No WithEventHook

	mustAddNodeWithKeys(t, g, "a", setter("a", "done_a"), []string{"a"}, []string{})
	mustAddNodeWithKeys(t, g, "b", setter("b", "done_b"), []string{"b"}, []string{"a"})
	g.Start("a")

	res, err := g.Run(context.Background(), State{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify execution still works correctly.
	if res.State["a"] != "done_a" {
		t.Errorf("expected state[a]=done_a, got %v", res.State["a"])
	}
	if res.State["b"] != "done_b" {
		t.Errorf("expected state[b]=done_b, got %v", res.State["b"])
	}
}

func TestEventHook_AllEventsHaveNonZeroTimestamps(t *testing.T) {
	hook := &recordingHook{}
	g := mustGraph(t, WithEventHook(hook))

	mustAddNodeWithKeys(t, g, "a", setter("a", "done_a"), []string{"a"}, []string{})
	mustAddNodeWithKeys(t, g, "b", setter("b", "done_b"), []string{"b"}, []string{"a"})
	g.Start("a")

	_, err := g.Run(context.Background(), State{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for i, ev := range hook.events {
		if ev.Timestamp.IsZero() {
			t.Errorf("event %d (%s) has zero timestamp", i, ev.Type)
		}
	}

	// Verify timestamps are in non-decreasing order.
	for i := 1; i < len(hook.events); i++ {
		if hook.events[i].Timestamp.Before(hook.events[i-1].Timestamp) {
			t.Errorf("event %d (%s) timestamp %v is before event %d (%s) timestamp %v",
				i, hook.events[i].Type, hook.events[i].Timestamp,
				i-1, hook.events[i-1].Type, hook.events[i-1].Timestamp)
		}
	}
}

func TestEventHook_ThreadIDPopulatedWithCheckpointer(t *testing.T) {
	hook := &recordingHook{}
	cp := &mockCheckpointer{}
	g := mustGraph(t, WithCheckpointer(cp), WithEventHook(hook))

	mustAddNodeWithKeys(t, g, "a", setter("a", "done_a"), []string{"a"}, []string{})
	mustAddNodeWithKeys(t, g, "b", setter("b", "done_b"), []string{"b"}, []string{"a"})
	g.Start("a")

	threadID := "thread-event-id"
	_, err := g.Run(context.Background(), State{}, WithThreadID(threadID))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for i, ev := range hook.events {
		if ev.ThreadID != threadID {
			t.Errorf("event %d (%s) has ThreadID=%q, expected %q",
				i, ev.Type, ev.ThreadID, threadID)
		}
	}
}

func TestEventHook_GraphCompletedWithError(t *testing.T) {
	hook := &recordingHook{}
	cp := &mockCheckpointer{}
	g := mustGraph(t, WithCheckpointer(cp), WithEventHook(hook))

	mustAddNodeWithKeys(t, g, "a", setter("a", "done_a"), []string{"a"}, []string{})
	mustAddNodeWithKeys(t, g, "b", setter("b", "done_b"), []string{"b"}, []string{"a"})
	g.Start("a")

	// InterruptBefore "b" will cause an interrupt error.
	if err := g.InterruptBefore("b"); err != nil {
		t.Fatalf("InterruptBefore failed: %v", err)
	}

	_, err := g.Run(context.Background(), State{}, WithThreadID("thread-err-event"))
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Find GraphCompleted event - it should be the last event.
	last := hook.events[len(hook.events)-1]
	if last.Type != EventGraphCompleted {
		t.Errorf("expected last event to be GraphCompleted, got %s", last.Type)
	}
	// The error should be the interrupt error.
	if last.Error == nil {
		t.Error("expected non-nil error on GraphCompleted when interrupted")
	}
}

func TestEventHook_EventSequenceOrder(t *testing.T) {
	hook := &recordingHook{}
	g := mustGraph(t, WithEventHook(hook))

	mustAddNodeWithKeys(t, g, "a", setter("a", "done_a"), []string{"a"}, []string{})
	mustAddNodeWithKeys(t, g, "b", setter("b", "done_b"), []string{"b"}, []string{"a"})
	g.Start("a")

	_, err := g.Run(context.Background(), State{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Expected sequence: GraphStarted, NodeStarted(a), NodeCompleted(a),
	// NodeStarted(b), NodeCompleted(b), GraphCompleted
	expectedTypes := []EventType{
		EventGraphStarted,
		EventNodeStarted,
		EventNodeCompleted,
		EventNodeStarted,
		EventNodeCompleted,
		EventGraphCompleted,
	}

	if len(hook.events) != len(expectedTypes) {
		t.Fatalf("expected %d events, got %d", len(expectedTypes), len(hook.events))
	}

	for i, expected := range expectedTypes {
		if hook.events[i].Type != expected {
			t.Errorf("event %d: expected %s, got %s", i, expected, hook.events[i].Type)
		}
	}
}

func TestEventHook_EventSequenceWithCheckpointer(t *testing.T) {
	hook := &recordingHook{}
	cp := &mockCheckpointer{}
	g := mustGraph(t, WithCheckpointer(cp), WithEventHook(hook))

	mustAddNodeWithKeys(t, g, "a", setter("a", "done_a"), []string{"a"}, []string{})
	mustAddNodeWithKeys(t, g, "b", setter("b", "done_b"), []string{"b"}, []string{"a"})
	g.Start("a")

	_, err := g.Run(context.Background(), State{}, WithThreadID("thread-seq"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Expected sequence: GraphStarted, NodeStarted(a), NodeCompleted(a), CheckpointSaved(a),
	// NodeStarted(b), NodeCompleted(b), CheckpointSaved(b), GraphCompleted
	expectedTypes := []EventType{
		EventGraphStarted,
		EventNodeStarted,
		EventNodeCompleted,
		EventCheckpointSaved,
		EventNodeStarted,
		EventNodeCompleted,
		EventCheckpointSaved,
		EventGraphCompleted,
	}

	if len(hook.events) != len(expectedTypes) {
		t.Fatalf("expected %d events, got %d; events: %v", len(expectedTypes), len(hook.events), eventTypes(hook.events))
	}

	for i, expected := range expectedTypes {
		if hook.events[i].Type != expected {
			t.Errorf("event %d: expected %s, got %s", i, expected, hook.events[i].Type)
		}
	}
}

func TestEventHook_NodeCompletedHasUpdatedState(t *testing.T) {
	hook := &recordingHook{}
	g := mustGraph(t, WithEventHook(hook))

	mustAddNodeWithKeys(t, g, "a", setter("a", "done_a"), []string{"a"}, []string{})
	g.Start("a")

	_, err := g.Run(context.Background(), State{"init": "yes"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Find NodeCompleted event for "a".
	var nodeCompleted *GraphEvent
	for i := range hook.events {
		if hook.events[i].Type == EventNodeCompleted && hook.events[i].NodeName == "a" {
			nodeCompleted = &hook.events[i]
			break
		}
	}

	if nodeCompleted == nil {
		t.Fatal("NodeCompleted event for 'a' not found")
	}

	// State should contain both the initial state and the node's output.
	if nodeCompleted.StateSnapshot["init"] != "yes" {
		t.Errorf("expected state[init]=yes, got %v", nodeCompleted.StateSnapshot["init"])
	}
	if nodeCompleted.StateSnapshot["a"] != "done_a" {
		t.Errorf("expected state[a]=done_a, got %v", nodeCompleted.StateSnapshot["a"])
	}
}

func TestEventHook_NodeStartedHasPreExecutionState(t *testing.T) {
	hook := &recordingHook{}
	g := mustGraph(t, WithEventHook(hook))

	mustAddNodeWithKeys(t, g, "a", setter("a", "done_a"), []string{"a"}, []string{})
	mustAddNodeWithKeys(t, g, "b", setter("b", "done_b"), []string{"b"}, []string{"a"})
	g.Start("a")

	_, err := g.Run(context.Background(), State{"init": "yes"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Find NodeStarted event for "b".
	var nodeStarted *GraphEvent
	for i := range hook.events {
		if hook.events[i].Type == EventNodeStarted && hook.events[i].NodeName == "b" {
			nodeStarted = &hook.events[i]
			break
		}
	}

	if nodeStarted == nil {
		t.Fatal("NodeStarted event for 'b' not found")
	}

	// State should contain "a"'s result (since "a" ran before "b").
	if nodeStarted.StateSnapshot["a"] != "done_a" {
		t.Errorf("expected state[a]=done_a before node 'b', got %v", nodeStarted.StateSnapshot["a"])
	}
	// State should NOT contain "b"'s result yet.
	if _, exists := nodeStarted.StateSnapshot["b"]; exists {
		t.Error("NodeStarted state for 'b' should NOT contain 'b' result yet")
	}
}

// ── helper ───────────────────────────────────────────────────────────────────

func eventTypes(events []GraphEvent) []EventType {
	types := make([]EventType, len(events))
	for i, ev := range events {
		types[i] = ev.Type
	}
	return types
}
