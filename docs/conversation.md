# Conversation System

The conversation system gives agents multi-turn conversation support. It handles loading previous messages before each LLM call and saving the updated conversation afterward. The `conversation` package ships with an in-memory store, six persistent drivers, and four composable strategies that control what gets sent to the model.

## Conversation Interface

Every conversation implementation satisfies this interface from the `agent` package:

```go
type Conversation interface {
    Load(ctx context.Context, conversationID string) ([]Message, error)
    Save(ctx context.Context, conversationID string, messages []Message) error
}
```

`Load` retrieves the message history for a conversation. `Save` persists the full message slice (the agent calls this after each successful invocation). You wire conversation into an agent with the `WithConversation` option:

```go
a, err := agent.Default(
    provider,
    prompt.Text("You are a helpful assistant."),
    nil,
    agent.WithConversation(myConversation, "conversation-123"),
)
```

For HTTP servers where a single agent serves multiple conversations, use `WithSharedConversation` instead and pass the conversation ID per-request via context:

```go
a, err := agent.New(provider, instructions, tools,
    agent.WithSharedConversation(myConversation),
)

// In each HTTP handler:
ctx := agent.WithConversationID(r.Context(), req.ConversationID)
result, _, err := a.Invoke(ctx, req.Message)
```

See [HTTP & Multi-Tenant Environments](http.md) for the full pattern.

## ConversationManager Interface

`ConversationManager` extends `Conversation` with lifecycle operations for listing and deleting conversations:

```go
type ConversationManager interface {
    agent.Conversation
    List(ctx context.Context) ([]string, error)
    Delete(ctx context.Context, conversationID string) error
}
```

`List` returns all conversation IDs in the store. `Delete` removes a conversation by ID (returns nil if not found). The in-memory `InMemory` implements `ConversationManager`.

## InMemory Store

```go
func NewInMemory() *InMemory
```

`NewInMemory` creates an empty in-memory store backed by a `map[string][]agent.Message`. It's thread-safe — reads use `sync.RWMutex` read locks, writes use exclusive locks. Both `Load` and `Save` deep-copy the message slice, so callers can't accidentally mutate stored data.

`InMemory` implements `ConversationManager`, so you can list and delete conversations:

```go
store := conversation.NewInMemory()

// ... use the store with an agent ...

ids, err := store.List(ctx)
fmt.Println("Conversations:", ids)

err = store.Delete(ctx, "old-conversation")
```

## Strategies

Strategies are `Conversation` implementations that wrap another `Conversation` and transform messages on `Load`, `Save`, or both. They follow a middleware pattern — you compose them by nesting one inside another.

### Window

```go
func NewWindow(inner Conversation, n int) *Window
```

Keeps only the last `n` messages on `Load`. `Save` passes through to the inner store unchanged. Use this to cap context length with a simple sliding window.

- `Load`: retrieves all messages from the inner store, returns only the last `n`
- `Save`: delegates directly to the inner store

### Filter

```go
func NewFilter(inner Conversation) *Filter
```

Strips `ToolUseBlock` and `ToolResultBlock` from messages on `Load`, keeping only `TextBlock` content. Messages with no remaining content blocks are omitted entirely. `Save` passes through unchanged.

This is useful when you want the model to see conversation text but not raw tool call/result noise.

### Summary

```go
func NewSummary(inner Conversation, threshold int, fn SummaryFunc, opts ...SummaryOption) (*Summary, error)
```

Automatically summarizes old messages when the conversation grows beyond `threshold` turns (user+assistant exchanges). When triggered, older messages are replaced with a compact summary turn, keeping the context window manageable without losing important history.

```go
type SummaryFunc func(ctx context.Context, messages []Message) ([2]Message, error)
```

The `SummaryFunc` receives the messages to summarize and returns a `[2]Message` — a user message with the summary text followed by an assistant acknowledgment.

#### DefaultSummaryFunc

```go
func DefaultSummaryFunc(provider Provider) SummaryFunc
```

Returns a batteries-included `SummaryFunc` that uses an LLM provider to condense messages into a concise paragraph. It formats all messages as text, sends them to the provider with a summarization prompt, and returns the result as a user+assistant turn. Pass this to `NewSummary` so you don't have to write your own.

#### NewSummaryFunc

```go
func NewSummaryFunc(provider Provider, systemPrompt string) SummaryFunc
```

Returns a `SummaryFunc` that uses the given provider and a custom system prompt to condense messages. Use this when you want to control what the summarizer focuses on — for example, preserving domain-specific details like table names, metrics, or decisions.

```go
s, err := conversation.NewSummary(store, 10,
    conversation.NewSummaryFunc(provider,
        "Summarize this analytics conversation. Preserve table names, "+
        "domain metrics, and specific numbers.",
    ),
)
```

`DefaultSummaryFunc` is built on top of `NewSummaryFunc` with a generic summarization prompt.

#### WithSummaryLogger

```go
func WithSummaryLogger(l Logger) SummaryOption
```

Sets a logger for error reporting during background summarization. Since summarization runs in a goroutine, errors can't be returned to the caller — the logger captures load failures, summarization errors, and save failures.

```go
type Logger interface {
    Printf(format string, v ...any)
}
```

#### WithPreserveRecentMessages

```go
func WithPreserveRecentMessages(n int) SummaryOption
```

Keeps the last `n` turns (user+assistant exchanges) out of the `SummaryFunc`. When summarization triggers, only messages before the last `n` turns are passed to the summarizer — the tail is always preserved verbatim after the summary in the result. Defaults to 0 (summarize all messages up to the cutoff).

If `n` turns is greater than or equal to the number of messages at trigger time, summarization is skipped entirely for that cycle.

```go
s, err := conversation.NewSummary(
    store, 10,
    conversation.DefaultSummaryFunc(provider),
    conversation.WithPreserveRecentMessages(2),
)
if err != nil {
    log.Fatal(err)
}
// Threshold of 10 turns (20 messages internally), trigger at 16 messages.
// When triggered with 16 messages: SummaryFunc receives msgs[0:12],
// result is [summary turn, msg12, msg13, msg14, msg15].
```

#### WithTriggerThreshold

```go
func WithTriggerThreshold(pct int) SummaryOption
```

Sets the percentage of the threshold at which summarization triggers. Defaults to 80. The value must be between 1 and 100; `NewSummary` returns an error otherwise.

```go
s, err := conversation.NewSummary(
    store, 10,
    conversation.DefaultSummaryFunc(provider),
    conversation.WithTriggerThreshold(60), // trigger at 60% instead of 80%
)
if err != nil {
    log.Fatal(err)
}
```

#### WithSummaryTimeout

```go
func WithSummaryTimeout(d time.Duration) SummaryOption
```

Sets a per-summarization timeout. Each background goroutine gets a context with this deadline. If the LLM call doesn't complete in time, the summarization is cancelled. Default: no timeout.

### TokenSummary

```go
func NewTokenSummary(inner Conversation, tokenThreshold int, fn SummaryFunc, opts ...TokenSummaryOption) (*TokenSummary, error)
```

Like `Summary`, but triggers based on actual provider-reported input token count instead of message count. The agent loop attaches cumulative `TokenUsage` to the context passed to `Save` (via `agent.WithTokenUsage`), and `TokenSummary` reads it to decide when to summarize. When `Save` is called outside the agent loop (no token usage in context), summarization is not triggered.

```go
s, err := conversation.NewTokenSummary(store, 100_000,
    conversation.DefaultSummaryFunc(provider),
    conversation.WithTokenPreserveRecentMessages(3),
    conversation.WithTokenTriggerThreshold(80),
)
```

| Option | Description | Default |
|---|---|---|
| `WithTokenSummaryLogger(l)` | Logger for background summarization errors | nil |
| `WithTokenPreserveRecentMessages(n)` | Turns to keep out of summarization | 0 |
| `WithTokenTriggerThreshold(pct)` | Percentage of token threshold to trigger at | 80 |
| `WithTokenSummaryTimeout(d)` | Per-summarization timeout | no timeout |

## Composable Middleware Pattern

Strategies wrap each other like middleware. Each strategy takes an `inner Conversation` as its first argument, so you build a pipeline by nesting constructors:

```go
store    := conversation.NewInMemory()          // base storage
windowed := conversation.NewWindow(store, 20) // keep last 20 messages
filtered := conversation.NewFilter(windowed)  // strip tool blocks
```

`Save` writes through to the base store unchanged. `Load` reads from the base store and transforms on the way back up — Window trims, Filter strips tool blocks.

## Code Example

This example composes Window and Filter strategies to keep the last 20 messages with tool blocks stripped:

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
)

func main() {
	provider, err := bedrock.Standard()
	if err != nil {
		log.Fatal(err)
	}

	// In-memory store as the base layer.
	store := conversation.NewInMemory()

	// Compose strategies: Window keeps last 20, Filter strips tool blocks.
	windowed := conversation.NewWindow(store, 20)
	filtered := conversation.NewFilter(windowed)

	a, err := agent.Default(
		provider,
		prompt.Text("You are a helpful assistant. Be concise."),
		nil,
		agent.WithConversation(filtered, "demo-conversation"),
	)
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()

	result, _, err := a.Invoke(ctx, "My name is Alice. Remember that.")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Turn 1:", result)

	result, _, err = a.Invoke(ctx, "What is my name?")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Turn 2:", result)

	// ConversationManager: list and delete conversations.
	ids, _ := store.List(ctx)
	fmt.Printf("\nConversations in store: %v\n", ids)

	_ = store.Delete(ctx, "demo-conversation")
	ids, _ = store.List(ctx)
	fmt.Printf("After delete: %v\n", ids)
}
```

## Persistent Conversation Drivers

For production use cases where conversation history must survive process restarts, persistent drivers are available as separate packages. All implement both `agent.Conversation` and `conversation.ConversationManager` (List, Delete).

### Provider Comparison

| Feature | In-Memory | Disk | SQLite | PostgreSQL | Redis | DynamoDB | S3 |
|---|---|---|---|---|---|---|---|
| **Package** | `agent/conversation` | `agent/conversation/disk` | `agent/conversation/sqlite` | `agent/conversation/postgres` | `agent/conversation/redis` | `agent/conversation/dynamodb` | `agent/conversation/s3` |
| **Persistence** | No | File per conversation | Single database file | PostgreSQL server | Redis server | AWS DynamoDB table | S3-compatible bucket |
| **External service** | None | None | None | PostgreSQL | Redis | DynamoDB | AWS S3 |
| **TTL / auto-expiry** | — | — | — | — | ✓ | ✓ | Via lifecycle rules |
| **Key prefix** | — | — | — | — | ✓ | ✓ | ✓ |
| **Custom endpoint** | — | — | — | — | — | ✓ | ✓ |
| **ACID transactions** | — | Atomic rename | ✓ (WAL mode) | ✓ (full) | — | ✓ (single-item) | — |
| **Concurrent access** | `sync.RWMutex` | `sync.RWMutex` | SQLite WAL | MVCC | Redis single-thread | DynamoDB | S3 |
| **Size limits** | Process memory | Filesystem | ~281 TB (SQLite max) | 1 GB per field | Redis `maxmemory` | 400 KB per item | 50 TB per object |
| **Best for** | Tests, short-lived | CLI tools, dev | Local apps, single-node | Production, multi-node | Multi-process, caching | Serverless, AWS-native | AWS S3, archival |

### Redis — agent/conversation/redis

Import: `github.com/camilbinas/gude-agents/agent/conversation/redis`

Stores conversation history as JSON in Redis string keys. Requires a running Redis instance.

```go
mem, err := redismemory.New(
    redismemory.RedisOptions{Addr: "localhost:6379"},
    redismemory.WithTTL(24*time.Hour),
    redismemory.WithKeyPrefix("myapp:"),
)
```

Options: `WithTTL(d time.Duration)`, `WithKeyPrefix(prefix string)`.

See [Redis Providers](redis.md) for full documentation.

### S3 — agent/conversation/s3

Import: `github.com/camilbinas/gude-agents/agent/conversation/s3`

Stores conversation history as JSON objects in Amazon S3. Uses the AWS SDK v2 for authentication and API calls.

```go
cfg, _ := awsconfig.LoadDefaultConfig(ctx)
mem, err := s3memory.New(cfg, "my-bucket",
    s3memory.WithKeyPrefix("conversations/"),
)
```

Options: `WithKeyPrefix(prefix string)`, `WithEndpoint(url string)`, `WithPathStyle(enabled bool)`.

No network calls are made at construction time — connectivity errors surface on the first `Save`/`Load` call.

### DynamoDB — agent/conversation/dynamodb

Import: `github.com/camilbinas/gude-agents/agent/conversation/dynamodb`

Stores conversation history as items in an Amazon DynamoDB table. The table must be created by the caller with `conversation_id` (String) as the partition key.

```go
cfg, _ := awsconfig.LoadDefaultConfig(ctx)
mem, err := dynamomemory.New(cfg, "my-conversations-table",
    dynamomemory.WithTTL(7*24*time.Hour),
    dynamomemory.WithKeyPrefix("prod:"),
)
```

Options: `WithKeyPrefix(prefix string)`, `WithTTL(d time.Duration)`, `WithTTLAttribute(attr string)`, `WithPartitionKey(attr string)`, `WithEndpoint(url string)`.

> **Item size limit:** DynamoDB items are capped at 400 KB. For long-running conversations with large tool results, pair this driver with `conversation.NewWindow` or `conversation.NewSummary` to bound item size.

> **List performance:** `List` performs a full-table Scan. Avoid calling it in hot paths on large tables.

### SQLite — agent/conversation/sqlite

Import: `github.com/camilbinas/gude-agents/agent/conversation/sqlite`

Stores conversation history as rows in a SQLite database, with messages serialized as JSON. Uses [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite), a pure-Go SQLite implementation — no CGo, cross-compiles cleanly.

```go
mem, err := sqlitememory.New("/tmp/agent-conversation.db")

// In-memory (useful for testing):
mem, err := sqlitememory.New(":memory:")

// With options:
mem, err := sqlitememory.New("agent.db",
    sqlitememory.WithTableName("chats"),
    sqlitememory.WithBusyTimeout(10*time.Second),
)
```

Options: `WithTableName(name string)` (default: `"conversations"`), `WithBusyTimeout(d time.Duration)` (default: 5s).

The database and table are created automatically. WAL journal mode is enabled for concurrent read performance. `List` returns conversations ordered by most recently updated first.

### PostgreSQL — agent/conversation/postgres

Import: `github.com/camilbinas/gude-agents/agent/conversation/postgres`

Stores conversation history as rows in a PostgreSQL table, with messages stored as JSONB. Uses [pgx/v5](https://github.com/jackc/pgx), the standard pure-Go PostgreSQL driver.

```go
pool, err := pgxpool.New(ctx, "postgres://user:pass@localhost:5432/mydb")
mem, err := pgmemory.New(pool)

// Auto-create the table (development):
mem, err := pgmemory.New(pool, pgmemory.WithAutoMigrate())

// With options:
mem, err := pgmemory.New(pool,
    pgmemory.WithTableName("agent_conversations"),
    pgmemory.WithAutoMigrate(),
)
```

Options: `WithTableName(name string)` (default: `"conversations"`), `WithAutoMigrate()` (off by default).

By default, the table must already exist with the expected schema (see package doc). Use `WithAutoMigrate` for development or when the DB user has CREATE TABLE permissions. PostgreSQL's MVCC provides full ACID transactions and handles concurrent access from multiple processes. `List` returns conversations ordered by most recently updated first.

### Disk — agent/conversation/disk

Import: `github.com/camilbinas/gude-agents/agent/conversation/disk`

Stores each conversation as a JSON file in a directory on the local filesystem. Uses atomic writes (write to temp file, then rename) for crash safety.

```go
mem, err := diskmemory.New("/tmp/agent-conversations")
// Creates files like /tmp/agent-conversations/conv-123.json
```

Conversation IDs are sanitized to prevent path traversal.

## See Also

- [Agent API Reference](agent-api.md) — `WithConversation` option and agent loop behavior
- [Redis Providers](redis.md) — persistent Redis-backed conversation store (`agent/conversation/redis`) and Redis vector store (`agent/rag/redis`)
- [Message Types](message-types.md) — `Message`, `ContentBlock`, `TextBlock`, `ToolUseBlock`, `ToolResultBlock`
- [Getting Started](getting-started.md) — installation and first agent
