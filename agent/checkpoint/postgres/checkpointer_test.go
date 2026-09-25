package postgres

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/camilbinas/gude-agents/agent/checkpoint"
	"github.com/jackc/pgx/v5/pgxpool"
)

const testTable = "checkpoints_test"

const testDDL = `
CREATE TABLE IF NOT EXISTS %s (
    thread_id   TEXT NOT NULL,
    version     INTEGER NOT NULL,
    label       TEXT NOT NULL DEFAULT '',
    state       JSONB NOT NULL,
    usage       JSONB NOT NULL,
    extra       JSONB,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (thread_id, version)
);`

// newTestStore connects to POSTGRES_URL, creates the table, and truncates it so
// each subtest starts clean.
func newTestStore(t *testing.T) *Checkpointer {
	t.Helper()

	url := os.Getenv("POSTGRES_URL")
	if url == "" {
		t.Skip("POSTGRES_URL not set, skipping postgres integration test")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("postgres unreachable at POSTGRES_URL: %v", err)
	}

	if _, err := pool.Exec(ctx, fmt.Sprintf(testDDL, testTable)); err != nil {
		pool.Close()
		t.Fatalf("create table: %v", err)
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf("TRUNCATE %s", testTable)); err != nil {
		pool.Close()
		t.Fatalf("truncate: %v", err)
	}

	c, err := New(pool, WithTableName(testTable))
	if err != nil {
		pool.Close()
		t.Fatalf("new: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return c
}

func TestConformance_Postgres(t *testing.T) {
	// One shared store; the contract uses distinct thread IDs per subtest.
	store := newTestStore(t)
	checkpoint.RunConformance(t, func(t *testing.T) checkpoint.Checkpointer {
		return store
	})
}

func TestNew_RequiresPool(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Error("expected error for nil pool, got nil")
	}
}

func TestNew_RejectsInvalidTableName(t *testing.T) {
	// A NUL byte cannot be safely interpolated as an identifier.
	if _, err := New(&pgxpool.Pool{}, WithTableName("bad\x00name")); err == nil {
		t.Error("expected error for table name containing NUL, got nil")
	}
}

func TestWithTableName_IgnoresEmpty(t *testing.T) {
	cfg := defaultConfig()
	WithTableName("")(cfg)
	if cfg.tableName != "checkpoints" {
		t.Errorf("tableName = %q, want checkpoints (empty override should be ignored)", cfg.tableName)
	}
}

// State must land in a JSONB column so it stays queryable from SQL — that is the
// reason this backend decomposes State instead of storing one opaque blob.
func TestState_IsQueryableAsJSONB(t *testing.T) {
	c := newTestStore(t)
	ctx := context.Background()

	if _, err := c.Save(ctx, "sql-query", checkpoint.Checkpoint{
		State: checkpoint.State{"stage": "review", "count": 3},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	var threadID string
	err := c.pool.QueryRow(ctx,
		fmt.Sprintf(`SELECT thread_id FROM %s WHERE state->>'stage' = $1`, testTable),
		"review",
	).Scan(&threadID)
	if err != nil {
		t.Fatalf("jsonb query: %v", err)
	}
	if threadID != "sql-query" {
		t.Errorf("thread_id = %q, want sql-query", threadID)
	}
}

// Extra must survive a round trip without being decomposed into known fields.
func TestExtra_SurvivesRoundTrip(t *testing.T) {
	c := newTestStore(t)
	ctx := context.Background()

	raw := []byte(`{"completed":{"a":true},"readiness":{"out_a":true,"out_b":false},"iterations":9}`)
	if _, err := c.Save(ctx, "extra-verbatim", checkpoint.Checkpoint{Extra: raw}); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := c.Load(ctx, "extra-verbatim")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded.Extra) == 0 {
		t.Fatal("Extra came back empty — payload was dropped")
	}

	// Compare semantically; Postgres JSONB does not preserve key order.
	var got, want map[string]any
	if err := jsonUnmarshal(loaded.Extra, &got); err != nil {
		t.Fatalf("unmarshal got: %v", err)
	}
	if err := jsonUnmarshal(raw, &want); err != nil {
		t.Fatalf("unmarshal want: %v", err)
	}

	gotReadiness, _ := got["readiness"].(map[string]any)
	if gotReadiness == nil {
		t.Fatal("readiness missing from Extra")
	}
	if v, ok := gotReadiness["out_b"]; !ok {
		t.Error("readiness.out_b missing — a false-valued entry was dropped")
	} else if v != false {
		t.Errorf("readiness.out_b = %v, want false", v)
	}
	if got["iterations"] != want["iterations"] {
		t.Errorf("iterations = %v, want %v", got["iterations"], want["iterations"])
	}
}
