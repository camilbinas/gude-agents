package graph

import (
	"context"
	"errors"
	"testing"
)

// ── interrupt annotation unit tests ──────────────────────────────────────────

func TestInterruptBefore_UnregisteredNode(t *testing.T) {
	g := mustGraph(t)
	mustAddNodeWithKeys(t, g, "a", setter("a", "done_a"), []string{"a"}, []string{})
	g.Start("a")

	err := g.InterruptBefore("nonexistent")
	if err == nil {
		t.Fatal("expected error for unregistered node, got nil")
	}

	var valErr *GraphValidationError
	if !errors.As(err, &valErr) {
		t.Fatalf("expected GraphValidationError, got %T: %v", err, err)
	}
	if valErr.Message != `InterruptBefore: node "nonexistent" is not registered` {
		t.Errorf("unexpected error message: %s", valErr.Message)
	}
}

func TestInterruptAfter_UnregisteredNode(t *testing.T) {
	g := mustGraph(t)
	mustAddNodeWithKeys(t, g, "a", setter("a", "done_a"), []string{"a"}, []string{})
	g.Start("a")

	err := g.InterruptAfter("nonexistent")
	if err == nil {
		t.Fatal("expected error for unregistered node, got nil")
	}

	var valErr *GraphValidationError
	if !errors.As(err, &valErr) {
		t.Fatalf("expected GraphValidationError, got %T: %v", err, err)
	}
	if valErr.Message != `InterruptAfter: node "nonexistent" is not registered` {
		t.Errorf("unexpected error message: %s", valErr.Message)
	}
}

func TestInterruptBefore_PausesBeforeNode(t *testing.T) {
	cp := &mockCheckpointer{}
	g := mustGraph(t, WithCheckpointer(cp))

	mustAddNodeWithKeys(t, g, "a", setter("a", "done_a"), []string{"a"}, []string{})
	mustAddNodeWithKeys(t, g, "b", setter("b", "done_b"), []string{"b"}, []string{"a"})
	mustAddNodeWithKeys(t, g, "c", setter("c", "done_c"), []string{"c"}, []string{"b"})
	g.Start("a")

	// Mark node "b" to interrupt before execution.
	if err := g.InterruptBefore("b"); err != nil {
		t.Fatalf("InterruptBefore failed: %v", err)
	}

	_, err := g.Run(context.Background(), State{"init": "yes"}, WithThreadID("thread-int-before"))
	if err == nil {
		t.Fatal("expected GraphInterruptError, got nil")
	}

	var intErr *GraphInterruptError
	if !errors.As(err, &intErr) {
		t.Fatalf("expected GraphInterruptError, got %T: %v", err, err)
	}

	// Verify interrupt result.
	if intErr.Result.NodeName != "b" {
		t.Errorf("expected interrupt at node 'b', got %q", intErr.Result.NodeName)
	}
	if intErr.Result.Type != InterruptTypeBefore {
		t.Errorf("expected InterruptTypeBefore, got %q", intErr.Result.Type)
	}

	// Verify checkpoint does NOT contain node "b" in Completed set.
	if intErr.Result.Checkpoint.Completed["b"] {
		t.Error("InterruptBefore checkpoint should NOT contain node 'b' in Completed set")
	}

	// Verify checkpoint DOES contain node "a" in Completed set (it ran before "b").
	if !intErr.Result.Checkpoint.Completed["a"] {
		t.Error("InterruptBefore checkpoint should contain node 'a' in Completed set")
	}

	// Verify the state has "a" result but NOT "b" result.
	if intErr.Result.Checkpoint.State["a"] != "done_a" {
		t.Errorf("expected state[a]=done_a, got %v", intErr.Result.Checkpoint.State["a"])
	}
	if _, exists := intErr.Result.Checkpoint.State["b"]; exists {
		t.Error("InterruptBefore checkpoint state should NOT contain key 'b'")
	}
}

func TestInterruptAfter_PausesAfterNode(t *testing.T) {
	cp := &mockCheckpointer{}
	g := mustGraph(t, WithCheckpointer(cp))

	mustAddNodeWithKeys(t, g, "a", setter("a", "done_a"), []string{"a"}, []string{})
	mustAddNodeWithKeys(t, g, "b", setter("b", "done_b"), []string{"b"}, []string{"a"})
	mustAddNodeWithKeys(t, g, "c", setter("c", "done_c"), []string{"c"}, []string{"b"})
	g.Start("a")

	// Mark node "b" to interrupt after execution.
	if err := g.InterruptAfter("b"); err != nil {
		t.Fatalf("InterruptAfter failed: %v", err)
	}

	_, err := g.Run(context.Background(), State{"init": "yes"}, WithThreadID("thread-int-after"))
	if err == nil {
		t.Fatal("expected GraphInterruptError, got nil")
	}

	var intErr *GraphInterruptError
	if !errors.As(err, &intErr) {
		t.Fatalf("expected GraphInterruptError, got %T: %v", err, err)
	}

	// Verify interrupt result.
	if intErr.Result.NodeName != "b" {
		t.Errorf("expected interrupt at node 'b', got %q", intErr.Result.NodeName)
	}
	if intErr.Result.Type != InterruptTypeAfter {
		t.Errorf("expected InterruptTypeAfter, got %q", intErr.Result.Type)
	}

	// Verify checkpoint DOES contain node "b" in Completed set.
	if !intErr.Result.Checkpoint.Completed["b"] {
		t.Error("InterruptAfter checkpoint should contain node 'b' in Completed set")
	}

	// Verify checkpoint DOES contain node "a" in Completed set.
	if !intErr.Result.Checkpoint.Completed["a"] {
		t.Error("InterruptAfter checkpoint should contain node 'a' in Completed set")
	}

	// Verify the state has both "a" and "b" results.
	if intErr.Result.Checkpoint.State["a"] != "done_a" {
		t.Errorf("expected state[a]=done_a, got %v", intErr.Result.Checkpoint.State["a"])
	}
	if intErr.Result.Checkpoint.State["b"] != "done_b" {
		t.Errorf("expected state[b]=done_b, got %v", intErr.Result.Checkpoint.State["b"])
	}

	// Verify node "c" was NOT executed.
	if _, exists := intErr.Result.Checkpoint.State["c"]; exists {
		t.Error("InterruptAfter checkpoint state should NOT contain key 'c'")
	}
}

func TestInterruptBefore_CheckpointDoesNotContainInterruptedNode(t *testing.T) {
	cp := &mockCheckpointer{}
	g := mustGraph(t, WithCheckpointer(cp))

	mustAddNodeWithKeys(t, g, "a", setter("a", "done_a"), []string{"a"}, []string{})
	mustAddNodeWithKeys(t, g, "b", setter("b", "done_b"), []string{"b"}, []string{"a"})
	g.Start("a")

	if err := g.InterruptBefore("b"); err != nil {
		t.Fatalf("InterruptBefore failed: %v", err)
	}

	_, err := g.Run(context.Background(), State{}, WithThreadID("thread-cp-before"))
	var intErr *GraphInterruptError
	if !errors.As(err, &intErr) {
		t.Fatalf("expected GraphInterruptError, got %T: %v", err, err)
	}

	// The checkpoint saved for InterruptBefore should NOT have the interrupted node in Completed.
	checkpoint := intErr.Result.Checkpoint
	if checkpoint.Completed["b"] {
		t.Error("InterruptBefore checkpoint Completed set must NOT contain the interrupted node 'b'")
	}

	// Node "a" should be in completed since it ran before "b".
	if !checkpoint.Completed["a"] {
		t.Error("InterruptBefore checkpoint Completed set should contain previously executed node 'a'")
	}

	// NodeName in checkpoint should be "b" (the node about to execute).
	if checkpoint.NodeName != "b" {
		t.Errorf("expected checkpoint NodeName='b', got %q", checkpoint.NodeName)
	}
}

func TestInterruptAfter_CheckpointContainsInterruptedNode(t *testing.T) {
	cp := &mockCheckpointer{}
	g := mustGraph(t, WithCheckpointer(cp))

	mustAddNodeWithKeys(t, g, "a", setter("a", "done_a"), []string{"a"}, []string{})
	mustAddNodeWithKeys(t, g, "b", setter("b", "done_b"), []string{"b"}, []string{"a"})
	g.Start("a")

	if err := g.InterruptAfter("b"); err != nil {
		t.Fatalf("InterruptAfter failed: %v", err)
	}

	_, err := g.Run(context.Background(), State{}, WithThreadID("thread-cp-after"))
	var intErr *GraphInterruptError
	if !errors.As(err, &intErr) {
		t.Fatalf("expected GraphInterruptError, got %T: %v", err, err)
	}

	// The checkpoint saved for InterruptAfter SHOULD have the interrupted node in Completed.
	checkpoint := intErr.Result.Checkpoint
	if !checkpoint.Completed["b"] {
		t.Error("InterruptAfter checkpoint Completed set must contain the interrupted node 'b'")
	}

	// Node "a" should also be in completed.
	if !checkpoint.Completed["a"] {
		t.Error("InterruptAfter checkpoint Completed set should contain previously executed node 'a'")
	}

	// NodeName in checkpoint should be "b" (the node that just executed).
	if checkpoint.NodeName != "b" {
		t.Errorf("expected checkpoint NodeName='b', got %q", checkpoint.NodeName)
	}

	// State should contain "b"'s result.
	if checkpoint.State["b"] != "done_b" {
		t.Errorf("expected checkpoint state[b]=done_b, got %v", checkpoint.State["b"])
	}
}

func TestInterrupt_NoOpWithoutCheckpointer(t *testing.T) {
	// When no checkpointer is configured, interrupts are no-ops.
	g := mustGraph(t) // No WithCheckpointer

	mustAddNodeWithKeys(t, g, "a", setter("a", "done_a"), []string{"a"}, []string{})
	mustAddNodeWithKeys(t, g, "b", setter("b", "done_b"), []string{"b"}, []string{"a"})
	mustAddNodeWithKeys(t, g, "c", setter("c", "done_c"), []string{"c"}, []string{"b"})
	g.Start("a")

	// Mark interrupts - they should be no-ops without a checkpointer.
	if err := g.InterruptBefore("b"); err != nil {
		t.Fatalf("InterruptBefore failed: %v", err)
	}
	if err := g.InterruptAfter("b"); err != nil {
		t.Fatalf("InterruptAfter failed: %v", err)
	}

	res, err := g.Run(context.Background(), State{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// All nodes should have executed since there's no checkpointer.
	if res.State["a"] != "done_a" {
		t.Errorf("expected state[a]=done_a, got %v", res.State["a"])
	}
	if res.State["b"] != "done_b" {
		t.Errorf("expected state[b]=done_b, got %v", res.State["b"])
	}
	if res.State["c"] != "done_c" {
		t.Errorf("expected state[c]=done_c, got %v", res.State["c"])
	}
}
