// Package redis provides an append-only Redis-backed conversation store.
package redis

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
	goredis "github.com/redis/go-redis/v9"
)

var (
	_ agent.ConversationManager = (*Conversation)(nil)
	_ agent.ContextStateStore   = (*Conversation)(nil)
)

type Option func(*config)
type config struct {
	ttl       time.Duration
	keyPrefix string
}

func WithTTL(d time.Duration) Option { return func(c *config) { c.ttl = d } }
func WithKeyPrefix(prefix string) Option {
	return func(c *config) {
		if prefix != "" {
			c.keyPrefix = prefix
		}
	}
}

type Conversation struct {
	client    *goredis.Client
	ttl       time.Duration
	keyPrefix string
}

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

// keyBase encodes the user ID before placing it in a hash tag. Every key
// touched by append/state Lua has exactly the same cluster slot, while caller
// controlled braces cannot alter that slot.
func (m *Conversation) keyBase(id string) string {
	return m.keyPrefix + "conv:{" + base64.RawURLEncoding.EncodeToString([]byte(id)) + "}"
}
func (m *Conversation) metaKey(id string) string   { return m.keyBase(id) + ":meta" }
func (m *Conversation) streamKey(id string) string { return m.keyBase(id) + ":messages" }

var appendScript = goredis.NewScript(`
local meta=KEYS[1]
local stream=KEYS[2]
local expected=ARGV[1]
local count=tonumber(ARGV[2])
local ttl=tonumber(ARGV[3])
local current=redis.call('HGET',meta,'revision')
if not current then current='0' end
if current ~= expected then return {-1,0} end
local last=redis.call('HGET',meta,'last_sequence')
if not last then last='0' end
if count == 0 then return {tonumber(current),tonumber(last)} end
for i=1,count do
  local seq=tonumber(last)+i
  redis.call('XADD',stream,tostring(seq)..'-0','message',ARGV[3+i])
end
local next=tonumber(current)+1
local nextlast=tonumber(last)+count
redis.call('HSET',meta,'revision',next,'last_sequence',nextlast)
if ttl > 0 then redis.call('PEXPIRE',meta,ttl); redis.call('PEXPIRE',stream,ttl) else redis.call('PERSIST',meta); redis.call('PERSIST',stream) end
return {next,nextlast}
`)

// context-state map is stored in metadata. Its individual namespace revisions
// are checked independently of canonical revision/last_sequence.
var saveStateScript = goredis.NewScript(`
local meta=KEYS[1]
local stream=KEYS[2]
local field=ARGV[1]
local revfield=ARGV[2]
local expected=ARGV[3]
local data=ARGV[4]
local ttl=tonumber(ARGV[5])
local current=redis.call('HGET',meta,revfield)
if not current then current='0' end
if current ~= expected then return -1 end
local next=tonumber(current)+1
redis.call('HSET',meta,field,data,revfield,next)
-- Metadata, canonical events, and derived state form one conversation. Refresh
-- (or remove) the expiry together so a live cursor cannot outlast its stream.
if ttl > 0 then redis.call('PEXPIRE',meta,ttl); redis.call('PEXPIRE',stream,ttl) else redis.call('PERSIST',meta); redis.call('PERSIST',stream) end
return next
`)

func (m *Conversation) stateFields(key string) (string, string) {
	encoded := base64.RawURLEncoding.EncodeToString([]byte(key))
	return "context:" + encoded, "context_revision:" + encoded
}
func marshalOne(message agent.Message) (string, error) {
	raw, err := conversation.MarshalMessages([]agent.Message{message})
	return string(raw), err
}

func (m *Conversation) Load(ctx context.Context, id string) (agent.ConversationSnapshot, error) {
	return m.LoadAfter(ctx, id, 0)
}
func (m *Conversation) LoadAfter(ctx context.Context, id string, after uint64) (agent.ConversationSnapshot, error) {
	meta, err := m.client.HMGet(ctx, m.metaKey(id), "revision", "last_sequence").Result()
	if err != nil {
		return agent.ConversationSnapshot{}, fmt.Errorf("redis conversation: load metadata: %w", err)
	}
	if meta[0] == nil && meta[1] == nil {
		return agent.ConversationSnapshot{Messages: []agent.Message{}}, nil
	}
	parse := func(v any) (uint64, error) {
		if v == nil {
			return 0, nil
		}
		return strconv.ParseUint(fmt.Sprint(v), 10, 64)
	}
	rev, err := parse(meta[0])
	if err != nil {
		return agent.ConversationSnapshot{}, fmt.Errorf("redis conversation: revision: %w", err)
	}
	last, err := parse(meta[1])
	if err != nil {
		return agent.ConversationSnapshot{}, fmt.Errorf("redis conversation: last sequence: %w", err)
	}
	start := "-"
	if after > 0 {
		start = "(" + strconv.FormatUint(after, 10) + "-0"
	}
	entries, err := m.client.XRange(ctx, m.streamKey(id), start, "+").Result()
	if errors.Is(err, goredis.Nil) {
		entries = nil
		err = nil
	}
	if err != nil {
		return agent.ConversationSnapshot{}, fmt.Errorf("redis conversation: load range: %w", err)
	}
	messages := make([]agent.Message, 0, len(entries))
	for _, e := range entries {
		raw, ok := e.Values["message"].(string)
		if !ok {
			return agent.ConversationSnapshot{}, fmt.Errorf("redis conversation: stream message %s missing", e.ID)
		}
		one, err := conversation.UnmarshalMessages([]byte(raw))
		if err != nil || len(one) != 1 {
			if err == nil {
				err = errors.New("expected one message")
			}
			return agent.ConversationSnapshot{}, fmt.Errorf("redis conversation: decode %s: %w", e.ID, err)
		}
		messages = append(messages, one[0])
	}
	return agent.ConversationSnapshot{Messages: messages, Revision: rev, LastSequence: last}, nil
}
func (m *Conversation) Append(ctx context.Context, id string, msgs []agent.Message, expected uint64) (agent.ConversationCursor, error) {
	if expected >= uint64(1<<63-1) {
		return agent.ConversationCursor{}, fmt.Errorf("redis conversation: revision %d exceeds Redis signed range", expected)
	}
	args := make([]any, 0, len(msgs)+3)
	args = append(args, expected, len(msgs), m.ttl.Milliseconds())
	for _, msg := range msgs {
		raw, err := marshalOne(msg)
		if err != nil {
			return agent.ConversationCursor{}, fmt.Errorf("redis conversation: marshal message: %w", err)
		}
		args = append(args, raw)
	}
	result, err := appendScript.Run(ctx, m.client, []string{m.metaKey(id), m.streamKey(id)}, args...).Int64Slice()
	if err != nil {
		return agent.ConversationCursor{}, fmt.Errorf("redis conversation: append: %w", err)
	}
	if len(result) != 2 {
		return agent.ConversationCursor{}, errors.New("redis conversation: malformed append response")
	}
	if result[0] < 0 {
		return agent.ConversationCursor{}, fmt.Errorf("redis conversation: append %q: %w", id, agent.ErrConversationConflict)
	}
	return agent.ConversationCursor{Revision: uint64(result[0]), LastSequence: uint64(result[1])}, nil
}
func (m *Conversation) LoadContextState(ctx context.Context, id, key string) (agent.ContextStateSnapshot, error) {
	field, revfield := m.stateFields(key)
	values, err := m.client.HMGet(ctx, m.metaKey(id), field, revfield).Result()
	if err != nil {
		return agent.ContextStateSnapshot{}, fmt.Errorf("redis conversation: load context state: %w", err)
	}
	if values[0] == nil {
		return agent.ContextStateSnapshot{}, nil
	}
	raw, ok := values[0].(string)
	if !ok {
		return agent.ContextStateSnapshot{}, errors.New("redis conversation: context state is not string")
	}
	rev := uint64(0)
	if values[1] != nil {
		rev, err = strconv.ParseUint(fmt.Sprint(values[1]), 10, 64)
		if err != nil {
			return agent.ContextStateSnapshot{}, err
		}
	}
	return agent.ContextStateSnapshot{Data: json.RawMessage(raw), Revision: rev}, nil
}
func (m *Conversation) SaveContextState(ctx context.Context, id, key string, data json.RawMessage, expected uint64) (uint64, error) {
	if !json.Valid(data) {
		return 0, fmt.Errorf("redis conversation: context state %q is invalid JSON", key)
	}
	field, revfield := m.stateFields(key)
	result, err := saveStateScript.Run(ctx, m.client, []string{m.metaKey(id), m.streamKey(id)}, field, revfield, expected, string(data), m.ttl.Milliseconds()).Int64()
	if err != nil {
		return 0, fmt.Errorf("redis conversation: save context state: %w", err)
	}
	if result < 0 {
		return 0, fmt.Errorf("redis conversation: context state %q: %w", key, agent.ErrContextStateConflict)
	}
	return uint64(result), nil
}
func (m *Conversation) List(ctx context.Context) ([]string, error) {
	pattern := m.keyPrefix + "conv:*:meta"
	var ids []string
	var cursor uint64
	for {
		keys, next, err := m.client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return nil, fmt.Errorf("redis conversation: list: %w", err)
		}
		for _, key := range keys {
			values, err := m.client.HGet(ctx, key, "revision").Result()
			if err != nil || values == "0" {
				continue
			}
			base := strings.TrimSuffix(strings.TrimPrefix(key, m.keyPrefix+"conv:{"), "}:meta")
			raw, err := base64.RawURLEncoding.DecodeString(base)
			if err == nil {
				ids = append(ids, string(raw))
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return ids, nil
}
func (m *Conversation) Delete(ctx context.Context, id string) error {
	if err := m.client.Del(ctx, m.metaKey(id), m.streamKey(id)).Err(); err != nil {
		return fmt.Errorf("redis conversation: delete: %w", err)
	}
	return nil
}
func (m *Conversation) Close() error { return m.client.Close() }
