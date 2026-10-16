// Package conformance holds the behavior every agent.ConversationManager
// implementation must share. Backends run it from their own tests.
package conformance

import (
	"context"
	"errors"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
)

// Msg builds a user message.
func Msg(text string) agent.Message {
	return agent.Message{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: text}}}
}

// RunAppend verifies Load/Append/List/Delete revision and sequence semantics.
func RunAppend(t *testing.T, store agent.ConversationManager) {
	t.Helper()
	ctx := context.Background()
	id := "conversation"
	missing, err := store.Load(ctx, id)
	if err != nil || missing.Revision != 0 || missing.LastSequence != 0 || len(missing.Messages) != 0 {
		t.Fatalf("missing=%+v err=%v", missing, err)
	}
	cursor, err := store.Append(ctx, id, []agent.Message{Msg("one"), Msg("two"), Msg("three")}, 0)
	if err != nil || cursor != (agent.ConversationCursor{Revision: 1, LastSequence: 3}) {
		t.Fatalf("first Append=%+v,%v", cursor, err)
	}
	if _, err := store.Append(ctx, id, []agent.Message{Msg("stale")}, 0); !errors.Is(err, agent.ErrConversationConflict) {
		t.Fatalf("stale Append=%v", err)
	}
	all, err := store.Load(ctx, id)
	if err != nil || len(all.Messages) != 3 || all.Revision != 1 || all.LastSequence != 3 {
		t.Fatalf("Load=%+v,%v", all, err)
	}
	tail, err := store.LoadAfter(ctx, id, 2)
	if err != nil || len(tail.Messages) != 1 || tail.Messages[0].Content[0].(agent.TextBlock).Text != "three" || tail.Revision != 1 || tail.LastSequence != 3 {
		t.Fatalf("LoadAfter=%+v,%v", tail, err)
	}
	empty, err := store.LoadAfter(ctx, id, 99)
	if err != nil || len(empty.Messages) != 0 || empty.Revision != 1 || empty.LastSequence != 3 {
		t.Fatalf("LoadAfter end=%+v,%v", empty, err)
	}
	nop, err := store.Append(ctx, id, nil, 1)
	if err != nil || nop != (agent.ConversationCursor{Revision: 1, LastSequence: 3}) {
		t.Fatalf("empty Append=%+v,%v", nop, err)
	}
	stateStore, ok := store.(agent.ContextStateStore)
	if !ok {
		t.Fatalf("%T lacks ContextStateStore", store)
	}
	stateRev, err := stateStore.SaveContextState(ctx, id, "test", []byte(`{"v":1}`), 0)
	if err != nil || stateRev != 1 {
		t.Fatalf("SaveContextState=%d,%v", stateRev, err)
	}
	unchanged, _ := store.Load(ctx, id)
	if unchanged.Revision != 1 || unchanged.LastSequence != 3 {
		t.Fatalf("state changed cursor: %+v", unchanged)
	}
	if _, err := stateStore.SaveContextState(ctx, id, "test", []byte(`{"v":2}`), 0); !errors.Is(err, agent.ErrContextStateConflict) {
		t.Fatalf("stale state=%v", err)
	}
	if err := store.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	afterDelete, _ := store.Load(ctx, id)
	if afterDelete.Revision != 0 || afterDelete.LastSequence != 0 || len(afterDelete.Messages) != 0 {
		t.Fatalf("deleted=%+v", afterDelete)
	}
	if _, err := stateStore.LoadContextState(ctx, id, "test"); err != nil {
		t.Fatal(err)
	}
}

// RunContextState verifies the agent.ContextStateStore contract.
func RunContextState(t *testing.T, store agent.ConversationManager) {
	t.Helper()
	stateStore, ok := store.(agent.ContextStateStore)
	if !ok {
		t.Fatalf("%T lacks ContextStateStore", store)
	}
	ctx := context.Background()
	if _, err := stateStore.SaveContextState(ctx, "state-only", "one", []byte(`{"v":1}`), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := stateStore.SaveContextState(ctx, "state-only", "two", []byte(`{"v":2}`), 0); err != nil {
		t.Fatal(err)
	}
	one, err := stateStore.LoadContextState(ctx, "state-only", "one")
	if err != nil || string(one.Data) != `{"v":1}` || one.Revision != 1 {
		t.Fatalf("first namespace=%+v err=%v", one, err)
	}
	if _, err := stateStore.SaveContextState(ctx, "state-only", "one", []byte(`not json`), 1); err == nil {
		t.Fatal("invalid context state JSON was accepted")
	}
	listed, err := store.List(ctx)
	if err != nil || len(listed) != 0 {
		t.Fatalf("state-only conversation listed=%v err=%v", listed, err)
	}
	if err := store.Delete(ctx, "state-only"); err != nil {
		t.Fatal(err)
	}
	deleted, err := stateStore.LoadContextState(ctx, "state-only", "one")
	if err != nil || deleted.Revision != 0 || len(deleted.Data) != 0 {
		t.Fatalf("deleted state=%+v err=%v", deleted, err)
	}
}
