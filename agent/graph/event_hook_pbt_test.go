package graph

import (
	"context"
	"fmt"
	"testing"

	"pgregory.net/rapid"
)

// Feature: graph-checkpointing, Property 22: Event Hook Completeness

func TestProperty_EventHookCompleteness(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		hook := &recordingHook{}

		// Generate a linear graph with random number of nodes.
		numNodes := rapid.IntRange(2, 10).Draw(rt, "numNodes")
		g, err := New[State](WithEventHook(hook))
		if err != nil {
			rt.Fatal(err)
		}

		names := make([]string, numNodes)
		for i := range names {
			names[i] = fmt.Sprintf("node%d", i)
			name := names[i]
			var inputKeys []string
			if i > 0 {
				inputKeys = []string{fmt.Sprintf("node%d_out", i-1)}
			}
			if _, err := g.Node(name, func(_ context.Context, s State) (State, error) {
				out := CopyState(s)
				out[name] = "done"
				out[name+"_out"] = "done"
				return out, nil
			}, In(inputKeys...), Out(name+"_out")); err != nil {
				rt.Fatal(err)
			}
		}
		g.Start(names[0])

		_, err = g.Run(context.Background(), State{})
		if err != nil {
			rt.Fatalf("Run failed: %v", err)
		}

		expectedNodes := names

		// Verify: exactly one GraphStarted as the first event.
		if len(hook.events) == 0 {
			rt.Fatal("no events recorded")
		}
		if hook.events[0].Type != EventGraphStarted {
			rt.Fatalf("first event should be GraphStarted, got %s", hook.events[0].Type)
		}

		// Verify: exactly one GraphCompleted as the last event.
		last := hook.events[len(hook.events)-1]
		if last.Type != EventGraphCompleted {
			rt.Fatalf("last event should be GraphCompleted, got %s", last.Type)
		}

		// Count GraphStarted and GraphCompleted events.
		graphStartedCount := 0
		graphCompletedCount := 0
		for _, ev := range hook.events {
			if ev.Type == EventGraphStarted {
				graphStartedCount++
			}
			if ev.Type == EventGraphCompleted {
				graphCompletedCount++
			}
		}
		if graphStartedCount != 1 {
			rt.Fatalf("expected exactly 1 GraphStarted, got %d", graphStartedCount)
		}
		if graphCompletedCount != 1 {
			rt.Fatalf("expected exactly 1 GraphCompleted, got %d", graphCompletedCount)
		}

		// Verify: exactly one NodeStarted + NodeCompleted pair per executed node.
		nodeStarted := make(map[string]int)
		nodeCompleted := make(map[string]int)
		for _, ev := range hook.events {
			if ev.Type == EventNodeStarted {
				nodeStarted[ev.NodeName]++
			}
			if ev.Type == EventNodeCompleted {
				nodeCompleted[ev.NodeName]++
			}
		}

		for _, name := range expectedNodes {
			if nodeStarted[name] != 1 {
				rt.Fatalf("expected exactly 1 NodeStarted for %q, got %d", name, nodeStarted[name])
			}
			if nodeCompleted[name] != 1 {
				rt.Fatalf("expected exactly 1 NodeCompleted for %q, got %d", name, nodeCompleted[name])
			}
		}

		// Verify: all events have non-zero timestamps.
		for i, ev := range hook.events {
			if ev.Timestamp.IsZero() {
				rt.Fatalf("event %d (%s) has zero timestamp", i, ev.Type)
			}
		}

		// Verify: timestamps are in non-decreasing order.
		for i := 1; i < len(hook.events); i++ {
			if hook.events[i].Timestamp.Before(hook.events[i-1].Timestamp) {
				rt.Fatalf("event %d (%s) timestamp %v is before event %d (%s) timestamp %v",
					i, hook.events[i].Type, hook.events[i].Timestamp,
					i-1, hook.events[i-1].Type, hook.events[i-1].Timestamp)
			}
		}

		// Verify: NodeStarted comes before NodeCompleted for each node.
		for _, name := range expectedNodes {
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
			if startIdx == -1 || completeIdx == -1 {
				rt.Fatalf("missing NodeStarted or NodeCompleted for %q", name)
			}
			if startIdx >= completeIdx {
				rt.Fatalf("NodeStarted (idx=%d) should come before NodeCompleted (idx=%d) for %q",
					startIdx, completeIdx, name)
			}
		}
	})
}
