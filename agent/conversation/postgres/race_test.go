package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation/internal/conformance"
	"github.com/jackc/pgx/v5/pgxpool"
)

func newRaceStore(t *testing.T) *Conversation {
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
	t.Cleanup(pool.Close)
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		t.Skipf("postgres unreachable at POSTGRES_URL: %v", err)
	}
	table := fmt.Sprintf("gude_race_%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), fmt.Sprintf(`DROP TABLE IF EXISTS %s_messages, %s`, table, table))
	})
	// No pre-created table: New must create the metadata table itself.
	store, err := New(pool, WithTableName(table))
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestConcurrentAppendOnMissingRowConflicts(t *testing.T) {
	store := newRaceStore(t)
	ctx := context.Background()
	const writers = 8
	errs := make([]error, writers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, errs[i] = store.Append(ctx, "new", []agent.Message{conformance.Msg(fmt.Sprint(i))}, 0)
		}()
	}
	close(start)
	wg.Wait()
	wins := 0
	for _, err := range errs {
		switch {
		case err == nil:
			wins++
		case !errors.Is(err, agent.ErrConversationConflict):
			t.Fatalf("want ErrConversationConflict, got %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("wins=%d, want 1", wins)
	}
	snap, err := store.Load(ctx, "new")
	if err != nil || snap.Revision != 1 || snap.LastSequence != 1 {
		t.Fatalf("snapshot=%+v err=%v", snap, err)
	}
}

func TestConcurrentSaveContextStateOnMissingRowConflicts(t *testing.T) {
	store := newRaceStore(t)
	ctx := context.Background()
	const writers = 8
	errs := make([]error, writers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, errs[i] = store.SaveContextState(ctx, "new", "k", []byte(fmt.Sprintf(`{"v":%d}`, i)), 0)
		}()
	}
	close(start)
	wg.Wait()
	wins := 0
	for _, err := range errs {
		switch {
		case err == nil:
			wins++
		case !errors.Is(err, agent.ErrContextStateConflict):
			t.Fatalf("want ErrContextStateConflict, got %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("wins=%d, want 1", wins)
	}
	// State creation must not make the conversation visible or advance the message cursor.
	if ids, err := store.List(ctx); err != nil || len(ids) != 0 {
		t.Fatalf("list=%v err=%v", ids, err)
	}
	if _, err := store.Append(ctx, "new", []agent.Message{conformance.Msg("a")}, 0); err != nil {
		t.Fatalf("append after state save: %v", err)
	}
}

func TestCloseDoesNotClosePool(t *testing.T) {
	store := newRaceStore(t)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.pool.Ping(context.Background()); err != nil {
		t.Fatalf("caller-owned pool closed: %v", err)
	}
}
