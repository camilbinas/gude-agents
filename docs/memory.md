# Memory System

The memory system gives agents multi-turn conversation support. It handles loading previous messages before each LLM call and saving the updated conversation afterward. The `memory` package ships with an in-memory store and four composable strategies that control what gets sent to the model.

## Memory Interface

Every memory implementation satisfies this interface from the `agent` package:

```go
type Memory interface {
    Load(ctx context.Context, conversationID string) ([]Message, error)
    Save(ctx context.Context, conversationID string, messages []Message) error
}
```

`Load` retrieves the message history for a conversation. `Save` persists the full message slice (the agent calls this after each successful invocation). You wire memory into an agent with the `WithMemory` option:

```go
a, err := agent.Default(
    provider,
    prompt.Text("You are a helpful assistant."),
    nil,
    agent.WithMemory(myMemory, "conversation-123"),
)
```

For HTTP servers where a single agent serves multiple conversations, use `WithSharedMemory` instead and pass the conversation ID per-request via context:

```go
a, err := agent.New(provider, instructions, tools,
    agent.WithSharedMemory(myMemory),
)

// In each HTTP handler:
ctx := agent.WithConversationID(r.Context(), req.ConversationID)
result, _, err := a.Invoke(ctx, req.Message)
```

See [HTTP & Multi-Tenant Environments](http.md) for the full pattern.

## MemoryManager Interface

`MemoryManager` extends `Memory` with lifecycle operations for listing and deleting conversations:

```go
type MemoryManager interface {
    agent.Memory
    List(ctx context.Context) ([]string, error)
    Delete(ctx context.Context, conversationID string) error
}
```

`List` returns all conversation IDs in the store. `Delete` removes a conversation by ID (returns nil if not found). The in-memory `Store` implements `MemoryManager`.

## Store (In-Memory)

```go
func NewStore() *Store
```

`NewStore` creates an empty in-memory store backed by a `map[string][]agent.Message`. It's thread-safe — reads use `sync.RWMutex` read locks, writes use exclusive locks. Both `Load` and `Save` deep-copy the message slice, so callers can't accidentally mutate stored data.

`Store` implements `MemoryManager`, so you can list and delete conversations:

```go
store := memory.NewStore()

// ... use the store with an agent ...

ids, err := store.List(ctx)
fmt.Println("Conversations:", ids)

err = store.Delete(ctx, "old-conversation")
```

## Strategies

Strategies are `Memory` implementations that wrap another `Memory` and transform messages on `Load`, `Save`, or both. They follow a middleware pattern — you compose them by nesting one inside another.

### Window

```go
func NewWindow(inner Memory, n int) *Window
```

Keeps only the last `n` messages on `Load`. `Save` passes through to the inner store unchanged. Use this to cap context length with a simple sliding window.

- `Load`: retrieves all messages from the inner store, returns only the last `n`
- `Save`: delegates directly to the inner store

### Filter

```go
func NewFilter(inner Memory) *Filter
```

Strips `ToolUseBlock` and `ToolResultBlock` from messages on `Load`, keeping only `TextBlock` content. Messages with no remaining content blocks are omitted entirely. `Save` passes through unchanged.

This is useful when you want the model to see conversation text but not raw tool call/result noise.

### Summary

```go
func NewSummary(inner Memory, threshold int, fn SummaryFunc, opts ...SummaryOption) *Summary
```

Triggers background summarization when the message count reaches 80% of `threshold`. When triggered, a goroutine calls the `SummaryFunc` on messages up to the cutoff point, replaces them with a single summary message, and saves the result back to the inner store.

```go
type SummaryFunc func(ctx context.Context, messages []Message) (Message, error)
```

Key behaviors:
- The 80% trigger is calculated as `(threshold * 80) / 100`
- Only one summarization runs at a time (subsequent saves skip if one is already in progress)
- Summarization runs in a background goroutine — `Save` returns immediately
- The summary message replaces all messages up to the cutoff; any messages added after the cutoff are preserved

#### DefaultSummaryFunc

```go
func DefaultSummaryFunc(provider Provider) SummaryFunc
```

Returns a batteries-included `SummaryFunc` that uses an LLM provider to condense messages into a single concise paragraph. It formats all messages as text, sends them to the provider with a summarization prompt, and returns the result as an assistant message. Pass this to `NewSummary` so you don't have to write your own.

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

Keeps the last `n` messages out of the `SummaryFunc`. When summarization triggers, only messages before the last `n` are passed to the summarizer — the tail is always preserved verbatim after the summary in the result. Defaults to 0 (summarize all messages up to the cutoff).

If `n` is greater than or equal to the number of messages at trigger time, summarization is skipped entirely for that cycle.

```go
s := memory.NewSummary(
    store, 20,
    memory.DefaultSummaryFunc(provider),
    memory.WithPreserveRecentMessages(5),
)
// When triggered with 16 messages: SummaryFunc receives msgs[0:11],
// result is [summary, msg11, msg12, msg13, msg14, msg15].
```

## Composable Middleware Pattern

Strategies wrap each other like middleware. Each strategy takes an `inner Memory` as its first argument, so you build a pipeline by nesting constructors. Messages flow through the chain: `Save` writes down to the base store, `Load` reads from the base store and transforms on the way back up.

```
Filter → Window → Store
```

In code, the innermost store is created first, then wrapped outward:

```go
store    := memory.NewStore()           // base storage
windowed := memory.NewWindow(store, 20) // keep last 20 messages
filtered := memory.NewFilter(windowed)  // strip tool blocks
```

When the agent calls `filtered.Load(ctx, id)`:
1. `Filter.Load` calls `Window.Load`
2. `Window.Load` calls `Store.Load`, then trims to last 20
3. `Filter` strips tool blocks from the trimmed result

When the agent calls `filtered.Save(ctx, id, msgs)`:
1. `Filter.Save` passes through to `Window.Save`
2. `Window.Save` passes through to `Store.Save`
3. The full message slice is stored (strategies only transform on Load)

## Code Example

This example composes Window and Filter strategies to keep the last 20 messages with tool blocks stripped:

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/memory"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
)

func main() {
	provider, err := bedrock.ClaudeSonnet4_6()
	if err != nil {
		log.Fatal(err)
	}

	// In-memory store as the base layer.
	store := memory.NewStore()

	// Compose strategies: Window keeps last 20, Filter strips tool blocks.
	windowed := memory.NewWindow(store, 20)
	filtered := memory.NewFilter(windowed)

	a, err := agent.Default(
		provider,
		prompt.Text("You are a helpful assistant. Be concise."),
		nil,
		agent.WithMemory(filtered, "demo-conversation"),
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

	// MemoryManager: list and delete conversations.
	ids, _ := store.List(ctx)
	fmt.Printf("\nConversations in store: %v\n", ids)

	_ = store.Delete(ctx, "demo-conversation")
	ids, _ = store.List(ctx)
	fmt.Printf("After delete: %v\n", ids)
}
```

## Persistent Memory Drivers

For production use cases where conversation history must survive process restarts, three persistent drivers are available as separate packages.

### Redis — agent/memory/redis

Import: `github.com/camilbinas/gude-agents/agent/memory/redis`

Stores conversation history as JSON in Redis string keys. Requires a running Redis instance.

```go
mem, err := redismemory.NewRedisMemory(
    redismemory.RedisOptions{Addr: "localhost:6379"},
    redismemory.WithTTL(24*time.Hour),
    redismemory.WithKeyPrefix("myapp:"),
)
```

Options: `WithTTL(d time.Duration)`, `WithKeyPrefix(prefix string)`.

Implements both `agent.Memory` and `memory.MemoryManager` (List, Delete). See [Redis Providers](redis.md) for full documentation.

### S3 — agent/memory/s3

Import: `github.com/camilbinas/gude-agents/agent/memory/s3`

Stores conversation history as JSON objects in any S3-compatible object store. Compatible with AWS S3, Cloudflare R2, MinIO, DigitalOcean Spaces, and Backblaze B2.

```go
cfg, _ := awsconfig.LoadDefaultConfig(ctx)
mem, err := s3memory.New(cfg, "my-bucket",
    s3memory.WithKeyPrefix("conversations/"),
)
```

Options: `WithKeyPrefix(prefix string)`, `WithEndpoint(url string)`, `WithPathStyle(enabled bool)`.

No network calls are made at construction time — connectivity errors surface on the first `Save`/`Load` call. Implements both `agent.Memory` and `memory.MemoryManager`.

### DynamoDB — agent/memory/dynamodb

Import: `github.com/camilbinas/gude-agents/agent/memory/dynamodb`

Stores conversation history as items in an Amazon DynamoDB table. The table must be created by the caller with `conversation_id` (String) as the partition key.

```go
cfg, _ := awsconfig.LoadDefaultConfig(ctx)
mem, err := dynamomemory.NewDynamoDBMemory(cfg, "my-conversations-table",
    dynamomemory.WithTTL(7*24*time.Hour),
    dynamomemory.WithKeyPrefix("prod:"),
)
```

Options: `WithKeyPrefix(prefix string)`, `WithTTL(d time.Duration)`, `WithTTLAttribute(attr string)`, `WithPartitionKey(attr string)`, `WithEndpoint(url string)`.

> **Item size limit:** DynamoDB items are capped at 400 KB. For long-running conversations with large tool results, pair this driver with `memory.NewWindow` or `memory.NewSummary` to bound item size.

> **List performance:** `List` performs a full-table Scan. Avoid calling it in hot paths on large tables.

Implements both `agent.Memory` and `memory.MemoryManager`.

## Persistent Memory Drivers

For production use cases where conversation history must survive process restarts, three persistent drivers are available as separate packages.

### Redis — agent/memory/redis

Import: `github.com/camilbinas/gude-agents/agent/memory/redis`

Stores conversation history as JSON in Redis string keys. Requires a running Redis instance.

```go
mem, err := redismemory.NewRedisMemory(
    redismemory.RedisOptions{Addr: "localhost:6379"},
    redismemory.WithTTL(24*time.Hour),
    redismemory.WithKeyPrefix("myapp:"),
)
```

Options: `WithTTL(d time.Duration)`, `WithKeyPrefix(prefix string)`.

Implements both `agent.Memory` and `memory.MemoryManager` (List, Delete). See [Redis Providers](redis.md) for full documentation.

### S3 — agent/memory/s3

Import: `github.com/camilbinas/gude-agents/agent/memory/s3`

Stores conversation history as JSON objects in any S3-compatible object store. Compatible with AWS S3, Cloudflare R2, MinIO, DigitalOcean Spaces, and Backblaze B2.

```go
cfg, _ := awsconfig.LoadDefaultConfig(ctx)
mem, err := s3memory.New(cfg, "my-bucket",
    s3memory.WithKeyPrefix("conversations/"),
)
```

Options: `WithKeyPrefix(prefix string)`, `WithEndpoint(url string)`, `WithPathStyle(enabled bool)`.

No network calls are made at construction time — connectivity errors surface on the first `Save`/`Load` call. Implements both `agent.Memory` and `memory.MemoryManager`.

### DynamoDB — agent/memory/dynamodb

Import: `github.com/camilbinas/gude-agents/agent/memory/dynamodb`

Stores conversation history as items in an Amazon DynamoDB table. The table must be created by the caller with `conversation_id` (String) as the partition key.

```go
cfg, _ := awsconfig.LoadDefaultConfig(ctx)
mem, err := dynamomemory.NewDynamoDBMemory(cfg, "my-conversations-table",
    dynamomemory.WithTTL(7*24*time.Hour),
    dynamomemory.WithKeyPrefix("prod:"),
)
```

Options: `WithKeyPrefix(prefix string)`, `WithTTL(d time.Duration)`, `WithTTLAttribute(attr string)`, `WithPartitionKey(attr string)`, `WithEndpoint(url string)`.

> **Item size limit:** DynamoDB items are capped at 400 KB. For long-running conversations with large tool results, pair this driver with `memory.NewWindow` or `memory.NewSummary` to bound item size.

> **List performance:** `List` performs a full-table Scan. Avoid calling it in hot paths on large tables.

Implements both `agent.Memory` and `memory.MemoryManager`.

## See Also

- [Agent API Reference](agent-api.md) — `WithMemory` option and agent loop behavior
- [Redis Providers](redis.md) — persistent Redis-backed memory (`agent/memory/redis`) and Redis vector store (`agent/rag/redis`)
- [Message Types](message-types.md) — `Message`, `ContentBlock`, `TextBlock`, `ToolUseBlock`, `ToolResultBlock`
- [Getting Started](getting-started.md) — installation and first agent
