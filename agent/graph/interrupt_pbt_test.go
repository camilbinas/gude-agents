package graph

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"pgregory.net/rapid"
)

// Feature: graph-checkpointing, Property 10: InterruptBefore Pauses Before Node Execution
//
// **Validates: Requirements 5.1, 5.3, 5.6**
//
// For any graph with a node marked InterruptBefore, when execution reaches that node,
// the returned InterruptResult SHALL have Type=Before and NodeName matching the marked
// node, and the checkpoint's Completed set SHALL NOT contain that node.

func TestProperty_InterruptBeforePausesBeforeNodeExecution(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		cp := &mockCheckpointer{}

		// Build a linear graph A → B → C.
		numNodes := rapid.IntRange(3, 6).Draw(rt, "numNodes")
		g, err := New[State](WithCheckpointer(cp))
		if err != nil {
			rt.Fatal(err)
		}

		names := make([]string, numNodes)
		for i := range names {
			names[i] = fmt.Sprintf("node%d", i)
			name := names[i]
			if err := g.AddNode(name, func(_ context.Context, s State) (State, error) {
				out := CopyState(s)
				out[name] = "executed"
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

		// Pick a random node (not the first) to interrupt before.
		interruptIdx := rapid.IntRange(1, numNodes-1).Draw(rt, "interruptIdx")
		if err := g.InterruptBefore(names[interruptIdx]); err != nil {
			rt.Fatal(err)
		}

		threadID := rapid.StringMatching(`thread-[a-z0-9]{3,8}`).Draw(rt, "threadID")
		_, err = g.Run(context.Background(), State{}, WithThreadID(threadID))

		var intErr *GraphInterruptError
		if !errors.As(err, &intErr) {
			rt.Fatalf("expected GraphInterruptError, got %T: %v", err, err)
		}

		// Verify interrupt result.
		if intErr.Result.Type != InterruptTypeBefore {
			rt.Fatalf("expected InterruptType=before, got %q", intErr.Result.Type)
		}
		if intErr.Result.NodeName != names[interruptIdx] {
			rt.Fatalf("expected NodeName=%q, got %q", names[interruptIdx], intErr.Result.NodeName)
		}

		// The interrupted node should NOT be in the completed set.
		if intErr.Result.Checkpoint.Completed[names[interruptIdx]] {
			rt.Fatalf("interrupted node %q should NOT be in completed set", names[interruptIdx])
		}

		// All nodes before the interrupt should be in the completed set.
		for i := 0; i < interruptIdx; i++ {
			if !intErr.Result.Checkpoint.Completed[names[i]] {
				rt.Fatalf("node %q (before interrupt) should be in completed set", names[i])
			}
		}
	})
}

// Feature: graph-checkpointing, Property 11: InterruptAfter Pauses After Node Execution
//
// **Validates: Requirements 5.2, 5.4, 5.6**
//
// For any graph with a node marked InterruptAfter, when that node completes, the
// returned InterruptResult SHALL have Type=After and NodeName matching the marked
// node, and the checkpoint's Completed set SHALL contain that node.

func TestProperty_InterruptAfterPausesAfterNodeExecution(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		cp := &mockCheckpointer{}

		// Build a linear graph.
		numNodes := rapid.IntRange(3, 6).Draw(rt, "numNodes")
		g, err := New[State](WithCheckpointer(cp))
		if err != nil {
			rt.Fatal(err)
		}

		names := make([]string, numNodes)
		for i := range names {
			names[i] = fmt.Sprintf("node%d", i)
			name := names[i]
			if err := g.AddNode(name, func(_ context.Context, s State) (State, error) {
				out := CopyState(s)
				out[name] = "executed"
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

		// Pick a random node to interrupt after (can be any node including first).
		interruptIdx := rapid.IntRange(0, numNodes-1).Draw(rt, "interruptIdx")
		if err := g.InterruptAfter(names[interruptIdx]); err != nil {
			rt.Fatal(err)
		}

		threadID := rapid.StringMatching(`thread-[a-z0-9]{3,8}`).Draw(rt, "threadID")
		_, err = g.Run(context.Background(), State{}, WithThreadID(threadID))

		var intErr *GraphInterruptError
		if !errors.As(err, &intErr) {
			rt.Fatalf("expected GraphInterruptError, got %T: %v", err, err)
		}

		// Verify interrupt result.
		if intErr.Result.Type != InterruptTypeAfter {
			rt.Fatalf("expected InterruptType=after, got %q", intErr.Result.Type)
		}
		if intErr.Result.NodeName != names[interruptIdx] {
			rt.Fatalf("expected NodeName=%q, got %q", names[interruptIdx], intErr.Result.NodeName)
		}

		// The interrupted node SHOULD be in the completed set (it ran).
		if !intErr.Result.Checkpoint.Completed[names[interruptIdx]] {
			rt.Fatalf("interrupted node %q should be in completed set (InterruptAfter)", names[interruptIdx])
		}

		// All nodes before and including the interrupt should be in the completed set.
		for i := 0; i <= interruptIdx; i++ {
			if !intErr.Result.Checkpoint.Completed[names[i]] {
				rt.Fatalf("node %q (at or before interrupt) should be in completed set", names[i])
			}
		}
	})
}
