package redis

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent/checkpoint"
	goredis "github.com/redis/go-redis/v9"
)

func newTestStore(t *testing.T) *Checkpointer {
	t.Helper()

	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		t.Skip("REDIS_ADDR not set, skipping redis integration test")
	}

	c, err := New(Options{Addr: addr}, WithKeyPrefix("gude:cptest:"))
	if err != nil {
		t.Skipf("redis unreachable at REDIS_ADDR: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	ctx := context.Background()
	ids, err := c.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, id := range ids {
		if err := c.Delete(ctx, id); err != nil {
			t.Fatalf("cleanup delete %s: %v", id, err)
		}
	}

	return c
}

func TestConformance_Redis(t *testing.T) {
	store := newTestStore(t)
	checkpoint.RunConformance(t, func(t *testing.T) checkpoint.Checkpointer {
		return store
	})
}

func TestWithKeyPrefix_IgnoresEmpty(t *testing.T) {
	cfg := &config{keyPrefix: "gude:checkpoint:"}
	WithKeyPrefix("")(cfg)
	if cfg.keyPrefix != "gude:checkpoint:" {
		t.Errorf("keyPrefix = %q, want the default to be retained", cfg.keyPrefix)
	}
}

func TestWithTTL(t *testing.T) {
	cfg := &config{}
	WithTTL(5 * time.Minute)(cfg)
	if cfg.ttl != 5*time.Minute {
		t.Errorf("ttl = %v, want 5m", cfg.ttl)
	}
}

func TestKeyLayout(t *testing.T) {
	c := &Checkpointer{keyPrefix: "p:"}
	if got, want := c.threadKey("t1"), "p:thread:t1"; got != want {
		t.Errorf("threadKey = %q, want %q", got, want)
	}
	if got, want := c.versionField(7), "v:7"; got != want {
		t.Errorf("versionField = %q, want %q", got, want)
	}
	if got, want := c.threadsKey(), "p:threads"; got != want {
		t.Errorf("threadsKey = %q, want %q", got, want)
	}
}

func TestExtra_SurvivesRoundTrip(t *testing.T) {
	c := newTestStore(t)
	ctx := context.Background()

	raw := []byte(`{"readiness":{"out_a":true,"out_b":false}}`)
	if _, err := c.Save(ctx, "extra-verbatim", checkpoint.Checkpoint{Extra: raw}); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := c.Load(ctx, "extra-verbatim")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if string(loaded.Extra) != string(raw) {
		t.Errorf("Extra = %s, want %s", loaded.Extra, raw)
	}
}

func TestSave_MarshalFailureDoesNotAdvanceVersion(t *testing.T) {
	c := newTestStore(t)
	ctx := context.Background()

	if _, err := c.Save(ctx, "atomic-save", checkpoint.Checkpoint{Extra: json.RawMessage(`{`)}); err == nil {
		t.Fatal("expected marshal error")
	}
	got, err := c.Save(ctx, "atomic-save", checkpoint.Checkpoint{})
	if err != nil {
		t.Fatalf("save after marshal failure: %v", err)
	}
	if got.Version != 1 {
		t.Errorf("version = %d, want 1", got.Version)
	}
}

func TestSaveIfVersion_RejectsInvalidInput(t *testing.T) {
	c := &Checkpointer{}

	if _, err := c.SaveIfVersion(context.Background(), "", checkpoint.Checkpoint{}, 0); !errors.Is(err, checkpoint.ErrThreadIDRequired) {
		t.Errorf("empty thread ID error = %v, want ErrThreadIDRequired", err)
	}
	if _, err := c.SaveIfVersion(context.Background(), "thread", checkpoint.Checkpoint{}, -1); err == nil || !strings.Contains(err.Error(), "expected version must be non-negative") {
		t.Errorf("negative expected version error = %v, want explanatory error", err)
	}
}

func TestSaveIfVersion_CreateRefreshPersistAndIndex(t *testing.T) {
	c := newTestStore(t)
	c.ttl = 2 * time.Second
	ctx := context.Background()
	threadID := "conditional-lifecycle"

	created, err := c.SaveIfVersion(ctx, threadID, checkpoint.Checkpoint{Label: "created"}, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ThreadID != threadID || created.Version != 1 || created.Label != "created" {
		t.Errorf("created checkpoint = %+v, want thread %q version 1 label created", created, threadID)
	}
	if created.Timestamp.IsZero() {
		t.Error("created timestamp is zero")
	}

	score, err := c.client.ZScore(ctx, c.threadsKey(), threadID).Result()
	if err != nil {
		t.Fatalf("thread missing from index: %v", err)
	}
	if score != 0 {
		t.Errorf("index score = %v, want 0", score)
	}

	if err := c.client.PExpire(ctx, c.threadKey(threadID), 50*time.Millisecond).Err(); err != nil {
		t.Fatalf("shorten TTL: %v", err)
	}
	updated, err := c.SaveIfVersion(ctx, threadID, checkpoint.Checkpoint{Label: "updated"}, 1)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Version != 2 {
		t.Errorf("updated version = %d, want 2", updated.Version)
	}
	remaining, err := c.client.PTTL(ctx, c.threadKey(threadID)).Result()
	if err != nil {
		t.Fatalf("read refreshed TTL: %v", err)
	}
	if remaining <= time.Second {
		t.Errorf("refreshed TTL = %v, want more than 1s", remaining)
	}

	c.ttl = 0
	persisted, err := c.SaveIfVersion(ctx, threadID, checkpoint.Checkpoint{Label: "persisted"}, 2)
	if err != nil {
		t.Fatalf("persist: %v", err)
	}
	if persisted.Version != 3 {
		t.Errorf("persisted version = %d, want 3", persisted.Version)
	}
	remaining, err = c.client.PTTL(ctx, c.threadKey(threadID)).Result()
	if err != nil {
		t.Fatalf("read persistent TTL: %v", err)
	}
	if remaining != -1 {
		t.Errorf("persistent TTL = %v, want -1", remaining)
	}
}

func TestSaveIfVersion_RejectsMismatchWithoutWriting(t *testing.T) {
	ctx := context.Background()

	t.Run("stale existing thread", func(t *testing.T) {
		c := newTestStore(t)
		if _, err := c.SaveIfVersion(ctx, "conditional-stale", checkpoint.Checkpoint{Label: "original"}, 0); err != nil {
			t.Fatalf("seed: %v", err)
		}

		_, err := c.SaveIfVersion(ctx, "conditional-stale", checkpoint.Checkpoint{Label: "stale"}, 0)
		if !errors.Is(err, checkpoint.ErrConflict) {
			t.Fatalf("stale save error = %v, want ErrConflict", err)
		}
		history, err := c.History(ctx, "conditional-stale")
		if err != nil {
			t.Fatalf("history: %v", err)
		}
		if len(history) != 1 || history[0].Label != "original" {
			t.Errorf("history = %+v, want only original checkpoint", history)
		}
	})

	t.Run("absent thread with nonzero expectation", func(t *testing.T) {
		c := newTestStore(t)
		threadID := "conditional-absent"

		_, err := c.SaveIfVersion(ctx, threadID, checkpoint.Checkpoint{}, 1)
		if !errors.Is(err, checkpoint.ErrConflict) {
			t.Fatalf("absent save error = %v, want ErrConflict", err)
		}
		exists, err := c.client.Exists(ctx, c.threadKey(threadID)).Result()
		if err != nil {
			t.Fatalf("check thread key: %v", err)
		}
		if exists != 0 {
			t.Errorf("thread key exists = %d, want 0", exists)
		}
		if _, err := c.client.ZScore(ctx, c.threadsKey(), threadID).Result(); !errors.Is(err, goredis.Nil) {
			t.Errorf("index lookup error = %v, want redis.Nil", err)
		}
	})
}

func TestSaveIfVersion_ConcurrentOneWinner(t *testing.T) {
	c := newTestStore(t)
	ctx := context.Background()
	threadID := "conditional-concurrent"
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup

	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := c.SaveIfVersion(ctx, threadID, checkpoint.Checkpoint{}, 0)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	var successes, conflicts int
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, checkpoint.ErrConflict):
			conflicts++
		default:
			t.Errorf("unexpected save error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Errorf("successes = %d, conflicts = %d; want 1 each", successes, conflicts)
	}

	history, err := c.History(ctx, threadID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(history) != 1 || history[0].Version != 1 {
		t.Errorf("history = %+v, want exactly version 1", history)
	}
}

func TestTTL_RefreshesCompleteHistoryThenRemovesThread(t *testing.T) {
	c := newTestStore(t)
	c.ttl = 400 * time.Millisecond
	ctx := context.Background()

	if _, err := c.Save(ctx, "ttl-thread", checkpoint.Checkpoint{Label: "first"}); err != nil {
		t.Fatalf("save first: %v", err)
	}
	time.Sleep(250 * time.Millisecond)
	if _, err := c.Save(ctx, "ttl-thread", checkpoint.Checkpoint{Label: "second"}); err != nil {
		t.Fatalf("save second: %v", err)
	}

	time.Sleep(250 * time.Millisecond)
	if _, err := c.LoadAt(ctx, "ttl-thread", 1); err != nil {
		t.Fatalf("first checkpoint expired before the thread: %v", err)
	}
	history, err := c.History(ctx, "ttl-thread")
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("history length = %d, want 2", len(history))
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		_, err := c.Load(ctx, "ttl-thread")
		if errors.Is(err, checkpoint.ErrNotFound) {
			break
		}
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("checkpoint thread did not expire")
		}
		time.Sleep(10 * time.Millisecond)
	}

	ids, err := c.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, id := range ids {
		if id == "ttl-thread" {
			t.Fatalf("expired thread remains in List: %v", ids)
		}
	}
}

func TestHistory_ReportsMissingOrMalformedVersions(t *testing.T) {
	ctx := context.Background()

	t.Run("missing", func(t *testing.T) {
		c := newTestStore(t)
		if _, err := c.Save(ctx, "history-missing", checkpoint.Checkpoint{}); err != nil {
			t.Fatalf("save: %v", err)
		}
		if err := c.client.HDel(ctx, c.threadKey("history-missing"), c.versionField(1)).Err(); err != nil {
			t.Fatalf("delete version field: %v", err)
		}
		if _, err := c.History(ctx, "history-missing"); err == nil {
			t.Fatal("expected missing-version error")
		}
	})

	t.Run("malformed", func(t *testing.T) {
		c := newTestStore(t)
		if _, err := c.Save(ctx, "history-malformed", checkpoint.Checkpoint{}); err != nil {
			t.Fatalf("save: %v", err)
		}
		if err := c.client.HSet(ctx, c.threadKey("history-malformed"), c.versionField(1), `{`).Err(); err != nil {
			t.Fatalf("corrupt version field: %v", err)
		}
		if _, err := c.History(ctx, "history-malformed"); err == nil {
			t.Fatal("expected malformed-version error")
		}
	})
}

func TestListCleanup_PreservesRecreatedThread(t *testing.T) {
	c := newTestStore(t)
	ctx := context.Background()
	threadID := "recreated-during-cleanup"

	if err := c.client.ZAdd(ctx, c.threadsKey(), goredis.Z{Member: threadID}).Err(); err != nil {
		t.Fatalf("seed stale index entry: %v", err)
	}
	if _, err := c.Save(ctx, threadID, checkpoint.Checkpoint{}); err != nil {
		t.Fatalf("recreate thread: %v", err)
	}

	exists, err := c.retainIndexedThread(ctx, threadID)
	if err != nil {
		t.Fatalf("atomic cleanup: %v", err)
	}
	if !exists {
		t.Fatal("recreated thread was treated as stale")
	}

	score, err := c.client.ZScore(ctx, c.threadsKey(), threadID).Result()
	if err != nil {
		t.Fatalf("recreated thread missing from index: %v", err)
	}
	if score != 0 {
		t.Errorf("index score = %v, want 0", score)
	}
}
