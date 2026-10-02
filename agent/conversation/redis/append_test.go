package redis

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/camilbinas/gude-agents/agent"
)

func TestAppendRangeAndContextState(t *testing.T) {
	mr := miniredis.RunT(t)
	store, err := New(Options{Addr: mr.Addr()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	messages := []agent.Message{{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "one"}}}, {Role: agent.RoleAssistant, Content: []agent.ContentBlock{agent.TextBlock{Text: "two"}}}}
	cursor, err := store.Append(ctx, "c", messages, 0)
	if err != nil || cursor.Revision != 1 || cursor.LastSequence != 2 {
		t.Fatalf("Append=%+v,%v", cursor, err)
	}
	tail, err := store.LoadAfter(ctx, "c", 1)
	if err != nil || len(tail.Messages) != 1 || tail.LastSequence != 2 {
		t.Fatalf("LoadAfter=%+v,%v", tail, err)
	}
	if _, err := store.SaveContextState(ctx, "c", "summary", []byte(`{"covered_through":1}`), 0); err != nil {
		t.Fatal(err)
	}
	all, _ := store.Load(ctx, "c")
	if all.Revision != 1 || all.LastSequence != 2 {
		t.Fatalf("state changed canonical cursor: %+v", all)
	}
	if err := store.Delete(ctx, "c"); err != nil {
		t.Fatal(err)
	}
	deleted, _ := store.Load(ctx, "c")
	if deleted.Revision != 0 || len(deleted.Messages) != 0 {
		t.Fatalf("deleted=%+v", deleted)
	}
}

func TestAppendKeysShareOneClusterSlot(t *testing.T) {
	store := &Conversation{keyPrefix: "gude:", ttl: time.Minute}
	if store.metaKey("id{unsafe}")[:11] != "gude:conv:{" {
		t.Fatalf("unexpected key %q", store.metaKey("id{unsafe}"))
	}
	if store.keyBase("a") != store.keyBase("a") {
		t.Fatal("unstable key")
	}
}
