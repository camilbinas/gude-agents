package redis

import (
	"context"
	"errors"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
)

func skipIfNoRedis(t *testing.T) string {
	t.Helper()
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		t.Skip("REDIS_ADDR not set")
	}
	return addr
}

func redisMessages(text string) []agent.Message {
	return []agent.Message{{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: text}}}}
}

func newTestConversation(t *testing.T, options ...Option) *Conversation {
	t.Helper()
	options = append(options, WithKeyPrefix("conversation-test:"+strings.ReplaceAll(t.Name(), "/", ":")+":"))
	m, err := New(Options{Addr: skipIfNoRedis(t)}, options...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		keys, _ := m.client.Keys(context.Background(), m.keyPrefix+"*").Result()
		if len(keys) > 0 {
			_ = m.client.Del(context.Background(), keys...).Err()
		}
		_ = m.Close()
	})
	return m
}

func TestNewUnreachable(t *testing.T) {
	_, err := New(Options{Addr: "localhost:1"})
	if err == nil || !strings.Contains(err.Error(), "ping") {
		t.Fatalf("New error = %v", err)
	}
}

func TestSaveLoadRevisionAndConflict(t *testing.T) {
	m := newTestConversation(t)
	ctx := context.Background()
	missing, err := m.Load(ctx, "missing")
	if err != nil || missing.Messages == nil || missing.Revision != 0 {
		t.Fatalf("missing = %+v, %v", missing, err)
	}
	rev, err := m.Save(ctx, "conv", redisMessages("one"), 0)
	if err != nil || rev != 1 {
		t.Fatalf("first Save = %d, %v", rev, err)
	}
	rev, err = m.Save(ctx, "conv", redisMessages("two"), rev)
	if err != nil || rev != 2 {
		t.Fatalf("second Save = %d, %v", rev, err)
	}
	if _, err := m.Save(ctx, "conv", redisMessages("stale"), 1); !errors.Is(err, agent.ErrConversationConflict) {
		t.Fatalf("stale Save = %v", err)
	}
	snapshot, err := m.Load(ctx, "conv")
	if err != nil || snapshot.Revision != 2 || !reflect.DeepEqual(snapshot.Messages, redisMessages("two")) {
		t.Fatalf("snapshot = %+v, %v", snapshot, err)
	}
}

func TestTTLAndNoExpiration(t *testing.T) {
	ctx := context.Background()
	withTTL := newTestConversation(t, WithTTL(10*time.Minute))
	if _, err := withTTL.Save(ctx, "ttl", redisMessages("x"), 0); err != nil {
		t.Fatal(err)
	}
	if ttl := withTTL.client.TTL(ctx, withTTL.keyPrefix+"ttl").Val(); ttl <= 0 || ttl > 10*time.Minute {
		t.Fatalf("TTL = %v", ttl)
	}
	withoutTTL := newTestConversation(t)
	if _, err := withoutTTL.Save(ctx, "persistent", redisMessages("x"), 0); err != nil {
		t.Fatal(err)
	}
	if ttl := withoutTTL.client.TTL(ctx, withoutTTL.keyPrefix+"persistent").Val(); ttl != -time.Second {
		t.Fatalf("TTL = %v", ttl)
	}
}

func TestListAndDelete(t *testing.T) {
	m := newTestConversation(t)
	ctx := context.Background()
	for _, id := range []string{"a", "b"} {
		if _, err := m.Save(ctx, id, redisMessages(id), 0); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := m.List(ctx)
	sort.Strings(ids)
	if err != nil || !reflect.DeepEqual(ids, []string{"a", "b"}) {
		t.Fatalf("List = %v, %v", ids, err)
	}
	if err := m.Delete(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := m.Load(ctx, "a")
	if err != nil || snapshot.Revision != 0 || len(snapshot.Messages) != 0 {
		t.Fatalf("deleted = %+v, %v", snapshot, err)
	}
}

func TestConversationManagerCompatibility(t *testing.T) {
	var _ agent.ConversationManager = (*Conversation)(nil)
}

func TestConcurrentCASOneWinner(t *testing.T) {
	store := newTestConversation(t)
	ctx := context.Background()
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := store.Save(ctx, "race", redisMessages("value"), 0)
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

func TestLegacyHashUpgradesWithCAS(t *testing.T) {
	m := newTestConversation(t)
	ctx := context.Background()
	key := m.keyPrefix + "legacy"
	legacyJSON := `[{"role":"user","content":[{"type":"text","text":"legacy"}]}]`
	if err := m.client.HSet(ctx, key, "messages", legacyJSON).Err(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := m.Load(ctx, "legacy")
	if err != nil || snapshot.Revision != 0 || len(snapshot.Messages) != 1 {
		t.Fatalf("legacy snapshot = %+v, %v", snapshot, err)
	}
	revision, err := m.Save(ctx, "legacy", redisMessages("upgraded"), 0)
	if err != nil || revision != 1 {
		t.Fatalf("upgrade Save = %d, %v", revision, err)
	}
	if _, err := m.Save(ctx, "legacy", redisMessages("stale"), 0); !errors.Is(err, agent.ErrConversationConflict) {
		t.Fatalf("second revision-zero Save = %v", err)
	}
}
