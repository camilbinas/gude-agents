package ratelimit

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// RequestReservation describes one request counter.
type RequestReservation struct {
	Key      string
	Limit    int
	Window   time.Duration
	Strategy WindowStrategy
}

// TokenCounter identifies one token-rate counter that receives recorded usage.
type TokenCounter struct {
	Key    string
	Window time.Duration
}

// TokenReservation describes an estimated token reservation.
type TokenReservation struct {
	Key      string
	Limit    int
	Window   time.Duration
	Strategy WindowStrategy
	Amount   int
}

// RateLimitReservation identifies one provider attempt.
type RateLimitReservation struct {
	ID       string
	Requests []RequestReservation
	Tokens   []TokenReservation
}

// RateLimitLeaseStore reserves and reconciles leases atomically.
type RateLimitLeaseStore interface {
	ReserveLease(ctx context.Context, reservation RateLimitReservation) (bool, error)
	CommitLease(ctx context.Context, id string, tokens []TokenReservation, actual int) error
	FailLease(ctx context.Context, id string, tokens []TokenReservation) error
}

// RateLimitStore persists legacy counters.
type RateLimitStore interface {
	// ReserveRequests is the legacy request-counter API.
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

// MemoryStore is a process-local RateLimitStore.
type MemoryStore struct {
	mu       sync.Mutex
	requests map[string][]time.Time
	tokens   map[string][]tokenEvent
	leases   map[string]memoryLease
	counters map[string]*memoryLeaseCounter
	now      func() time.Time
}

type memoryLeaseState uint8

const (
	memoryLeasePending memoryLeaseState = iota
	memoryLeaseCommitted
	memoryLeaseFailed
)

type memoryLease struct {
	state  memoryLeaseState
	tokens []TokenReservation
}

type memoryLeaseCounter struct {
	strategy WindowStrategy
	window   time.Duration

	requests []time.Time
	tokens   []tokenEvent
	reserved int

	fixedStart    time.Time
	fixedRequests int
	fixedTokens   int
}

// NewMemoryStore creates an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		requests: make(map[string][]time.Time),
		tokens:   make(map[string][]tokenEvent),
		leases:   make(map[string]memoryLease),
		counters: make(map[string]*memoryLeaseCounter),
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

// ReserveLease implements RateLimitLeaseStore under one mutex. It charges RPM
// immediately and reserves estimated TPM so concurrent callers cannot spend
// the same token capacity.
func (ms *MemoryStore) ReserveLease(ctx context.Context, reservation RateLimitReservation) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if reservation.ID == "" {
		return false, errors.New("rate limit store: lease ID is required")
	}
	for _, r := range reservation.Requests {
		if r.Window <= 0 || r.Limit <= 0 {
			return false, errInvalidWindow
		}
	}
	for _, r := range reservation.Tokens {
		if r.Window <= 0 || r.Limit <= 0 || r.Amount < 0 {
			return false, errInvalidWindow
		}
	}

	ms.mu.Lock()
	defer ms.mu.Unlock()
	if _, ok := ms.leases[reservation.ID]; ok {
		return true, nil
	}
	now := ms.now()
	reqCounters := make([]*memoryLeaseCounter, 0, len(reservation.Requests))
	for _, r := range reservation.Requests {
		c := ms.leaseCounter("req:"+r.Key, r.Strategy, r.Window)
		c.prune(now)
		if c.requestCount() >= r.Limit {
			return false, nil
		}
		reqCounters = append(reqCounters, c)
	}
	tokCounters := make([]*memoryLeaseCounter, 0, len(reservation.Tokens))
	for _, r := range reservation.Tokens {
		c := ms.leaseCounter("tok:"+r.Key, r.Strategy, r.Window)
		c.prune(now)
		if c.tokenCount()+c.reserved+r.Amount > r.Limit {
			return false, nil
		}
		tokCounters = append(tokCounters, c)
	}
	for _, c := range reqCounters {
		c.addRequest(now)
	}
	for i, c := range tokCounters {
		c.reserved += reservation.Tokens[i].Amount
	}
	ms.leases[reservation.ID] = memoryLease{state: memoryLeasePending, tokens: append([]TokenReservation(nil), reservation.Tokens...)}
	return true, nil
}

// CommitLease replaces an estimated reservation with actual provider usage.
// Actual usage may exceed the estimate because provider consumption is reality;
// future admissions are blocked until the excess expires.
func (ms *MemoryStore) CommitLease(ctx context.Context, id string, tokens []TokenReservation, actual int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if actual < 0 {
		actual = 0
	}
	ms.mu.Lock()
	defer ms.mu.Unlock()
	lease, ok := ms.leases[id]
	if !ok || lease.state == memoryLeaseCommitted {
		return nil
	}
	if lease.state != memoryLeasePending {
		return errors.New("rate limit store: lease already failed")
	}
	now := ms.now()
	for _, r := range lease.tokens {
		c := ms.leaseCounter("tok:"+r.Key, r.Strategy, r.Window)
		c.prune(now)
		c.reserved -= r.Amount
		if c.reserved < 0 {
			c.reserved = 0
		}
		c.addTokens(now, actual)
	}
	lease.state = memoryLeaseCommitted
	ms.leases[id] = lease
	return nil
}

// FailLease conservatively finalizes the estimated reservation when provider
// usage is ambiguous. It never silently refunds potentially consumed tokens.
func (ms *MemoryStore) FailLease(ctx context.Context, id string, tokens []TokenReservation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	ms.mu.Lock()
	defer ms.mu.Unlock()
	lease, ok := ms.leases[id]
	if !ok || lease.state == memoryLeaseFailed {
		return nil
	}
	if lease.state != memoryLeasePending {
		return errors.New("rate limit store: lease already committed")
	}
	now := ms.now()
	for _, r := range lease.tokens {
		c := ms.leaseCounter("tok:"+r.Key, r.Strategy, r.Window)
		c.prune(now)
		c.reserved -= r.Amount
		if c.reserved < 0 {
			c.reserved = 0
		}
		c.addTokens(now, r.Amount)
	}
	lease.state = memoryLeaseFailed
	ms.leases[id] = lease
	return nil
}

func (ms *MemoryStore) leaseCounter(key string, strategy WindowStrategy, window time.Duration) *memoryLeaseCounter {
	if c := ms.counters[key]; c != nil {
		return c
	}
	c := &memoryLeaseCounter{strategy: strategy, window: window}
	ms.counters[key] = c
	return c
}

func (c *memoryLeaseCounter) prune(now time.Time) {
	if c.strategy == FixedWindow {
		if c.fixedStart.IsZero() || now.Sub(c.fixedStart) >= c.window {
			c.fixedStart = now
			c.fixedRequests = 0
			c.fixedTokens = 0
			c.reserved = 0
		}
		return
	}
	c.requests = pruneRequests(c.requests, now.Add(-c.window))
	c.tokens = pruneTokens(c.tokens, now.Add(-c.window))
}

func (c *memoryLeaseCounter) requestCount() int {
	if c.strategy == FixedWindow {
		return c.fixedRequests
	}
	return len(c.requests)
}

func (c *memoryLeaseCounter) tokenCount() int {
	if c.strategy == FixedWindow {
		return c.fixedTokens
	}
	return sumTokens(c.tokens)
}

func (c *memoryLeaseCounter) addRequest(now time.Time) {
	if c.strategy == FixedWindow {
		c.fixedRequests++
		return
	}
	c.requests = append(c.requests, now)
}

func (c *memoryLeaseCounter) addTokens(now time.Time, amount int) {
	if c.strategy == FixedWindow {
		c.fixedTokens += amount
		return
	}
	if amount > 0 {
		c.tokens = append(c.tokens, tokenEvent{at: now, tokens: amount})
	}
}
