package ratelimit

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// RequestCounter describes one RPM counter included in an atomic reservation.
type RequestCounter struct {
	Key      string
	Limit    int
	Window   time.Duration
	Strategy WindowStrategy
}

// TokenReservation describes one TPM counter and its estimated charge.
type TokenReservation struct {
	Key      string
	Limit    int
	Window   time.Duration
	Strategy WindowStrategy
	Amount   int
}

// Reservation describes every counter reserved by a single opaque lease.
type Reservation struct {
	ID       string
	Requests []RequestCounter
	Tokens   []TokenReservation
}

// Store is the lease-only persistence contract implemented by MemoryStore and
// the Redis backend. Every terminal operation is idempotency-aware and returns
// typed lifecycle errors rather than backend status codes.
type Store interface {
	Reserve(context.Context, Reservation) (bool, error)
	Commit(context.Context, string, int) error
	Release(context.Context, string) error
}

type StoreConfig struct {
	PendingTTL, TerminalTTL               time.Duration
	pendingConfigured, terminalConfigured bool
}
type StoreOption func(*StoreConfig)

// WithPendingLeaseTTL configures a positive minimum pending TTL. The effective
// value is always at least twice the largest rate window in the reservation.
func WithPendingLeaseTTL(ttl time.Duration) StoreOption {
	return func(c *StoreConfig) { c.PendingTTL, c.pendingConfigured = ttl, true }
}

// WithTerminalLeaseTTL configures a positive minimum terminal-retention TTL.
func WithTerminalLeaseTTL(ttl time.Duration) StoreOption {
	return func(c *StoreConfig) { c.TerminalTTL, c.terminalConfigured = ttl, true }
}

// MemoryStore is a lease-only, process-local Store.
type MemoryStore struct {
	mu       sync.Mutex
	leases   map[string]memoryLease
	counters map[string]*memoryCounter
	now      func() time.Time
	config   StoreConfig
}
type leaseState uint8

const (
	pending leaseState = iota
	committed
	released
	expired
)

type memoryLease struct {
	state                       leaseState
	tokens                      []TokenReservation
	window                      time.Duration // maximum request/token window captured at Reserve
	pendingUntil, terminalUntil time.Time
}
type tokenEvent struct {
	at     time.Time
	tokens int
}
type memoryCounter struct {
	strategy                   WindowStrategy
	window                     time.Duration
	requests                   []time.Time
	tokens                     []tokenEvent
	reserved                   int
	fixedStart                 time.Time
	fixedRequests, fixedTokens int
}

// NewMemoryStore creates a validated lease-only MemoryStore.
func NewMemoryStore(opts ...StoreOption) (*MemoryStore, error) {
	c := StoreConfig{}
	for _, opt := range opts {
		opt(&c)
	}
	if (c.pendingConfigured && c.PendingTTL <= 0) || (c.terminalConfigured && c.TerminalTTL <= 0) {
		return nil, errors.New("lease TTLs must be positive when configured")
	}
	return &MemoryStore{leases: map[string]memoryLease{}, counters: map[string]*memoryCounter{}, now: time.Now, config: c}, nil
}

func (m *MemoryStore) Reserve(ctx context.Context, reservation Reservation) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if reservation.ID == "" {
		return false, errors.New("rate limit reservation requires an ID")
	}
	if err := validateReservation(reservation); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	m.cleanup(now)
	if _, ok := m.leases[reservation.ID]; ok {
		return true, nil
	}
	for _, r := range reservation.Requests {
		c := m.counter("r:"+r.Key, r.Strategy, r.Window)
		c.prune(now)
		if c.requestCount() >= r.Limit {
			return false, nil
		}
	}
	for _, r := range reservation.Tokens {
		c := m.counter("t:"+r.Key, r.Strategy, r.Window)
		c.prune(now)
		if c.tokenCount()+c.reserved+r.Amount > r.Limit {
			return false, nil
		}
	}
	for _, r := range reservation.Requests {
		m.counter("r:"+r.Key, r.Strategy, r.Window).addRequest(now)
	}
	for _, r := range reservation.Tokens {
		m.counter("t:"+r.Key, r.Strategy, r.Window).reserved += r.Amount
	}
	window := reservationWindow(reservation)
	m.leases[reservation.ID] = memoryLease{state: pending, tokens: append([]TokenReservation(nil), reservation.Tokens...), window: window, pendingUntil: now.Add(m.pendingTTL(window)), terminalUntil: now.Add(m.terminalTTL(window))}
	return true, nil
}
func (m *MemoryStore) Commit(ctx context.Context, id string, actual int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if actual < 0 {
		actual = 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	m.cleanup(now)
	lease, ok := m.leases[id]
	if !ok {
		return ErrLeaseUnknown
	}
	if lease.state != pending {
		return terminalError(lease.state, committed)
	}
	m.settle(now, &lease, actual)
	lease.state = committed
	lease.terminalUntil = now.Add(m.terminalTTL(lease.window))
	m.leases[id] = lease
	return nil
}
func (m *MemoryStore) Release(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	m.cleanup(now)
	lease, ok := m.leases[id]
	if !ok {
		return ErrLeaseUnknown
	}
	if lease.state != pending {
		return terminalError(lease.state, released)
	}
	m.settle(now, &lease, -1)
	lease.state = released
	lease.terminalUntil = now.Add(m.terminalTTL(lease.window))
	m.leases[id] = lease
	return nil
}
func (m *MemoryStore) settle(now time.Time, lease *memoryLease, actual int) {
	for _, r := range lease.tokens {
		c := m.counter("t:"+r.Key, r.Strategy, r.Window)
		c.prune(now)
		c.reserved -= r.Amount
		if c.reserved < 0 {
			c.reserved = 0
		}
		n := actual
		if n < 0 {
			n = r.Amount
		}
		c.addTokens(now, n)
	}
}
func (m *MemoryStore) cleanup(now time.Time) {
	for id, l := range m.leases {
		if l.state == pending && !now.Before(l.pendingUntil) {
			m.settle(now, &l, -1)
			l.state = expired
			l.terminalUntil = now.Add(m.terminalTTL(l.window))
			m.leases[id] = l
		}
		if l.state != pending && !now.Before(l.terminalUntil) {
			delete(m.leases, id)
		}
	}
}
func (m *MemoryStore) pendingTTL(window time.Duration) time.Duration {
	ttl := 2 * window
	if m.config.PendingTTL > ttl {
		ttl = m.config.PendingTTL
	}
	return ttl
}
func (m *MemoryStore) terminalTTL(window time.Duration) time.Duration {
	ttl := window
	if m.config.TerminalTTL > ttl {
		ttl = m.config.TerminalTTL
	}
	return ttl
}
func (m *MemoryStore) counter(key string, strategy WindowStrategy, window time.Duration) *memoryCounter {
	if c := m.counters[key]; c != nil {
		return c
	}
	c := &memoryCounter{strategy: strategy, window: window}
	m.counters[key] = c
	return c
}
func (c *memoryCounter) prune(now time.Time) {
	if c.strategy == FixedWindow {
		if c.fixedStart.IsZero() || now.Sub(c.fixedStart) >= c.window {
			c.fixedStart, c.fixedRequests, c.fixedTokens, c.reserved = now, 0, 0, 0
		}
		return
	}
	cutoff := now.Add(-c.window)
	c.requests = pruneTimes(c.requests, cutoff)
	c.tokens = pruneTokenEvents(c.tokens, cutoff)
}
func (c *memoryCounter) requestCount() int {
	if c.strategy == FixedWindow {
		return c.fixedRequests
	}
	return len(c.requests)
}
func (c *memoryCounter) tokenCount() int {
	if c.strategy == FixedWindow {
		return c.fixedTokens
	}
	total := 0
	for _, e := range c.tokens {
		total += e.tokens
	}
	return total
}
func (c *memoryCounter) addRequest(now time.Time) {
	if c.strategy == FixedWindow {
		c.fixedRequests++
		return
	}
	c.requests = append(c.requests, now)
}
func (c *memoryCounter) addTokens(now time.Time, n int) {
	if n <= 0 {
		return
	}
	if c.strategy == FixedWindow {
		c.fixedTokens += n
		return
	}
	c.tokens = append(c.tokens, tokenEvent{now, n})
}
func pruneTimes(events []time.Time, cutoff time.Time) []time.Time {
	return events[sort.Search(len(events), func(i int) bool { return !events[i].Before(cutoff) }):]
}
func pruneTokenEvents(events []tokenEvent, cutoff time.Time) []tokenEvent {
	return events[sort.Search(len(events), func(i int) bool { return !events[i].at.Before(cutoff) }):]
}
func reservationWindow(r Reservation) time.Duration {
	var w time.Duration
	for _, c := range r.Requests {
		if c.Window > w {
			w = c.Window
		}
	}
	for _, c := range r.Tokens {
		if c.Window > w {
			w = c.Window
		}
	}
	return w
}
func validateReservation(r Reservation) error {
	for _, c := range r.Requests {
		if c.Limit <= 0 || c.Window <= 0 {
			return errors.New("invalid request counter")
		}
	}
	for _, c := range r.Tokens {
		if c.Limit <= 0 || c.Window <= 0 || c.Amount < 0 {
			return errors.New("invalid token counter")
		}
	}
	return nil
}
func terminalError(state, requested leaseState) error {
	if state == expired {
		return ErrLeaseExpired
	}
	if state == requested {
		return ErrLeaseTerminal
	}
	return ErrLeaseCrossTerminal
}
