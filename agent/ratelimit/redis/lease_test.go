package redis

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent/ratelimit"
)

func leaseReservation(id string, amount int) ratelimit.RateLimitReservation {
	return ratelimit.RateLimitReservation{
		ID:       id,
		Requests: []ratelimit.RequestReservation{{Key: "key:a", Limit: 100, Window: time.Minute, Strategy: ratelimit.SlidingWindow}, {Key: "global", Limit: 100, Window: time.Minute, Strategy: ratelimit.SlidingWindow}},
		Tokens:   []ratelimit.TokenReservation{{Key: "key:a", Limit: 1000, Window: time.Minute, Strategy: ratelimit.SlidingWindow, Amount: amount}, {Key: "global", Limit: 1000, Window: time.Minute, Strategy: ratelimit.SlidingWindow, Amount: amount}},
	}
}

func TestReserveLeaseConcurrentTPM(t *testing.T) {
	_, client := setupMiniredis(t)
	store := NewStore(client)
	var admitted atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range 10 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			ok, err := store.ReserveLease(context.Background(), leaseReservation("lease-"+string(rune('a'+i)), 500))
			if err != nil {
				t.Errorf("ReserveLease: %v", err)
				return
			}
			if ok {
				admitted.Add(1)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	if got := admitted.Load(); got != 2 {
		t.Fatalf("admitted = %d, want 2", got)
	}
}

func TestLeaseCommitAndFailReconcileTokens(t *testing.T) {
	_, client := setupMiniredis(t)
	store := NewStore(client)
	ctx := context.Background()
	reservation := leaseReservation("lease", 500)
	ok, err := store.ReserveLease(ctx, reservation)
	if err != nil || !ok {
		t.Fatalf("ReserveLease = %v, %v", ok, err)
	}
	if err := store.CommitLease(ctx, "lease", reservation.Tokens, 300); err != nil {
		t.Fatal(err)
	}
	if err := store.CommitLease(ctx, "lease", reservation.Tokens, 300); err != nil {
		t.Fatal(err)
	}
	ok, err = store.ReserveLease(ctx, leaseReservation("lease-2", 700))
	if err != nil || !ok {
		t.Fatalf("reconciled admission = %v, %v", ok, err)
	}
	ok, err = store.ReserveLease(ctx, leaseReservation("lease-3", 1))
	if err != nil || ok {
		t.Fatalf("over-limit admission = %v, %v", ok, err)
	}

	_, failureClient := setupMiniredis(t)
	failureStore := NewStore(failureClient)
	fail := leaseReservation("failed", 200)
	ok, err = failureStore.ReserveLease(ctx, fail)
	if err != nil || !ok {
		t.Fatalf("failure reserve = %v, %v", ok, err)
	}
	if err := failureStore.FailLease(ctx, "failed", fail.Tokens); err != nil {
		t.Fatal(err)
	}
}

func TestReserveLeaseRejectsFixedWindowExplicitly(t *testing.T) {
	_, client := setupMiniredis(t)
	store := NewStore(client)
	reservation := leaseReservation("fixed", 1)
	reservation.Tokens[0].Strategy = ratelimit.FixedWindow
	if _, err := store.ReserveLease(context.Background(), reservation); err == nil {
		t.Fatal("expected explicit fixed-window error")
	}
}

func TestReserveLeaseIsIdempotent(t *testing.T) {
	_, client := setupMiniredis(t)
	store := NewStore(client)
	reservation := leaseReservation("same", 500)
	ok, err := store.ReserveLease(context.Background(), reservation)
	if err != nil || !ok {
		t.Fatalf("first reserve = %v, %v", ok, err)
	}
	ok, err = store.ReserveLease(context.Background(), reservation)
	if err != nil || !ok {
		t.Fatalf("idempotent reserve = %v, %v", ok, err)
	}
	ok, err = store.ReserveLease(context.Background(), leaseReservation("other", 600))
	if err != nil || ok {
		t.Fatalf("duplicate reserve double-counted: %v, %v", ok, err)
	}
}

func TestCommitLeaseRejectsFailedLease(t *testing.T) {
	_, client := setupMiniredis(t)
	store := NewStore(client)
	reservation := leaseReservation("failed", 50)
	if ok, err := store.ReserveLease(context.Background(), reservation); err != nil || !ok {
		t.Fatalf("reserve = %v, %v", ok, err)
	}
	if err := store.FailLease(context.Background(), reservation.ID, reservation.Tokens); err != nil {
		t.Fatal(err)
	}
	if err := store.CommitLease(context.Background(), reservation.ID, reservation.Tokens, 50); err == nil {
		t.Fatal("expected failed-lease error")
	}
}
