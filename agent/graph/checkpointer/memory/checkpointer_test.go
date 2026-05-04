package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent/graph"
)

func TestSave_AssignsSequentialVersions(t *testing.T) {
	cp := New()
	ctx := context.Background()

	for i := 1; i <= 3; i++ {
		saved, err := cp.Save(ctx, "thread-1", graph.Checkpoint{
			State:    graph.State{"step": i},
			NodeName: "node",
		})
		if err != nil {
			t.Fatalf("Save %d: %v", i, err)
		}
		if saved.Version != i {
			t.Errorf("Save %d: expected version=%d, got %d", i, i, saved.Version)
		}
		if saved.ThreadID != "thread-1" {
			t.Errorf("Save %d: expected ThreadID='thread-1', got %q", i, saved.ThreadID)
		}
	}
}

func TestLoad_ReturnsLatestCheckpoint(t *testing.T) {
	cp := New()
	ctx := context.Background()

	// Save 3 checkpoints.
	for i := 1; i <= 3; i++ {
		_, err := cp.Save(ctx, "thread-1", graph.Checkpoint{
			State:    graph.State{"step": i},
			NodeName: "node-" + string(rune('a'+i-1)),
		})
		if err != nil {
			t.Fatalf("Save %d: %v", i, err)
		}
	}

	// Load should return the latest (version 3).
	latest, err := cp.Load(ctx, "thread-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if latest.Version != 3 {
		t.Errorf("expected version=3, got %d", latest.Version)
	}
	if latest.NodeName != "node-c" {
		t.Errorf("expected NodeName='node-c', got %q", latest.NodeName)
	}
}

func TestLoadAt_ReturnsSpecificVersion(t *testing.T) {
	cp := New()
	ctx := context.Background()

	// Save 3 checkpoints.
	for i := 1; i <= 3; i++ {
		_, err := cp.Save(ctx, "thread-1", graph.Checkpoint{
			State:    graph.State{"step": i},
			NodeName: "node-" + string(rune('a'+i-1)),
		})
		if err != nil {
			t.Fatalf("Save %d: %v", i, err)
		}
	}

	// LoadAt version 2.
	v2, err := cp.LoadAt(ctx, "thread-1", 2)
	if err != nil {
		t.Fatalf("LoadAt(2): %v", err)
	}
	if v2.Version != 2 {
		t.Errorf("expected version=2, got %d", v2.Version)
	}
	if v2.NodeName != "node-b" {
		t.Errorf("expected NodeName='node-b', got %q", v2.NodeName)
	}
	if v2.State["step"] != 2 {
		t.Errorf("expected state[step]=2, got %v", v2.State["step"])
	}
}

func TestHistory_ReturnsOrderedMetadata(t *testing.T) {
	cp := New()
	ctx := context.Background()

	nodes := []string{"alpha", "beta", "gamma"}
	for _, name := range nodes {
		_, err := cp.Save(ctx, "thread-1", graph.Checkpoint{
			State:     graph.State{"node": name},
			NodeName:  name,
			Timestamp: time.Now(),
		})
		if err != nil {
			t.Fatalf("Save(%s): %v", name, err)
		}
	}

	history, err := cp.History(ctx, "thread-1")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(history))
	}

	// Verify ordering: version 1, 2, 3.
	for i, meta := range history {
		expectedVersion := i + 1
		if meta.Version != expectedVersion {
			t.Errorf("entry %d: expected version=%d, got %d", i, expectedVersion, meta.Version)
		}
		if meta.NodeName != nodes[i] {
			t.Errorf("entry %d: expected NodeName=%q, got %q", i, nodes[i], meta.NodeName)
		}
		if meta.Timestamp.IsZero() {
			t.Errorf("entry %d: expected non-zero timestamp", i)
		}
	}
}

func TestList_ReturnsAllThreadIDs(t *testing.T) {
	cp := New()
	ctx := context.Background()

	threads := []string{"thread-a", "thread-b", "thread-c"}
	for _, tid := range threads {
		_, err := cp.Save(ctx, tid, graph.Checkpoint{
			State:    graph.State{"x": 1},
			NodeName: "node",
		})
		if err != nil {
			t.Fatalf("Save(%s): %v", tid, err)
		}
	}

	ids, err := cp.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(ids) != 3 {
		t.Fatalf("expected 3 thread IDs, got %d", len(ids))
	}

	// Check all threads are present.
	idSet := make(map[string]bool)
	for _, id := range ids {
		idSet[id] = true
	}
	for _, tid := range threads {
		if !idSet[tid] {
			t.Errorf("expected thread %q in list", tid)
		}
	}
}

func TestDelete_RemovesAllCheckpointsForThread(t *testing.T) {
	cp := New()
	ctx := context.Background()

	// Save checkpoints for two threads.
	for i := 1; i <= 3; i++ {
		_, _ = cp.Save(ctx, "thread-keep", graph.Checkpoint{State: graph.State{"i": i}, NodeName: "n"})
		_, _ = cp.Save(ctx, "thread-delete", graph.Checkpoint{State: graph.State{"i": i}, NodeName: "n"})
	}

	// Delete one thread.
	if err := cp.Delete(ctx, "thread-delete"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Load from deleted thread should fail.
	_, err := cp.Load(ctx, "thread-delete")
	if !errors.Is(err, graph.ErrCheckpointNotFound) {
		t.Errorf("expected ErrCheckpointNotFound after delete, got %v", err)
	}

	// List should not include deleted thread.
	ids, err := cp.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, id := range ids {
		if id == "thread-delete" {
			t.Error("deleted thread should not appear in List")
		}
	}

	// Other thread should still be accessible.
	_, err = cp.Load(ctx, "thread-keep")
	if err != nil {
		t.Errorf("expected thread-keep to still be accessible, got %v", err)
	}
}

func TestLoad_NonExistentThread_ReturnsErrCheckpointNotFound(t *testing.T) {
	cp := New()
	ctx := context.Background()

	_, err := cp.Load(ctx, "nonexistent")
	if !errors.Is(err, graph.ErrCheckpointNotFound) {
		t.Errorf("expected ErrCheckpointNotFound, got %v", err)
	}
}

func TestLoadAt_NonExistentThread_ReturnsErrCheckpointNotFound(t *testing.T) {
	cp := New()
	ctx := context.Background()

	_, err := cp.LoadAt(ctx, "nonexistent", 1)
	if !errors.Is(err, graph.ErrCheckpointNotFound) {
		t.Errorf("expected ErrCheckpointNotFound, got %v", err)
	}
}

func TestLoadAt_NonExistentVersion_ReturnsErrCheckpointNotFound(t *testing.T) {
	cp := New()
	ctx := context.Background()

	// Save one checkpoint.
	_, _ = cp.Save(ctx, "thread-1", graph.Checkpoint{State: graph.State{"x": 1}, NodeName: "n"})

	// Try to load a version that doesn't exist.
	_, err := cp.LoadAt(ctx, "thread-1", 99)
	if !errors.Is(err, graph.ErrCheckpointNotFound) {
		t.Errorf("expected ErrCheckpointNotFound for non-existent version, got %v", err)
	}
}
