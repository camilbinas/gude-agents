package graph

import (
	"context"
	"fmt"
	"testing"

	"pgregory.net/rapid"
)

// Feature: graph-checkpointing, Property 22: Event Hook Completeness
//
// **Validates: Requirements 16.12, 16.13, 16.14, 16.19**
//
// For any graph execution with a GraphEventHook configured, the event hook SHALL
// receive exactly one GraphStarted event (as the first event), exactly one
// GraphCompleted event (as the last event), and exactly one NodeStarted +
// NodeCompleted pair per successfully executed node, with all events having
// non-zero timestamps in non-decreasing order.

func TestProperty_EventHookCompleteness(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		hook := &recordingHook{}

		// Generate a random graph topology.
		topoType := rapid.IntRange(0, 2).Draw(rt, "topology")

		var expectedNodes []string

		switch topoType {
		case 0:
			// Linear graph.
			numNodes := rapid.IntRange(2, 7).Draw(rt, "numNodes")
			g, err := New[State](WithEventHook(hook))
			if err != nil {
				rt.Fatal(err)
			}

			names := make([]string, numNodes)
			for i := range names {
				names[i] = fmt.Sprintf("node%d", i)
				name := names[i]
				if err := g.AddNode(name, func(_ context.Context, s State) (State, error) {
					out := CopyState(s)
					out[name] = "done"
					return out, nil
				}); err != nil {
					rt.Fatal(err)
				}
			}
			g.SetEntry(names[0])
			for i := 0; i < numNodes-1; i++ {
				if err := g.AddEdge(names[i], names[i+1]); err != nil {
					rt.Fatal(err)
				}
			}

			_, err = g.Run(context.Background(), State{})
			if err != nil {
				rt.Fatalf("linear Run failed: %v", err)
			}
			expectedNodes = names

		case 1:
			// Conditional graph: start → (branchA or branchB)
			chooseBranch := rapid.Bool().Draw(rt, "chooseBranch")
			g, err := New[State](WithEventHook(hook))
			if err != nil {
				rt.Fatal(err)
			}

			if err := g.AddNode("start", func(_ context.Context, s State) (State, error) {
				out := CopyState(s)
				out["start"] = "done"
				return out, nil
			}); err != nil {
				rt.Fatal(err)
			}
			if err := g.AddNode("branchA", func(_ context.Context, s State) (State, error) {
				out := CopyState(s)
				out["branchA"] = "done"
				return out, nil
			}); err != nil {
				rt.Fatal(err)
			}
			if err := g.AddNode("branchB", func(_ context.Context, s State) (State, error) {
				out := CopyState(s)
				out["branchB"] = "done"
				return out, nil
			}); err != nil {
				rt.Fatal(err)
			}

			g.SetEntry("start")
			if err := g.AddConditionalEdge("start", func(_ context.Context, s State) (string, error) {
				if s["choose_a"] == true {
					return "branchA", nil
				}
				return "branchB", nil
			}); err != nil {
				rt.Fatal(err)
			}

			_, err = g.Run(context.Background(), State{"choose_a": chooseBranch})
			if err != nil {
				rt.Fatalf("conditional Run failed: %v", err)
			}

			if chooseBranch {
				expectedNodes = []string{"start", "branchA"}
			} else {
				expectedNodes = []string{"start", "branchB"}
			}

		case 2:
			// Longer linear graph with varied node count.
			numNodes := rapid.IntRange(4, 10).Draw(rt, "numNodesLong")
			g, err := New[State](WithEventHook(hook))
			if err != nil {
				rt.Fatal(err)
			}

			names := make([]string, numNodes)
			for i := range names {
				names[i] = fmt.Sprintf("step%d", i)
				name := names[i]
				if err := g.AddNode(name, func(_ context.Context, s State) (State, error) {
					out := CopyState(s)
					out[name] = "done"
					return out, nil
				}); err != nil {
					rt.Fatal(err)
				}
			}
			g.SetEntry(names[0])
			for i := 0; i < numNodes-1; i++ {
				if err := g.AddEdge(names[i], names[i+1]); err != nil {
					rt.Fatal(err)
				}
			}

			_, err = g.Run(context.Background(), State{})
			if err != nil {
				rt.Fatalf("long linear Run failed: %v", err)
			}
			expectedNodes = names
		}

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
