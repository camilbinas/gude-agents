// Package redis provides a Redis-backed agent.ExecutionStore.
//
// Each execution is one Redis hash holding a version counter and the serialized
// execution. Create and Save are single Lua scripts, so the existence check or
// version comparison and the write are one atomic step, and every key is a
// single slot in cluster mode.
//
// # Usage
//
//	executions := redis.New(client)
//	a, err := agent.New(provider, instructions,
//	    agent.WithConversationStore(conversations),
//	    agent.WithExecutionStore(executions),
//	)
//
// Redis is only durable if the server is configured for persistence (AOF or
// snapshots) and, when you set WithTTL, an expired execution is gone for good.
// Import it with an alias when you also use the Redis conversation store:
//
//	redisexec "github.com/camilbinas/gude-agents/agent/execution/redis"
package redis

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/execution/internal/record"
	goredis "github.com/redis/go-redis/v9"
)

const (
	defaultKeyPrefix = "gude:execution:"
	versionField     = "version"
	payloadField     = "payload"

	createScript = `
if redis.call("EXISTS", KEYS[1]) == 1 then
    return 0
end
redis.call("HSET", KEYS[1], "version", 1, "payload", ARGV[1])
if tonumber(ARGV[2]) > 0 then
    redis.call("PEXPIRE", KEYS[1], ARGV[2])
end
return 1
`
	saveScript = `
local current = redis.call("HGET", KEYS[1], "version")
if not current or tonumber(current) ~= tonumber(ARGV[1]) then
    return 0
end
local nextVersion = tonumber(current) + 1
redis.call("HSET", KEYS[1], "version", nextVersion, "payload", ARGV[2])
if tonumber(ARGV[3]) > 0 then
    redis.call("PEXPIRE", KEYS[1], ARGV[3])
else
    redis.call("PERSIST", KEYS[1])
end
return nextVersion
`
)

var _ agent.ExecutionStore = (*Store)(nil)

// Store is an agent.ExecutionStore backed by Redis.
type Store struct {
	client goredis.UniversalClient
	prefix string
	ttl    time.Duration
	create *goredis.Script
	save   *goredis.Script
}

// New returns a Store over a go-redis client. The caller owns the client's
// lifecycle. A nil client does not panic: every operation returns an error.
func New(client goredis.UniversalClient, opts ...Option) *Store {
	cfg := config{prefix: defaultKeyPrefix}
	for _, opt := range opts {
		opt(&cfg)
	}
	return &Store{
		client: client,
		prefix: cfg.prefix,
		ttl:    cfg.ttl,
		create: goredis.NewScript(createScript),
		save:   goredis.NewScript(saveScript),
	}
}

// Create stores a new execution at version 1. The ID must be absent.
func (s *Store) Create(ctx context.Context, execution agent.Execution) (agent.Execution, error) {
	if err := s.check(execution.ID); err != nil {
		return agent.Execution{}, err
	}
	payload, err := record.Marshal(execution)
	if err != nil {
		return agent.Execution{}, err
	}
	created, err := s.create.Run(ctx, s.client, []string{s.key(execution.ID)}, string(payload), s.ttlMillis()).Int64()
	if err != nil {
		return agent.Execution{}, record.Wrap("create", err)
	}
	if created != 1 {
		return agent.Execution{}, record.Conflict("create", execution.ID)
	}
	return record.Unmarshal(payload, execution.ID, 1)
}

// Load returns the current execution.
func (s *Store) Load(ctx context.Context, id string) (agent.Execution, error) {
	if err := s.check(id); err != nil {
		return agent.Execution{}, err
	}
	values, err := s.client.HMGet(ctx, s.key(id), versionField, payloadField).Result()
	if err != nil {
		return agent.Execution{}, record.Wrap("load", err)
	}
	if len(values) != 2 || values[0] == nil || values[1] == nil {
		return agent.Execution{}, record.NotFound(id)
	}
	versionText, okVersion := values[0].(string)
	payload, okPayload := values[1].(string)
	if !okVersion || !okPayload {
		return agent.Execution{}, fmt.Errorf("execution/redis: execution %q has a malformed hash", id)
	}
	n, err := strconv.ParseInt(versionText, 10, 64)
	if err != nil {
		return agent.Execution{}, fmt.Errorf("execution/redis: execution %q has a malformed version: %w", id, err)
	}
	version, err := record.Version(n)
	if err != nil {
		return agent.Execution{}, err
	}
	return record.Unmarshal([]byte(payload), id, version)
}

// Save replaces the execution when expectedVersion is exactly the stored
// version. The comparison and the write run as one Lua script; a stale or
// missing version is a conflict.
func (s *Store) Save(ctx context.Context, execution agent.Execution, expectedVersion uint64) (agent.Execution, error) {
	if err := s.check(execution.ID); err != nil {
		return agent.Execution{}, err
	}
	if err := record.CheckExpected(execution.ID, expectedVersion); err != nil {
		return agent.Execution{}, err
	}
	payload, err := record.Marshal(execution)
	if err != nil {
		return agent.Execution{}, err
	}
	next, err := s.save.Run(ctx, s.client, []string{s.key(execution.ID)},
		strconv.FormatUint(expectedVersion, 10), string(payload), s.ttlMillis()).Int64()
	if err != nil {
		return agent.Execution{}, record.Wrap("save", err)
	}
	if next == 0 {
		return agent.Execution{}, record.Conflict("save", execution.ID)
	}
	version, err := record.Version(next)
	if err != nil {
		return agent.Execution{}, err
	}
	return record.Unmarshal(payload, execution.ID, version)
}

func (s *Store) key(id string) string { return s.prefix + id }

func (s *Store) ttlMillis() int64 { return s.ttl.Milliseconds() }

func (s *Store) check(id string) error {
	if s == nil || s.client == nil {
		return errors.New("execution/redis: client is required")
	}
	return record.ValidateID(id)
}
