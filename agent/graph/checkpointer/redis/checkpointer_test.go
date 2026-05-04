package redis

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/graph"
)

// skipIfNoRedis skips the test if REDIS_ADDR is not set and returns the address.
func skipIfNoRedis(t *testing.T) string {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		t.Skip("REDIS_ADDR not set, skipping integration test")
	}
	return addr
}

// newTestCheckpointer creates a Checkpointer with a unique prefix for test isolation.
func newTestCheckpointer(t *testing.T, prefix string) *Checkpointer {
	t.Helper()
	addr := skipIfNoRedis(t)
	cp, err := New(Options{Addr: addr}, WithKeyPrefix(prefix))
	if err != nil {
		t.Fatalf("failed to create Checkpointer: %v", err)
	}
	t.Cleanup(func() { cp.Close() })
	return cp
}

func TestNew_UnreachableAddr(t *testing.T) {
	_, err := New(Options{Addr: "localhost:1"})
	if err == nil {
		t.Fatal("expected error for unreachable address, got nil")
	}
	if !contains(err.Error(), "ping") {
		t.Fatalf("expected error to contain 'ping', got: %v", err)
	}
}

func TestCheckpointer_SaveLoadRoundTrip(t *testing.T) {
	prefix := "test-cp-roundtrip:"
	cp := newTestCheckpointer(t, prefix)
	ctx := context.Background()

	threadID := "thread-roundtrip-1"
	defer cp.Delete(ctx, threadID)

	input := graph.Checkpoint{
		State:      graph.State{"key": "value", "count": float64(42)},
		Completed:  map[string]bool{"node_a": true},
		Iterations: 3,
		Usage:      agent.TokenUsage{InputTokens: 100, OutputTokens: 50},
		NodeName:   "node_a",
	}

	saved, err := cp.Save(ctx, threadID, input)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	if saved.Version != 1 {
		t.Fatalf("expected version 1, got %d", saved.Version)
	}
	if saved.ThreadID != threadID {
		t.Fatalf("expected threadID %q, got %q", threadID, saved.ThreadID)
	}
	if saved.Timestamp.IsZero() {
		t.Fatal("expected non-zero timestamp")
	}

	loaded, err := cp.Load(ctx, threadID)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if loaded.Version != saved.Version {
		t.Fatalf("version mismatch: saved=%d loaded=%d", saved.Version, loaded.Version)
	}
	if loaded.ThreadID != threadID {
		t.Fatalf("threadID mismatch: expected %q, got %q", threadID, loaded.ThreadID)
	}
	if loaded.NodeName != "node_a" {
		t.Fatalf("node name mismatch: expected %q, got %q", "node_a", loaded.NodeName)
	}
	if loaded.Iterations != 3 {
		t.Fatalf("iterations mismatch: expected 3, got %d", loaded.Iterations)
	}
	if loaded.Usage.InputTokens != 100 || loaded.Usage.OutputTokens != 50 {
		t.Fatalf("usage mismatch: %+v", loaded.Usage)
	}
}

func TestCheckpointer_LoadNotFound(t *testing.T) {
	prefix := "test-cp-notfound:"
	cp := newTestCheckpointer(t, prefix)
	ctx := context.Background()

	_, err := cp.Load(ctx, "nonexistent-thread")
	if err != graph.ErrCheckpointNotFound {
		t.Fatalf("expected ErrCheckpointNotFound, got: %v", err)
	}
}

func TestCheckpointer_LoadAt(t *testing.T) {
	prefix := "test-cp-loadat:"
	cp := newTestCheckpointer(t, prefix)
	ctx := context.Background()

	threadID := "thread-loadat-1"
	defer cp.Delete(ctx, threadID)

	// Save 3 versions.
	for i := 0; i < 3; i++ {
		_, err := cp.Save(ctx, threadID, graph.Checkpoint{
			NodeName: "node_" + string(rune('a'+i)),
			State:    graph.State{"step": float64(i)},
		})
		if err != nil {
			t.Fatalf("Save %d failed: %v", i, err)
		}
	}

	// Load version 2.
	loaded, err := cp.LoadAt(ctx, threadID, 2)
	if err != nil {
		t.Fatalf("LoadAt(2) failed: %v", err)
	}
	if loaded.Version != 2 {
		t.Fatalf("expected version 2, got %d", loaded.Version)
	}
	if loaded.NodeName != "node_b" {
		t.Fatalf("expected node_b, got %q", loaded.NodeName)
	}

	// Load non-existent version.
	_, err = cp.LoadAt(ctx, threadID, 99)
	if err != graph.ErrCheckpointNotFound {
		t.Fatalf("expected ErrCheckpointNotFound for version 99, got: %v", err)
	}
}

func TestCheckpointer_History(t *testing.T) {
	prefix := "test-cp-history:"
	cp := newTestCheckpointer(t, prefix)
	ctx := context.Background()

	threadID := "thread-history-1"
	defer cp.Delete(ctx, threadID)

	nodes := []string{"start", "process", "end"}
	for _, name := range nodes {
		_, err := cp.Save(ctx, threadID, graph.Checkpoint{NodeName: name})
		if err != nil {
			t.Fatalf("Save(%q) failed: %v", name, err)
		}
	}

	metas, err := cp.History(ctx, threadID)
	if err != nil {
		t.Fatalf("History failed: %v", err)
	}

	if len(metas) != 3 {
		t.Fatalf("expected 3 metas, got %d", len(metas))
	}

	// Verify ordering (oldest first).
	for i, meta := range metas {
		if meta.Version != i+1 {
			t.Fatalf("meta[%d] version: expected %d, got %d", i, i+1, meta.Version)
		}
		if meta.NodeName != nodes[i] {
			t.Fatalf("meta[%d] node: expected %q, got %q", i, nodes[i], meta.NodeName)
		}
	}
}

func TestCheckpointer_List(t *testing.T) {
	prefix := "test-cp-list:"
	cp := newTestCheckpointer(t, prefix)
	ctx := context.Background()

	threads := []string{"thread-list-a", "thread-list-b", "thread-list-c"}
	for _, id := range threads {
		_, err := cp.Save(ctx, id, graph.Checkpoint{NodeName: "init"})
		if err != nil {
			t.Fatalf("Save(%q) failed: %v", id, err)
		}
	}
	defer func() {
		for _, id := range threads {
			cp.Delete(ctx, id)
		}
	}()

	listed, err := cp.List(ctx)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	// All our threads should be present.
	found := make(map[string]bool)
	for _, id := range listed {
		found[id] = true
	}
	for _, id := range threads {
		if !found[id] {
			t.Fatalf("thread %q not found in List result: %v", id, listed)
		}
	}
}

func TestCheckpointer_Delete(t *testing.T) {
	prefix := "test-cp-delete:"
	cp := newTestCheckpointer(t, prefix)
	ctx := context.Background()

	threadID := "thread-delete-1"

	// Save a couple of versions.
	for i := 0; i < 3; i++ {
		_, err := cp.Save(ctx, threadID, graph.Checkpoint{NodeName: "node"})
		if err != nil {
			t.Fatalf("Save failed: %v", err)
		}
	}

	// Delete.
	if err := cp.Delete(ctx, threadID); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Load should return not found.
	_, err := cp.Load(ctx, threadID)
	if err != graph.ErrCheckpointNotFound {
		t.Fatalf("expected ErrCheckpointNotFound after delete, got: %v", err)
	}

	// List should not contain the thread.
	listed, err := cp.List(ctx)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	for _, id := range listed {
		if id == threadID {
			t.Fatal("deleted thread still appears in List")
		}
	}
}

func TestCheckpointer_VersionIncrement(t *testing.T) {
	prefix := "test-cp-version:"
	cp := newTestCheckpointer(t, prefix)
	ctx := context.Background()

	threadID := "thread-version-1"
	defer cp.Delete(ctx, threadID)

	for i := 1; i <= 5; i++ {
		saved, err := cp.Save(ctx, threadID, graph.Checkpoint{NodeName: "step"})
		if err != nil {
			t.Fatalf("Save %d failed: %v", i, err)
		}
		if saved.Version != i {
			t.Fatalf("expected version %d, got %d", i, saved.Version)
		}
	}
}

func TestCheckpointer_TTL(t *testing.T) {
	addr := skipIfNoRedis(t)
	prefix := "test-cp-ttl:"
	ttl := 10 * time.Minute

	cp, err := New(Options{Addr: addr}, WithKeyPrefix(prefix), WithTTL(ttl))
	if err != nil {
		t.Fatalf("failed to create Checkpointer: %v", err)
	}
	defer cp.Close()

	ctx := context.Background()
	threadID := "thread-ttl-1"
	defer cp.Delete(ctx, threadID)

	_, err = cp.Save(ctx, threadID, graph.Checkpoint{NodeName: "node"})
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Check TTL on the version key.
	versionKey := cp.versionKey(threadID, 1)
	remaining := cp.client.TTL(ctx, versionKey).Val()
	if remaining <= 0 {
		t.Fatalf("expected positive TTL, got %v", remaining)
	}
	if remaining > ttl {
		t.Fatalf("TTL %v exceeds configured %v", remaining, ttl)
	}
}

func TestCheckpointer_DefaultKeyPrefix(t *testing.T) {
	addr := skipIfNoRedis(t)
	cp, err := New(Options{Addr: addr})
	if err != nil {
		t.Fatalf("failed to create Checkpointer: %v", err)
	}
	defer cp.Close()

	if cp.keyPrefix != "gude:checkpoint:" {
		t.Fatalf("expected default keyPrefix %q, got %q", "gude:checkpoint:", cp.keyPrefix)
	}
}

// contains checks if s contains substr.
func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
