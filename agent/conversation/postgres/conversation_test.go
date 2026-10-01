package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func skipIfNoPostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("POSTGRES_URL")
	if url == "" {
		t.Skip("POSTGRES_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	return pool
}

func newTestMemory(t *testing.T) *Conversation {
	t.Helper()
	pool := skipIfNoPostgres(t)
	table := fmt.Sprintf("conversation_test_%d", time.Now().UnixNano())
	ddl := fmt.Sprintf(`CREATE TABLE %s (
		conversation_id TEXT PRIMARY KEY,
		messages JSONB NOT NULL,
		revision BIGINT NOT NULL,
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`, table)
	if _, err := pool.Exec(context.Background(), ddl); err != nil {
		t.Fatal(err)
	}
	m, err := New(pool, WithTableName(table))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), fmt.Sprintf("DROP TABLE IF EXISTS %s", table))
		m.Close()
	})
	return m
}

func pgMessages(text string) []agent.Message {
	return []agent.Message{{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: text}}}}
}

func TestNewValidation(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("expected nil pool error")
	}
	pool := skipIfNoPostgres(t)
	defer pool.Close()
	if _, err := New(pool, WithTableName("bad\x00name")); err == nil {
		t.Fatal("expected invalid identifier error")
	}
}

func TestSaveLoadRevisionAndConflict(t *testing.T) {
	m := newTestMemory(t)
	ctx := context.Background()
	missing, err := m.Load(ctx, "missing")
	if err != nil || missing.Messages == nil || missing.Revision != 0 {
		t.Fatalf("missing = %+v, %v", missing, err)
	}
	rev, err := m.Save(ctx, "conv", pgMessages("one"), 0)
	if err != nil || rev != 1 {
		t.Fatalf("first Save = %d, %v", rev, err)
	}
	rev, err = m.Save(ctx, "conv", pgMessages("two"), rev)
	if err != nil || rev != 2 {
		t.Fatalf("second Save = %d, %v", rev, err)
	}
	if _, err := m.Save(ctx, "conv", pgMessages("stale"), 1); !errors.Is(err, agent.ErrConversationConflict) {
		t.Fatalf("stale Save = %v", err)
	}
	snapshot, err := m.Load(ctx, "conv")
	if err != nil || snapshot.Revision != 2 || !reflect.DeepEqual(snapshot.Messages, pgMessages("two")) {
		t.Fatalf("snapshot = %+v, %v", snapshot, err)
	}
}

func TestListAndDelete(t *testing.T) {
	m := newTestMemory(t)
	ctx := context.Background()
	for _, id := range []string{"alpha", "beta"} {
		if _, err := m.Save(ctx, id, pgMessages(id), 0); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := m.List(ctx)
	if err != nil || len(ids) != 2 {
		t.Fatalf("List = %v, %v", ids, err)
	}
	if err := m.Delete(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := m.Load(ctx, "alpha")
	if err != nil || snapshot.Revision != 0 {
		t.Fatalf("deleted = %+v, %v", snapshot, err)
	}
}

func TestCustomColumns(t *testing.T) {
	pool := skipIfNoPostgres(t)
	table := fmt.Sprintf("conversation_custom_%d", time.Now().UnixNano())
	_, err := pool.Exec(context.Background(), fmt.Sprintf(`CREATE TABLE %s (id TEXT PRIMARY KEY, data JSONB NOT NULL, version BIGINT NOT NULL, modified TIMESTAMPTZ NOT NULL DEFAULT NOW())`, table))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = pool.Exec(context.Background(), "DROP TABLE "+table); pool.Close() }()
	m, err := New(pool, WithTableName(table), WithColumns("id", "data", "modified"), WithRevisionColumn("version"))
	if err != nil {
		t.Fatal(err)
	}
	rev, err := m.Save(context.Background(), "x", pgMessages("x"), 0)
	if err != nil || rev != 1 {
		t.Fatalf("Save = %d, %v", rev, err)
	}
}

func TestConversationManagerCompatibility(t *testing.T) {
	var _ agent.ConversationManager = (*Conversation)(nil)
}

func TestConfigDefaults(t *testing.T) {
	cfg := defaultConfig()
	if cfg.colRevision != "revision" || !strings.Contains(cfg.tableName, "conversation") {
		t.Fatalf("config = %+v", cfg)
	}
	_ = pgx.Identifier{cfg.tableName}.Sanitize()
}

func TestConcurrentCASOneWinner(t *testing.T) {
	store := newTestMemory(t)
	ctx := context.Background()
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := store.Save(ctx, "race", pgMessages("value"), 0)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	var successes, conflicts int
	for err := range errs {
		if err == nil {
			successes++
		} else if errors.Is(err, agent.ErrConversationConflict) {
			conflicts++
		} else {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d, want 1 and 1", successes, conflicts)
	}
}

func TestNewMigratesLegacyTableAndRow(t *testing.T) {
	pool := skipIfNoPostgres(t)
	table := fmt.Sprintf("conversation_legacy_%d", time.Now().UnixNano())
	ctx := context.Background()
	if _, err := pool.Exec(ctx, fmt.Sprintf(`CREATE TABLE %s (
		conversation_id TEXT PRIMARY KEY,
		messages JSONB NOT NULL,
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`, table)); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pool.Exec(context.Background(), "DROP TABLE IF EXISTS "+table)
		pool.Close()
	}()
	legacyJSON := `[{"role":"user","content":[{"type":"text","text":"legacy"}]}]`
	if _, err := pool.Exec(ctx, "INSERT INTO "+table+" (conversation_id, messages) VALUES ($1, $2)", "legacy", legacyJSON); err != nil {
		t.Fatal(err)
	}
	m, err := New(pool, WithTableName(table))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := m.Load(ctx, "legacy")
	if err != nil || snapshot.Revision != 0 || len(snapshot.Messages) != 1 {
		t.Fatalf("legacy snapshot = %+v, %v", snapshot, err)
	}
	revision, err := m.Save(ctx, "legacy", pgMessages("upgraded"), 0)
	if err != nil || revision != 1 {
		t.Fatalf("upgrade Save = %d, %v", revision, err)
	}
	if _, err := m.Save(ctx, "legacy", pgMessages("stale"), 0); !errors.Is(err, agent.ErrConversationConflict) {
		t.Fatalf("second revision-zero Save = %v", err)
	}
}

// ---------------------------------------------------------------------------
// Identifier hardening regression tests
// ---------------------------------------------------------------------------

// TestNew_TableNameWithSpaceQuoteAndReservedWord verifies that table names
// containing quotes, spaces, and SQL reserved words are safely sanitized via
// pgx.Identifier.Sanitize and remain usable end to end.
func TestNew_TableNameWithSpaceQuoteAndReservedWord(t *testing.T) {
	for _, name := range []string{
		`weird"table`,
		"my conversations",
		"select",
		"order",
	} {
		t.Run(name, func(t *testing.T) {
			// Conversation.Close() closes the underlying pool, so use a
			// dedicated pool per subtest rather than the shared one, and
			// drop the table before closing (mirrors TestCustomColumns).
			subPool := skipIfNoPostgres(t)

			sanitized := pgx.Identifier{name}.Sanitize()
			ddl := fmt.Sprintf(`CREATE TABLE %s (
				conversation_id TEXT PRIMARY KEY,
				messages JSONB NOT NULL,
				revision BIGINT NOT NULL,
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			)`, sanitized)
			if _, err := subPool.Exec(context.Background(), ddl); err != nil {
				t.Fatalf("create table: %v", err)
			}
			defer func() {
				_, _ = subPool.Exec(context.Background(), fmt.Sprintf("DROP TABLE IF EXISTS %s", sanitized))
				subPool.Close()
			}()

			m, err := New(subPool, WithTableName(name))
			if err != nil {
				t.Fatalf("New(%q): %v", name, err)
			}

			ctx := context.Background()
			if _, err := m.Save(ctx, "conv", pgMessages("hello"), 0); err != nil {
				t.Fatalf("Save: %v", err)
			}
			snapshot, err := m.Load(ctx, "conv")
			if err != nil || !reflect.DeepEqual(snapshot.Messages, pgMessages("hello")) {
				t.Fatalf("Load = %+v, %v", snapshot, err)
			}
		})
	}
}

// TestNew_SanitizesTableNameForDDLInterpolation verifies that a
// SQL-fragment-shaped table name cannot break out of the identifier
// position in the ALTER TABLE migration issued by New.
func TestNew_SanitizesTableNameForDDLInterpolation(t *testing.T) {
	pool := skipIfNoPostgres(t)

	maliciousName := `evil"; DROP TABLE pg_catalog.pg_tables; --`
	sanitized := pgx.Identifier{maliciousName}.Sanitize()
	ddl := fmt.Sprintf(`CREATE TABLE %s (
		conversation_id TEXT PRIMARY KEY,
		messages JSONB NOT NULL,
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`, sanitized)
	if _, err := pool.Exec(context.Background(), ddl); err != nil {
		t.Fatalf("create table: %v", err)
	}
	defer func() {
		_, _ = pool.Exec(context.Background(), fmt.Sprintf("DROP TABLE IF EXISTS %s", sanitized))
		pool.Close()
	}()

	// New() runs an ALTER TABLE ... ADD COLUMN IF NOT EXISTS migration using
	// the sanitized table name. If sanitization were broken, this call would
	// either error out or execute injected SQL; here it must simply succeed
	// against the literal table.
	m, err := New(pool, WithTableName(maliciousName))
	if err != nil {
		t.Fatalf("New(%q): %v", maliciousName, err)
	}

	if _, err := m.Save(context.Background(), "conv", pgMessages("safe"), 0); err != nil {
		t.Fatalf("Save: %v", err)
	}
}
