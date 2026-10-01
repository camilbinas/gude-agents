package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/testutil"
	"pgregory.net/rapid"
)

func tempDB(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "test.db")
}

func textMessages(text string) []agent.Message {
	return []agent.Message{{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: text}}}}
}

func TestNew(t *testing.T) {
	if _, err := New(""); err == nil {
		t.Fatal("expected empty DSN error")
	}
	m, err := New(":memory:", WithTableName("custom_convos"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	// tableName is stored quoted (sanitized) for safe SQL interpolation.
	if m.tableName != `"custom_convos"` {
		t.Fatalf("table name = %q", m.tableName)
	}
}

func TestSaveLoadRevisionAndConflict(t *testing.T) {
	m, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	ctx := context.Background()

	missing, err := m.Load(ctx, "missing")
	if err != nil || missing.Messages == nil || len(missing.Messages) != 0 || missing.Revision != 0 {
		t.Fatalf("missing snapshot = %+v, %v", missing, err)
	}

	first := textMessages("first")
	rev, err := m.Save(ctx, "conv", first, 0)
	if err != nil || rev != 1 {
		t.Fatalf("first save = %d, %v", rev, err)
	}
	second := textMessages("second")
	rev, err = m.Save(ctx, "conv", second, rev)
	if err != nil || rev != 2 {
		t.Fatalf("second save = %d, %v", rev, err)
	}
	if _, err := m.Save(ctx, "conv", textMessages("stale"), 1); !errors.Is(err, agent.ErrConversationConflict) {
		t.Fatalf("stale save error = %v", err)
	}

	snapshot, err := m.Load(ctx, "conv")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Revision != 2 || !reflect.DeepEqual(snapshot.Messages, second) {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestSaveMissingWithNonzeroRevisionConflicts(t *testing.T) {
	m, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	_, err = m.Save(context.Background(), "missing", textMessages("x"), 4)
	if !errors.Is(err, agent.ErrConversationConflict) {
		t.Fatalf("error = %v", err)
	}
}

func TestListDeleteAndToolBlocks(t *testing.T) {
	m, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	ctx := context.Background()
	toolMessages := []agent.Message{
		{Role: agent.RoleAssistant, Content: []agent.ContentBlock{
			agent.TextBlock{Text: "working"},
			agent.ToolUseBlock{ToolUseID: "tu-1", Name: "search", Input: []byte(`{"q":"test"}`)},
		}},
		{Role: agent.RoleUser, Content: []agent.ContentBlock{
			agent.ToolResultBlock{ToolUseID: "tu-1", Content: "found", IsError: false},
		}},
	}
	for _, id := range []string{"alpha", "beta"} {
		if _, err := m.Save(ctx, id, toolMessages, 0); err != nil {
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
		t.Fatalf("idempotent delete: %v", err)
	}
	snapshot, err := m.Load(ctx, "beta")
	if err != nil || !reflect.DeepEqual(snapshot.Messages, toolMessages) {
		t.Fatalf("remaining = %+v, %v", snapshot, err)
	}
}

func TestFilePersistenceIncludesRevision(t *testing.T) {
	path := tempDB(t)
	m, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	rev, err := m.Save(ctx, "conv", textMessages("persisted"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}

	m, err = New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	snapshot, err := m.Load(ctx, "conv")
	if err != nil || snapshot.Revision != rev || !reflect.DeepEqual(snapshot.Messages, textMessages("persisted")) {
		t.Fatalf("reopened snapshot = %+v, %v", snapshot, err)
	}
}

func TestConversationManagerCompatibility(t *testing.T) {
	var _ agent.ConversationManager = (*Conversation)(nil)
}

func genMessages(t *rapid.T) []agent.Message { return testutil.GenMessages(t, 10) }

func TestProperty_SaveLoadCASRoundTrip(t *testing.T) {
	m, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	rapid.Check(t, func(t *rapid.T) {
		id := rapid.StringMatching(`conv-[a-zA-Z0-9]{4,16}`).Draw(t, "id")
		messages := genMessages(t)
		rev, err := m.Save(context.Background(), id, messages, 0)
		if err != nil || rev != 1 {
			t.Fatalf("Save = %d, %v", rev, err)
		}
		snapshot, err := m.Load(context.Background(), id)
		if err != nil || snapshot.Revision != rev || !reflect.DeepEqual(snapshot.Messages, messages) {
			t.Fatalf("Load = %+v, %v", snapshot, err)
		}
		if err := m.Delete(context.Background(), id); err != nil {
			t.Fatal(err)
		}
	})
}

func TestProperty_ListCompleteness(t *testing.T) {
	m, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	rapid.Check(t, func(t *rapid.T) {
		ctx := context.Background()
		ids := map[string]bool{}
		for i := 0; i < rapid.IntRange(1, 10).Draw(t, "count"); i++ {
			id := rapid.StringMatching(`conv-[a-zA-Z0-9]{4,16}`).Draw(t, fmt.Sprintf("id_%d", i))
			ids[id] = true
			_, _ = m.Save(ctx, id, genMessages(t), 0)
		}
		listed, err := m.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range listed {
			delete(ids, id)
			_ = m.Delete(ctx, id)
		}
		if len(ids) != 0 {
			t.Fatalf("missing IDs: %v", ids)
		}
	})
}

func TestConcurrentCASOneWinner(t *testing.T) {
	path := tempDB(t)
	first, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	ctx := context.Background()
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, store := range []*Conversation{first, second} {
		wg.Add(1)
		go func(store *Conversation) {
			defer wg.Done()
			<-start
			_, err := store.Save(ctx, "race", textMessages("value"), 0)
			errs <- err
		}(store)
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
	path := tempDB(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE conversations (
		conversation_id TEXT PRIMARY KEY,
		messages TEXT NOT NULL,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatal(err)
	}
	legacyJSON := `[{"role":"user","content":[{"type":"text","text":"legacy"}]}]`
	if _, err := db.Exec(`INSERT INTO conversations (conversation_id, messages) VALUES (?, ?)`, "legacy", legacyJSON); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	m, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	snapshot, err := m.Load(context.Background(), "legacy")
	if err != nil || snapshot.Revision != 0 || len(snapshot.Messages) != 1 {
		t.Fatalf("legacy snapshot = %+v, %v", snapshot, err)
	}
	revision, err := m.Save(context.Background(), "legacy", textMessages("upgraded"), 0)
	if err != nil || revision != 1 {
		t.Fatalf("upgrade Save = %d, %v", revision, err)
	}
	if _, err := m.Save(context.Background(), "legacy", textMessages("stale"), 0); !errors.Is(err, agent.ErrConversationConflict) {
		t.Fatalf("second revision-zero Save = %v", err)
	}
}

// ---------------------------------------------------------------------------
// Identifier hardening regression tests
// ---------------------------------------------------------------------------

func TestNew_RejectsNULByteTableName(t *testing.T) {
	_, err := New(":memory:", WithTableName("evil\x00name"))
	if err == nil {
		t.Fatal("expected error for NUL byte in table name")
	}
}

func TestNew_RejectsEmptyTableName(t *testing.T) {
	_, err := New(":memory:", WithTableName(""))
	if err == nil {
		t.Fatal("expected error for empty table name")
	}
}

// TestNew_TableNameWithEmbeddedQuoteIsSafe verifies that a table name
// containing a double quote and SQL-fragment-shaped content does not break
// out of the identifier position: the store still creates exactly one table
// (the quoted, literally-named one) and behaves normally against it.
func TestNew_TableNameWithEmbeddedQuoteIsSafe(t *testing.T) {
	maliciousName := `evil"; DROP TABLE sqlite_master; --`
	m, err := New(":memory:", WithTableName(maliciousName))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer m.Close()

	ctx := context.Background()
	if _, err := m.Save(ctx, "conv", textMessages("hello"), 0); err != nil {
		t.Fatalf("Save: %v", err)
	}
	snapshot, err := m.Load(ctx, "conv")
	if err != nil || !reflect.DeepEqual(snapshot.Messages, textMessages("hello")) {
		t.Fatalf("Load = %+v, %v", snapshot, err)
	}

	// The literal table (with the embedded quote in its name) must exist.
	var tableCount int
	row := m.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, maliciousName)
	if err := row.Scan(&tableCount); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if tableCount != 1 {
		t.Fatalf("expected exactly one table named %q, got count=%d", maliciousName, tableCount)
	}
}

// TestNew_TableNameWithSpaceAndReservedWord verifies table names containing
// spaces or SQL reserved words are safely quoted and usable.
func TestNew_TableNameWithSpaceAndReservedWord(t *testing.T) {
	for _, name := range []string{"my conversations", "select", "table", "order"} {
		t.Run(name, func(t *testing.T) {
			m, err := New(":memory:", WithTableName(name))
			if err != nil {
				t.Fatalf("New(%q): %v", name, err)
			}
			defer m.Close()

			ctx := context.Background()
			if _, err := m.Save(ctx, "conv", textMessages("x"), 0); err != nil {
				t.Fatalf("Save: %v", err)
			}
			if _, err := m.Load(ctx, "conv"); err != nil {
				t.Fatalf("Load: %v", err)
			}
		})
	}
}

func TestSqliteIdentifier_DoublesEmbeddedQuotes(t *testing.T) {
	got := sqliteIdentifier(`ta"ble`)
	want := `"ta""ble"`
	if got != want {
		t.Errorf("sqliteIdentifier(%q) = %q, want %q", `ta"ble`, got, want)
	}
}
