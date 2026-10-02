package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestNewMigratesLegacySnapshotIntoEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE conversations (conversation_id TEXT PRIMARY KEY, messages TEXT NOT NULL, revision INTEGER NOT NULL DEFAULT 0, updated_at DATETIME)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO conversations(conversation_id,messages,revision) VALUES('c','[{"role":"user","content":[{"type":"text","text":"legacy"}]}]',4)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	store, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	snapshot, err := store.Load(context.Background(), "c")
	if err != nil || snapshot.Revision != 4 || snapshot.LastSequence != 1 || len(snapshot.Messages) != 1 {
		t.Fatalf("snapshot=%+v,%v", snapshot, err)
	}
}

func TestAppendWritesRowsNotLegacyBlob(t *testing.T) {
	store, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.Append(context.Background(), "c", nil, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO "conversations"(conversation_id,revision,last_sequence,messages) VALUES('x',0,0,'[]')`); err != nil {
		t.Fatal(err)
	}
}
