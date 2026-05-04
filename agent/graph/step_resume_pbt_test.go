package graph

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"pgregory.net/rapid"
)

// Feature: graph-checkpointing, Property 12: Step Advances Exactly One Node
//
// **Validates: Requirements 6.1, 6.3, 6.5**
//
// For any graph and thread, each call to Step SHALL execute exactly one node,
// increment the checkpoint version by one, and the StepResult SHALL contain the
// name of the executed node and the updated state.

func TestProperty_StepAdvancesExactlyOneNode(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		cp := &mockCheckpointer{}

		// Build a linear graph.
		numNodes := rapid.IntRange(2, 6).Draw(rt, "numNodes")
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

		threadID := rapid.StringMatching(`thread-[a-z0-9]{3,8}`).Draw(rt, "threadID")

		// Step through the graph one node at a time.
		for i := 0; i < numNodes; i++ {
			result, err := g.Step(context.Background(), State{}, threadID)
			if err != nil {
				rt.Fatalf("Step %d failed: %v", i, err)
			}

			// Verify version increments by 1.
			expectedVersion := i + 1
			if result.Version != expectedVersion {
				rt.Fatalf("Step %d: version=%d, expected %d", i, result.Version, expectedVersion)
			}

			// Verify the correct node was executed.
			if result.NodeName != names[i] {
				rt.Fatalf("Step %d: NodeName=%q, expected %q", i, result.NodeName, names[i])
			}

			// Verify state contains the executed node's output.
			if result.State[names[i]] != "done" {
				rt.Fatalf("Step %d: state[%q]=%v, expected 'done'", i, names[i], result.State[names[i]])
			}

			// Verify Done flag.
			isLast := i == numNodes-1
			if result.Done != isLast {
				rt.Fatalf("Step %d: Done=%v, expected %v", i, result.Done, isLast)
			}
		}
	})
}

// Feature: graph-checkpointing, Property 13: Resume Completes From Checkpoint
//
// **Validates: Requirements 7.1, 7.2, 7.4, 7.6**
//
// For any graph that was interrupted, calling Resume SHALL continue execution from
// the next node after the interrupt point and eventually return a Result.

func TestProperty_ResumeCompletesFromCheckpoint(t *testing.T) {
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

		// Interrupt before a middle node.
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

		// Remove the interrupt so Resume can complete.
		g.interruptBefore[names[interruptIdx]] = false

		// Resume execution.
		result, err := g.Resume(context.Background(), threadID, nil)
		if err != nil {
			rt.Fatalf("Resume failed: %v", err)
		}

		// Verify all nodes were executed.
		for _, name := range names {
			if result.State[name] != "done" {
				rt.Fatalf("after Resume: state[%q]=%v, expected 'done'", name, result.State[name])
			}
		}
	})
}

// Feature: graph-checkpointing, Property 14: Resume State Merge
//
// **Validates: Requirements 7.3**
//
// For any state updates provided to Resume, the merged state SHALL be visible to
// the next node executed, with update keys overwriting checkpoint keys.

func TestProperty_ResumeStateMerge(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		cp := &mockCheckpointer{}

		// Build a linear graph: A → B → C
		// B will check for the merged state.
		g, err := New[State](WithCheckpointer(cp))
		if err != nil {
			rt.Fatal(err)
		}

		mergeKey := rapid.StringMatching(`merge_[a-z]{2,6}`).Draw(rt, "mergeKey")
		mergeVal := rapid.StringMatching(`[a-z]{3,10}`).Draw(rt, "mergeVal")

		var observedMergeVal any

		if err := g.AddNode("a", func(_ context.Context, s State) (State, error) {
			out := CopyState(s)
			out["a"] = "done"
			return out, nil
		}); err != nil {
			rt.Fatal(err)
		}
		if err := g.AddNode("b", func(_ context.Context, s State) (State, error) {
			out := CopyState(s)
			out["b"] = "done"
			observedMergeVal = s[mergeKey]
			return out, nil
		}); err != nil {
			rt.Fatal(err)
		}
		if err := g.AddNode("c", func(_ context.Context, s State) (State, error) {
			out := CopyState(s)
			out["c"] = "done"
			return out, nil
		}); err != nil {
			rt.Fatal(err)
		}

		g.SetEntry("a")
		if err := g.AddEdge("a", "b"); err != nil {
			rt.Fatal(err)
		}
		if err := g.AddEdge("b", "c"); err != nil {
			rt.Fatal(err)
		}

		// Interrupt before B.
		if err := g.InterruptBefore("b"); err != nil {
			rt.Fatal(err)
		}

		threadID := rapid.StringMatching(`thread-[a-z0-9]{3,8}`).Draw(rt, "threadID")
		_, err = g.Run(context.Background(), State{}, WithThreadID(threadID))

		var intErr *GraphInterruptError
		if !errors.As(err, &intErr) {
			rt.Fatalf("expected GraphInterruptError, got %T: %v", err, err)
		}

		// Remove interrupt for Resume.
		g.interruptBefore["b"] = false

		// Resume with state updates.
		stateUpdates := State{mergeKey: mergeVal}
		result, err := g.Resume(context.Background(), threadID, &stateUpdates)
		if err != nil {
			rt.Fatalf("Resume failed: %v", err)
		}

		// Verify the merged state was visible to node B.
		if observedMergeVal != mergeVal {
			rt.Fatalf("node B observed mergeKey=%v, expected %q", observedMergeVal, mergeVal)
		}

		// Verify the merged key is in the final state.
		if result.State[mergeKey] != mergeVal {
			rt.Fatalf("final state[%q]=%v, expected %q", mergeKey, result.State[mergeKey], mergeVal)
		}
	})
}

// Feature: graph-checkpointing, Property 15: RewindTo Sets Position
//
// **Validates: Requirements 8.1, 8.2**
//
// For any valid checkpoint version V in a thread's history, after RewindTo(thread, V),
// calling Resume SHALL continue execution from the node that follows the state at V.

func TestProperty_RewindToSetsPosition(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		cp := &mockCheckpointer{}

		// Build a linear graph: A → B → C → D
		g, err := New[State](WithCheckpointer(cp))
		if err != nil {
			rt.Fatal(err)
		}

		names := []string{"a", "b", "c", "d"}
		for _, name := range names {
			n := name
			if err := g.AddNode(n, func(_ context.Context, s State) (State, error) {
				out := CopyState(s)
				out[n] = "done"
				return out, nil
			}); err != nil {
				rt.Fatal(err)
			}
		}
		g.SetEntry("a")
		if err := g.AddEdge("a", "b"); err != nil {
			rt.Fatal(err)
		}
		if err := g.AddEdge("b", "c"); err != nil {
			rt.Fatal(err)
		}
		if err := g.AddEdge("c", "d"); err != nil {
			rt.Fatal(err)
		}

		threadID := rapid.StringMatching(`thread-[a-z0-9]{3,8}`).Draw(rt, "threadID")

		// Run the full graph.
		_, err = g.Run(context.Background(), State{}, WithThreadID(threadID))
		if err != nil {
			rt.Fatalf("Run failed: %v", err)
		}

		// Rewind to a random version (1 to numNodes-1 to ensure there's a next node).
		rewindVersion := rapid.IntRange(1, len(names)-1).Draw(rt, "rewindVersion")
		if err := g.RewindTo(context.Background(), threadID, rewindVersion); err != nil {
			rt.Fatalf("RewindTo failed: %v", err)
		}

		// Resume from the rewound position.
		result, err := g.Resume(context.Background(), threadID, nil)
		if err != nil {
			rt.Fatalf("Resume after RewindTo failed: %v", err)
		}

		// Verify that execution continued from the correct position.
		// The node at rewindVersion index was the last completed, so the next
		// node should have been executed.
		for i := rewindVersion; i < len(names); i++ {
			if result.State[names[i]] != "done" {
				rt.Fatalf("after RewindTo(%d) + Resume: state[%q]=%v, expected 'done'",
					rewindVersion, names[i], result.State[names[i]])
			}
		}
	})
}

// Feature: graph-checkpointing, Property 16: RewindTo Preserves History
//
// **Validates: Requirements 8.3**
//
// For any rewind operation to version V, all checkpoints at versions > V SHALL
// remain accessible via LoadAt and appear in History.

func TestProperty_RewindToPreservesHistory(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		cp := &mockCheckpointer{}

		// Build a linear graph: A → B → C → D
		g, err := New[State](WithCheckpointer(cp))
		if err != nil {
			rt.Fatal(err)
		}

		names := []string{"a", "b", "c", "d"}
		for _, name := range names {
			n := name
			if err := g.AddNode(n, func(_ context.Context, s State) (State, error) {
				out := CopyState(s)
				out[n] = "done"
				return out, nil
			}); err != nil {
				rt.Fatal(err)
			}
		}
		g.SetEntry("a")
		if err := g.AddEdge("a", "b"); err != nil {
			rt.Fatal(err)
		}
		if err := g.AddEdge("b", "c"); err != nil {
			rt.Fatal(err)
		}
		if err := g.AddEdge("c", "d"); err != nil {
			rt.Fatal(err)
		}

		threadID := rapid.StringMatching(`thread-[a-z0-9]{3,8}`).Draw(rt, "threadID")

		// Run the full graph (creates 4 checkpoints).
		_, err = g.Run(context.Background(), State{}, WithThreadID(threadID))
		if err != nil {
			rt.Fatalf("Run failed: %v", err)
		}

		// Rewind to a version.
		rewindVersion := rapid.IntRange(1, len(names)-1).Draw(rt, "rewindVersion")
		if err := g.RewindTo(context.Background(), threadID, rewindVersion); err != nil {
			rt.Fatalf("RewindTo failed: %v", err)
		}

		// Verify all original checkpoints are still accessible via LoadAt.
		for v := 1; v <= len(names); v++ {
			_, err := cp.LoadAt(context.Background(), threadID, v)
			if err != nil {
				rt.Fatalf("LoadAt(version=%d) after RewindTo(%d) failed: %v", v, rewindVersion, err)
			}
		}

		// Verify History still contains all entries (original + rewind marker).
		history, err := cp.History(context.Background(), threadID)
		if err != nil {
			rt.Fatalf("History failed: %v", err)
		}

		// History should have at least the original checkpoints.
		if len(history) < len(names) {
			rt.Fatalf("History has %d entries, expected at least %d", len(history), len(names))
		}
	})
}

// Feature: graph-checkpointing, Property 17: Version Numbering After Rewind
//
// **Validates: Requirements 8.5**
//
// For any rewind to version V followed by Resume, new checkpoints SHALL have
// versions starting from max(all existing versions) + 1, not from V + 1.

func TestProperty_VersionNumberingAfterRewind(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		cp := &mockCheckpointer{}

		// Build a linear graph: A → B → C → D
		g, err := New[State](WithCheckpointer(cp))
		if err != nil {
			rt.Fatal(err)
		}

		names := []string{"a", "b", "c", "d"}
		for _, name := range names {
			n := name
			if err := g.AddNode(n, func(_ context.Context, s State) (State, error) {
				out := CopyState(s)
				out[n] = "done"
				return out, nil
			}); err != nil {
				rt.Fatal(err)
			}
		}
		g.SetEntry("a")
		if err := g.AddEdge("a", "b"); err != nil {
			rt.Fatal(err)
		}
		if err := g.AddEdge("b", "c"); err != nil {
			rt.Fatal(err)
		}
		if err := g.AddEdge("c", "d"); err != nil {
			rt.Fatal(err)
		}

		threadID := rapid.StringMatching(`thread-[a-z0-9]{3,8}`).Draw(rt, "threadID")

		// Run the full graph (creates 4 checkpoints, versions 1-4).
		_, err = g.Run(context.Background(), State{}, WithThreadID(threadID))
		if err != nil {
			rt.Fatalf("Run failed: %v", err)
		}

		// Record the max version before rewind.
		// After Run: 4 checkpoints. RewindTo saves one more (version 5).
		rewindVersion := rapid.IntRange(1, len(names)-1).Draw(rt, "rewindVersion")
		if err := g.RewindTo(context.Background(), threadID, rewindVersion); err != nil {
			rt.Fatalf("RewindTo failed: %v", err)
		}

		// The rewind marker is version 5 (4 original + 1 rewind).
		// Resume should create new checkpoints starting from version 6.
		maxVersionBeforeResume := len(cp.saved)

		_, err = g.Resume(context.Background(), threadID, nil)
		if err != nil {
			rt.Fatalf("Resume after RewindTo failed: %v", err)
		}

		// Verify new checkpoints have versions > maxVersionBeforeResume.
		for i := maxVersionBeforeResume; i < len(cp.saved); i++ {
			if cp.saved[i].Version <= maxVersionBeforeResume {
				rt.Fatalf("new checkpoint version=%d should be > %d",
					cp.saved[i].Version, maxVersionBeforeResume)
			}
		}

		// Verify new versions are sequential starting from maxVersionBeforeResume + 1.
		for i := maxVersionBeforeResume; i < len(cp.saved); i++ {
			expectedVersion := i + 1
			if cp.saved[i].Version != expectedVersion {
				rt.Fatalf("new checkpoint[%d] version=%d, expected %d",
					i, cp.saved[i].Version, expectedVersion)
			}
		}
	})
}
