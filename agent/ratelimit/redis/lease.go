package redis

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/camilbinas/gude-agents/agent/ratelimit"
	goredis "github.com/redis/go-redis/v9"
)

const serverNow = `
local t = redis.call("TIME")
local now_ms = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
`

var reserveScript = goredis.NewScript(serverNow + `
local id, reqN, tokN, pendingTTL, terminalTTL = ARGV[1], tonumber(ARGV[2]), tonumber(ARGV[3]), tonumber(ARGV[4]), tonumber(ARGV[5])
local ledger = KEYS[#KEYS]
if redis.call("HGET", ledger, "state") then return 1 end
local arg, key = 6, 1
local function prune(z,h,total,window)
 local cutoff=now_ms-window; local members=redis.call("ZRANGEBYSCORE",z,"-inf",cutoff-1); local sum=tonumber(redis.call("GET",total) or "0")
 for _,member in ipairs(members) do sum=sum-tonumber(redis.call("HGET",h,member) or "0"); redis.call("HDEL",h,member); redis.call("ZREM",z,member) end
 if sum<0 then sum=0 end; redis.call("SET",total,sum); return sum
end
for i=1,reqN do local w,limit=tonumber(ARGV[arg]),tonumber(ARGV[arg+1]); arg=arg+2; redis.call("ZREMRANGEBYSCORE",KEYS[key],"-inf",now_ms-w-1); if redis.call("ZCARD",KEYS[key])>=limit then return 0 end; key=key+1 end
for i=1,tokN do local w,limit,amount=tonumber(ARGV[arg]),tonumber(ARGV[arg+1]),tonumber(ARGV[arg+2]); arg=arg+3; if prune(KEYS[key],KEYS[key+1],KEYS[key+2],w)+amount>limit then return 0 end; key=key+3 end
arg,key=6,1
for i=1,reqN do local w=tonumber(ARGV[arg]); arg=arg+2; redis.call("ZADD",KEYS[key],now_ms,id); redis.call("PEXPIRE",KEYS[key],w); key=key+1 end
redis.call("HSET",ledger,"state","pending","expires",now_ms+pendingTTL,"terminal_ttl",terminalTTL,"token_n",tokN)
for i=1,tokN do local w,amount=tonumber(ARGV[arg]),tonumber(ARGV[arg+2]); arg=arg+3; local z,h,total=KEYS[key],KEYS[key+1],KEYS[key+2]; redis.call("ZADD",z,now_ms,id); redis.call("HSET",h,id,amount); redis.call("INCRBY",total,amount); redis.call("PEXPIRE",z,w); redis.call("PEXPIRE",h,w); redis.call("PEXPIRE",total,w); redis.call("HSET",ledger,"z"..i,z,"h"..i,h,"t"..i,total,"w"..i,w); key=key+3 end
redis.call("PEXPIRE",ledger,pendingTTL+terminalTTL)
return 1
`)

var terminalScript = goredis.NewScript(serverNow + `
local action, actual, tokenN = ARGV[1], tonumber(ARGV[2]), tonumber(ARGV[3])
local ledger=KEYS[#KEYS]; local state=redis.call("HGET",ledger,"state")
if not state then return -2 end
if state=="expired" then return -3 end
if state~="pending" then if state==action then return -4 else return -5 end end
local expires=tonumber(redis.call("HGET",ledger,"expires") or "0"); local terminalTTL=tonumber(redis.call("HGET",ledger,"terminal_ttl") or "0")
if now_ms>=expires then action="expired"; actual=-1 end
local arg,key=4,1
for i=1,tokenN do local w=tonumber(ARGV[arg]); arg=arg+1; local z,h,total=KEYS[key],KEYS[key+1],KEYS[key+2]; local old=tonumber(redis.call("HGET",h,ledger) or "0"); if old==0 then old=tonumber(redis.call("HGET",h,ARGV[arg]) or "0") end; key=key+3 end
-- The ledger ID is passed separately after all windows to avoid exposing a raw script status.
local id=ARGV[arg]
key=1; arg=4
for i=1,tokenN do local w=tonumber(ARGV[arg]); arg=arg+1; local z,h,total=KEYS[key],KEYS[key+1],KEYS[key+2]; local old=tonumber(redis.call("HGET",h,id) or "0"); local n=actual; if n<0 then n=old end; local sum=tonumber(redis.call("GET",total) or "0")-old+n; if sum<0 then sum=0 end; redis.call("HSET",h,id,n); redis.call("ZADD",z,now_ms,id); redis.call("SET",total,sum); redis.call("PEXPIRE",z,w); redis.call("PEXPIRE",h,w); redis.call("PEXPIRE",total,w); key=key+3 end
redis.call("HSET",ledger,"state",action); redis.call("PEXPIRE",ledger,terminalTTL)
if action=="expired" then return -3 end
return 1
`)

func (s *Store) Reserve(ctx context.Context, reservation ratelimit.Reservation) (bool, error) {
	if s.leaseTTLsConfigured && (s.pendingTTL <= 0 || s.terminalTTL <= 0) {
		return false, errors.New("redis lease TTLs must be positive when configured")
	}
	if reservation.ID == "" {
		return false, errors.New("rate limit reservation requires an ID")
	}
	window := time.Duration(0)
	for _, c := range reservation.Requests {
		if c.Strategy != ratelimit.SlidingWindow || c.Limit <= 0 || !validTTL(c.Window) {
			return false, fmt.Errorf("redis requires valid sliding request counters")
		}
		if c.Window > window {
			window = c.Window
		}
	}
	for _, c := range reservation.Tokens {
		if c.Strategy != ratelimit.SlidingWindow || c.Limit <= 0 || c.Amount < 0 || !validTTL(c.Window) {
			return false, fmt.Errorf("redis requires valid sliding token counters")
		}
		if c.Window > window {
			window = c.Window
		}
	}
	if window == 0 {
		return false, errors.New("rate limit reservation has no counters")
	}
	keys := make([]string, 0, len(reservation.Requests)+len(reservation.Tokens)*3+1)
	args := []any{reservation.ID, len(reservation.Requests), len(reservation.Tokens), s.effectivePending(window).Milliseconds(), s.effectiveTerminal(window).Milliseconds()}
	for _, c := range reservation.Requests {
		keys = append(keys, s.requestKey(c.Key))
		args = append(args, c.Window.Milliseconds(), c.Limit)
	}
	for _, c := range reservation.Tokens {
		keys = append(keys, s.tokenZKey(c.Key), s.tokenHKey(c.Key), s.tokenTKey(c.Key))
		args = append(args, c.Window.Milliseconds(), c.Limit, c.Amount)
	}
	keys = append(keys, s.ledgerKey(reservation.ID))
	result, err := reserveScript.Run(ctx, s.client, keys, args...).Int()
	return result == 1, err
}
func (s *Store) Commit(ctx context.Context, id string, actual int) error {
	if actual < 0 {
		actual = 0
	}
	return s.terminal(ctx, id, "committed", actual)
}
func (s *Store) Release(ctx context.Context, id string) error {
	return s.terminal(ctx, id, "released", -1)
}
func (s *Store) terminal(ctx context.Context, id, action string, actual int) error {
	ledger := s.ledgerKey(id)
	metadata, err := s.client.HGetAll(ctx, ledger).Result()
	if err != nil {
		return err
	}
	state := metadata["state"]
	if state == "" {
		return ratelimit.ErrLeaseUnknown
	}
	if state == "expired" {
		return ratelimit.ErrLeaseExpired
	}
	if state != "pending" {
		if state == action {
			return ratelimit.ErrLeaseTerminal
		}
		return ratelimit.ErrLeaseCrossTerminal
	}
	n, err := strconv.Atoi(metadata["token_n"])
	if err != nil {
		return ratelimit.ErrLeaseUnknown
	}
	keys := make([]string, 0, n*3+1)
	args := []any{action, actual, n}
	for i := 1; i <= n; i++ {
		w, err := strconv.ParseInt(metadata["w"+strconv.Itoa(i)], 10, 64)
		if err != nil {
			return ratelimit.ErrLeaseUnknown
		}
		keys = append(keys, metadata["z"+strconv.Itoa(i)], metadata["h"+strconv.Itoa(i)], metadata["t"+strconv.Itoa(i)])
		args = append(args, w)
	}
	args = append(args, id)
	keys = append(keys, ledger)
	result, err := terminalScript.Run(ctx, s.client, keys, args...).Int()
	if err != nil {
		return err
	}
	switch result {
	case 1:
		return nil
	case -2:
		return ratelimit.ErrLeaseUnknown
	case -3:
		return ratelimit.ErrLeaseExpired
	case -4:
		return ratelimit.ErrLeaseTerminal
	default:
		return ratelimit.ErrLeaseCrossTerminal
	}
}
