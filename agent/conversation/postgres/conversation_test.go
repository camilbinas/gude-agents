package postgres

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
	"pgregory.net/rapid"
)

// skipIfNoPostgres skips the test if POSTGRES_URL is not set and returns a pool.
func skipIfNoPostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("POSTGRES_URL")
	if url == "" {
		t.Skip("POSTGRES_URL not set, skipping postgres test")
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
	// Use test name sanitized for SQL.
	name := fmt.Sprintf("test_%d", os.Getpid())
	return name
}

// newTestMemory creates a PostgresMemory with a unique table and registers cleanup.
func newTestMemory(t *testing.T) *PostgresConversation {
	t.Helper()
	pool := skipIfNoPostgres(t)
	table := uniqueTable(t)

	m, err := New(pool, WithTableName(table), WithAutoMigrate())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	t.Cleanup(func() {
		pool.Exec(context.Background(), fmt.Sprintf("DROP TABLE IF EXISTS %s", table))
		m.Close()
	})

	return m
}

func TestNew_NilPool(t *testing.T) {
	_, err := New(nil)
	if err == nil {
		t.Fatal("expected error for nil pool")
	}
}

func TestNew_CreatesTable(t *testing.T) {
	m := newTestMemory(t)
	if m == nil {
		t.Fatal("expected non-nil PostgresMemory")
	}
}

func TestNew_CustomTableName(t *testing.T) {
	pool := skipIfNoPostgres(t)
	table := fmt.Sprintf("custom_%d", os.Getpid())

	m, err := New(pool, WithTableName(table), WithAutoMigrate())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() {
		pool.Exec(context.Background(), fmt.Sprintf("DROP TABLE IF EXISTS %s", table))
		m.Close()
	}()

	if m.tableName != table {
		t.Fatalf("expected table name %q, got %q", table, m.tableName)
	}

	ctx := context.Background()
	msgs := []agent.Message{
		{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hello"}}},
	}
	if err := m.Save(ctx, "conv-1", msgs); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := m.Load(ctx, "conv-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 message, got %d", len(loaded))
	}
}

func TestSaveAndLoad(t *testing.T) {
	m := newTestMemory(t)
	ctx := context.Background()

	msgs := []agent.Message{
		{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hello"}}},
		{Role: agent.RoleAssistant, Content: []agent.ContentBlock{agent.TextBlock{Text: "hi there"}}},
	}

	if err := m.Save(ctx, "conv-1", msgs); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := m.Load(ctx, "conv-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(loaded))
	}
	if tb, ok := loaded[0].Content[0].(agent.TextBlock); !ok || tb.Text != "hello" {
		t.Errorf("expected 'hello', got %v", loaded[0].Content[0])
	}
	if tb, ok := loaded[1].Content[0].(agent.TextBlock); !ok || tb.Text != "hi there" {
		t.Errorf("expected 'hi there', got %v", loaded[1].Content[0])
	}
}

func TestLoad_NotFound(t *testing.T) {
	m := newTestMemory(t)

	msgs, err := m.Load(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if msgs == nil {
		t.Fatal("expected non-nil empty slice, got nil")
	}
	if len(msgs) != 0 {
		t.Errorf("expected empty slice, got %d messages", len(msgs))
	}
}

func TestList(t *testing.T) {
	m := newTestMemory(t)
	ctx := context.Background()

	msg := []agent.Message{
		{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "x"}}},
	}

	m.Save(ctx, "alpha", msg)
	m.Save(ctx, "beta", msg)
	m.Save(ctx, "gamma", msg)

	ids, err := m.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ids) != 3 {
		t.Fatalf("expected 3 conversations, got %d: %v", len(ids), ids)
	}
}

func TestDelete(t *testing.T) {
	m := newTestMemory(t)
	ctx := context.Background()

	msg := []agent.Message{
		{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "x"}}},
	}

	m.Save(ctx, "to-delete", msg)

	loaded, _ := m.Load(ctx, "to-delete")
	if len(loaded) == 0 {
		t.Fatal("expected message before delete")
	}

	if err := m.Delete(ctx, "to-delete"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	loaded, _ = m.Load(ctx, "to-delete")
	if len(loaded) != 0 {
		t.Errorf("expected empty after delete, got %d", len(loaded))
	}
}

func TestDelete_NotFound(t *testing.T) {
	m := newTestMemory(t)

	if err := m.Delete(context.Background(), "ghost"); err != nil {
		t.Fatalf("Delete nonexistent: %v", err)
	}
}

func TestSave_Overwrite(t *testing.T) {
	m := newTestMemory(t)
	ctx := context.Background()

	msgs1 := []agent.Message{
		{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "first"}}},
	}
	msgs2 := []agent.Message{
		{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "second"}}},
		{Role: agent.RoleAssistant, Content: []agent.ContentBlock{agent.TextBlock{Text: "reply"}}},
	}

	m.Save(ctx, "conv", msgs1)
	m.Save(ctx, "conv", msgs2)

	loaded, _ := m.Load(ctx, "conv")
	if len(loaded) != 2 {
		t.Fatalf("expected 2 messages after overwrite, got %d", len(loaded))
	}
	if tb, ok := loaded[0].Content[0].(agent.TextBlock); !ok || tb.Text != "second" {
		t.Errorf("expected 'second', got %v", loaded[0].Content[0])
	}
}

func TestToolBlocks(t *testing.T) {
	m := newTestMemory(t)
	ctx := context.Background()

	msgs := []agent.Message{
		{Role: agent.RoleAssistant, Content: []agent.ContentBlock{
			agent.TextBlock{Text: "Let me look that up."},
			agent.ToolUseBlock{ToolUseID: "tu-1", Name: "search", Input: []byte(`{"q":"test"}`)},
		}},
		{Role: agent.RoleUser, Content: []agent.ContentBlock{
			agent.ToolResultBlock{ToolUseID: "tu-1", Content: "found it", IsError: false},
		}},
	}

	m.Save(ctx, "tools", msgs)
	loaded, _ := m.Load(ctx, "tools")

	if len(loaded) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(loaded))
	}

	tu, ok := loaded[0].Content[1].(agent.ToolUseBlock)
	if !ok {
		t.Fatalf("expected ToolUseBlock, got %T", loaded[0].Content[1])
	}
	if tu.Name != "search" {
		t.Errorf("expected tool name 'search', got %q", tu.Name)
	}

	tr, ok := loaded[1].Content[0].(agent.ToolResultBlock)
	if !ok {
		t.Fatalf("expected ToolResultBlock, got %T", loaded[1].Content[0])
	}
	if tr.Content != "found it" {
		t.Errorf("expected 'found it', got %q", tr.Content)
	}
}

func TestDeleteLeavesOtherConversations(t *testing.T) {
	m := newTestMemory(t)
	ctx := context.Background()

	msgs := []agent.Message{
		{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hi"}}},
	}

	m.Save(ctx, "keep-me", msgs)
	m.Save(ctx, "delete-me", msgs)

	if err := m.Delete(ctx, "delete-me"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	remaining, err := m.Load(ctx, "keep-me")
	if err != nil {
		t.Fatalf("Load(keep-me): %v", err)
	}
	if !reflect.DeepEqual(msgs, remaining) {
		t.Fatalf("remaining conversation mismatch:\n  expected: %+v\n  got:      %+v", msgs, remaining)
	}

	listed, err := m.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, id := range listed {
		if id == "delete-me" {
			t.Fatal("deleted conversation still appears in List")
		}
	}
}

func TestWithMemoryOption(t *testing.T) {
	m := newTestMemory(t)

	opt := agent.WithConversation(m, "test-conv")
	if opt == nil {
		t.Fatal("expected non-nil option from WithConversation")
	}
}

// --- Property-Based Tests ---

func genMessages(t *rapid.T) []agent.Message { return testutil.GenMessages(t, 10) }

func TestProperty_SaveLoadRoundTrip(t *testing.T) {
	m := newTestMemory(t)

	rapid.Check(t, func(t *rapid.T) {
		messages := genMessages(t)
		convID := rapid.StringMatching(`conv-[a-zA-Z0-9]{4,16}`).Draw(t, "conversationID")

		ctx := context.Background()

		if err := m.Save(ctx, convID, messages); err != nil {
			t.Fatalf("Save failed: %v", err)
		}

		loaded, err := m.Load(ctx, convID)
		if err != nil {
			t.Fatalf("Load failed: %v", err)
		}

		if !reflect.DeepEqual(messages, loaded) {
			t.Fatalf("round-trip mismatch:\n  saved:  %+v\n  loaded: %+v", messages, loaded)
		}

		m.Delete(ctx, convID)
	})
}

func TestProperty_Overwrite(t *testing.T) {
	m := newTestMemory(t)

	rapid.Check(t, func(t *rapid.T) {
		convID := rapid.StringMatching(`conv-[a-zA-Z0-9]{4,16}`).Draw(t, "conversationID")
		msgs1 := genMessages(t)
		msgs2 := genMessages(t)

		ctx := context.Background()

		m.Save(ctx, convID, msgs1)
		m.Save(ctx, convID, msgs2)

		loaded, err := m.Load(ctx, convID)
		if err != nil {
			t.Fatalf("Load failed: %v", err)
		}

		if !reflect.DeepEqual(msgs2, loaded) {
			t.Fatalf("overwrite mismatch:\n  expected: %+v\n  got:      %+v", msgs2, loaded)
		}

		m.Delete(ctx, convID)
	})
}

func TestProperty_ListCompleteness(t *testing.T) {
	m := newTestMemory(t)

	rapid.Check(t, func(t *rapid.T) {
		ctx := context.Background()

		numConvs := rapid.IntRange(1, 10).Draw(t, "numConversations")
		ids := make(map[string]bool, numConvs)
		for i := 0; i < numConvs; i++ {
			id := rapid.StringMatching(`conv-[a-zA-Z0-9]{4,16}`).Draw(t, fmt.Sprintf("id_%d", i))
			ids[id] = true
			msgs := genMessages(t)
			m.Save(ctx, id, msgs)
		}

		listed, err := m.List(ctx)
		if err != nil {
			t.Fatalf("List failed: %v", err)
		}

		listedSet := make(map[string]bool, len(listed))
		for _, id := range listed {
			listedSet[id] = true
		}
		for id := range ids {
			if !listedSet[id] {
				t.Fatalf("saved conversation %q not found in List", id)
			}
		}

		for id := range ids {
			m.Delete(ctx, id)
		}
	})
}
