package graph

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"pgregory.net/rapid"
)

// Feature: graph-checkpointing, Property 12: Step Advances Exactly One Node

func TestProperty_StepAdvancesExactlyOneNode(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		cp := &mockCheckpointer{}

		numNodes := rapid.IntRange(2, 6).Draw(rt, "numNodes")
		g, err := New[State](WithCheckpointer(cp))
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

		threadID := rapid.StringMatching(`thread-[a-z0-9]{3,8}`).Draw(rt, "threadID")

		for i := 0; i < numNodes; i++ {
			result, err := g.Step(context.Background(), State{}, threadID)
			if err != nil {
				rt.Fatalf("Step %d failed: %v", i, err)
			}

			expectedVersion := i + 1
			if result.Version != expectedVersion {
				rt.Fatalf("Step %d: version=%d, expected %d", i, result.Version, expectedVersion)
			}

			if result.NodeName != names[i] {
				rt.Fatalf("Step %d: NodeName=%q, expected %q", i, result.NodeName, names[i])
			}

			if result.State[names[i]] != "done" {
				rt.Fatalf("Step %d: state[%q]=%v, expected 'done'", i, names[i], result.State[names[i]])
			}

			isLast := i == numNodes-1
			if result.Done != isLast {
				rt.Fatalf("Step %d: Done=%v, expected %v", i, result.Done, isLast)
			}
		}
	})
}

// Feature: graph-checkpointing, Property 13: Resume Completes From Checkpoint

func TestProperty_ResumeCompletesFromCheckpoint(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		cp := &mockCheckpointer{}

		numNodes := rapid.IntRange(3, 6).Draw(rt, "numNodes")
		g, err := New[State](WithCheckpointer(cp))
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

func TestProperty_ResumeStateMerge(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		cp := &mockCheckpointer{}

		g, err := New[State](WithCheckpointer(cp))
		if err != nil {
			rt.Fatal(err)
		}

		mergeKey := rapid.StringMatching(`merge_[a-z]{2,6}`).Draw(rt, "mergeKey")
		mergeVal := rapid.StringMatching(`[a-z]{3,10}`).Draw(rt, "mergeVal")

		var observedMergeVal any

		if _, err := g.Node("a", func(_ context.Context, s State) (State, error) {
			out := CopyState(s)
			out["a"] = "done"
			out["a_out"] = "done"
			return out, nil
		}, In(), Out("a_out")); err != nil {
			rt.Fatal(err)
		}
		if _, err := g.Node("b", func(_ context.Context, s State) (State, error) {
			out := CopyState(s)
			out["b"] = "done"
			out["b_out"] = "done"
			observedMergeVal = s[mergeKey]
			return out, nil
		}, In("a_out"), Out("b_out")); err != nil {
			rt.Fatal(err)
		}
		if _, err := g.Node("c", func(_ context.Context, s State) (State, error) {
			out := CopyState(s)
			out["c"] = "done"
			out["c_out"] = "done"
			return out, nil
		}, In("b_out"), Out("c_out")); err != nil {
			rt.Fatal(err)
		}

		g.Start("a")

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

		if observedMergeVal != mergeVal {
			rt.Fatalf("node B observed mergeKey=%v, expected %q", observedMergeVal, mergeVal)
		}

		if result.State[mergeKey] != mergeVal {
			rt.Fatalf("final state[%q]=%v, expected %q", mergeKey, result.State[mergeKey], mergeVal)
		}
	})
}

// Feature: graph-checkpointing, Property 15: RewindTo Sets Position

func TestProperty_RewindToSetsPosition(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		cp := &mockCheckpointer{}

		g, err := New[State](WithCheckpointer(cp))
		if err != nil {
			rt.Fatal(err)
		}

		names := []string{"a", "b", "c", "d"}
		for i, name := range names {
			n := name
			var inputKeys []string
			if i > 0 {
				inputKeys = []string{names[i-1] + "_out"}
			}
			if _, err := g.Node(n, func(_ context.Context, s State) (State, error) {
				out := CopyState(s)
				out[n] = "done"
				out[n+"_out"] = "done"
				return out, nil
			}, In(inputKeys...), Out(n+"_out")); err != nil {
				rt.Fatal(err)
			}
		}
		g.Start("a")

		threadID := rapid.StringMatching(`thread-[a-z0-9]{3,8}`).Draw(rt, "threadID")

		_, err = g.Run(context.Background(), State{}, WithThreadID(threadID))
		if err != nil {
			rt.Fatalf("Run failed: %v", err)
		}

		rewindVersion := rapid.IntRange(1, len(names)-1).Draw(rt, "rewindVersion")
		if err := g.RewindTo(context.Background(), threadID, rewindVersion); err != nil {
			rt.Fatalf("RewindTo failed: %v", err)
		}

		result, err := g.Resume(context.Background(), threadID, nil)
		if err != nil {
			rt.Fatalf("Resume after RewindTo failed: %v", err)
		}

		for i := rewindVersion; i < len(names); i++ {
			if result.State[names[i]] != "done" {
				rt.Fatalf("after RewindTo(%d) + Resume: state[%q]=%v, expected 'done'",
					rewindVersion, names[i], result.State[names[i]])
			}
		}
	})
}

// Feature: graph-checkpointing, Property 16: RewindTo Preserves History

func TestProperty_RewindToPreservesHistory(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		cp := &mockCheckpointer{}

		g, err := New[State](WithCheckpointer(cp))
		if err != nil {
			rt.Fatal(err)
		}

		names := []string{"a", "b", "c", "d"}
		for i, name := range names {
			n := name
			var inputKeys []string
			if i > 0 {
				inputKeys = []string{names[i-1] + "_out"}
			}
			if _, err := g.Node(n, func(_ context.Context, s State) (State, error) {
				out := CopyState(s)
				out[n] = "done"
				out[n+"_out"] = "done"
				return out, nil
			}, In(inputKeys...), Out(n+"_out")); err != nil {
				rt.Fatal(err)
			}
		}
		g.Start("a")

		threadID := rapid.StringMatching(`thread-[a-z0-9]{3,8}`).Draw(rt, "threadID")

		_, err = g.Run(context.Background(), State{}, WithThreadID(threadID))
		if err != nil {
			rt.Fatalf("Run failed: %v", err)
		}

		rewindVersion := rapid.IntRange(1, len(names)-1).Draw(rt, "rewindVersion")
		if err := g.RewindTo(context.Background(), threadID, rewindVersion); err != nil {
			rt.Fatalf("RewindTo failed: %v", err)
		}

		for v := 1; v <= len(names); v++ {
			_, err := cp.LoadAt(context.Background(), threadID, v)
			if err != nil {
				rt.Fatalf("LoadAt(version=%d) after RewindTo(%d) failed: %v", v, rewindVersion, err)
			}
		}

		history, err := cp.History(context.Background(), threadID)
		if err != nil {
			rt.Fatalf("History failed: %v", err)
		}

		if len(history) < len(names) {
			rt.Fatalf("History has %d entries, expected at least %d", len(history), len(names))
		}
	})
}

// Feature: graph-checkpointing, Property 17: Version Numbering After Rewind

func TestProperty_VersionNumberingAfterRewind(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		cp := &mockCheckpointer{}

		g, err := New[State](WithCheckpointer(cp))
		if err != nil {
			rt.Fatal(err)
		}

		names := []string{"a", "b", "c", "d"}
		for i, name := range names {
			n := name
			var inputKeys []string
			if i > 0 {
				inputKeys = []string{names[i-1] + "_out"}
			}
			if _, err := g.Node(n, func(_ context.Context, s State) (State, error) {
				out := CopyState(s)
				out[n] = "done"
				out[n+"_out"] = "done"
				return out, nil
			}, In(inputKeys...), Out(n+"_out")); err != nil {
				rt.Fatal(err)
			}
		}
		g.Start("a")

		threadID := rapid.StringMatching(`thread-[a-z0-9]{3,8}`).Draw(rt, "threadID")

		_, err = g.Run(context.Background(), State{}, WithThreadID(threadID))
		if err != nil {
			rt.Fatalf("Run failed: %v", err)
		}

		rewindVersion := rapid.IntRange(1, len(names)-1).Draw(rt, "rewindVersion")
		if err := g.RewindTo(context.Background(), threadID, rewindVersion); err != nil {
			rt.Fatalf("RewindTo failed: %v", err)
		}

		maxVersionBeforeResume := len(cp.saved)

		_, err = g.Resume(context.Background(), threadID, nil)
		if err != nil {
			rt.Fatalf("Resume after RewindTo failed: %v", err)
		}

		for i := maxVersionBeforeResume; i < len(cp.saved); i++ {
			if cp.saved[i].Version <= maxVersionBeforeResume {
				rt.Fatalf("new checkpoint version=%d should be > %d",
					cp.saved[i].Version, maxVersionBeforeResume)
			}
		}

		for i := maxVersionBeforeResume; i < len(cp.saved); i++ {
			expectedVersion := i + 1
			if cp.saved[i].Version != expectedVersion {
				rt.Fatalf("new checkpoint[%d] version=%d, expected %d",
					i, cp.saved[i].Version, expectedVersion)
			}
		}
	})
}
