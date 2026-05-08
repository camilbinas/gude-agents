package graph

import (
	"context"
	"errors"
	"testing"
)

// ── Step tests ───────────────────────────────────────────────────────────────

func TestStep_ExecutesOneNodeAndReturnsStepResult(t *testing.T) {
	cp := &mockCheckpointer{}
	g := mustGraph(t, WithCheckpointer(cp))

	mustAddNodeWithKeys(t, g, "a", setter("a", "done_a"), []string{"a"}, []string{})
	mustAddNodeWithKeys(t, g, "b", setter("b", "done_b"), []string{"b"}, []string{"a"})
	mustAddNodeWithKeys(t, g, "c", setter("c", "done_c"), []string{"c"}, []string{"b"})
	g.Start("a")

	result, err := g.Step(context.Background(), State{"init": "yes"}, "thread-step-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have executed only node "a".
	if result.NodeName != "a" {
		t.Errorf("expected NodeName='a', got %q", result.NodeName)
	}
	if result.State["a"] != "done_a" {
		t.Errorf("expected state[a]=done_a, got %v", result.State["a"])
	}
	if result.Version != 1 {
		t.Errorf("expected Version=1, got %d", result.Version)
	}
	if result.Done {
		t.Error("expected Done=false, got true")
	}
}

func TestStep_NoCheckpointStartsFromEntry(t *testing.T) {
	cp := &mockCheckpointer{}
	g := mustGraph(t, WithCheckpointer(cp))

	mustAddNodeWithKeys(t, g, "entry", setter("entry", "done_entry"), []string{"entry"}, []string{})
	mustAddNodeWithKeys(t, g, "next", setter("next", "done_next"), []string{"next"}, []string{"entry"})
	g.Start("entry")

	result, err := g.Step(context.Background(), State{"seed": "value"}, "thread-step-entry")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.NodeName != "entry" {
		t.Errorf("expected NodeName='entry', got %q", result.NodeName)
	}
	if result.State["entry"] != "done_entry" {
		t.Errorf("expected state[entry]=done_entry, got %v", result.State["entry"])
	}
	if result.State["seed"] != "value" {
		t.Errorf("expected state[seed]=value, got %v", result.State["seed"])
	}
}

func TestStep_WithExistingCheckpointContinuesFromNextNode(t *testing.T) {
	cp := &mockCheckpointer{}
	g := mustGraph(t, WithCheckpointer(cp))

	mustAddNodeWithKeys(t, g, "a", setter("a", "done_a"), []string{"a"}, []string{})
	mustAddNodeWithKeys(t, g, "b", setter("b", "done_b"), []string{"b"}, []string{"a"})
	mustAddNodeWithKeys(t, g, "c", setter("c", "done_c"), []string{"c"}, []string{"b"})
	g.Start("a")

	// First step: executes "a".
	result1, err := g.Step(context.Background(), State{"init": "yes"}, "thread-step-cont")
	if err != nil {
		t.Fatalf("step 1 error: %v", err)
	}
	if result1.NodeName != "a" {
		t.Fatalf("step 1: expected node 'a', got %q", result1.NodeName)
	}

	// Second step: should execute "b".
	result2, err := g.Step(context.Background(), nil, "thread-step-cont")
	if err != nil {
		t.Fatalf("step 2 error: %v", err)
	}
	if result2.NodeName != "b" {
		t.Errorf("step 2: expected node 'b', got %q", result2.NodeName)
	}
	if result2.State["b"] != "done_b" {
		t.Errorf("step 2: expected state[b]=done_b, got %v", result2.State["b"])
	}
	if result2.Version != 2 {
		t.Errorf("step 2: expected Version=2, got %d", result2.Version)
	}
	if result2.Done {
		t.Error("step 2: expected Done=false, got true")
	}

	// Third step: should execute "c" and be done.
	result3, err := g.Step(context.Background(), nil, "thread-step-cont")
	if err != nil {
		t.Fatalf("step 3 error: %v", err)
	}
	if result3.NodeName != "c" {
		t.Errorf("step 3: expected node 'c', got %q", result3.NodeName)
	}
	if !result3.Done {
		t.Error("step 3: expected Done=true, got false")
	}
}

func TestStep_DoneWhenNoOutgoingRoute(t *testing.T) {
	cp := &mockCheckpointer{}
	g := mustGraph(t, WithCheckpointer(cp))

	mustAddNodeWithKeys(t, g, "only", setter("only", "done"), []string{"only"}, []string{})
	g.Start("only")
	// No downstream nodes — terminal node.

	result, err := g.Step(context.Background(), State{}, "thread-step-done")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Done {
		t.Error("expected Done=true for terminal node")
	}
	if result.NodeName != "only" {
		t.Errorf("expected NodeName='only', got %q", result.NodeName)
	}
}

func TestStep_NoCheckpointerReturnsError(t *testing.T) {
	g := mustGraph(t) // No checkpointer
	mustAddNodeWithKeys(t, g, "a", noop, []string{"a_out"}, []string{})
	g.Start("a")

	_, err := g.Step(context.Background(), State{}, "thread-1")
	if !errors.Is(err, ErrNoCheckpointer) {
		t.Fatalf("expected ErrNoCheckpointer, got %v", err)
	}
}

func TestStep_EmptyThreadIDReturnsError(t *testing.T) {
	cp := &mockCheckpointer{}
	g := mustGraph(t, WithCheckpointer(cp))
	mustAddNodeWithKeys(t, g, "a", noop, []string{"a_out"}, []string{})
	g.Start("a")

	_, err := g.Step(context.Background(), State{}, "")
	if !errors.Is(err, ErrThreadIDRequired) {
		t.Fatalf("expected ErrThreadIDRequired, got %v", err)
	}
}

// ── Resume tests ─────────────────────────────────────────────────────────────

func TestResume_ContinuesFromInterruptPointToCompletion(t *testing.T) {
	cp := &mockCheckpointer{}
	g := mustGraph(t, WithCheckpointer(cp))

	mustAddNodeWithKeys(t, g, "a", setter("a", "done_a"), []string{"a"}, []string{})
	mustAddNodeWithKeys(t, g, "b", setter("b", "done_b"), []string{"b"}, []string{"a"})
	mustAddNodeWithKeys(t, g, "c", setter("c", "done_c"), []string{"c"}, []string{"b"})
	g.Start("a")

	// Set interrupt before "b".
	if err := g.InterruptBefore("b"); err != nil {
		t.Fatalf("InterruptBefore: %v", err)
	}

	// Run until interrupt.
	_, err := g.Run(context.Background(), State{"init": "yes"}, WithThreadID("thread-resume-1"))
	var intErr *GraphInterruptError
	if !errors.As(err, &intErr) {
		t.Fatalf("expected GraphInterruptError, got %v", err)
	}
	if intErr.Result.NodeName != "b" {
		t.Fatalf("expected interrupt at 'b', got %q", intErr.Result.NodeName)
	}

	// Resume execution.
	result, err := g.Resume(context.Background(), "thread-resume-1", nil)
	if err != nil {
		t.Fatalf("Resume error: %v", err)
	}

	// Should have completed all remaining nodes.
	if result.State["b"] != "done_b" {
		t.Errorf("expected state[b]=done_b, got %v", result.State["b"])
	}
	if result.State["c"] != "done_c" {
		t.Errorf("expected state[c]=done_c, got %v", result.State["c"])
	}
}

func TestResume_WithStateUpdatesMergesIntoCheckpointState(t *testing.T) {
	cp := &mockCheckpointer{}
	g := mustGraph(t, WithCheckpointer(cp))

	mustAddNodeWithKeys(t, g, "a", setter("a", "done_a"), []string{"a"}, []string{})
	mustAddNodeWithKeys(t, g, "b", func(_ context.Context, s State) (State, error) {
		out := CopyState(s)
		// Read the injected value to prove merge happened.
		out["saw_injected"] = s["injected"]
		return out, nil
	}, []string{"saw_injected"}, []string{"a"})
	g.Start("a")

	// Set interrupt before "b".
	if err := g.InterruptBefore("b"); err != nil {
		t.Fatalf("InterruptBefore: %v", err)
	}

	// Run until interrupt.
	_, err := g.Run(context.Background(), State{}, WithThreadID("thread-resume-merge"))
	var intErr *GraphInterruptError
	if !errors.As(err, &intErr) {
		t.Fatalf("expected GraphInterruptError, got %v", err)
	}

	// Resume with state updates.
	updates := State{"injected": "hello"}
	result, err := g.Resume(context.Background(), "thread-resume-merge", &updates)
	if err != nil {
		t.Fatalf("Resume error: %v", err)
	}

	if result.State["saw_injected"] != "hello" {
		t.Errorf("expected state[saw_injected]=hello, got %v", result.State["saw_injected"])
	}
}

func TestResume_NonExistentThreadReturnsError(t *testing.T) {
	cp := &mockCheckpointer{}
	g := mustGraph(t, WithCheckpointer(cp))
	mustAddNodeWithKeys(t, g, "a", noop, []string{"a_out"}, []string{})
	g.Start("a")

	_, err := g.Resume(context.Background(), "nonexistent-thread", nil)
	if !errors.Is(err, ErrCheckpointNotFound) {
		t.Fatalf("expected ErrCheckpointNotFound, got %v", err)
	}
}

func TestResume_NoCheckpointerReturnsError(t *testing.T) {
	g := mustGraph(t) // No checkpointer
	mustAddNodeWithKeys(t, g, "a", noop, []string{"a_out"}, []string{})
	g.Start("a")

	_, err := g.Resume(context.Background(), "thread-1", nil)
	if !errors.Is(err, ErrNoCheckpointer) {
		t.Fatalf("expected ErrNoCheckpointer, got %v", err)
	}
}

func TestResume_EmptyThreadIDReturnsError(t *testing.T) {
	cp := &mockCheckpointer{}
	g := mustGraph(t, WithCheckpointer(cp))
	mustAddNodeWithKeys(t, g, "a", noop, []string{"a_out"}, []string{})
	g.Start("a")

	_, err := g.Resume(context.Background(), "", nil)
	if !errors.Is(err, ErrThreadIDRequired) {
		t.Fatalf("expected ErrThreadIDRequired, got %v", err)
	}
}

func TestResume_HitsAnotherInterrupt(t *testing.T) {
	cp := &mockCheckpointer{}
	g := mustGraph(t, WithCheckpointer(cp))

	mustAddNodeWithKeys(t, g, "a", setter("a", "done_a"), []string{"a"}, []string{})
	mustAddNodeWithKeys(t, g, "b", setter("b", "done_b"), []string{"b"}, []string{"a"})
	mustAddNodeWithKeys(t, g, "c", setter("c", "done_c"), []string{"c"}, []string{"b"})
	mustAddNodeWithKeys(t, g, "d", setter("d", "done_d"), []string{"d"}, []string{"c"})
	g.Start("a")

	// Interrupt before "b" and before "c".
	if err := g.InterruptBefore("b"); err != nil {
		t.Fatalf("InterruptBefore(b): %v", err)
	}
	if err := g.InterruptBefore("c"); err != nil {
		t.Fatalf("InterruptBefore(c): %v", err)
	}

	// Run until first interrupt at "b".
	_, err := g.Run(context.Background(), State{}, WithThreadID("thread-resume-multi"))
	var intErr *GraphInterruptError
	if !errors.As(err, &intErr) {
		t.Fatalf("expected first interrupt, got %v", err)
	}
	if intErr.Result.NodeName != "b" {
		t.Fatalf("expected interrupt at 'b', got %q", intErr.Result.NodeName)
	}

	// Resume — should hit interrupt at "c".
	_, err = g.Resume(context.Background(), "thread-resume-multi", nil)
	if !errors.As(err, &intErr) {
		t.Fatalf("expected second interrupt, got %v", err)
	}
	if intErr.Result.NodeName != "c" {
		t.Errorf("expected interrupt at 'c', got %q", intErr.Result.NodeName)
	}
}

// ── RewindTo tests ───────────────────────────────────────────────────────────

func TestRewindTo_SetsPositionCorrectly(t *testing.T) {
	cp := &mockCheckpointer{}
	g := mustGraph(t, WithCheckpointer(cp))

	mustAddNodeWithKeys(t, g, "a", setter("a", "done_a"), []string{"a"}, []string{})
	mustAddNodeWithKeys(t, g, "b", setter("b", "done_b"), []string{"b"}, []string{"a"})
	mustAddNodeWithKeys(t, g, "c", setter("c", "done_c"), []string{"c"}, []string{"b"})
	g.Start("a")

	// Run the full graph.
	_, err := g.Run(context.Background(), State{"init": "yes"}, WithThreadID("thread-rewind-1"))
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}

	// Should have 3 checkpoints (a=v1, b=v2, c=v3).
	if len(cp.saved) != 3 {
		t.Fatalf("expected 3 checkpoints, got %d", len(cp.saved))
	}

	// Rewind to version 1 (after node "a").
	if err := g.RewindTo(context.Background(), "thread-rewind-1", 1); err != nil {
		t.Fatalf("RewindTo error: %v", err)
	}

	// Load latest should now return the rewind checkpoint (version 4) with state from version 1.
	latest, err := cp.Load(context.Background(), "thread-rewind-1")
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if latest.NodeName != "a" {
		t.Errorf("expected latest NodeName='a', got %q", latest.NodeName)
	}
	if latest.State["a"] != "done_a" {
		t.Errorf("expected state[a]=done_a, got %v", latest.State["a"])
	}
	// The rewind checkpoint should NOT have "b" or "c" in state.
	if _, exists := latest.State["b"]; exists {
		t.Error("rewind checkpoint should not have state[b]")
	}
}

func TestRewindTo_PreservesHistory(t *testing.T) {
	cp := &mockCheckpointer{}
	g := mustGraph(t, WithCheckpointer(cp))

	mustAddNodeWithKeys(t, g, "a", setter("a", "done_a"), []string{"a"}, []string{})
	mustAddNodeWithKeys(t, g, "b", setter("b", "done_b"), []string{"b"}, []string{"a"})
	mustAddNodeWithKeys(t, g, "c", setter("c", "done_c"), []string{"c"}, []string{"b"})
	g.Start("a")

	// Run the full graph.
	_, err := g.Run(context.Background(), State{}, WithThreadID("thread-rewind-hist"))
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}

	// Rewind to version 1.
	if err := g.RewindTo(context.Background(), "thread-rewind-hist", 1); err != nil {
		t.Fatalf("RewindTo error: %v", err)
	}

	// All original versions should still be accessible.
	for v := 1; v <= 3; v++ {
		_, err := cp.LoadAt(context.Background(), "thread-rewind-hist", v)
		if err != nil {
			t.Errorf("LoadAt(version=%d) failed: %v", v, err)
		}
	}

	// History should include all checkpoints (original 3 + rewind marker).
	history, err := cp.History(context.Background(), "thread-rewind-hist")
	if err != nil {
		t.Fatalf("History error: %v", err)
	}
	if len(history) != 4 {
		t.Errorf("expected 4 history entries, got %d", len(history))
	}
}

func TestRewindTo_VersionNumberingContinuesFromMax(t *testing.T) {
	cp := &mockCheckpointer{}
	g := mustGraph(t, WithCheckpointer(cp))

	mustAddNodeWithKeys(t, g, "a", setter("a", "done_a"), []string{"a"}, []string{})
	mustAddNodeWithKeys(t, g, "b", setter("b", "done_b"), []string{"b"}, []string{"a"})
	mustAddNodeWithKeys(t, g, "c", setter("c", "done_c"), []string{"c"}, []string{"b"})
	g.Start("a")

	// Run the full graph (creates versions 1, 2, 3).
	_, err := g.Run(context.Background(), State{}, WithThreadID("thread-rewind-ver"))
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}

	// Rewind to version 1 (creates version 4 as rewind marker).
	if err := g.RewindTo(context.Background(), "thread-rewind-ver", 1); err != nil {
		t.Fatalf("RewindTo error: %v", err)
	}

	// The rewind marker should be version 4.
	latest, err := cp.Load(context.Background(), "thread-rewind-ver")
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if latest.Version != 4 {
		t.Errorf("expected rewind marker version=4, got %d", latest.Version)
	}

	// Resume from rewind point — new checkpoints should continue from version 5.
	result, err := g.Resume(context.Background(), "thread-rewind-ver", nil)
	if err != nil {
		t.Fatalf("Resume error: %v", err)
	}

	// Verify execution completed.
	if result.State["c"] != "done_c" {
		t.Errorf("expected state[c]=done_c, got %v", result.State["c"])
	}

	// Check that new checkpoints have versions > 4.
	history, err := cp.History(context.Background(), "thread-rewind-ver")
	if err != nil {
		t.Fatalf("History error: %v", err)
	}

	// Original 3 + rewind marker (4) + resumed execution (b=5, c=6).
	if len(history) < 5 {
		t.Fatalf("expected at least 5 history entries, got %d", len(history))
	}

	// All versions after the rewind marker should be > 4.
	for _, meta := range history {
		if meta.Version > 4 && meta.Version <= 4 {
			t.Errorf("expected versions after rewind to be > 4, got %d", meta.Version)
		}
	}
}

func TestRewindTo_NonExistentVersionReturnsError(t *testing.T) {
	cp := &mockCheckpointer{}
	g := mustGraph(t, WithCheckpointer(cp))
	mustAddNodeWithKeys(t, g, "a", noop, []string{"a_out"}, []string{})
	g.Start("a")

	err := g.RewindTo(context.Background(), "nonexistent-thread", 99)
	if !errors.Is(err, ErrCheckpointNotFound) {
		t.Fatalf("expected ErrCheckpointNotFound, got %v", err)
	}
}

func TestRewindTo_NoCheckpointerReturnsError(t *testing.T) {
	g := mustGraph(t) // No checkpointer
	mustAddNodeWithKeys(t, g, "a", noop, []string{"a_out"}, []string{})
	g.Start("a")

	err := g.RewindTo(context.Background(), "thread-1", 1)
	if !errors.Is(err, ErrNoCheckpointer) {
		t.Fatalf("expected ErrNoCheckpointer, got %v", err)
	}
}

func TestRewindTo_EmptyThreadIDReturnsError(t *testing.T) {
	cp := &mockCheckpointer{}
	g := mustGraph(t, WithCheckpointer(cp))
	mustAddNodeWithKeys(t, g, "a", noop, []string{"a_out"}, []string{})
	g.Start("a")

	err := g.RewindTo(context.Background(), "", 1)
	if !errors.Is(err, ErrThreadIDRequired) {
		t.Fatalf("expected ErrThreadIDRequired, got %v", err)
	}
}
