package agent

import (
	"context"
	"sort"
	"sync"
	"time"
)

// RateLimitStore abstracts rate limit counter persistence.
// Implementations must be safe for concurrent use.
type RateLimitStore interface {
	// IncrementRequests atomically increments the request counter for the
	// given key and window, returning the new count.
	IncrementRequests(ctx context.Context, key string, window time.Duration) (int, error)

	// IncrementTokens atomically increments the token counter for the given
	// key and window by amount, returning the new total.
	IncrementTokens(ctx context.Context, key string, window time.Duration, amount int) (int, error)

	// GetRequestCount returns the current request count within the window for key.
	GetRequestCount(ctx context.Context, key string, window time.Duration) (int, error)

	// GetTokenCount returns the current token count within the window for key.
	GetTokenCount(ctx context.Context, key string, window time.Duration) (int, error)
}

// memBucket holds per-key sliding-window event data for the MemoryStore.
type memBucket struct {
	requests []time.Time
	tokens   []tokenEvent
}

// MemoryStore is an in-memory implementation of RateLimitStore using
// sliding-window counters. It is safe for concurrent use.
type MemoryStore struct {
	mu      sync.Mutex
	buckets map[string]*memBucket
	now     func() time.Time
}

// NewMemoryStore creates a new MemoryStore with an empty bucket map.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		buckets: make(map[string]*memBucket),
		now:     time.Now,
	}
}

// getBucket returns the memBucket for key, creating one if it doesn't exist.
// Caller must hold ms.mu.
func (ms *MemoryStore) getBucket(key string) *memBucket {
	b, ok := ms.buckets[key]
	if !ok {
		b = &memBucket{}
		ms.buckets[key] = b
	}
	return b
}

// pruneRequests removes request timestamps that fall outside the window.
// Returns the pruned slice (events within [now-window, now]).
func pruneRequests(requests []time.Time, cutoff time.Time) []time.Time {
	i := sort.Search(len(requests), func(j int) bool {
		return !requests[j].Before(cutoff)
	})
	return requests[i:]
}

// pruneTokens removes token events that fall outside the window.
// Returns the pruned slice (events within [now-window, now]).
func pruneTokens(tokens []tokenEvent, cutoff time.Time) []tokenEvent {
	i := sort.Search(len(tokens), func(j int) bool {
		return !tokens[j].at.Before(cutoff)
	})
	return tokens[i:]
}

// sumTokens returns the total token count from a slice of token events.
func sumTokens(tokens []tokenEvent) int {
	total := 0
	for _, e := range tokens {
		total += e.tokens
	}
	return total
}

// IncrementRequests adds a request timestamp for key and returns the count
// of requests within the sliding window.
func (ms *MemoryStore) IncrementRequests(_ context.Context, key string, window time.Duration) (int, error) {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	now := ms.now()
	b := ms.getBucket(key)

	// Add the new request event.
	b.requests = append(b.requests, now)

	// Prune expired entries and count.
	cutoff := now.Add(-window)
	b.requests = pruneRequests(b.requests, cutoff)

	return len(b.requests), nil
}

// IncrementTokens adds a token event for key and returns the total tokens
// within the sliding window.
func (ms *MemoryStore) IncrementTokens(_ context.Context, key string, window time.Duration, amount int) (int, error) {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	now := ms.now()
	b := ms.getBucket(key)

	// Add the new token event.
	b.tokens = append(b.tokens, tokenEvent{at: now, tokens: amount})

	// Prune expired entries and sum.
	cutoff := now.Add(-window)
	b.tokens = pruneTokens(b.tokens, cutoff)

	return sumTokens(b.tokens), nil
}

// GetRequestCount returns the number of requests within the sliding window for key.
func (ms *MemoryStore) GetRequestCount(_ context.Context, key string, window time.Duration) (int, error) {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	now := ms.now()
	b := ms.getBucket(key)

	cutoff := now.Add(-window)
	b.requests = pruneRequests(b.requests, cutoff)

	return len(b.requests), nil
}

// GetTokenCount returns the total tokens within the sliding window for key.
func (ms *MemoryStore) GetTokenCount(_ context.Context, key string, window time.Duration) (int, error) {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	now := ms.now()
	b := ms.getBucket(key)

	cutoff := now.Add(-window)
	b.tokens = pruneTokens(b.tokens, cutoff)

	return sumTokens(b.tokens), nil
}
