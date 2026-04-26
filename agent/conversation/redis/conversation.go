package redis

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
	goredis "github.com/redis/go-redis/v9"
)

// Compile-time check: RedisMemory implements agent.Conversation.
var _ agent.Conversation = (*RedisConversation)(nil)

// Compile-time check: RedisMemory implements conversation.ConversationManager.
var _ conversation.ConversationManager = (*RedisConversation)(nil)

// RedisConversationOption configures a RedisMemory instance.
type RedisConversationOption func(*RedisConversation)

// WithTTL sets the TTL for conversation keys. 0 means no expiration.
func WithTTL(d time.Duration) RedisConversationOption {
	return func(m *RedisConversation) {
		m.ttl = d
	}
}

// WithKeyPrefix sets the key prefix. Default: "gude:"
func WithKeyPrefix(prefix string) RedisConversationOption {
	return func(m *RedisConversation) {
		m.keyPrefix = prefix
	}
}

// RedisConversation implements agent.Conversation using Redis.
// Documented in docs/redis.md — update when changing constructor, options, or methods.
type RedisConversation struct {
	client    *goredis.Client
	ttl       time.Duration
	keyPrefix string
}

// New creates a new RedisMemory. Pings Redis to verify connectivity.
func New(opts RedisOptions, mopts ...RedisConversationOption) (*RedisConversation, error) {
	client := newClient(opts)

	m := &RedisConversation{
		client:    client,
		ttl:       0,
		keyPrefix: "gude:",
	}

	for _, o := range mopts {
		o(m)
	}

	if err := client.Ping(context.Background()).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("redis conversation: ping: %w", err)
	}

	return m, nil
}

// Save persists messages for the given conversation ID.
func (m *RedisConversation) Save(ctx context.Context, conversationID string, messages []agent.Message) error {
	data, err := conversation.MarshalMessages(messages)
	if err != nil {
		return fmt.Errorf("redis conversation: marshal: %w", err)
	}
	key := m.keyPrefix + conversationID
	if err := m.client.Set(ctx, key, data, m.ttl).Err(); err != nil {
		return fmt.Errorf("redis conversation: save: %w", err)
	}
	return nil
}

// Load retrieves messages for the given conversation ID.
func (m *RedisConversation) Load(ctx context.Context, conversationID string) ([]agent.Message, error) {
	key := m.keyPrefix + conversationID
	data, err := m.client.Get(ctx, key).Bytes()
	if err != nil {
		if errors.Is(err, goredis.Nil) {
			return []agent.Message{}, nil
		}
		return nil, fmt.Errorf("redis conversation: load: %w", err)
	}
	messages, err := conversation.UnmarshalMessages(data)
	if err != nil {
		return nil, fmt.Errorf("redis conversation: unmarshal: %w", err)
	}
	return messages, nil
}

// List returns all conversation IDs by scanning keys with the configured prefix.
func (m *RedisConversation) List(ctx context.Context) ([]string, error) {
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

// Delete removes a conversation key from Redis.
func (m *RedisConversation) Delete(ctx context.Context, conversationID string) error {
	key := m.keyPrefix + conversationID
	if err := m.client.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("redis conversation: delete: %w", err)
	}
	return nil
}

// Close closes the underlying Redis client.
func (m *RedisConversation) Close() error {
	return m.client.Close()
}
