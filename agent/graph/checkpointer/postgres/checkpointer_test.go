//go:build integration

package postgres

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent/graph"
	"github.com/jackc/pgx/v5/pgxpool"
)

// skipIfNoPostgres skips the test if POSTGRES_URL is not set and returns a pool.
func skipIfNoPostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("POSTGRES_URL")
	if url == "" {
		t.Skip("POSTGRES_URL not set, skipping postgres integration test")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("failed to connect to postgres: %v", err)
	}
	return pool
}

// uniqueTable returns a unique table name for test isolation.
func uniqueTable(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("graph_checkpoints_test_%d", os.Getpid())
}

// newTestCheckpointer creates a Checkpointer with a unique table and registers cleanup.
func newTestCheckpointer(t *testing.T) *Checkpointer {
	t.Helper()
	pool := skipIfNoPostgres(t)
	table := uniqueTable(t)

	// Create the table manually.
	ddl := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			thread_id   TEXT NOT NULL,
			version     INTEGER NOT NULL,
			node_name   TEXT NOT NULL,
			state       JSONB NOT NULL,
			metadata    JSONB NOT NULL,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (thread_id, version)
		)
	`, table)
	if _, err := pool.Exec(context.Background(), ddl); err != nil {
		t.Fatalf("create table: %v", err)
	}

	cp, err := New(pool, WithTableName(table))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	t.Cleanup(func() {
		pool.Exec(context.Background(), fmt.Sprintf("DROP TABLE IF EXISTS %s", table))
		cp.Close()
	})

	return cp
}

func TestNew_NilPool(t *testing.T) {
	_, err := New(nil)
	if err == nil {
		t.Fatal("expected error for nil pool")
	}
}

func TestSaveAndLoad(t *testing.T) {
	cp := newTestCheckpointer(t)
	ctx := context.Background()

	checkpoint := graph.Checkpoint{
		State:      graph.State{"key": "value", "count": float64(42)},
		Completed:  map[string]bool{"node_a": true},
		Iterations: 1,
		NodeName:   "node_a",
		Timestamp:  time.Now().Truncate(time.Microsecond),
	}

	saved, err := cp.Save(ctx, "thread-1", checkpoint)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if saved.Version != 1 {
		t.Fatalf("expected version 1, got %d", saved.Version)
	}
	if saved.ThreadID != "thread-1" {
		t.Fatalf("expected thread_id 'thread-1', got %q", saved.ThreadID)
	}

	loaded, err := cp.Load(ctx, "thread-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Version != 1 {
		t.Fatalf("expected version 1, got %d", loaded.Version)
	}
	if loaded.NodeName != "node_a" {
		t.Fatalf("expected node_name 'node_a', got %q", loaded.NodeName)
	}
	if loaded.State["key"] != "value" {
		t.Fatalf("expected state key 'value', got %v", loaded.State["key"])
	}
	if !loaded.Completed["node_a"] {
		t.Fatal("expected completed to contain 'node_a'")
	}
}

func TestLoad_NotFound(t *testing.T) {
	cp := newTestCheckpointer(t)

	_, err := cp.Load(context.Background(), "nonexistent")
	if err != graph.ErrCheckpointNotFound {
		t.Fatalf("expected ErrCheckpointNotFound, got %v", err)
	}
}

func TestLoadAt(t *testing.T) {
	cp := newTestCheckpointer(t)
	ctx := context.Background()

	// Save multiple versions.
	for i := 0; i < 3; i++ {
		_, err := cp.Save(ctx, "thread-1", graph.Checkpoint{
			State:     graph.State{"step": float64(i)},
			NodeName:  fmt.Sprintf("node_%d", i),
			Timestamp: time.Now().Truncate(time.Microsecond),
		})
		if err != nil {
			t.Fatalf("Save %d: %v", i, err)
		}
	}

	// Load version 2.
	loaded, err := cp.LoadAt(ctx, "thread-1", 2)
	if err != nil {
		t.Fatalf("LoadAt: %v", err)
	}
	if loaded.Version != 2 {
		t.Fatalf("expected version 2, got %d", loaded.Version)
	}
	if loaded.NodeName != "node_1" {
		t.Fatalf("expected node_name 'node_1', got %q", loaded.NodeName)
	}
}

func TestLoadAt_NotFound(t *testing.T) {
	cp := newTestCheckpointer(t)

	_, err := cp.LoadAt(context.Background(), "thread-1", 99)
	if err != graph.ErrCheckpointNotFound {
		t.Fatalf("expected ErrCheckpointNotFound, got %v", err)
	}
}

func TestHistory(t *testing.T) {
	cp := newTestCheckpointer(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		_, err := cp.Save(ctx, "thread-1", graph.Checkpoint{
			NodeName:  fmt.Sprintf("node_%d", i),
			Timestamp: time.Now().Truncate(time.Microsecond),
		})
		if err != nil {
			t.Fatalf("Save %d: %v", i, err)
		}
	}

	metas, err := cp.History(ctx, "thread-1")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(metas) != 5 {
		t.Fatalf("expected 5 history entries, got %d", len(metas))
	}

	// Verify ordering (ascending).
	for i, m := range metas {
		expectedVersion := i + 1
		if m.Version != expectedVersion {
			t.Errorf("entry %d: expected version %d, got %d", i, expectedVersion, m.Version)
		}
		expectedNode := fmt.Sprintf("node_%d", i)
		if m.NodeName != expectedNode {
			t.Errorf("entry %d: expected node %q, got %q", i, expectedNode, m.NodeName)
		}
	}
}

func TestList(t *testing.T) {
	cp := newTestCheckpointer(t)
	ctx := context.Background()

	threads := []string{"thread-a", "thread-b", "thread-c"}
	for _, tid := range threads {
		_, err := cp.Save(ctx, tid, graph.Checkpoint{
			NodeName:  "node_0",
			Timestamp: time.Now().Truncate(time.Microsecond),
		})
		if err != nil {
			t.Fatalf("Save %s: %v", tid, err)
		}
	}

	ids, err := cp.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	idSet := make(map[string]bool)
	for _, id := range ids {
		idSet[id] = true
	}
	for _, tid := range threads {
		if !idSet[tid] {
			t.Errorf("expected thread %q in list, not found", tid)
		}
	}
}

func TestDelete(t *testing.T) {
	cp := newTestCheckpointer(t)
	ctx := context.Background()

	// Save multiple versions.
	for i := 0; i < 3; i++ {
		_, err := cp.Save(ctx, "thread-del", graph.Checkpoint{
			NodeName:  fmt.Sprintf("node_%d", i),
			Timestamp: time.Now().Truncate(time.Microsecond),
		})
		if err != nil {
			t.Fatalf("Save %d: %v", i, err)
		}
	}

	// Delete all.
	if err := cp.Delete(ctx, "thread-del"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Verify Load returns not found.
	_, err := cp.Load(ctx, "thread-del")
	if err != graph.ErrCheckpointNotFound {
		t.Fatalf("expected ErrCheckpointNotFound after delete, got %v", err)
	}

	// Verify List does not include deleted thread.
	ids, err := cp.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, id := range ids {
		if id == "thread-del" {
			t.Fatal("deleted thread still appears in List")
		}
	}
}

func TestAppendOnly(t *testing.T) {
	cp := newTestCheckpointer(t)
	ctx := context.Background()

	// Save multiple checkpoints and verify each gets a new version (no updates).
	for i := 0; i < 3; i++ {
		saved, err := cp.Save(ctx, "thread-append", graph.Checkpoint{
			State:     graph.State{"step": float64(i)},
			NodeName:  fmt.Sprintf("node_%d", i),
			Timestamp: time.Now().Truncate(time.Microsecond),
		})
		if err != nil {
			t.Fatalf("Save %d: %v", i, err)
		}
		expectedVersion := i + 1
		if saved.Version != expectedVersion {
			t.Fatalf("save %d: expected version %d, got %d", i, expectedVersion, saved.Version)
		}
	}

	// Verify all versions are accessible (append-only, no overwrites).
	for i := 1; i <= 3; i++ {
		loaded, err := cp.LoadAt(ctx, "thread-append", i)
		if err != nil {
			t.Fatalf("LoadAt version %d: %v", i, err)
		}
		if loaded.Version != i {
			t.Fatalf("expected version %d, got %d", i, loaded.Version)
		}
	}

	// Verify history shows all 3 entries.
	metas, err := cp.History(ctx, "thread-append")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(metas) != 3 {
		t.Fatalf("expected 3 history entries, got %d", len(metas))
	}
}

func TestLoadReturnsLatest(t *testing.T) {
	cp := newTestCheckpointer(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		_, err := cp.Save(ctx, "thread-1", graph.Checkpoint{
			NodeName:  fmt.Sprintf("node_%d", i),
			Timestamp: time.Now().Truncate(time.Microsecond),
		})
		if err != nil {
			t.Fatalf("Save %d: %v", i, err)
		}
	}

	loaded, err := cp.Load(ctx, "thread-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Version != 5 {
		t.Fatalf("expected latest version 5, got %d", loaded.Version)
	}
	if loaded.NodeName != "node_4" {
		t.Fatalf("expected node_name 'node_4', got %q", loaded.NodeName)
	}
}
