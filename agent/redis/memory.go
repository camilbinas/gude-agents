package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/memory"
	goredis "github.com/redis/go-redis/v9"
)

// jsonContentBlock is the JSON envelope for a ContentBlock with a type discriminator.
type jsonContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	Content   string          `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

// jsonMessage is the JSON envelope for an agent.Message.
type jsonMessage struct {
	Role    string             `json:"role"`
	Content []jsonContentBlock `json:"content"`
}

// Compile-time check: RedisMemory implements agent.Memory.
var _ agent.Memory = (*RedisMemory)(nil)

// Compile-time check: RedisMemory implements memory.MemoryManager.
var _ memory.MemoryManager = (*RedisMemory)(nil)

// RedisMemoryOption configures a RedisMemory instance.
type RedisMemoryOption func(*RedisMemory)

// WithTTL sets the TTL for conversation keys. 0 means no expiration.
func WithTTL(d time.Duration) RedisMemoryOption {
	return func(m *RedisMemory) {
		m.ttl = d
	}
}

// WithKeyPrefix sets the key prefix. Default: "gude:memory:"
func WithKeyPrefix(prefix string) RedisMemoryOption {
	return func(m *RedisMemory) {
		m.keyPrefix = prefix
	}
}

// RedisMemory implements agent.Memory using Redis.
// Documented in docs/redis.md — update when changing constructor, options, or methods.
type RedisMemory struct {
	client    *goredis.Client
	ttl       time.Duration
	keyPrefix string
}

// NewRedisMemory creates a new RedisMemory. Pings Redis to verify connectivity.
func NewRedisMemory(opts RedisOptions, mopts ...RedisMemoryOption) (*RedisMemory, error) {
	client := newClient(opts)

	m := &RedisMemory{
		client:    client,
		ttl:       0,
		keyPrefix: "gude:memory:",
	}

	for _, o := range mopts {
		o(m)
	}

	if err := client.Ping(context.Background()).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("redis memory: ping: %w", err)
	}

	return m, nil
}

// Save persists messages for the given conversation ID.
func (m *RedisMemory) Save(ctx context.Context, conversationID string, messages []agent.Message) error {
	jmsgs := make([]jsonMessage, len(messages))
	for i, msg := range messages {
		blocks := make([]jsonContentBlock, len(msg.Content))
		for j, cb := range msg.Content {
			blocks[j] = contentBlockToJSON(cb)
		}
		jmsgs[i] = jsonMessage{
			Role:    string(msg.Role),
			Content: blocks,
		}
	}

	data, err := json.Marshal(jmsgs)
	if err != nil {
		return fmt.Errorf("redis memory: marshal: %w", err)
	}

	key := m.keyPrefix + conversationID
	if err := m.client.Set(ctx, key, data, m.ttl).Err(); err != nil {
		return fmt.Errorf("redis memory: save: %w", err)
	}

	return nil
}

// contentBlockToJSON converts a ContentBlock to its JSON envelope representation.
func contentBlockToJSON(cb agent.ContentBlock) jsonContentBlock {
	switch b := cb.(type) {
	case agent.TextBlock:
		return jsonContentBlock{Type: "text", Text: b.Text}
	case agent.ToolUseBlock:
		return jsonContentBlock{Type: "tool_use", ToolUseID: b.ToolUseID, Name: b.Name, Input: b.Input}
	case agent.ToolResultBlock:
		return jsonContentBlock{Type: "tool_result", ToolUseID: b.ToolUseID, Content: b.Content, IsError: b.IsError}
	default:
		return jsonContentBlock{Type: "unknown"}
	}
}

// Load retrieves messages for the given conversation ID.
func (m *RedisMemory) Load(ctx context.Context, conversationID string) ([]agent.Message, error) {
	key := m.keyPrefix + conversationID
	data, err := m.client.Get(ctx, key).Bytes()
	if err != nil {
		if errors.Is(err, goredis.Nil) {
			return []agent.Message{}, nil
		}
		return nil, fmt.Errorf("redis memory: load: %w", err)
	}

	var jmsgs []jsonMessage
	if err := json.Unmarshal(data, &jmsgs); err != nil {
		return nil, fmt.Errorf("redis memory: unmarshal: %w", err)
	}

	messages := make([]agent.Message, len(jmsgs))
	for i, jm := range jmsgs {
		blocks := make([]agent.ContentBlock, len(jm.Content))
		for j, jcb := range jm.Content {
			blocks[j] = jsonToContentBlock(jcb)
		}
		messages[i] = agent.Message{
			Role:    agent.Role(jm.Role),
			Content: blocks,
		}
	}

	return messages, nil
}

// jsonToContentBlock converts a JSON envelope back to a ContentBlock.
func jsonToContentBlock(jcb jsonContentBlock) agent.ContentBlock {
	switch jcb.Type {
	case "text":
		return agent.TextBlock{Text: jcb.Text}
	case "tool_use":
		return agent.ToolUseBlock{ToolUseID: jcb.ToolUseID, Name: jcb.Name, Input: jcb.Input}
	case "tool_result":
		return agent.ToolResultBlock{ToolUseID: jcb.ToolUseID, Content: jcb.Content, IsError: jcb.IsError}
	default:
		return agent.TextBlock{}
	}
}

// List returns all conversation IDs by scanning keys with the configured prefix.
func (m *RedisMemory) List(ctx context.Context) ([]string, error) {
	pattern := m.keyPrefix + "*"
	var ids []string
	var cursor uint64

	for {
		keys, next, err := m.client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return nil, fmt.Errorf("redis memory: list: %w", err)
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
func (m *RedisMemory) Delete(ctx context.Context, conversationID string) error {
	key := m.keyPrefix + conversationID
	if err := m.client.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("redis memory: delete: %w", err)
	}
	return nil
}

// Close closes the underlying Redis client.
func (m *RedisMemory) Close() error {
	return m.client.Close()
}
