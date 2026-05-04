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

		mustAddNode(t, g, "a", setter("a", "done_a"))
		mustAddNode(t, g, "b", setter("b", "done_b"))
		mustAddNode(t, g, "c", setter("c", "done_c"))
		g.SetEntry("a")
		mustAddEdge(t, g, "a", "b")
		mustAddEdge(t, g, "b", "c")

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
		mustAddNode(t, g, "a", setter("a", "done_a"))
		mustAddNode(t, g, "bad", func(_ context.Context, s State) (State, error) {
			out := CopyState(s)
			out["channel"] = make(chan int)
			return out, nil
		})
		mustAddNode(t, g, "c", setter("c", "done_c"))
		g.SetEntry("a")
		mustAddEdge(t, g, "a", "bad")
		mustAddEdge(t, g, "bad", "c")

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

		mustAddNode(t, g, "a", setter("a", "done_a"))
		mustAddNode(t, g, "b", setter("b", "done_b"))
		g.SetEntry("a")
		mustAddEdge(t, g, "a", "b")

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

		mustAddNode(t, g, "a", setter("a", "done_a"))
		mustAddNode(t, g, "b", setter("b", "done_b"))
		g.SetEntry("a")
		mustAddEdge(t, g, "a", "b")

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

		mustAddNode(t, g, "a", setter("a", "done_a"))
		g.SetEntry("a")

		_, err := g.Run(context.Background(), State{})
		if !errors.Is(err, ErrThreadIDRequired) {
			t.Fatalf("expected ErrThreadIDRequired, got %v", err)
		}
	})
}
