package agent

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// RequestReservation describes one request-rate counter that a reservation
// must check and charge: at most Limit requests per Window for Key.
type RequestReservation struct {
	Key    string
	Limit  int
	Window time.Duration
}

// TokenCounter identifies one token-rate counter that receives recorded usage.
type TokenCounter struct {
	Key    string
	Window time.Duration
}

// RateLimitStore persists rate-limit counters, typically shared across
// processes. Implementations must be safe for concurrent use.
//
// The RateLimiter never reads a request count and increments it later:
// request admission is a single ReserveRequests call, so concurrent callers
// cannot all observe spare capacity and overshoot the limit.
type RateLimitStore interface {
	// ReserveRequests checks every reservation's limit and, if all have
	// capacity, records one request on every counter. If any limit would be
	// exceeded it returns (false, nil) and leaves every counter unchanged.
	//
	// The outcome is all-or-nothing. Implementations that cannot update
	// several counters in one atomic operation (for example to stay
	// Redis Cluster friendly) must reserve counters one at a time, each
	// atomically, and refund earlier reservations when a later one is
	// rejected or fails. A refund that itself fails may leave a counter
	// over-counted until its window expires, never under-counted, so the
	// limit is never exceeded.
	ReserveRequests(ctx context.Context, reservations []RequestReservation) (bool, error)

	// RecordTokens records amount tokens of actual usage on every counter.
	// It records reality and never rejects for exceeding a limit; limits are
	// enforced before later calls. It must record on all counters or none
	// (refunding earlier counters on failure, with the same over-count-only
	// guarantee as ReserveRequests).
	RecordTokens(ctx context.Context, counters []TokenCounter, amount int) error

	// GetTokenCount returns the tokens recorded for key within window.
	GetTokenCount(ctx context.Context, key string, window time.Duration) (int, error)
}

// MemoryStore is an in-memory RateLimitStore using sliding windows. All
// counters touched by one ReserveRequests or RecordTokens call are updated in
// a single critical section. It is safe for concurrent use but not shared
// across processes.
type MemoryStore struct {
	mu       sync.Mutex
	requests map[string][]time.Time
	tokens   map[string][]tokenEvent
	now      func() time.Time
}

// NewMemoryStore creates an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		requests: make(map[string][]time.Time),
		tokens:   make(map[string][]tokenEvent),
		now:      time.Now,
	}
}

var errInvalidWindow = errors.New("rate limit store: window must be > 0")

// pruneRequests drops request timestamps before cutoff.
func pruneRequests(requests []time.Time, cutoff time.Time) []time.Time {
	i := sort.Search(len(requests), func(j int) bool {
		return !requests[j].Before(cutoff)
	})
	return requests[i:]
}

// pruneTokens drops token events before cutoff.
func pruneTokens(tokens []tokenEvent, cutoff time.Time) []tokenEvent {
	i := sort.Search(len(tokens), func(j int) bool {
		return !tokens[j].at.Before(cutoff)
	})
	return tokens[i:]
}

// sumTokens returns the total tokens in events.
func sumTokens(tokens []tokenEvent) int {
	total := 0
	for _, e := range tokens {
		total += e.tokens
	}
	return total
}

// ReserveRequests implements RateLimitStore atomically under one lock.
func (ms *MemoryStore) ReserveRequests(_ context.Context, reservations []RequestReservation) (bool, error) {
	for _, r := range reservations {
		if r.Window <= 0 {
			return false, errInvalidWindow
		}
	}
	ms.mu.Lock()
	defer ms.mu.Unlock()

	now := ms.now()
	for _, r := range reservations {
		events := pruneRequests(ms.requests[r.Key], now.Add(-r.Window))
		ms.setRequests(r.Key, events)
		if len(events) >= r.Limit {
			return false, nil
		}
	}
	for _, r := range reservations {
		ms.requests[r.Key] = append(ms.requests[r.Key], now)
	}
	return true, nil
}

// RecordTokens implements RateLimitStore atomically under one lock.
func (ms *MemoryStore) RecordTokens(_ context.Context, counters []TokenCounter, amount int) error {
	for _, c := range counters {
		if c.Window <= 0 {
			return errInvalidWindow
		}
	}
	if amount <= 0 || len(counters) == 0 {
		return nil
	}
	ms.mu.Lock()
	defer ms.mu.Unlock()

	now := ms.now()
	for _, c := range counters {
		events := pruneTokens(ms.tokens[c.Key], now.Add(-c.Window))
		ms.tokens[c.Key] = append(events, tokenEvent{at: now, tokens: amount})
	}
	return nil
}

// GetTokenCount implements RateLimitStore.
func (ms *MemoryStore) GetTokenCount(_ context.Context, key string, window time.Duration) (int, error) {
	if window <= 0 {
		return 0, errInvalidWindow
	}
	ms.mu.Lock()
	defer ms.mu.Unlock()

	events := pruneTokens(ms.tokens[key], ms.now().Add(-window))
	if len(events) == 0 {
		delete(ms.tokens, key)
		return 0, nil
	}
	ms.tokens[key] = events
	return sumTokens(events), nil
}

// setRequests stores pruned events, dropping empty keys so idle counters do
// not accumulate. Caller holds ms.mu.
func (ms *MemoryStore) setRequests(key string, events []time.Time) {
	if len(events) == 0 {
		delete(ms.requests, key)
		return
	}
	ms.requests[key] = events
}
