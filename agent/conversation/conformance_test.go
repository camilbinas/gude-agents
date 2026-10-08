package conversation_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	conv "github.com/camilbinas/gude-agents/agent/conversation"
	"github.com/camilbinas/gude-agents/agent/conversation/sqlite"
)

func msg(text string) agent.Message {
	return agent.Message{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: text}}}
}

func runConformance(t *testing.T, store agent.ConversationManager) {
	t.Helper()
	ctx := context.Background()
	id := "conversation"
	missing, err := store.Load(ctx, id)
	if err != nil || missing.Revision != 0 || missing.LastSequence != 0 || len(missing.Messages) != 0 {
		t.Fatalf("missing=%+v err=%v", missing, err)
	}
	cursor, err := store.Append(ctx, id, []agent.Message{msg("one"), msg("two"), msg("three")}, 0)
	if err != nil || cursor != (agent.ConversationCursor{Revision: 1, LastSequence: 3}) {
		t.Fatalf("first Append=%+v,%v", cursor, err)
	}
	if _, err := store.Append(ctx, id, []agent.Message{msg("stale")}, 0); !errors.Is(err, agent.ErrConversationConflict) {
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

func TestAppendConformanceInMemory(t *testing.T) { runConformance(t, conv.NewInMemory()) }
func TestAppendConformanceSQLite(t *testing.T) {
	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	runConformance(t, s)
}
func TestAppendConcurrentCASOneWinner(t *testing.T) {
	s := conv.NewInMemory()
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := s.Append(context.Background(), "race", []agent.Message{msg(fmt.Sprint(i))}, 0)
			errs <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	wins := 0
	for err := range errs {
		if err == nil {
			wins++
		} else if !errors.Is(err, agent.ErrConversationConflict) {
			t.Fatal(err)
		}
	}
	if wins != 1 {
		t.Fatalf("wins=%d", wins)
	}
}

func runContextStateConformance(t *testing.T, store agent.ConversationManager) {
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

func TestContextStateConformanceInMemory(t *testing.T) {
	runContextStateConformance(t, conv.NewInMemory())
}
func TestContextStateConformanceSQLite(t *testing.T) {
	store, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	runContextStateConformance(t, store)
}
