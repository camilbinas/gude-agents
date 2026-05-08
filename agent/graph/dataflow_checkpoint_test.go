package graph

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// ── Data-flow checkpoint/interrupt integration tests ─────────────────────────

func TestDataFlowCheckpoint_ReadinessSetIncludedInCheckpoint(t *testing.T) {
	// Test that checkpoint saved after data-flow node completion includes ReadinessSet.
	cp := &mockCheckpointer{}
	g := mustGraph(t, WithCheckpointer(cp))

	// Build a linear chain: entry → b → c
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

	_, err := g.Run(context.Background(), State{"init": "yes"}, WithThreadID("t-readiness"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have 3 checkpoints (one per node).
	if len(cp.saved) != 3 {
		t.Fatalf("expected 3 checkpoints, got %d", len(cp.saved))
	}

	// After entry node: readiness set should include "init" (from initial state).
	// Note: "x" is added by updateReadiness AFTER the checkpoint is saved,
	// so it won't be in this checkpoint's readiness set.
	rs0 := cp.saved[0].ReadinessSet
	if rs0 == nil {
		t.Fatal("checkpoint 0: ReadinessSet is nil")
	}
	if !rs0["init"] {
		t.Errorf("checkpoint 0: expected 'init' in ReadinessSet, got %v", rs0)
	}

	// After node b: readiness set should include "init" and "x" (entry's output was added
	// by updateReadiness before b was scheduled).
	rs1 := cp.saved[1].ReadinessSet
	if rs1 == nil {
		t.Fatal("checkpoint 1: ReadinessSet is nil")
	}
	if !rs1["x"] {
		t.Errorf("checkpoint 1: expected 'x' in ReadinessSet, got %v", rs1)
	}

	// After node c: readiness set should include "y" (b's output).
	rs2 := cp.saved[2].ReadinessSet
	if rs2 == nil {
		t.Fatal("checkpoint 2: ReadinessSet is nil")
	}
	if !rs2["y"] {
		t.Errorf("checkpoint 2: expected 'y' in ReadinessSet, got %v", rs2)
	}
}

func TestDataFlowCheckpoint_InterruptBeforePausesAndResumesContinues(t *testing.T) {
	// Test InterruptBefore pauses data-flow node, resume continues correctly.
	cp := &mockCheckpointer{}
	g := mustGraph(t, WithCheckpointer(cp))

	var executionOrder []string
	var mu sync.Mutex
	trackExec := func(name string) NodeFunc[State] {
		return func(_ context.Context, s State) (State, error) {
			mu.Lock()
			executionOrder = append(executionOrder, name)
			mu.Unlock()
			out := CopyState(s)
			out[name+"_out"] = true
			return out, nil
		}
	}

	mustAddNodeWithKeys(t, g, "entry", trackExec("entry"), []string{"entry_out"}, []string{})
	mustAddNodeWithKeys(t, g, "b", trackExec("b"), []string{"b_out"}, []string{"entry_out"})
	mustAddNodeWithKeys(t, g, "c", trackExec("c"), []string{"c_out"}, []string{"b_out"})
	g.Start("entry")

	// Set interrupt before "b".
	if err := g.InterruptBefore("b"); err != nil {
		t.Fatalf("InterruptBefore: %v", err)
	}

	// Run — should interrupt before node b.
	_, err := g.Run(context.Background(), State{"init": "yes"}, WithThreadID("t-int-before"))
	var intErr *GraphInterruptError
	if !errors.As(err, &intErr) {
		t.Fatalf("expected GraphInterruptError, got %v", err)
	}
	if intErr.Result.NodeName != "b" {
		t.Errorf("expected interrupt at node 'b', got %q", intErr.Result.NodeName)
	}
	if intErr.Result.Type != InterruptTypeBefore {
		t.Errorf("expected InterruptTypeBefore, got %v", intErr.Result.Type)
	}

	// Verify only entry executed so far.
	if len(executionOrder) != 1 || executionOrder[0] != "entry" {
		t.Errorf("expected only 'entry' executed, got %v", executionOrder)
	}

	// Verify checkpoint has ReadinessSet.
	lastCp := cp.saved[len(cp.saved)-1]
	if lastCp.ReadinessSet == nil {
		t.Fatal("interrupt checkpoint: ReadinessSet is nil")
	}
	if !lastCp.ReadinessSet["entry_out"] {
		t.Errorf("interrupt checkpoint: expected 'entry_out' in ReadinessSet")
	}

	// Resume — should continue from node b.
	result, err := g.Resume(context.Background(), "t-int-before", nil)
	if err != nil {
		t.Fatalf("Resume error: %v", err)
	}

	// Verify all nodes executed.
	if len(executionOrder) != 3 {
		t.Errorf("expected 3 nodes executed, got %d: %v", len(executionOrder), executionOrder)
	}
	if executionOrder[1] != "b" || executionOrder[2] != "c" {
		t.Errorf("expected execution order [entry, b, c], got %v", executionOrder)
	}

	// Verify final state.
	if result.State["c_out"] != true {
		t.Errorf("expected state[c_out]=true, got %v", result.State["c_out"])
	}
}

func TestDataFlowCheckpoint_InterruptAfterPausesAndResumesContinues(t *testing.T) {
	// Test InterruptAfter pauses data-flow node, resume continues correctly.
	cp := &mockCheckpointer{}
	g := mustGraph(t, WithCheckpointer(cp))

	var executionOrder []string
	var mu sync.Mutex
	trackExec := func(name string) NodeFunc[State] {
		return func(_ context.Context, s State) (State, error) {
			mu.Lock()
			executionOrder = append(executionOrder, name)
			mu.Unlock()
			out := CopyState(s)
			out[name+"_out"] = true
			return out, nil
		}
	}

	mustAddNodeWithKeys(t, g, "entry", trackExec("entry"), []string{"entry_out"}, []string{})
	mustAddNodeWithKeys(t, g, "b", trackExec("b"), []string{"b_out"}, []string{"entry_out"})
	mustAddNodeWithKeys(t, g, "c", trackExec("c"), []string{"c_out"}, []string{"b_out"})
	g.Start("entry")

	// Set interrupt after "b".
	if err := g.InterruptAfter("b"); err != nil {
		t.Fatalf("InterruptAfter: %v", err)
	}

	// Run — should interrupt after node b.
	_, err := g.Run(context.Background(), State{"init": "yes"}, WithThreadID("t-int-after"))
	var intErr *GraphInterruptError
	if !errors.As(err, &intErr) {
		t.Fatalf("expected GraphInterruptError, got %v", err)
	}
	if intErr.Result.NodeName != "b" {
		t.Errorf("expected interrupt at node 'b', got %q", intErr.Result.NodeName)
	}
	if intErr.Result.Type != InterruptTypeAfter {
		t.Errorf("expected InterruptTypeAfter, got %v", intErr.Result.Type)
	}

	// Verify entry and b executed.
	if len(executionOrder) != 2 {
		t.Errorf("expected 2 nodes executed before interrupt, got %d: %v", len(executionOrder), executionOrder)
	}

	// Verify checkpoint has ReadinessSet with entry's output (b_out is added by
	// updateReadiness which runs after checkpoint save, but entry_out should be there).
	lastCp := cp.saved[len(cp.saved)-1]
	if lastCp.ReadinessSet == nil {
		t.Fatal("interrupt checkpoint: ReadinessSet is nil")
	}
	if !lastCp.ReadinessSet["entry_out"] {
		t.Errorf("interrupt checkpoint: expected 'entry_out' in ReadinessSet, got %v", lastCp.ReadinessSet)
	}

	// Resume — should continue from after b, executing c.
	result, err := g.Resume(context.Background(), "t-int-after", nil)
	if err != nil {
		t.Fatalf("Resume error: %v", err)
	}

	// Verify c executed.
	if len(executionOrder) != 3 {
		t.Errorf("expected 3 nodes executed total, got %d: %v", len(executionOrder), executionOrder)
	}
	if executionOrder[2] != "c" {
		t.Errorf("expected 'c' as third execution, got %v", executionOrder)
	}

	// Verify final state.
	if result.State["c_out"] != true {
		t.Errorf("expected state[c_out]=true, got %v", result.State["c_out"])
	}
}

func TestDataFlowCheckpoint_ResumeReEvaluatesReadinessAndSchedules(t *testing.T) {
	// Test resume re-evaluates readiness and schedules next nodes.
	// Diamond: entry → (b, c) → d
	cp := &mockCheckpointer{}
	g := mustGraph(t, WithCheckpointer(cp))

	var executionOrder []string
	var mu sync.Mutex
	trackExec := func(name string) NodeFunc[State] {
		return func(_ context.Context, s State) (State, error) {
			mu.Lock()
			executionOrder = append(executionOrder, name)
			mu.Unlock()
			out := CopyState(s)
			out[name+"_out"] = true
			return out, nil
		}
	}

	mustAddNodeWithKeys(t, g, "entry", trackExec("entry"), []string{"entry_out"}, []string{})
	mustAddNodeWithKeys(t, g, "b", trackExec("b"), []string{"b_out"}, []string{"entry_out"})
	mustAddNodeWithKeys(t, g, "c", trackExec("c"), []string{"c_out"}, []string{"entry_out"})
	mustAddNodeWithKeys(t, g, "d", trackExec("d"), []string{"d_out"}, []string{"b_out", "c_out"})
	g.Start("entry")

	// Interrupt after entry to test that resume re-evaluates readiness.
	if err := g.InterruptAfter("entry"); err != nil {
		t.Fatalf("InterruptAfter: %v", err)
	}

	// Run — should interrupt after entry.
	_, err := g.Run(context.Background(), State{"init": "yes"}, WithThreadID("t-readiness-resume"))
	var intErr *GraphInterruptError
	if !errors.As(err, &intErr) {
		t.Fatalf("expected GraphInterruptError, got %v", err)
	}

	// Only entry executed.
	if len(executionOrder) != 1 {
		t.Errorf("expected 1 node executed, got %d: %v", len(executionOrder), executionOrder)
	}

	// Resume — should schedule b and c (both ready since entry_out is available),
	// then d (ready after b_out and c_out).
	result, err := g.Resume(context.Background(), "t-readiness-resume", nil)
	if err != nil {
		t.Fatalf("Resume error: %v", err)
	}

	// All 4 nodes should have executed.
	if len(executionOrder) != 4 {
		t.Errorf("expected 4 nodes executed total, got %d: %v", len(executionOrder), executionOrder)
	}

	// d should be last.
	if executionOrder[len(executionOrder)-1] != "d" {
		t.Errorf("expected 'd' as last execution, got %v", executionOrder)
	}

	// Verify final state has all outputs.
	if result.State["d_out"] != true {
		t.Errorf("expected state[d_out]=true, got %v", result.State["d_out"])
	}
}

func TestDataFlowCheckpoint_InterruptedConcurrentExecutionResumesCorrectly(t *testing.T) {
	// Test interrupted concurrent execution resumes correctly.
	// Graph: entry → (b, c) → d
	// Interrupt before "b" — since b and c are both ready concurrently,
	// but InterruptBefore on b should fire before b executes.
	cp := &mockCheckpointer{}
	g := mustGraph(t, WithCheckpointer(cp))

	var executionOrder []string
	var mu sync.Mutex
	trackExec := func(name string) NodeFunc[State] {
		return func(_ context.Context, s State) (State, error) {
			mu.Lock()
			executionOrder = append(executionOrder, name)
			mu.Unlock()
			out := CopyState(s)
			out[name+"_out"] = true
			return out, nil
		}
	}

	mustAddNodeWithKeys(t, g, "entry", trackExec("entry"), []string{"entry_out"}, []string{})
	mustAddNodeWithKeys(t, g, "b", trackExec("b"), []string{"b_out"}, []string{"entry_out"})
	mustAddNodeWithKeys(t, g, "c", trackExec("c"), []string{"c_out"}, []string{"entry_out"})
	mustAddNodeWithKeys(t, g, "d", trackExec("d"), []string{"d_out"}, []string{"b_out", "c_out"})
	g.Start("entry")

	// Interrupt before "b" — b is alphabetically first among concurrent nodes.
	if err := g.InterruptBefore("b"); err != nil {
		t.Fatalf("InterruptBefore: %v", err)
	}

	// Run — entry executes, then scheduling finds b and c ready.
	// Since b has InterruptBefore, it should interrupt before executing b.
	_, err := g.Run(context.Background(), State{"init": "yes"}, WithThreadID("t-concurrent-int"))
	var intErr *GraphInterruptError
	if !errors.As(err, &intErr) {
		t.Fatalf("expected GraphInterruptError, got %v", err)
	}
	if intErr.Result.NodeName != "b" {
		t.Errorf("expected interrupt at 'b', got %q", intErr.Result.NodeName)
	}

	// Only entry should have executed (b was interrupted before execution).
	if len(executionOrder) != 1 || executionOrder[0] != "entry" {
		t.Errorf("expected only 'entry' executed, got %v", executionOrder)
	}

	// Resume — should execute b, c, then d.
	result, err := g.Resume(context.Background(), "t-concurrent-int", nil)
	if err != nil {
		t.Fatalf("Resume error: %v", err)
	}

	// All 4 nodes should have executed.
	if len(executionOrder) != 4 {
		t.Errorf("expected 4 nodes executed total, got %d: %v", len(executionOrder), executionOrder)
	}

	// d should be last.
	if executionOrder[len(executionOrder)-1] != "d" {
		t.Errorf("expected 'd' as last execution, got %v", executionOrder)
	}

	// Verify final state.
	if result.State["d_out"] != true {
		t.Errorf("expected state[d_out]=true, got %v", result.State["d_out"])
	}
	if result.State["b_out"] != true {
		t.Errorf("expected state[b_out]=true, got %v", result.State["b_out"])
	}
	if result.State["c_out"] != true {
		t.Errorf("expected state[c_out]=true, got %v", result.State["c_out"])
	}
}
