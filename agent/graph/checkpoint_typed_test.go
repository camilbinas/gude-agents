package graph

import (
	"context"
	"errors"
	"testing"
)

// testState is a simple typed state for testing typed graph checkpointing.
type testState struct {
	Counter int    `json:"counter"`
	Message string `json:"message"`
}

func mustNewGraph[S any](t *testing.T, opts ...GraphOption) *Graph[S] {
	t.Helper()
	g, err := New[S](opts...)
	if err != nil {
		t.Fatalf("New[S]: %v", err)
	}
	return g
}

func TestGraph_TypedStep_RoundTripsState(t *testing.T) {
	cp := &mockCheckpointer{}
	g := mustNewGraph[testState](t, WithCheckpointer(cp))

	err := g.AddNode("increment", func(_ context.Context, s testState) (testState, error) {
		s.Counter++
		s.Message = "incremented"
		return s, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = g.AddNode("double", func(_ context.Context, s testState) (testState, error) {
		s.Counter *= 2
		s.Message = "doubled"
		return s, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	g.SetEntry("increment")
	if err := g.AddEdge("increment", "double"); err != nil {
		t.Fatal(err)
	}

	// Step 1: execute "increment".
	result, err := g.Step(context.Background(), testState{Counter: 5, Message: "start"}, "typed-step-1")
	if err != nil {
		t.Fatalf("Step 1: %v", err)
	}
	if result.NodeName != "increment" {
		t.Errorf("Step 1: expected NodeName='increment', got %q", result.NodeName)
	}
	if result.State.Counter != 6 {
		t.Errorf("Step 1: expected Counter=6, got %d", result.State.Counter)
	}
	if result.State.Message != "incremented" {
		t.Errorf("Step 1: expected Message='incremented', got %q", result.State.Message)
	}
	if result.Version != 1 {
		t.Errorf("Step 1: expected Version=1, got %d", result.Version)
	}
	if result.Done {
		t.Error("Step 1: expected Done=false")
	}

	// Step 2: execute "double".
	result2, err := g.Step(context.Background(), testState{}, "typed-step-1")
	if err != nil {
		t.Fatalf("Step 2: %v", err)
	}
	if result2.NodeName != "double" {
		t.Errorf("Step 2: expected NodeName='double', got %q", result2.NodeName)
	}
	if result2.State.Counter != 12 {
		t.Errorf("Step 2: expected Counter=12, got %d", result2.State.Counter)
	}
	if result2.State.Message != "doubled" {
		t.Errorf("Step 2: expected Message='doubled', got %q", result2.State.Message)
	}
	if !result2.Done {
		t.Error("Step 2: expected Done=true")
	}
}

func TestGraph_TypedResume_ContinuesWithState(t *testing.T) {
	cp := &mockCheckpointer{}
	g := mustNewGraph[testState](t, WithCheckpointer(cp))

	err := g.AddNode("first", func(_ context.Context, s testState) (testState, error) {
		s.Counter = 10
		s.Message = "first done"
		return s, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = g.AddNode("second", func(_ context.Context, s testState) (testState, error) {
		s.Counter += 5
		s.Message = "second done"
		return s, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = g.AddNode("third", func(_ context.Context, s testState) (testState, error) {
		s.Counter *= 2
		s.Message = "third done"
		return s, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	g.SetEntry("first")
	if err := g.AddEdge("first", "second"); err != nil {
		t.Fatal(err)
	}
	if err := g.AddEdge("second", "third"); err != nil {
		t.Fatal(err)
	}

	// Set interrupt before "second".
	if err := g.InterruptBefore("second"); err != nil {
		t.Fatal(err)
	}

	// Run until interrupt.
	_, err = g.Run(context.Background(), testState{Counter: 0}, WithThreadID("typed-resume-1"))
	var intErr *GraphInterruptError
	if !errors.As(err, &intErr) {
		t.Fatalf("expected GraphInterruptError, got %v", err)
	}
	if intErr.Result.NodeName != "second" {
		t.Fatalf("expected interrupt at 'second', got %q", intErr.Result.NodeName)
	}

	// Resume with state updates.
	updates := testState{Counter: 100}
	result, err := g.Resume(context.Background(), "typed-resume-1", &updates)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}

	// The counter should reflect: updates merged (100), then second (+5=105), then third (*2=210).
	if result.State.Counter != 210 {
		t.Errorf("expected Counter=210, got %d", result.State.Counter)
	}
	if result.State.Message != "third done" {
		t.Errorf("expected Message='third done', got %q", result.State.Message)
	}
}

func TestGraph_TypedInterruptBefore(t *testing.T) {
	cp := &mockCheckpointer{}
	g := mustNewGraph[testState](t, WithCheckpointer(cp))

	err := g.AddNode("nodeA", func(_ context.Context, s testState) (testState, error) {
		return s, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	g.SetEntry("nodeA")

	// InterruptBefore on a registered node should succeed.
	if err := g.InterruptBefore("nodeA"); err != nil {
		t.Errorf("InterruptBefore(nodeA): unexpected error: %v", err)
	}

	// InterruptBefore on an unregistered node should return validation error.
	err = g.InterruptBefore("nonexistent")
	if err == nil {
		t.Error("InterruptBefore(nonexistent): expected error, got nil")
	}
	var ve *GraphValidationError
	if !errors.As(err, &ve) {
		t.Errorf("InterruptBefore(nonexistent): expected GraphValidationError, got %T: %v", err, err)
	}
}

func TestGraph_TypedInterruptAfter(t *testing.T) {
	cp := &mockCheckpointer{}
	g := mustNewGraph[testState](t, WithCheckpointer(cp))

	err := g.AddNode("nodeA", func(_ context.Context, s testState) (testState, error) {
		return s, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	g.SetEntry("nodeA")

	// InterruptAfter on a registered node should succeed.
	if err := g.InterruptAfter("nodeA"); err != nil {
		t.Errorf("InterruptAfter(nodeA): unexpected error: %v", err)
	}

	// InterruptAfter on an unregistered node should return validation error.
	err = g.InterruptAfter("nonexistent")
	if err == nil {
		t.Error("InterruptAfter(nonexistent): expected error, got nil")
	}
	var ve *GraphValidationError
	if !errors.As(err, &ve) {
		t.Errorf("InterruptAfter(nonexistent): expected GraphValidationError, got %T: %v", err, err)
	}
}

func TestGraph_TypedRewindTo(t *testing.T) {
	cp := &mockCheckpointer{}
	g := mustNewGraph[testState](t, WithCheckpointer(cp))

	err := g.AddNode("a", func(_ context.Context, s testState) (testState, error) {
		s.Counter = 1
		return s, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = g.AddNode("b", func(_ context.Context, s testState) (testState, error) {
		s.Counter = 2
		return s, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	g.SetEntry("a")
	if err := g.AddEdge("a", "b"); err != nil {
		t.Fatal(err)
	}

	// Run the full graph.
	_, err = g.Run(context.Background(), testState{}, WithThreadID("typed-rewind-1"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// RewindTo version 1.
	if err := g.RewindTo(context.Background(), "typed-rewind-1", 1); err != nil {
		t.Fatalf("RewindTo: %v", err)
	}

	// Resume should re-execute from after version 1 (node "a"), so "b" runs again.
	result, err := g.Resume(context.Background(), "typed-rewind-1", nil)
	if err != nil {
		t.Fatalf("Resume after rewind: %v", err)
	}
	if result.State.Counter != 2 {
		t.Errorf("expected Counter=2 after rewind+resume, got %d", result.State.Counter)
	}
}
