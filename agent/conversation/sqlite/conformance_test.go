package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation/internal/conformance"
	"github.com/camilbinas/gude-agents/agent/conversation/sqlite"
)

func TestAppendConformanceSQLite(t *testing.T) {
	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	conformance.RunAppend(t, s)
}
func TestContextStateConformanceSQLite(t *testing.T) {
	store, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	conformance.RunContextState(t, store)
}

// A successful Append is committed before it returns: an independent handle on
// the same file-backed store observes the messages and the returned cursor
// immediately, without any flush step.
func TestAppendIsVisibleToIndependentSQLiteHandle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversations.db")
	writer, err := sqlite.New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	reader, err := sqlite.New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	ctx := context.Background()
	cursor, err := writer.Append(ctx, "c", []agent.Message{conformance.Msg("one"), conformance.Msg("two")}, 0)
	if err != nil || cursor != (agent.ConversationCursor{Revision: 1, LastSequence: 2}) {
		t.Fatalf("Append = %+v, %v", cursor, err)
	}
	seen, err := reader.Load(ctx, "c")
	if err != nil || len(seen.Messages) != 2 || seen.Revision != cursor.Revision || seen.LastSequence != cursor.LastSequence {
		t.Fatalf("independent handle observed %+v, %v; want the committed cursor %+v", seen, err, cursor)
	}
	// The revision CAS holds across handles as well.
	if _, err := reader.Append(ctx, "c", []agent.Message{conformance.Msg("stale")}, 0); !errors.Is(err, agent.ErrConversationConflict) {
		t.Fatalf("stale Append from second handle = %v, want ErrConversationConflict", err)
	}
	if _, err := reader.Append(ctx, "c", []agent.Message{conformance.Msg("three")}, cursor.Revision); err != nil {
		t.Fatalf("Append with current revision: %v", err)
	}
	final, err := writer.Load(ctx, "c")
	if err != nil || len(final.Messages) != 3 || final.Revision != 2 {
		t.Fatalf("writer observed %+v, %v", final, err)
	}
}
