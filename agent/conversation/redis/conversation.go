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

// Compile-time interface checks.
var _ agent.Conversation = (*Conversation)(nil)
var _ conversation.ConversationManager = (*Conversation)(nil)

// Option configures a Conversation instance.
type Option func(*config)

type config struct {
	ttl       time.Duration
	keyPrefix string
}

// WithTTL sets the TTL for conversation keys. 0 means no expiration.
func WithTTL(d time.Duration) Option {
	return func(c *config) {
		c.ttl = d
	}
}

// WithKeyPrefix sets the key prefix. Default: "gude:"
func WithKeyPrefix(prefix string) Option {
	return func(c *config) {
		if prefix != "" {
			c.keyPrefix = prefix
		}
	}
}

// Conversation implements agent.Conversation using Redis.
type Conversation struct {
	client    *goredis.Client
	ttl       time.Duration
	keyPrefix string
}

// New creates a new Redis conversation store. Pings Redis to verify connectivity.
func New(opts Options, mopts ...Option) (*Conversation, error) {
	cfg := &config{
		ttl:       0,
		keyPrefix: "gude:",
	}
	for _, o := range mopts {
		o(cfg)
	}

	client := newClient(opts)

	if err := client.Ping(context.Background()).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("redis conversation: ping: %w", err)
	}

	return &Conversation{
		client:    client,
		ttl:       cfg.ttl,
		keyPrefix: cfg.keyPrefix,
	}, nil
}

// Save persists messages for the given conversation ID.
func (m *Conversation) Save(ctx context.Context, conversationID string, messages []agent.Message) error {
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
func (m *Conversation) Load(ctx context.Context, conversationID string) ([]agent.Message, error) {
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

// Delete removes a conversation key from Redis.
func (m *Conversation) Delete(ctx context.Context, conversationID string) error {
	key := m.keyPrefix + conversationID
	if err := m.client.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("redis conversation: delete: %w", err)
	}
	return nil
}

// Close closes the underlying Redis client.
func (m *Conversation) Close() error {
	return m.client.Close()
}
