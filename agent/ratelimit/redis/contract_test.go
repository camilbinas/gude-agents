package redis

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	agent "github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/ratelimit"
	goredis "github.com/redis/go-redis/v9"
)

func testStore(t *testing.T) (*miniredis.Miniredis, *Store) {
	t.Helper()
	server := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return server, NewStore(client)
}
func testReservation(id string, amount int) ratelimit.Reservation {
	return ratelimit.Reservation{ID: id, Requests: []ratelimit.RequestCounter{{Key: "key", Limit: 2, Window: time.Minute, Strategy: ratelimit.SlidingWindow}, {Key: "global", Limit: 2, Window: time.Minute, Strategy: ratelimit.SlidingWindow}}, Tokens: []ratelimit.TokenReservation{{Key: "key", Limit: 100, Window: time.Minute, Strategy: ratelimit.SlidingWindow, Amount: amount}, {Key: "global", Limit: 100, Window: time.Minute, Strategy: ratelimit.SlidingWindow, Amount: amount}}}
}

func TestRedisLeaseContract(t *testing.T) {
	server, store := testStore(t)
	ctx := context.Background()
	first := testReservation("one", 40)
	ok, err := store.Reserve(ctx, first)
	if err != nil || !ok {
		t.Fatalf("reserve = %v,%v", ok, err)
	}
	if err := store.Commit(ctx, "one", 10); err != nil {
		t.Fatal(err)
	}
	if err := store.Commit(ctx, "one", 10); !errors.Is(err, agent.ErrRateLimitLeaseTerminal) {
		t.Fatalf("double commit=%v", err)
	}
	server.FastForward(59 * time.Second)
	if err := store.Commit(ctx, "one", 10); !errors.Is(err, agent.ErrRateLimitLeaseTerminal) {
		t.Fatalf("terminal ledger was not retained through its counter window: %v", err)
	}
	if err := store.Release(ctx, "one"); !errors.Is(err, agent.ErrRateLimitLeaseCrossTerminal) {
		t.Fatalf("cross terminal=%v", err)
	}
	second := testReservation("two", 80)
	ok, err = store.Reserve(ctx, second)
	if err != nil || !ok {
		t.Fatalf("actual-lower reconciliation = %v,%v", ok, err)
	}
	if err := store.Release(ctx, "two"); err != nil {
		t.Fatal(err)
	}
	if err := store.Release(ctx, "two"); !errors.Is(err, agent.ErrRateLimitLeaseTerminal) {
		t.Fatalf("double release=%v", err)
	}
	if err := store.Commit(ctx, "missing", 1); !errors.Is(err, agent.ErrRateLimitLeaseUnknown) {
		t.Fatalf("unknown=%v", err)
	}
}
func TestRedisAtomicMultiLimitAndPendingTTL(t *testing.T) {
	_, store := testStore(t)
	ctx := context.Background()
	one := testReservation("one", 60)
	ok, err := store.Reserve(ctx, one)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if ok, err = store.Reserve(ctx, testReservation("two", 50)); err != nil || ok {
		t.Fatalf("multi counter reject=%v,%v", ok, err)
	}

	short := testReservation("expired", 7)
	for i := range short.Requests {
		short.Requests[i].Window = time.Millisecond
	}
	for i := range short.Tokens {
		short.Tokens[i].Window = time.Millisecond
	}
	store.terminalTTL = time.Minute
	if ok, err = store.Reserve(ctx, short); err != nil || !ok {
		t.Fatalf("short reserve=%v,%v", ok, err)
	}
	time.Sleep(5 * time.Millisecond)
	if err := store.Commit(ctx, "expired", 1); !errors.Is(err, agent.ErrRateLimitLeaseExpired) {
		t.Fatalf("late commit=%v", err)
	}
}
