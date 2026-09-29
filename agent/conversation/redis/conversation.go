package redis

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
	goredis "github.com/redis/go-redis/v9"
)

var _ agent.ConversationManager = (*Conversation)(nil)

type Option func(*config)

type config struct {
	ttl       time.Duration
	keyPrefix string
}

// WithTTL sets the TTL for conversation keys. Zero means no expiration.
func WithTTL(d time.Duration) Option { return func(c *config) { c.ttl = d } }

// WithKeyPrefix sets the key prefix. Default: "gude:".
func WithKeyPrefix(prefix string) Option {
	return func(c *config) {
		if prefix != "" {
			c.keyPrefix = prefix
		}
	}
}

// Conversation implements agent.ConversationManager using Redis hashes.
type Conversation struct {
	client    *goredis.Client
	ttl       time.Duration
	keyPrefix string
}

// New creates a Redis conversation store and verifies connectivity.
func New(opts Options, mopts ...Option) (*Conversation, error) {
	cfg := &config{keyPrefix: "gude:"}
	for _, o := range mopts {
		o(cfg)
	}
	client := newClient(opts)
	if err := client.Ping(context.Background()).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("redis conversation: ping: %w", err)
	}
	return &Conversation{client: client, ttl: cfg.ttl, keyPrefix: cfg.keyPrefix}, nil
}

var saveScript = goredis.NewScript(`
local exists = redis.call('EXISTS', KEYS[1])
local expected = ARGV[1]
if exists == 0 then
  if expected ~= '0' then return -1 end
else
  local current = redis.call('HGET', KEYS[1], 'revision')
  if not current then current = '0' end
  if current ~= expected then return -1 end
end
redis.call('HSET', KEYS[1], 'messages', ARGV[2])
local next = redis.call('HINCRBY', KEYS[1], 'revision', 1)
local ttl = tonumber(ARGV[3])
if ttl > 0 then
  redis.call('PEXPIRE', KEYS[1], ttl)
else
  redis.call('PERSIST', KEYS[1])
end
return next
`)

// Save atomically persists messages when expectedRevision matches.
func (m *Conversation) Save(ctx context.Context, conversationID string, messages []agent.Message, expectedRevision uint64) (uint64, error) {
	if expectedRevision >= uint64(1<<63-1) {
		return 0, fmt.Errorf("redis conversation: revision %d exceeds Redis signed integer range", expectedRevision)
	}
	data, err := conversation.MarshalMessages(messages)
	if err != nil {
		return 0, fmt.Errorf("redis conversation: marshal: %w", err)
	}
	result, err := saveScript.Run(ctx, m.client, []string{m.keyPrefix + conversationID}, expectedRevision, data, m.ttl.Milliseconds()).Int64()
	if err != nil {
		return 0, fmt.Errorf("redis conversation: save: %w", err)
	}
	if result < 0 {
		return 0, fmt.Errorf("redis conversation: save %q: %w", conversationID, agent.ErrConversationConflict)
	}
	return uint64(result), nil
}

// Load returns a snapshot. Missing keys return revision zero and a non-nil
// empty message slice.
func (m *Conversation) Load(ctx context.Context, conversationID string) (agent.ConversationSnapshot, error) {
	values, err := m.client.HGetAll(ctx, m.keyPrefix+conversationID).Result()
	if err != nil {
		return agent.ConversationSnapshot{}, fmt.Errorf("redis conversation: load: %w", err)
	}
	if len(values) == 0 {
		return agent.ConversationSnapshot{Messages: []agent.Message{}}, nil
	}
	data, ok := values["messages"]
	if !ok {
		return agent.ConversationSnapshot{}, errors.New("redis conversation: load: messages field missing")
	}
	revision := uint64(0)
	if revisionValue, exists := values["revision"]; exists {
		revision, err = strconv.ParseUint(revisionValue, 10, 64)
		if err != nil {
			return agent.ConversationSnapshot{}, fmt.Errorf("redis conversation: load revision: %w", err)
		}
	}
	messages, err := conversation.UnmarshalMessages([]byte(data))
	if err != nil {
		return agent.ConversationSnapshot{}, fmt.Errorf("redis conversation: unmarshal: %w", err)
	}
	return agent.ConversationSnapshot{Messages: messages, Revision: revision}, nil
}

// List returns conversation IDs matching the configured prefix.
func (m *Conversation) List(ctx context.Context) ([]string, error) {
	pattern := m.keyPrefix + "*"
	var ids []string
	var cursor uint64
	for {
		keys, next, err := m.client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return nil, fmt.Errorf("redis conversation: list: %w", err)
		}
		for _, key := range keys {
			ids = append(ids, strings.TrimPrefix(key, m.keyPrefix))
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return ids, nil
}

// Delete removes a conversation key.
func (m *Conversation) Delete(ctx context.Context, conversationID string) error {
	if err := m.client.Del(ctx, m.keyPrefix+conversationID).Err(); err != nil {
		return fmt.Errorf("redis conversation: delete: %w", err)
	}
	return nil
}

// Close closes the Redis client.
func (m *Conversation) Close() error { return m.client.Close() }
