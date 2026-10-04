package redis

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/camilbinas/gude-agents/agent/ratelimit"
	"github.com/redis/go-redis/v9"
)

var _ ratelimit.RateLimitLeaseStore = (*Store)(nil)

// leaseTag co-locates every lease key in one Redis Cluster hash slot so one
// Lua script can atomically admit per-key and global counters together.
func (s *Store) leaseTag() string {
	return strings.NewReplacer("{", "(", "}", ")").Replace(s.prefix) + ":{gude-ratelimit}:lease"
}

func (s *Store) leaseRequestKey(key string) string { return s.leaseTag() + ":req:" + key }
func (s *Store) leaseTokenZKey(key string) string  { return s.leaseTag() + ":tok:z:" + key }
func (s *Store) leaseTokenHKey(key string) string  { return s.leaseTag() + ":tok:h:" + key }
func (s *Store) leaseTokenTKey(key string) string  { return s.leaseTag() + ":tok:t:" + key }
func (s *Store) leaseLedgerKey(id string) string   { return s.leaseTag() + ":op:" + id }

// reserveLeaseScript checks all request and token counters before mutating
// any of them. Token totals include committed events and active estimates.
var reserveLeaseScript = redis.NewScript(serverNow + `
local id = ARGV[1]
local req_n = tonumber(ARGV[2])
local tok_n = tonumber(ARGV[3])
local arg = 4
local key = 1
local max_window = 1
local ledger = KEYS[#KEYS]
if redis.call("HGET", ledger, "state") then return 1 end

local function prune_token(z, h, total, window)
  local cutoff = now_ms - window
  local expired = redis.call("ZRANGEBYSCORE", z, "-inf", cutoff - 1)
  local sum = tonumber(redis.call("GET", total) or "0")
  for _, member in ipairs(expired) do
    local amount = tonumber(redis.call("HGET", h, member) or "0")
    sum = sum - amount
    redis.call("HDEL", h, member)
    redis.call("ZREM", z, member)
  end
  if sum < 0 then sum = 0 end
  redis.call("SET", total, sum)
  return sum
end

for i = 1, req_n do
  local window = tonumber(ARGV[arg]); local limit = tonumber(ARGV[arg + 1]); arg = arg + 2
  if window > max_window then max_window = window end
  redis.call("ZREMRANGEBYSCORE", KEYS[key], "-inf", now_ms - window - 1)
  if redis.call("ZCARD", KEYS[key]) >= limit then return 0 end
  key = key + 1
end
for i = 1, tok_n do
  local window = tonumber(ARGV[arg]); local limit = tonumber(ARGV[arg + 1]); local amount = tonumber(ARGV[arg + 2]); arg = arg + 3
  if window > max_window then max_window = window end
  local total = prune_token(KEYS[key], KEYS[key + 1], KEYS[key + 2], window)
  if total + amount > limit then return 0 end
  key = key + 3
end

key = 1
for i = 1, req_n do
  local window = tonumber(ARGV[4 + (i - 1) * 2])
  redis.call("ZADD", KEYS[key], now_ms, id)
  redis.call("PEXPIRE", KEYS[key], window)
  key = key + 1
end
arg = 4 + req_n * 2
for i = 1, tok_n do
  local window = tonumber(ARGV[arg]); local amount = tonumber(ARGV[arg + 2]); arg = arg + 3
  local z = KEYS[key]; local h = KEYS[key + 1]; local total = KEYS[key + 2]
  local current = tonumber(redis.call("GET", total) or "0")
  redis.call("ZADD", z, now_ms, id)
  redis.call("HSET", h, id, amount)
  redis.call("SET", total, current + amount)
  redis.call("PEXPIRE", z, window); redis.call("PEXPIRE", h, window); redis.call("PEXPIRE", total, window)
  key = key + 3
end
ledger = KEYS[key]
redis.call("HSET", ledger, "state", "pending")
redis.call("PEXPIRE", ledger, max_window)
return 1
`)

var commitLeaseScript = redis.NewScript(serverNow + `
local id = ARGV[1]
local actual = tonumber(ARGV[2])
local tok_n = tonumber(ARGV[3])
local ledger = KEYS[#KEYS]
local state = redis.call("HGET", ledger, "state")
if state == "committed" then return 1 end
if state == "failed" then return -1 end
if state ~= "pending" then return -2 end
local arg = 4
local key = 1
for i = 1, tok_n do
  local window = tonumber(ARGV[arg]); arg = arg + 1
  local z = KEYS[key]; local h = KEYS[key + 1]; local total = KEYS[key + 2]
  local old = tonumber(redis.call("HGET", h, id) or "0")
  local current = tonumber(redis.call("GET", total) or "0")
  current = current - old + actual
  if current < 0 then current = 0 end
  redis.call("HSET", h, id, actual)
  redis.call("ZADD", z, now_ms, id)
  redis.call("SET", total, current)
  redis.call("PEXPIRE", z, window); redis.call("PEXPIRE", h, window); redis.call("PEXPIRE", total, window)
  key = key + 3
end
redis.call("HSET", ledger, "state", "committed", "actual", actual)
return 1
`)

var failLeaseScript = redis.NewScript(`
local ledger = KEYS[1]
local state = redis.call("HGET", ledger, "state")
if state == "failed" then return 1 end
if state == "committed" then return -1 end
if state ~= "pending" then return -2 end
redis.call("HSET", ledger, "state", "failed")
return 1
`)

// ReserveLease atomically reserves RPM plus estimated TPM. Redis currently
// implements sliding windows; FixedWindow is rejected explicitly rather than
// silently changing semantics.
func (s *Store) ReserveLease(ctx context.Context, reservation ratelimit.RateLimitReservation) (bool, error) {
	if reservation.ID == "" {
		return false, errors.New("redis rate limit store: lease ID is required")
	}
	for _, r := range reservation.Requests {
		if r.Strategy != ratelimit.SlidingWindow {
			return false, errors.New("redis rate limit store: fixed windows are not supported")
		}
		if err := validWindow(r.Window); err != nil || r.Limit <= 0 {
			return false, fmt.Errorf("redis rate limit store: invalid request reservation")
		}
	}
	for _, r := range reservation.Tokens {
		if r.Strategy != ratelimit.SlidingWindow {
			return false, errors.New("redis rate limit store: fixed windows are not supported")
		}
		if err := validWindow(r.Window); err != nil || r.Limit <= 0 || r.Amount < 0 {
			return false, fmt.Errorf("redis rate limit store: invalid token reservation")
		}
	}
	keys := make([]string, 0, len(reservation.Requests)+len(reservation.Tokens)*3+1)
	args := []any{reservation.ID, len(reservation.Requests), len(reservation.Tokens)}
	for _, r := range reservation.Requests {
		keys = append(keys, s.leaseRequestKey(r.Key))
		args = append(args, r.Window.Milliseconds(), r.Limit)
	}
	for _, r := range reservation.Tokens {
		keys = append(keys, s.leaseTokenZKey(r.Key), s.leaseTokenHKey(r.Key), s.leaseTokenTKey(r.Key))
		args = append(args, r.Window.Milliseconds(), r.Limit, r.Amount)
	}
	keys = append(keys, s.leaseLedgerKey(reservation.ID))
	result, err := reserveLeaseScript.Run(ctx, s.client, keys, args...).Int()
	if err != nil {
		return false, err
	}
	return result == 1, nil
}

func (s *Store) CommitLease(ctx context.Context, id string, tokens []ratelimit.TokenReservation, actual int) error {
	if actual < 0 {
		actual = 0
	}
	keys := make([]string, 0, len(tokens)*3+1)
	args := []any{id, actual, len(tokens)}
	for _, r := range tokens {
		if r.Strategy != ratelimit.SlidingWindow {
			return errors.New("redis rate limit store: fixed windows are not supported")
		}
		keys = append(keys, s.leaseTokenZKey(r.Key), s.leaseTokenHKey(r.Key), s.leaseTokenTKey(r.Key))
		args = append(args, r.Window.Milliseconds())
	}
	keys = append(keys, s.leaseLedgerKey(id))
	result, err := commitLeaseScript.Run(ctx, s.client, keys, args...).Int()
	if err != nil {
		return err
	}
	if result < 0 {
		return errors.New("redis rate limit store: lease is not pending")
	}
	return nil
}

func (s *Store) FailLease(ctx context.Context, id string, tokens []ratelimit.TokenReservation) error {
	result, err := failLeaseScript.Run(ctx, s.client, []string{s.leaseLedgerKey(id)}).Int()
	if err != nil {
		return err
	}
	if result < 0 {
		return errors.New("redis rate limit store: lease is not pending")
	}
	return nil
}
