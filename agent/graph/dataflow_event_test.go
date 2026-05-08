package graph

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// ── Collecting event hook for data-flow event tests ──────────────────────────

type collectingHook struct {
	events []GraphEvent
	mu     sync.Mutex
}

func (h *collectingHook) OnEvent(e GraphEvent) {
	h.mu.Lock()
	h.events = append(h.events, e)
	h.mu.Unlock()
}

// ── Task 13.1 & 13.2: Event emission for data-flow nodes ────────────────────

func TestDataFlowEvent_NodeStartedAndCompletedForSequentialNodes(t *testing.T) {
	// Verify EventNodeStarted emitted before each node executes and
	// EventNodeCompleted emitted after each node completes for sequential data-flow nodes.
	hook := &collectingHook{}
	g := mustGraph(t, WithEventHook(hook))

	// Linear chain: entry → b → c
	mustAddNodeWithKeys(t, g, "entry", func(_ context.Context, s State) (State, error) {
		out := CopyState(s)
		out["x"] = "from_entry"
		return out, nil
	}, []string{"x"}, []string{})
	mustAddNodeWithKeys(t, g, "b", func(_ context.Context, s State) (State, error) {
		out := CopyState(s)
		out["y"] = "from_b"
		return out, nil
	}, []string{"y"}, []string{"x"})
	mustAddNodeWithKeys(t, g, "c", func(_ context.Context, s State) (State, error) {
		out := CopyState(s)
		out["z"] = "from_c"
		return out, nil
	}, []string{"z"}, []string{"y"})
	g.Start("entry")

	_, err := g.Run(context.Background(), State{"init": "yes"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hook.mu.Lock()
	events := make([]GraphEvent, len(hook.events))
	copy(events, hook.events)
	hook.mu.Unlock()

	// Verify each node has a NodeStarted followed by NodeCompleted.
	for _, nodeName := range []string{"entry", "b", "c"} {
		startIdx := -1
		completeIdx := -1
		for i, ev := range events {
			if ev.NodeName == nodeName && ev.Type == EventNodeStarted {
				startIdx = i
			}
			if ev.NodeName == nodeName && ev.Type == EventNodeCompleted {
				completeIdx = i
			}
		}
		if startIdx == -1 {
			t.Errorf("missing NodeStarted event for node %q", nodeName)
			continue
		}
		if completeIdx == -1 {
			t.Errorf("missing NodeCompleted event for node %q", nodeName)
			continue
		}
		if startIdx >= completeIdx {
			t.Errorf("NodeStarted (idx=%d) should come before NodeCompleted (idx=%d) for node %q",
				startIdx, completeIdx, nodeName)
		}
	}

	// Verify ordering: entry events before b events before c events.
	nodeOrder := []string{}
	for _, ev := range events {
		if ev.Type == EventNodeStarted {
			nodeOrder = append(nodeOrder, ev.NodeName)
		}
	}
	if len(nodeOrder) != 3 {
		t.Fatalf("expected 3 NodeStarted events, got %d: %v", len(nodeOrder), nodeOrder)
	}
	if nodeOrder[0] != "entry" || nodeOrder[1] != "b" || nodeOrder[2] != "c" {
		t.Errorf("expected NodeStarted order [entry, b, c], got %v", nodeOrder)
	}
}

func TestDataFlowEvent_ConcurrentNodesHaveNonDecreasingTimestamps(t *testing.T) {
	// Verify concurrent nodes emit events with monotonically non-decreasing timestamps.
	hook := &collectingHook{}
	g := mustGraph(t, WithEventHook(hook))

	// Diamond: entry → (b, c) → d
	mustAddNodeWithKeys(t, g, "entry", func(_ context.Context, s State) (State, error) {
		out := CopyState(s)
		out["entry_out"] = true
		return out, nil
	}, []string{"entry_out"}, []string{})
	mustAddNodeWithKeys(t, g, "b", func(_ context.Context, s State) (State, error) {
		out := CopyState(s)
		out["b_out"] = true
		return out, nil
	}, []string{"b_out"}, []string{"entry_out"})
	mustAddNodeWithKeys(t, g, "c", func(_ context.Context, s State) (State, error) {
		out := CopyState(s)
		out["c_out"] = true
		return out, nil
	}, []string{"c_out"}, []string{"entry_out"})
	mustAddNodeWithKeys(t, g, "d", func(_ context.Context, s State) (State, error) {
		out := CopyState(s)
		out["d_out"] = true
		return out, nil
	}, []string{"d_out"}, []string{"b_out", "c_out"})
	g.Start("entry")

	_, err := g.Run(context.Background(), State{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hook.mu.Lock()
	events := make([]GraphEvent, len(hook.events))
	copy(events, hook.events)
	hook.mu.Unlock()

	// All events should have non-decreasing timestamps.
	for i := 1; i < len(events); i++ {
		if events[i].Timestamp.Before(events[i-1].Timestamp) {
			t.Errorf("event %d (%s, node=%s) timestamp %v is before event %d (%s, node=%s) timestamp %v",
				i, events[i].Type, events[i].NodeName, events[i].Timestamp,
				i-1, events[i-1].Type, events[i-1].NodeName, events[i-1].Timestamp)
		}
	}

	// Verify b and c both have NodeStarted and NodeCompleted events.
	for _, nodeName := range []string{"b", "c"} {
		foundStart := false
		foundComplete := false
		for _, ev := range events {
			if ev.NodeName == nodeName && ev.Type == EventNodeStarted {
				foundStart = true
			}
			if ev.NodeName == nodeName && ev.Type == EventNodeCompleted {
				foundComplete = true
			}
		}
		if !foundStart {
			t.Errorf("missing NodeStarted for concurrent node %q", nodeName)
		}
		if !foundComplete {
			t.Errorf("missing NodeCompleted for concurrent node %q", nodeName)
		}
	}
}

func TestDataFlowEvent_CheckpointSavedEmitted(t *testing.T) {
	// Verify EventCheckpointSaved emitted after checkpoint save for data-flow nodes.
	hook := &collectingHook{}
	cp := &mockCheckpointer{}
	g := mustGraph(t, WithCheckpointer(cp), WithEventHook(hook))

	// Linear chain: entry → b
	mustAddNodeWithKeys(t, g, "entry", func(_ context.Context, s State) (State, error) {
		out := CopyState(s)
		out["x"] = "from_entry"
		return out, nil
	}, []string{"x"}, []string{})
	mustAddNodeWithKeys(t, g, "b", func(_ context.Context, s State) (State, error) {
		out := CopyState(s)
		out["y"] = "from_b"
		return out, nil
	}, []string{"y"}, []string{"x"})
	g.Start("entry")

	_, err := g.Run(context.Background(), State{"init": "yes"}, WithThreadID("t-cp-event"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hook.mu.Lock()
	events := make([]GraphEvent, len(hook.events))
	copy(events, hook.events)
	hook.mu.Unlock()

	// Collect CheckpointSaved events.
	var cpEvents []GraphEvent
	for _, ev := range events {
		if ev.Type == EventCheckpointSaved {
			cpEvents = append(cpEvents, ev)
		}
	}

	// Should have one checkpoint per node (entry and b).
	if len(cpEvents) != 2 {
		t.Fatalf("expected 2 CheckpointSaved events, got %d", len(cpEvents))
	}

	// Verify node names.
	if cpEvents[0].NodeName != "entry" {
		t.Errorf("expected first CheckpointSaved for 'entry', got %q", cpEvents[0].NodeName)
	}
	if cpEvents[1].NodeName != "b" {
		t.Errorf("expected second CheckpointSaved for 'b', got %q", cpEvents[1].NodeName)
	}

	// Verify versions are sequential.
	if cpEvents[0].Version != 1 {
		t.Errorf("expected first checkpoint version=1, got %d", cpEvents[0].Version)
	}
	if cpEvents[1].Version != 2 {
		t.Errorf("expected second checkpoint version=2, got %d", cpEvents[1].Version)
	}

	// Verify CheckpointSaved comes after NodeCompleted for each node.
	for _, nodeName := range []string{"entry", "b"} {
		completedIdx := -1
		cpIdx := -1
		for i, ev := range events {
			if ev.NodeName == nodeName && ev.Type == EventNodeCompleted {
				completedIdx = i
			}
			if ev.NodeName == nodeName && ev.Type == EventCheckpointSaved {
				cpIdx = i
			}
		}
		if completedIdx == -1 || cpIdx == -1 {
			t.Errorf("missing events for node %q", nodeName)
			continue
		}
		if cpIdx <= completedIdx {
			t.Errorf("CheckpointSaved (idx=%d) should come after NodeCompleted (idx=%d) for node %q",
				cpIdx, completedIdx, nodeName)
		}
	}
}

func TestDataFlowEvent_InterruptFiredEmitted(t *testing.T) {
	// Verify EventInterruptFired emitted on interrupt for data-flow nodes.
	hook := &collectingHook{}
	cp := &mockCheckpointer{}
	g := mustGraph(t, WithCheckpointer(cp), WithEventHook(hook))

	// Linear chain: entry → b → c, interrupt before b.
	mustAddNodeWithKeys(t, g, "entry", func(_ context.Context, s State) (State, error) {
		out := CopyState(s)
		out["x"] = "from_entry"
		return out, nil
	}, []string{"x"}, []string{})
	mustAddNodeWithKeys(t, g, "b", func(_ context.Context, s State) (State, error) {
		out := CopyState(s)
		out["y"] = "from_b"
		return out, nil
	}, []string{"y"}, []string{"x"})
	mustAddNodeWithKeys(t, g, "c", func(_ context.Context, s State) (State, error) {
		out := CopyState(s)
		out["z"] = "from_c"
		return out, nil
	}, []string{"z"}, []string{"y"})
	g.Start("entry")

	if err := g.InterruptBefore("b"); err != nil {
		t.Fatalf("InterruptBefore: %v", err)
	}

	_, err := g.Run(context.Background(), State{"init": "yes"}, WithThreadID("t-int-event"))
	var intErr *GraphInterruptError
	if !errors.As(err, &intErr) {
		t.Fatalf("expected GraphInterruptError, got %v", err)
	}

	hook.mu.Lock()
	events := make([]GraphEvent, len(hook.events))
	copy(events, hook.events)
	hook.mu.Unlock()

	// Find InterruptFired event.
	var intEvents []GraphEvent
	for _, ev := range events {
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
	if intEv.ThreadID != "t-int-event" {
		t.Errorf("expected ThreadID='t-int-event', got %q", intEv.ThreadID)
	}
}

func TestDataFlowEvent_InterruptAfterFired(t *testing.T) {
	// Verify EventInterruptFired emitted for InterruptAfter on data-flow nodes.
	hook := &collectingHook{}
	cp := &mockCheckpointer{}
	g := mustGraph(t, WithCheckpointer(cp), WithEventHook(hook))

	mustAddNodeWithKeys(t, g, "entry", func(_ context.Context, s State) (State, error) {
		out := CopyState(s)
		out["x"] = "from_entry"
		return out, nil
	}, []string{"x"}, []string{})
	mustAddNodeWithKeys(t, g, "b", func(_ context.Context, s State) (State, error) {
		out := CopyState(s)
		out["y"] = "from_b"
		return out, nil
	}, []string{"y"}, []string{"x"})
	g.Start("entry")

	if err := g.InterruptAfter("entry"); err != nil {
		t.Fatalf("InterruptAfter: %v", err)
	}

	_, err := g.Run(context.Background(), State{"init": "yes"}, WithThreadID("t-int-after-event"))
	var intErr *GraphInterruptError
	if !errors.As(err, &intErr) {
		t.Fatalf("expected GraphInterruptError, got %v", err)
	}

	hook.mu.Lock()
	events := make([]GraphEvent, len(hook.events))
	copy(events, hook.events)
	hook.mu.Unlock()

	// Find InterruptFired event.
	var intEvents []GraphEvent
	for _, ev := range events {
		if ev.Type == EventInterruptFired {
			intEvents = append(intEvents, ev)
		}
	}

	if len(intEvents) != 1 {
		t.Fatalf("expected 1 InterruptFired event, got %d", len(intEvents))
	}

	intEv := intEvents[0]
	if intEv.NodeName != "entry" {
		t.Errorf("expected InterruptFired node='entry', got %q", intEv.NodeName)
	}
	if intEv.InterruptType != InterruptTypeAfter {
		t.Errorf("expected InterruptType=after, got %q", intEv.InterruptType)
	}
	if intEv.Version == 0 {
		t.Error("expected non-zero version on InterruptFired event")
	}
}

func TestDataFlowEvent_FullEventSequence(t *testing.T) {
	// Verify the full event sequence for a data-flow graph with checkpointing.
	// Expected: GraphStarted, NodeStarted(entry), NodeCompleted(entry), CheckpointSaved(entry),
	//           NodeStarted(b), NodeCompleted(b), CheckpointSaved(b), GraphCompleted
	hook := &collectingHook{}
	cp := &mockCheckpointer{}
	g := mustGraph(t, WithCheckpointer(cp), WithEventHook(hook))

	mustAddNodeWithKeys(t, g, "entry", func(_ context.Context, s State) (State, error) {
		out := CopyState(s)
		out["x"] = "from_entry"
		return out, nil
	}, []string{"x"}, []string{})
	mustAddNodeWithKeys(t, g, "b", func(_ context.Context, s State) (State, error) {
		out := CopyState(s)
		out["y"] = "from_b"
		return out, nil
	}, []string{"y"}, []string{"x"})
	g.Start("entry")

	_, err := g.Run(context.Background(), State{"init": "yes"}, WithThreadID("t-full-seq"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hook.mu.Lock()
	events := make([]GraphEvent, len(hook.events))
	copy(events, hook.events)
	hook.mu.Unlock()

	expectedTypes := []EventType{
		EventGraphStarted,
		EventNodeStarted,     // entry
		EventNodeCompleted,   // entry
		EventCheckpointSaved, // entry
		EventNodeStarted,     // b
		EventNodeCompleted,   // b
		EventCheckpointSaved, // b
		EventGraphCompleted,
	}

	if len(events) != len(expectedTypes) {
		types := make([]EventType, len(events))
		for i, ev := range events {
			types[i] = ev.Type
		}
		t.Fatalf("expected %d events, got %d: %v", len(expectedTypes), len(events), types)
	}

	for i, expected := range expectedTypes {
		if events[i].Type != expected {
			t.Errorf("event %d: expected %s, got %s", i, expected, events[i].Type)
		}
	}
}
