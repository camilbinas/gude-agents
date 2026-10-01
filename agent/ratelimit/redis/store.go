// Package redis provides a Redis-backed implementation of the
// agent.RateLimitStore interface for distributed rate limiting.
//
// Every counter is a Redis sorted set of events scored by Redis server time
// (TIME inside the script), so instances with skewed clocks share one
// consistent window. Each check-and-record runs as a single-key Lua script,
// which is atomic and Redis Cluster friendly: per-key and global counters may
// live on different slots.
//
// A reservation spanning several counters (per-key and global) reserves them
// one at a time and refunds earlier reservations when a later one is rejected
// or fails. Concurrent callers therefore can never push any counter past its
// limit.
//
// A script error is treated as an ambiguous outcome: Redis may have executed
// the write before the client observed a network error or timeout. The
// attempted member is therefore refunded along with every earlier one. Refunds
// are ZREMs of unique members, so removing a member that was never written is
// harmless. If a refund itself fails, that counter stays over-counted until the
// event leaves its window: the limiter errs toward rejecting, never toward
// exceeding a limit.
package redis

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	agent "github.com/camilbinas/gude-agents/agent"
	"github.com/redis/go-redis/v9"
)

// Ensure Store implements agent.RateLimitStore at compile time.
var _ agent.RateLimitStore = (*Store)(nil)

// refundTimeout bounds compensating writes, which must still run after the
// caller's context is cancelled.
const refundTimeout = 5 * time.Second

// Store implements agent.RateLimitStore using Redis sorted sets with
// sliding-window semantics.
type Store struct {
	client redis.UniversalClient
	prefix string    // key prefix for namespacing
	random io.Reader // source of event IDs; crypto/rand.Reader in production
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
		random: rand.Reader,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// requestKey returns the Redis key for a request counter. The framework adds
// no Redis Cluster hash tag: each script touches exactly one key.
func (s *Store) requestKey(key string) string {
	return s.prefix + ":req:" + key
}

// tokenKey returns the Redis key for a token counter.
func (s *Store) tokenKey(key string) string {
	return s.prefix + ":tok:" + key
}

// randomHex128From returns 128 random bits from r, hex encoded. It fails
// rather than falling back to a weaker or partial value.
func randomHex128From(r io.Reader) (string, error) {
	var b [16]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return "", fmt.Errorf("generate rate limit event ID: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// newMembers generates n unique event IDs before any mutation runs, so an RNG
// failure aborts the operation without touching Redis.
func (s *Store) newMembers(n int) ([]string, error) {
	members := make([]string, n)
	for i := range members {
		id, err := randomHex128From(s.random)
		if err != nil {
			return nil, err
		}
		members[i] = id
	}
	return members, nil
}

// serverNow is shared by all scripts: it reads Redis server time in ms.
// replicate_commands makes TIME usable before writes on Redis < 5; newer
// servers replicate script effects by default.
const serverNow = `
if redis.replicate_commands then redis.replicate_commands() end
local t = redis.call("TIME")
local now_ms = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
`

// reserveScript prunes expired events, rejects (returns 0) without writing
// when the counter is at its limit, and otherwise records the event
// (returns 1).
var reserveScript = redis.NewScript(serverNow + `
local key = KEYS[1]
local window_ms = tonumber(ARGV[1])
local limit = tonumber(ARGV[2])
local member = ARGV[3]

redis.call("ZREMRANGEBYSCORE", key, "-inf", now_ms - window_ms)
if redis.call("ZCARD", key) >= limit then
  return 0
end
redis.call("ZADD", key, now_ms, member)
redis.call("PEXPIRE", key, window_ms)
return 1
`)

// recordScript records an event unconditionally and prunes expired ones.
var recordScript = redis.NewScript(serverNow + `
local key = KEYS[1]
local window_ms = tonumber(ARGV[1])
local member = ARGV[2]

redis.call("ZREMRANGEBYSCORE", key, "-inf", now_ms - window_ms)
redis.call("ZADD", key, now_ms, member)
redis.call("PEXPIRE", key, window_ms)
return 1
`)

// getTokenCountScript prunes expired events and sums the token amounts
// encoded in the members ("<32 hex id>:<amount>").
var getTokenCountScript = redis.NewScript(serverNow + `
local key = KEYS[1]
local window_ms = tonumber(ARGV[1])

redis.call("ZREMRANGEBYSCORE", key, "-inf", now_ms - window_ms)
local members = redis.call("ZRANGE", key, 0, -1)
local total = 0
for _, m in ipairs(members) do
  local sep = string.find(m, ":", 33, true)
  if sep then
    local amt = tonumber(string.sub(m, sep + 1))
    if amt then
      total = total + amt
    end
  end
end
return total
`)

type placed struct{ key, member string }

// refund removes events recorded by a failed multi-counter operation. It is
// idempotent: ZREM of a member that was never written is a no-op.
func (s *Store) refund(ctx context.Context, done []placed) error {
	if len(done) == 0 {
		return nil
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), refundTimeout)
	defer cancel()
	var errs []error
	for _, p := range done {
		if err := s.client.ZRem(rctx, p.key, p.member).Err(); err != nil {
			errs = append(errs, fmt.Errorf("refund %s: %w", p.key, err))
		}
	}
	return errors.Join(errs...)
}

func validWindow(w time.Duration) error {
	if w.Milliseconds() <= 0 {
		return fmt.Errorf("redis rate limit store: window must be >= 1ms, got %v", w)
	}
	return nil
}

// ReserveRequests implements agent.RateLimitStore. Each counter is checked
// and charged by one atomic script; earlier charges are refunded when a later
// counter rejects, and earlier plus the attempted charge are refunded when a
// script errors, so the outcome is all-or-nothing.
func (s *Store) ReserveRequests(ctx context.Context, reservations []agent.RequestReservation) (bool, error) {
	for _, r := range reservations {
		if err := validWindow(r.Window); err != nil {
			return false, err
		}
	}
	members, err := s.newMembers(len(reservations))
	if err != nil {
		return false, err
	}
	var done []placed
	for i, r := range reservations {
		current := placed{key: s.requestKey(r.Key), member: members[i]}
		ok, err := reserveScript.Run(ctx, s.client, []string{current.key}, r.Window.Milliseconds(), r.Limit, current.member).Int()
		if err != nil {
			// Ambiguous: the script may have written current before the
			// error reached us, so refund it too.
			return false, errors.Join(err, s.refund(ctx, append(done, current)))
		}
		if ok != 1 {
			// A rejecting script never writes its own member.
			return false, s.refund(ctx, done)
		}
		done = append(done, current)
	}
	return true, nil
}

// RecordTokens implements agent.RateLimitStore. Usage is recorded on every
// counter; if one write errors, it and all earlier writes are refunded.
func (s *Store) RecordTokens(ctx context.Context, counters []agent.TokenCounter, amount int) error {
	for _, c := range counters {
		if err := validWindow(c.Window); err != nil {
			return err
		}
	}
	if amount <= 0 || len(counters) == 0 {
		return nil
	}
	ids, err := s.newMembers(len(counters))
	if err != nil {
		return err
	}
	var done []placed
	for i, c := range counters {
		current := placed{key: s.tokenKey(c.Key), member: ids[i] + ":" + strconv.Itoa(amount)}
		if err := recordScript.Run(ctx, s.client, []string{current.key}, c.Window.Milliseconds(), current.member).Err(); err != nil {
			// Ambiguous: the script may have written current before the
			// error reached us, so refund it too.
			return errors.Join(err, s.refund(ctx, append(done, current)))
		}
		done = append(done, current)
	}
	return nil
}

// GetTokenCount implements agent.RateLimitStore.
func (s *Store) GetTokenCount(ctx context.Context, key string, window time.Duration) (int, error) {
	if err := validWindow(window); err != nil {
		return 0, err
	}
	return getTokenCountScript.Run(ctx, s.client, []string{s.tokenKey(key)}, window.Milliseconds()).Int()
}
