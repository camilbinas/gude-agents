// Package redis provides a Redis-backed implementation of the
// agent.RateLimitStore interface for distributed rate limiting.
package redis

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"

	agent "github.com/camilbinas/gude-agents/agent"
	"github.com/redis/go-redis/v9"
)

// Ensure Store implements agent.RateLimitStore at compile time.
var _ agent.RateLimitStore = (*Store)(nil)

// Store implements agent.RateLimitStore using Redis sorted sets with
// sliding-window semantics.
type Store struct {
	client redis.UniversalClient
	prefix string // key prefix for namespacing
}

// Option configures a Store.
type Option func(*Store)

// WithPrefix sets the key prefix used for namespacing Redis keys.
// Default prefix is "ratelimit".
func WithPrefix(prefix string) Option {
	return func(s *Store) {
		s.prefix = prefix
	}
}

// NewStore creates a new Redis-backed rate limit store.
func NewStore(client redis.UniversalClient, opts ...Option) *Store {
	s := &Store{
		client: client,
		prefix: "ratelimit",
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// requestKey returns the Redis key for request tracking.
func (s *Store) requestKey(key string) string {
	return fmt.Sprintf("%s:req:%s", s.prefix, key)
}

// tokenKey returns the Redis key for token tracking.
func (s *Store) tokenKey(key string) string {
	return fmt.Sprintf("%s:tok:%s", s.prefix, key)
}

// incrementRequestsScript atomically adds a request entry, prunes expired
// entries, sets TTL, and returns the count.
var incrementRequestsScript = redis.NewScript(`
local key = KEYS[1]
local now_ms = tonumber(ARGV[1])
local window_ms = tonumber(ARGV[2])
local member = ARGV[3]
local cutoff = now_ms - window_ms

-- Add new entry with score = now_ms
redis.call("ZADD", key, now_ms, member)

-- Remove entries outside the window
redis.call("ZREMRANGEBYSCORE", key, "-inf", cutoff)

-- Set TTL to window duration (in seconds, rounded up)
local ttl_sec = math.ceil(window_ms / 1000)
redis.call("EXPIRE", key, ttl_sec)

-- Return current count
return redis.call("ZCARD", key)
`)

// uniqueID generates a unique identifier for sorted set members.
func uniqueID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// IncrementRequests atomically increments the request counter for the given
// key and window, returning the new count.
func (s *Store) IncrementRequests(ctx context.Context, key string, window time.Duration) (int, error) {
	rKey := s.requestKey(key)
	nowMs := time.Now().UnixMilli()
	windowMs := window.Milliseconds()
	member := uniqueID() + ":" + strconv.FormatInt(nowMs, 10)

	result, err := incrementRequestsScript.Run(ctx, s.client, []string{rKey}, nowMs, windowMs, member).Int()
	if err != nil {
		return 0, err
	}
	return result, nil
}

// incrementTokensScript atomically adds a token entry, prunes expired
// entries, sets TTL, and returns the sum of tokens in the window.
var incrementTokensScript = redis.NewScript(`
local key = KEYS[1]
local now_ms = tonumber(ARGV[1])
local window_ms = tonumber(ARGV[2])
local member = ARGV[3]
local cutoff = now_ms - window_ms

-- Add new entry with score = now_ms
redis.call("ZADD", key, now_ms, member)

-- Remove entries outside the window
redis.call("ZREMRANGEBYSCORE", key, "-inf", cutoff)

-- Set TTL to window duration (in seconds, rounded up)
local ttl_sec = math.ceil(window_ms / 1000)
redis.call("EXPIRE", key, ttl_sec)

-- Get all members in the window and sum their amounts
local members = redis.call("ZRANGEBYSCORE", key, cutoff, "+inf")
local total = 0
for _, m in ipairs(members) do
    -- member format is "uniqueID:amount" where uniqueID is 32 hex chars
    local sep = string.find(m, ":", 33)
    if sep then
        local amt = tonumber(string.sub(m, sep + 1))
        if amt then
            total = total + amt
        end
    end
end
return total
`)

// IncrementTokens atomically increments the token counter for the given
// key and window by amount, returning the new total.
func (s *Store) IncrementTokens(ctx context.Context, key string, window time.Duration, amount int) (int, error) {
	rKey := s.tokenKey(key)
	nowMs := time.Now().UnixMilli()
	windowMs := window.Milliseconds()
	// Member format: "uniqueID:amount" — unique ID ensures uniqueness, amount is parseable.
	member := uniqueID() + ":" + strconv.Itoa(amount)

	result, err := incrementTokensScript.Run(ctx, s.client, []string{rKey}, nowMs, windowMs, member).Int()
	if err != nil {
		return 0, err
	}
	return result, nil
}

// getRequestCountScript prunes expired entries and returns the count.
var getRequestCountScript = redis.NewScript(`
local key = KEYS[1]
local now_ms = tonumber(ARGV[1])
local window_ms = tonumber(ARGV[2])
local cutoff = now_ms - window_ms

-- Remove entries outside the window
redis.call("ZREMRANGEBYSCORE", key, "-inf", cutoff)

-- Return current count
return redis.call("ZCARD", key)
`)

// GetRequestCount returns the current request count within the window for key.
func (s *Store) GetRequestCount(ctx context.Context, key string, window time.Duration) (int, error) {
	rKey := s.requestKey(key)
	nowMs := time.Now().UnixMilli()
	windowMs := window.Milliseconds()

	result, err := getRequestCountScript.Run(ctx, s.client, []string{rKey}, nowMs, windowMs).Int()
	if err != nil {
		return 0, err
	}
	return result, nil
}

// getTokenCountScript prunes expired entries and returns the sum of tokens.
var getTokenCountScript = redis.NewScript(`
local key = KEYS[1]
local now_ms = tonumber(ARGV[1])
local window_ms = tonumber(ARGV[2])
local cutoff = now_ms - window_ms

-- Remove entries outside the window
redis.call("ZREMRANGEBYSCORE", key, "-inf", cutoff)

-- Get all members in the window and sum their amounts
local members = redis.call("ZRANGEBYSCORE", key, cutoff, "+inf")
local total = 0
for _, m in ipairs(members) do
    -- member format is "uniqueID:amount" where uniqueID is 32 hex chars
    local sep = string.find(m, ":", 33)
    if sep then
        local amt = tonumber(string.sub(m, sep + 1))
        if amt then
            total = total + amt
        end
    end
end
return total
`)

// GetTokenCount returns the current token count within the window for key.
func (s *Store) GetTokenCount(ctx context.Context, key string, window time.Duration) (int, error) {
	rKey := s.tokenKey(key)
	nowMs := time.Now().UnixMilli()
	windowMs := window.Milliseconds()

	result, err := getTokenCountScript.Run(ctx, s.client, []string{rKey}, nowMs, windowMs).Int()
	if err != nil {
		return 0, err
	}
	return result, nil
}
