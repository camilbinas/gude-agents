package redis

import (
	"context"
	"encoding/json"
	"errors"
	"os"
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
