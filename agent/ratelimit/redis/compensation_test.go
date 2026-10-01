package redis

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	agent "github.com/camilbinas/gude-agents/agent"
	goredis "github.com/redis/go-redis/v9"
)

var errInjected = errors.New("injected: connection reset after server executed script")

// ambiguousHook simulates the ambiguous network failure: the script for
// failKey runs on the server (so its write is applied), then the client is
// handed an error instead of the reply. With failRefunds set, ZREM
// compensation is rejected without reaching the server.
type ambiguousHook struct {
	mu          sync.Mutex
	failKey     string
	remaining   int // number of script calls on failKey to fail
	failRefunds bool
	injected    int
}

func (h *ambiguousHook) DialHook(next goredis.DialHook) goredis.DialHook { return next }

func (h *ambiguousHook) ProcessPipelineHook(next goredis.ProcessPipelineHook) goredis.ProcessPipelineHook {
	return next
}

func (h *ambiguousHook) ProcessHook(next goredis.ProcessHook) goredis.ProcessHook {
	return func(ctx context.Context, cmd goredis.Cmder) error {
		name := strings.ToLower(cmd.Name())
		if name == "zrem" {
			h.mu.Lock()
			fail := h.failRefunds
			h.mu.Unlock()
			if fail {
				err := errors.New("injected: refund unavailable")
				cmd.SetErr(err)
				return err
			}
		}
		err := next(ctx, cmd)
		if err != nil || (name != "evalsha" && name != "eval") {
			return err
		}
		args := cmd.Args()
		if len(args) < 4 {
			return nil
		}
		key, _ := args[3].(string)
		h.mu.Lock()
		defer h.mu.Unlock()
		if key != h.failKey || h.remaining == 0 {
			return nil
		}
		h.remaining--
		h.injected++
		cmd.SetErr(errInjected)
		return errInjected
	}
}

func setupAmbiguous(t *testing.T, failKey string) (*ambiguousHook, *Store, func(string) int) {
	t.Helper()
	mr, client := setupMiniredis(t)
	hook := &ambiguousHook{failKey: failKey, remaining: 1}
	client.AddHook(hook)
	count := func(key string) int { return reqCount(t, mr, key) }
	return hook, NewStore(client), count
}

func assertInjected(t *testing.T, hook *ambiguousHook, err error) {
	t.Helper()
	if !errors.Is(err, errInjected) {
		t.Fatalf("error = %v, want injected ambiguous error", err)
	}
	if hook.injected != 1 {
		t.Fatalf("injected %d failures, want 1 (script must have run on the server)", hook.injected)
	}
}

// The current (last) script writes, then the client sees an error: both the
// earlier reservation and the ambiguous one must be refunded.
func TestReserveRequests_AmbiguousErrorOnLaterCounterRefundsAll(t *testing.T) {
	hook, store, count := setupAmbiguous(t, "ratelimit:req:global")
	ok, err := store.ReserveRequests(context.Background(), []agent.RequestReservation{
		res("key:a", 10, time.Minute), res("global", 10, time.Minute),
	})
	if ok {
		t.Fatal("reservation reported success despite error")
	}
	assertInjected(t, hook, err)
	if n := count("ratelimit:req:key:a"); n != 0 {
		t.Fatalf("per-key events = %d, want 0", n)
	}
	if n := count("ratelimit:req:global"); n != 0 {
		t.Fatalf("global events = %d, want 0 (ambiguous write must be refunded)", n)
	}
}

// The first script writes, then the client sees an error.
func TestReserveRequests_AmbiguousErrorOnFirstCounterRefundsIt(t *testing.T) {
	hook, store, count := setupAmbiguous(t, "ratelimit:req:key:a")
	ok, err := store.ReserveRequests(context.Background(), []agent.RequestReservation{
		res("key:a", 10, time.Minute), res("global", 10, time.Minute),
	})
	if ok {
		t.Fatal("reservation reported success despite error")
	}
	assertInjected(t, hook, err)
	if n := count("ratelimit:req:key:a"); n != 0 {
		t.Fatalf("per-key events = %d, want 0 (ambiguous write must be refunded)", n)
	}
	if n := count("ratelimit:req:global"); n != 0 {
		t.Fatalf("global events = %d, want 0 (never attempted)", n)
	}
}

func TestRecordTokens_AmbiguousErrorOnLaterCounterRefundsAll(t *testing.T) {
	hook, store, _ := setupAmbiguous(t, "ratelimit:tok:global")
	ctx := context.Background()
	err := store.RecordTokens(ctx, []agent.TokenCounter{{Key: "key:a", Window: time.Minute}, {Key: "global", Window: time.Minute}}, 50)
	assertInjected(t, hook, err)
	for _, key := range []string{"key:a", "global"} {
		if n, err := store.GetTokenCount(ctx, key, time.Minute); err != nil || n != 0 {
			t.Fatalf("tokens %s = %d, %v, want 0", key, n, err)
		}
	}
}

func TestRecordTokens_AmbiguousErrorOnFirstCounterRefundsIt(t *testing.T) {
	hook, store, _ := setupAmbiguous(t, "ratelimit:tok:key:a")
	ctx := context.Background()
	err := store.RecordTokens(ctx, []agent.TokenCounter{{Key: "key:a", Window: time.Minute}, {Key: "global", Window: time.Minute}}, 50)
	assertInjected(t, hook, err)
	for _, key := range []string{"key:a", "global"} {
		if n, err := store.GetTokenCount(ctx, key, time.Minute); err != nil || n != 0 {
			t.Fatalf("tokens %s = %d, %v, want 0", key, n, err)
		}
	}
}

// When compensation itself fails, the error must surface and counters may
// only stay over-counted (the writes that really happened), never under.
func TestCompensationFailureOnlyOverCounts(t *testing.T) {
	t.Run("ReserveRequests", func(t *testing.T) {
		hook, store, count := setupAmbiguous(t, "ratelimit:req:global")
		hook.failRefunds = true
		ok, err := store.ReserveRequests(context.Background(), []agent.RequestReservation{
			res("key:a", 10, time.Minute), res("global", 10, time.Minute),
		})
		if ok {
			t.Fatal("reservation reported success despite error")
		}
		assertInjected(t, hook, err)
		if !strings.Contains(err.Error(), "refund") {
			t.Fatalf("error = %v, want refund failure reported", err)
		}
		if a, g := count("ratelimit:req:key:a"), count("ratelimit:req:global"); a != 1 || g != 1 {
			t.Fatalf("events key:a=%d global=%d, want 1 and 1 (over-count, never lost)", a, g)
		}
	})
	t.Run("RecordTokens", func(t *testing.T) {
		hook, store, _ := setupAmbiguous(t, "ratelimit:tok:global")
		hook.failRefunds = true
		ctx := context.Background()
		err := store.RecordTokens(ctx, []agent.TokenCounter{{Key: "key:a", Window: time.Minute}, {Key: "global", Window: time.Minute}}, 50)
		assertInjected(t, hook, err)
		if !strings.Contains(err.Error(), "refund") {
			t.Fatalf("error = %v, want refund failure reported", err)
		}
		hook.failRefunds = false
		for _, key := range []string{"key:a", "global"} {
			if n, err := store.GetTokenCount(ctx, key, time.Minute); err != nil || n != 50 {
				t.Fatalf("tokens %s = %d, %v, want 50 (over-count, never lost)", key, n, err)
			}
		}
	})
}

// Refunding a member the ambiguous script never wrote (the error happened
// before execution) is harmless and leaves unrelated events intact.
func TestRefundOfUnwrittenMemberIsHarmless(t *testing.T) {
	mr, client := setupMiniredis(t)
	store := NewStore(client)
	ctx := context.Background()
	if ok, err := store.ReserveRequests(ctx, []agent.RequestReservation{res("global", 10, time.Minute)}); !ok || err != nil {
		t.Fatalf("seed = %v, %v", ok, err)
	}
	if err := store.refund(ctx, []placed{{key: "ratelimit:req:global", member: "never-written"}, {key: "ratelimit:req:missing", member: "x"}}); err != nil {
		t.Fatalf("refund of unwritten members: %v", err)
	}
	if n := reqCount(t, mr, "ratelimit:req:global"); n != 1 {
		t.Fatalf("global events = %d, want 1", n)
	}
}

// End to end: an Acquire whose Redis script executed but whose reply was lost
// must fail without consuming request quota.
func TestAcquire_AmbiguousStoreErrorConsumesNoQuota(t *testing.T) {
	mr, client := setupMiniredis(t)
	hook := &ambiguousHook{failKey: "ratelimit:req:global", remaining: 1}
	client.AddHook(hook)
	rl, err := agent.NewRateLimiter(agent.RPM(1), agent.WithGlobalRPM(1), agent.WithStore(NewStore(client)))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := rl.Acquire(ctx, "a"); err == nil {
		t.Fatal("Acquire succeeded despite ambiguous store error")
	}
	if hook.injected != 1 {
		t.Fatalf("injected = %d, want 1", hook.injected)
	}
	if a, g := reqCount(t, mr, "ratelimit:req:key:a"), reqCount(t, mr, "ratelimit:req:global"); a != 0 || g != 0 {
		t.Fatalf("events key:a=%d global=%d after failed Acquire, want 0 and 0", a, g)
	}
	// With RPM and global RPM of 1, the next Acquire only succeeds if the
	// failed one consumed nothing.
	release, err := rl.Acquire(ctx, "a")
	if err != nil {
		t.Fatalf("Acquire after failed attempt: %v (quota was consumed)", err)
	}
	release()
}
