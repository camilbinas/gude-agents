package redis

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	agent "github.com/camilbinas/gude-agents/agent"
	goredis "github.com/redis/go-redis/v9"
)

func setupMiniredis(t *testing.T) (*miniredis.Miniredis, *goredis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	mr.SetTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	client := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return mr, client
}

func reqCount(t *testing.T, mr *miniredis.Miniredis, key string) int {
	t.Helper()
	if !mr.Exists(key) {
		return 0
	}
	members, err := mr.ZMembers(key)
	if err != nil {
		t.Fatalf("ZMembers(%s): %v", key, err)
	}
	return len(members)
}

func res(key string, limit int, window time.Duration) agent.RequestReservation {
	return agent.RequestReservation{Key: key, Limit: limit, Window: window}
}

func TestReserveRequests_EnforcesLimit(t *testing.T) {
	mr, client := setupMiniredis(t)
	store := NewStore(client)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		ok, err := store.ReserveRequests(ctx, []agent.RequestReservation{res("k", 3, time.Minute)})
		if err != nil || !ok {
			t.Fatalf("reservation %d = %v, %v", i, ok, err)
		}
	}
	ok, err := store.ReserveRequests(ctx, []agent.RequestReservation{res("k", 3, time.Minute)})
	if err != nil || ok {
		t.Fatalf("reservation over limit = %v, %v, want false, nil", ok, err)
	}
	if n := reqCount(t, mr, "ratelimit:req:k"); n != 3 {
		t.Fatalf("stored events = %d, want 3 (rejection must not write)", n)
	}
}

// TestReserveRequests_RejectionRefundsEarlierCounters verifies the
// all-or-nothing contract across per-key and global counters.
func TestReserveRequests_RejectionRefundsEarlierCounters(t *testing.T) {
	mr, client := setupMiniredis(t)
	store := NewStore(client)
	ctx := context.Background()
	if ok, err := store.ReserveRequests(ctx, []agent.RequestReservation{res("global", 1, time.Minute)}); !ok || err != nil {
		t.Fatalf("fill global = %v, %v", ok, err)
	}
	ok, err := store.ReserveRequests(ctx, []agent.RequestReservation{res("key:a", 10, time.Minute), res("global", 1, time.Minute)})
	if err != nil || ok {
		t.Fatalf("reservation = %v, %v, want false, nil", ok, err)
	}
	if n := reqCount(t, mr, "ratelimit:req:key:a"); n != 0 {
		t.Fatalf("per-key counter = %d after global rejection, want 0 (refunded)", n)
	}
	if n := reqCount(t, mr, "ratelimit:req:global"); n != 1 {
		t.Fatalf("global counter = %d, want 1", n)
	}
}

// TestReserveRequests_ConcurrentNeverExceedsLimit races 100 reservations
// through the Lua script against one counter and across per-key + global.
func TestReserveRequests_ConcurrentNeverExceedsLimit(t *testing.T) {
	mr, client := setupMiniredis(t)
	store := NewStore(client)
	ctx := context.Background()

	var ok, rejected atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := "key:" + string(rune('a'+i%10))
			allowed, err := store.ReserveRequests(ctx, []agent.RequestReservation{res(key, 1000, time.Minute), res("global", 10, time.Minute)})
			switch {
			case err != nil:
				t.Errorf("reserve: %v", err)
			case allowed:
				ok.Add(1)
			default:
				rejected.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if ok.Load() != 10 || rejected.Load() != 90 {
		t.Fatalf("ok=%d rejected=%d, want 10 and 90", ok.Load(), rejected.Load())
	}
	if n := reqCount(t, mr, "ratelimit:req:global"); n != 10 {
		t.Fatalf("global events = %d, want 10", n)
	}
	total := 0
	for i := 0; i < 10; i++ {
		total += reqCount(t, mr, "ratelimit:req:key:"+string(rune('a'+i)))
	}
	if total != 10 {
		t.Fatalf("sum of per-key events = %d, want 10 (rejected reservations must be refunded)", total)
	}
}

func TestReserveRequests_UsesServerTimeWindow(t *testing.T) {
	mr, client := setupMiniredis(t)
	store := NewStore(client)
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	r := []agent.RequestReservation{res("k", 1, 10*time.Second)}

	if ok, _ := store.ReserveRequests(ctx, r); !ok {
		t.Fatal("first reservation rejected")
	}
	mr.SetTime(base.Add(5 * time.Second))
	if ok, _ := store.ReserveRequests(ctx, r); ok {
		t.Fatal("reservation inside the window accepted")
	}
	mr.SetTime(base.Add(11 * time.Second))
	if ok, err := store.ReserveRequests(ctx, r); !ok || err != nil {
		t.Fatalf("reservation after the window = %v, %v", ok, err)
	}
}

func TestRecordTokens_AllCountersAndSum(t *testing.T) {
	_, client := setupMiniredis(t)
	store := NewStore(client)
	ctx := context.Background()
	counters := []agent.TokenCounter{{Key: "key:a", Window: time.Minute}, {Key: "global", Window: time.Minute}}
	for _, amt := range []int{10, 25, 100} {
		if err := store.RecordTokens(ctx, counters, amt); err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range []string{"key:a", "global"} {
		n, err := store.GetTokenCount(ctx, key, time.Minute)
		if err != nil || n != 135 {
			t.Fatalf("GetTokenCount(%s) = %d, %v, want 135", key, n, err)
		}
	}
}

func TestRecordTokens_WindowExpiry(t *testing.T) {
	mr, client := setupMiniredis(t)
	store := NewStore(client)
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.RecordTokens(ctx, []agent.TokenCounter{{Key: "k", Window: 2 * time.Second}}, 42); err != nil {
		t.Fatal(err)
	}
	mr.SetTime(base.Add(3 * time.Second))
	if n, err := store.GetTokenCount(ctx, "k", 2*time.Second); err != nil || n != 0 {
		t.Fatalf("tokens after window = %d, %v, want 0", n, err)
	}
}

// TestRecordTokens_FailureRefundsEarlierCounters forces the second counter's
// script to fail and verifies the first counter's write is removed.
func TestRecordTokens_FailureRefundsEarlierCounters(t *testing.T) {
	mr, client := setupMiniredis(t)
	store := NewStore(client)
	ctx := context.Background()
	// A plain string at the global token key makes ZADD fail with WRONGTYPE.
	if err := mr.Set("ratelimit:tok:global", "not-a-zset"); err != nil {
		t.Fatal(err)
	}
	err := store.RecordTokens(ctx, []agent.TokenCounter{{Key: "key:a", Window: time.Minute}, {Key: "global", Window: time.Minute}}, 50)
	if err == nil {
		t.Fatal("expected error from the failing counter")
	}
	if n, _ := store.GetTokenCount(ctx, "key:a", time.Minute); n != 0 {
		t.Fatalf("per-key tokens = %d after partial failure, want 0 (refunded)", n)
	}
}

func TestReserveRequests_ScriptErrorRefunds(t *testing.T) {
	mr, client := setupMiniredis(t)
	store := NewStore(client)
	if err := mr.Set("ratelimit:req:global", "not-a-zset"); err != nil {
		t.Fatal(err)
	}
	ok, err := store.ReserveRequests(context.Background(), []agent.RequestReservation{res("key:a", 5, time.Minute), res("global", 5, time.Minute)})
	if err == nil || ok {
		t.Fatalf("reservation = %v, %v, want error", ok, err)
	}
	if n := reqCount(t, mr, "ratelimit:req:key:a"); n != 0 {
		t.Fatalf("per-key counter = %d after script error, want 0", n)
	}
}

type failingReader struct{}

var errRNG = errors.New("rng unavailable")

func (failingReader) Read([]byte) (int, error) { return 0, errRNG }

// TestRNGFailureAbortsBeforeMutation verifies an RNG failure returns an
// error and never writes to Redis.
func TestRNGFailureAbortsBeforeMutation(t *testing.T) {
	mr, client := setupMiniredis(t)
	store := NewStore(client)
	store.random = failingReader{}
	ctx := context.Background()

	if ok, err := store.ReserveRequests(ctx, []agent.RequestReservation{res("k", 5, time.Minute)}); ok || !errors.Is(err, errRNG) {
		t.Fatalf("ReserveRequests = %v, %v, want RNG error", ok, err)
	}
	if err := store.RecordTokens(ctx, []agent.TokenCounter{{Key: "k", Window: time.Minute}}, 10); !errors.Is(err, errRNG) {
		t.Fatalf("RecordTokens = %v, want RNG error", err)
	}
	if keys := mr.Keys(); len(keys) != 0 {
		t.Fatalf("Redis was mutated despite RNG failure: %v", keys)
	}
}

func TestRandomHex128From(t *testing.T) {
	id, err := randomHex128From(strings.NewReader(strings.Repeat("\x01", 16)))
	if err != nil || id != strings.Repeat("01", 16) {
		t.Fatalf("id = %q, %v", id, err)
	}
	if _, err := randomHex128From(strings.NewReader("short")); err == nil {
		t.Fatal("short read must fail, not return a partial ID")
	}
	store := NewStore(nil)
	id, err = randomHex128From(store.random)
	if err != nil || !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(id) {
		t.Fatalf("crypto ID = %q, %v", id, err)
	}
}

func TestKeyFormat(t *testing.T) {
	store := NewStore(nil, WithPrefix("myapp"))
	if got := store.requestKey("key:conv-1"); got != "myapp:req:key:conv-1" {
		t.Fatalf("requestKey = %q", got)
	}
	if got := store.tokenKey("global"); got != "myapp:tok:global" {
		t.Fatalf("tokenKey = %q", got)
	}
	// The framework never adds a Redis Cluster hash tag of its own; a brace
	// in the caller key is passed through verbatim as part of one key.
	if got := store.requestKey("key:{x}"); got != "myapp:req:key:{x}" {
		t.Fatalf("requestKey with braces = %q", got)
	}
	if strings.ContainsAny(store.requestKey("key:plain"), "{}") {
		t.Fatal("framework-generated key contains a hash tag")
	}
}

func TestErrorPropagation(t *testing.T) {
	mr, _ := setupMiniredis(t)
	client := goredis.NewClient(&goredis.Options{
		Addr:        mr.Addr(),
		MaxRetries:  0,
		DialTimeout: 100 * time.Millisecond,
		ReadTimeout: 100 * time.Millisecond,
		PoolSize:    1,
	})
	defer client.Close()
	store := NewStore(client)
	ctx := context.Background()
	mr.Close()

	if _, err := store.ReserveRequests(ctx, []agent.RequestReservation{res("k", 5, time.Minute)}); err == nil {
		t.Error("ReserveRequests: expected error when Redis is unavailable")
	}
	if err := store.RecordTokens(ctx, []agent.TokenCounter{{Key: "k", Window: time.Minute}}, 10); err == nil {
		t.Error("RecordTokens: expected error when Redis is unavailable")
	}
	if _, err := store.GetTokenCount(ctx, "k", time.Minute); err == nil {
		t.Error("GetTokenCount: expected error when Redis is unavailable")
	}
}

// TestRateLimiterWithRedisStore exercises the limiter end to end through
// the Lua paths: global rejection must not consume per-key budget.
func TestRateLimiterWithRedisStore(t *testing.T) {
	mr, client := setupMiniredis(t)
	rl, err := agent.NewRateLimiter(agent.RPM(5), agent.WithGlobalRPM(2), agent.TPM(1000), agent.WithGlobalTPM(1000), agent.WithStore(NewStore(client)))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, k := range []string{"a", "b"} {
		release, err := rl.Acquire(ctx, k)
		if err != nil {
			t.Fatalf("Acquire(%s): %v", k, err)
		}
		release()
	}
	if _, err := rl.Acquire(ctx, "c"); !errors.Is(err, agent.ErrRateLimitExceeded) {
		t.Fatalf("Acquire(c) = %v, want ErrRateLimitExceeded", err)
	}
	if n := reqCount(t, mr, "ratelimit:req:key:c"); n != 0 {
		t.Fatalf("per-key c events = %d, want 0", n)
	}
	if err := rl.Record(ctx, "a", agent.TokenUsage{InputTokens: 30}); err != nil {
		t.Fatal(err)
	}
	store := NewStore(client)
	for _, k := range []string{"key:a", "global"} {
		if n, _ := store.GetTokenCount(ctx, k, time.Minute); n != 30 {
			t.Fatalf("tokens %s = %d, want 30", k, n)
		}
	}
}
