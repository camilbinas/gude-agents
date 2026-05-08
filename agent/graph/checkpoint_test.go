package graph

import (
	"context"
	"errors"
	"testing"
)

// ── mock checkpointer for error injection ────────────────────────────────────

type mockCheckpointer struct {
	saved   []Checkpoint
	saveErr error
}

func (m *mockCheckpointer) Save(_ context.Context, threadID string, cp Checkpoint) (Checkpoint, error) {
	if m.saveErr != nil {
		return Checkpoint{}, m.saveErr
	}
	version := len(m.saved) + 1
	cp.ThreadID = threadID
	cp.Version = version
	m.saved = append(m.saved, cp)
	return cp, nil
}

func (m *mockCheckpointer) Load(_ context.Context, threadID string) (Checkpoint, error) {
	for i := len(m.saved) - 1; i >= 0; i-- {
		if m.saved[i].ThreadID == threadID {
			return m.saved[i], nil
		}
	}
	return Checkpoint{}, ErrCheckpointNotFound
}

func (m *mockCheckpointer) LoadAt(_ context.Context, threadID string, version int) (Checkpoint, error) {
	for _, cp := range m.saved {
		if cp.ThreadID == threadID && cp.Version == version {
			return cp, nil
		}
	}
	return Checkpoint{}, ErrCheckpointNotFound
}

func (m *mockCheckpointer) History(_ context.Context, threadID string) ([]CheckpointMeta, error) {
	var metas []CheckpointMeta
	for _, cp := range m.saved {
		if cp.ThreadID == threadID {
			metas = append(metas, CheckpointMeta{
				Version:   cp.Version,
				NodeName:  cp.NodeName,
				Timestamp: cp.Timestamp,
			})
		}
	}
	return metas, nil
}

func (m *mockCheckpointer) List(_ context.Context) ([]string, error) {
	seen := make(map[string]bool)
	var ids []string
	for _, cp := range m.saved {
		if !seen[cp.ThreadID] {
			seen[cp.ThreadID] = true
			ids = append(ids, cp.ThreadID)
		}
	}
	return ids, nil
}

func (m *mockCheckpointer) Delete(_ context.Context, threadID string) error {
	var remaining []Checkpoint
	for _, cp := range m.saved {
		if cp.ThreadID != threadID {
			remaining = append(remaining, cp)
		}
	}
	m.saved = remaining
	return nil
}

// ── checkpoint integration tests ─────────────────────────────────────────────

func TestCheckpointSaveIntegration(t *testing.T) {
	t.Run("checkpoints saved after each node with InMemory backend", func(t *testing.T) {
		cp := &mockCheckpointer{}
		g := mustGraph(t, WithCheckpointer(cp))

		mustAddNodeWithKeys(t, g, "a", setter("a", "done_a"), []string{"a"}, []string{})
		mustAddNodeWithKeys(t, g, "b", setter("b", "done_b"), []string{"b"}, []string{"a"})
		mustAddNodeWithKeys(t, g, "c", setter("c", "done_c"), []string{"c"}, []string{"b"})
		g.Start("a")

		_, err := g.Run(context.Background(), State{"init": "yes"}, WithThreadID("thread-1"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Should have 3 checkpoints (one per node).
		if len(cp.saved) != 3 {
			t.Fatalf("expected 3 checkpoints, got %d", len(cp.saved))
		}

		// Verify versions are sequential.
		for i, saved := range cp.saved {
			expectedVersion := i + 1
			if saved.Version != expectedVersion {
				t.Errorf("checkpoint %d: expected version %d, got %d", i, expectedVersion, saved.Version)
			}
		}

		// Verify node names match execution order.
		expectedNodes := []string{"a", "b", "c"}
		for i, saved := range cp.saved {
			if saved.NodeName != expectedNodes[i] {
				t.Errorf("checkpoint %d: expected node %q, got %q", i, expectedNodes[i], saved.NodeName)
			}
		}

		// Verify state accumulates correctly.
		// After node "a": state should have "init" and "a".
		if cp.saved[0].State["a"] != "done_a" {
			t.Errorf("checkpoint 0: expected state[a]=done_a, got %v", cp.saved[0].State["a"])
		}
		if cp.saved[0].State["init"] != "yes" {
			t.Errorf("checkpoint 0: expected state[init]=yes, got %v", cp.saved[0].State["init"])
		}

		// After node "b": state should have "init", "a", and "b".
		if cp.saved[1].State["b"] != "done_b" {
			t.Errorf("checkpoint 1: expected state[b]=done_b, got %v", cp.saved[1].State["b"])
		}

		// After node "c": state should have all keys.
		if cp.saved[2].State["c"] != "done_c" {
			t.Errorf("checkpoint 2: expected state[c]=done_c, got %v", cp.saved[2].State["c"])
		}

		// Verify thread ID is set.
		for i, saved := range cp.saved {
			if saved.ThreadID != "thread-1" {
				t.Errorf("checkpoint %d: expected threadID=thread-1, got %q", i, saved.ThreadID)
			}
		}
	})

	t.Run("serialization validation error aborts execution", func(t *testing.T) {
		cp := &mockCheckpointer{}
		g := mustGraph(t, WithCheckpointer(cp))

		// Node "bad" puts a non-serializable value (channel) into state.
		mustAddNodeWithKeys(t, g, "a", setter("a", "done_a"), []string{"a"}, []string{})
		mustAddNodeWithKeys(t, g, "bad", func(_ context.Context, s State) (State, error) {
			out := CopyState(s)
			out["channel"] = make(chan int)
			return out, nil
		}, []string{"bad_out"}, []string{"a"})
		mustAddNodeWithKeys(t, g, "c", setter("c", "done_c"), []string{"c"}, []string{"bad_out"})
		g.Start("a")

		_, err := g.Run(context.Background(), State{}, WithThreadID("thread-2"))
		if err == nil {
			t.Fatal("expected serialization error, got nil")
		}

		var serErr *StateSerializationError
		if !errors.As(err, &serErr) {
			t.Fatalf("expected StateSerializationError, got %T: %v", err, err)
		}

		// Node "a" should have checkpointed successfully, but "bad" should fail.
		if len(cp.saved) != 1 {
			t.Errorf("expected 1 checkpoint (only node a), got %d", len(cp.saved))
		}
	})

	t.Run("checkpointer save error aborts execution", func(t *testing.T) {
		saveErr := errors.New("disk full")
		cp := &mockCheckpointer{saveErr: saveErr}
		g := mustGraph(t, WithCheckpointer(cp))

		mustAddNodeWithKeys(t, g, "a", setter("a", "done_a"), []string{"a"}, []string{})
		mustAddNodeWithKeys(t, g, "b", setter("b", "done_b"), []string{"b"}, []string{"a"})
		g.Start("a")

		_, err := g.Run(context.Background(), State{}, WithThreadID("thread-3"))
		if err == nil {
			t.Fatal("expected save error, got nil")
		}
		if !errors.Is(err, saveErr) {
			t.Errorf("expected save error %q, got %v", saveErr, err)
		}

		// No checkpoints should be saved since the first save fails.
		if len(cp.saved) != 0 {
			t.Errorf("expected 0 checkpoints, got %d", len(cp.saved))
		}
	})

	t.Run("no checkpointing when no checkpointer configured", func(t *testing.T) {
		g := mustGraph(t) // No WithCheckpointer

		mustAddNodeWithKeys(t, g, "a", setter("a", "done_a"), []string{"a"}, []string{})
		mustAddNodeWithKeys(t, g, "b", setter("b", "done_b"), []string{"b"}, []string{"a"})
		g.Start("a")

		res, err := g.Run(context.Background(), State{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Execution should complete normally without any checkpointing.
		if res.State["a"] != "done_a" {
			t.Errorf("expected state[a]=done_a, got %v", res.State["a"])
		}
		if res.State["b"] != "done_b" {
			t.Errorf("expected state[b]=done_b, got %v", res.State["b"])
		}
	})

	t.Run("ErrThreadIDRequired when checkpointer configured but no threadID", func(t *testing.T) {
		cp := &mockCheckpointer{}
		g := mustGraph(t, WithCheckpointer(cp))

		mustAddNodeWithKeys(t, g, "a", setter("a", "done_a"), []string{"a"}, []string{})
		g.Start("a")

		_, err := g.Run(context.Background(), State{})
		if !errors.Is(err, ErrThreadIDRequired) {
			t.Fatalf("expected ErrThreadIDRequired, got %v", err)
		}
	})
}

// ── Struct state checkpointing ───────────────────────────────────────────────

// testState is a simple struct state for testing graph checkpointing with Graph[S].
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

func TestGraph_StructState_Step_RoundTripsState(t *testing.T) {
	cp := &mockCheckpointer{}
	g := mustNewGraph[testState](t, WithCheckpointer(cp))

	_, err := g.Node("increment", func(_ context.Context, s testState) (testState, error) {
		s.Counter++
		s.Message = "incremented"
		return s, nil
	}, In(), Out("counter"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = g.Node("double", func(_ context.Context, s testState) (testState, error) {
		s.Counter *= 2
		s.Message = "doubled"
		return s, nil
	}, In("counter"), Out("message"))
	if err != nil {
		t.Fatal(err)
	}
	g.Start("increment")

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

func TestGraph_StructState_Resume_ContinuesWithState(t *testing.T) {
	cp := &mockCheckpointer{}
	g := mustNewGraph[testState](t, WithCheckpointer(cp))

	_, err := g.Node("first", func(_ context.Context, s testState) (testState, error) {
		s.Counter = 10
		s.Message = "first done"
		return s, nil
	}, In(), Out("first_out"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = g.Node("second", func(_ context.Context, s testState) (testState, error) {
		s.Counter += 5
		s.Message = "second done"
		return s, nil
	}, In("first_out"), Out("second_out"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = g.Node("third", func(_ context.Context, s testState) (testState, error) {
		s.Counter *= 2
		s.Message = "third done"
		return s, nil
	}, In("second_out"), Out("third_out"))
	if err != nil {
		t.Fatal(err)
	}
	g.Start("first")

	if err := g.InterruptBefore("second"); err != nil {
		t.Fatal(err)
	}

	_, err = g.Run(context.Background(), testState{Counter: 0}, WithThreadID("typed-resume-1"))
	var intErr *GraphInterruptError
	if !errors.As(err, &intErr) {
		t.Fatalf("expected GraphInterruptError, got %v", err)
	}
	if intErr.Result.NodeName != "second" {
		t.Fatalf("expected interrupt at 'second', got %q", intErr.Result.NodeName)
	}

	updates := testState{Counter: 100}
	result, err := g.Resume(context.Background(), "typed-resume-1", &updates)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}

	if result.State.Counter != 210 {
		t.Errorf("expected Counter=210, got %d", result.State.Counter)
	}
	if result.State.Message != "third done" {
		t.Errorf("expected Message='third done', got %q", result.State.Message)
	}
}

func TestGraph_StructState_InterruptBefore(t *testing.T) {
	cp := &mockCheckpointer{}
	g := mustNewGraph[testState](t, WithCheckpointer(cp))

	_, err := g.Node("nodeA", func(_ context.Context, s testState) (testState, error) {
		return s, nil
	}, In(), Out("nodeA_out"))
	if err != nil {
		t.Fatal(err)
	}
	g.Start("nodeA")

	if err := g.InterruptBefore("nodeA"); err != nil {
		t.Errorf("InterruptBefore(nodeA): unexpected error: %v", err)
	}

	err = g.InterruptBefore("nonexistent")
	if err == nil {
		t.Error("InterruptBefore(nonexistent): expected error, got nil")
	}
	var ve *GraphValidationError
	if !errors.As(err, &ve) {
		t.Errorf("InterruptBefore(nonexistent): expected GraphValidationError, got %T: %v", err, err)
	}
}

func TestGraph_StructState_InterruptAfter(t *testing.T) {
	cp := &mockCheckpointer{}
	g := mustNewGraph[testState](t, WithCheckpointer(cp))

	_, err := g.Node("nodeA", func(_ context.Context, s testState) (testState, error) {
		return s, nil
	}, In(), Out("nodeA_out"))
	if err != nil {
		t.Fatal(err)
	}
	g.Start("nodeA")

	if err := g.InterruptAfter("nodeA"); err != nil {
		t.Errorf("InterruptAfter(nodeA): unexpected error: %v", err)
	}

	err = g.InterruptAfter("nonexistent")
	if err == nil {
		t.Error("InterruptAfter(nonexistent): expected error, got nil")
	}
	var ve *GraphValidationError
	if !errors.As(err, &ve) {
		t.Errorf("InterruptAfter(nonexistent): expected GraphValidationError, got %T: %v", err, err)
	}
}

func TestGraph_StructState_RewindTo(t *testing.T) {
	cp := &mockCheckpointer{}
	g := mustNewGraph[testState](t, WithCheckpointer(cp))

	_, err := g.Node("a", func(_ context.Context, s testState) (testState, error) {
		s.Counter = 1
		return s, nil
	}, In(), Out("a_out"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = g.Node("b", func(_ context.Context, s testState) (testState, error) {
		s.Counter = 2
		return s, nil
	}, In("a_out"), Out("b_out"))
	if err != nil {
		t.Fatal(err)
	}
	g.Start("a")

	_, err = g.Run(context.Background(), testState{}, WithThreadID("typed-rewind-1"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if err := g.RewindTo(context.Background(), "typed-rewind-1", 1); err != nil {
		t.Fatalf("RewindTo: %v", err)
	}

	result, err := g.Resume(context.Background(), "typed-rewind-1", nil)
	if err != nil {
		t.Fatalf("Resume after rewind: %v", err)
	}
	if result.State.Counter != 2 {
		t.Errorf("expected Counter=2 after rewind+resume, got %d", result.State.Counter)
	}
}
