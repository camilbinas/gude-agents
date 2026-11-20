package postgres

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent/conversation/internal/conformance"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Without POSTGRES_URL (or when the server is unreachable) the test is skipped,
// like the other PostgreSQL store tests.
func TestSnapshotConsistency(t *testing.T) {
	url := os.Getenv("POSTGRES_URL")
	if url == "" {
		t.Skip("POSTGRES_URL not set, skipping postgres integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		t.Skipf("postgres unreachable at POSTGRES_URL: %v", err)
	}
	// The caller owns the metadata table; the store migrates the rest. Use
	// unique tables so the test never touches application data.
	table := fmt.Sprintf("gude_snapshot_%d", time.Now().UnixNano())
	if _, err := pool.Exec(ctx, fmt.Sprintf(`CREATE TABLE %s (conversation_id TEXT PRIMARY KEY, messages JSONB NOT NULL DEFAULT '[]'::jsonb, updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`, table)); err != nil {
		t.Fatalf("create table: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), fmt.Sprintf(`DROP TABLE IF EXISTS %s_messages, %s`, table, table))
	})
	store, err := New(pool, WithTableName(table))
	if err != nil {
		t.Fatal(err)
	}
	conformance.RunSnapshotConsistency(t, store)
}
