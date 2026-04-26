# Long-Term Memory

Long-term memory gives agents knowledge storage and semantic retrieval, scoped by an identifier. The identifier can represent a user, team, project, tenant, or any other entity. While the [conversation system](conversation.md) stores conversation message history (ordered messages keyed by conversation ID), long-term memory stores discrete facts — preferences, past decisions, project context — that persist across conversations and are retrieved by semantic similarity.

The framework provides two approaches to long-term memory:

1. **Composable building blocks** (recommended for new code) — assemble `ScopedStore`, `NewRememberTool`, `NewRecallTool`, and any `VectorStore` backend into the memory pattern you need.
2. **Memory interface** (backward compatible) — the original `Remember`/`Recall` interface, now backed by the composable layer internally.

Both approaches use the same underlying storage and produce identical results. The composable approach gives you more flexibility: custom tool names, multiple stores per agent, and the ability to mix memory and semantic patterns.

## Composable Building Blocks

The composable approach separates concerns into independent pieces you wire together:

| Building Block | Package | Purpose |
|---|---|---|
| `VectorStore` | `agent` | Stores document embeddings and performs similarity search |
| `ScopedStore` | `agent/rag` | Wraps any VectorStore to partition documents by identifier |
| `NewRememberTool` | `agent/memory` | Tool that stores facts into a ScopedStore |
| `NewRecallTool` | `agent/memory` | Tool that retrieves facts from a ScopedStore |
| `Adapter` | `agent/memory` | Bridges composable blocks into the Memory interface |

The key insight: "long-term memory" and "semantic search" are not different storage systems — they are different usage patterns on top of the same vector similarity infrastructure.

### ScopedStore

Import: `github.com/camilbinas/gude-agents/agent/rag`

`ScopedStore` wraps any `agent.VectorStore` to partition documents by identifier. It injects a reserved metadata key (`_scope_id`) into each document on `Add` and filters results by that key on `Search`.

```go
func NewScopedStore(store agent.VectorStore) *ScopedStore
```

When the underlying store implements the optional `ScopedSearcher` interface (as the Redis VectorStore does), `ScopedStore` uses native TAG filtering for efficient scoped queries. Otherwise, it over-fetches and post-filters by metadata — transparent to the caller.

```go
import "github.com/camilbinas/gude-agents/agent/rag"

// Wrap any VectorStore with scoping.
memStore := rag.NewMemoryStore()
scopedStore := rag.NewScopedStore(memStore)
```

See [ScopedStore in the RAG docs](rag.md) for the full API reference.

### NewRememberTool

```go
func NewRememberTool(store *rag.ScopedStore, embedder agent.Embedder, opts ...ToolOption) tool.Tool
```

Creates a tool that stores facts into a `ScopedStore`. The tool extracts the identifier from the agent context via `agent.GetIdentifier`, embeds the fact, and stores it with metadata including a `created_at` timestamp and any user-provided key-value pairs.

- **Default name**: `"remember"`
- **Default description**: Instructs the LLM to store facts, preferences, and decisions for later recall.
- **Input schema**: Same as `RememberTool` — accepts `fact` (required) and `metadata` (optional).
- **Returns**: `"Remembered."` on success.

### NewRecallTool

```go
func NewRecallTool(store *rag.ScopedStore, embedder agent.Embedder, opts ...ToolOption) tool.Tool
```

Creates a tool that retrieves facts from a `ScopedStore`. The tool extracts the identifier from the agent context, embeds the query, and searches the scoped store.

- **Default name**: `"recall"`
- **Default description**: Instructs the LLM to retrieve previously stored facts and context.
- **Input schema**: Same as `RecallTool` — accepts `query` (required) and `limit` (optional, defaults to 5).
- **Returns**: Formatted results with fact text, metadata, timestamp, and similarity score. Returns `"No relevant memories found."` when no results match.

### ToolOption

Functional options for customizing tool names and descriptions:

```go
type ToolOption func(*toolConfig)

func WithToolName(name string) ToolOption
func WithToolDescription(desc string) ToolOption
```

Use `WithToolName` to give tools distinct names when running multiple stores in the same agent. Use `WithToolDescription` to tailor the LLM prompt for a specific domain.

### Adapter

```go
func NewAdapter(store *rag.ScopedStore, embedder agent.Embedder) *Adapter
```

`Adapter` implements the `Memory` interface using a `ScopedStore` and `Embedder`. Use it when you want the composable storage layer but need to pass a `Memory` to existing code.

- `Remember` validates inputs, embeds the fact, and stores it in the scoped store with `created_at` and user metadata.
- `Recall` validates inputs, embeds the query, searches the scoped store, and converts `ScoredDocument` results to `[]Entry`. Internal metadata keys (`_scope_id`, `created_at`) are excluded from the returned `Entry.Metadata`.

## Composable Patterns

### Long-Term Memory (VectorStore + ScopedStore + Tools)

The standard long-term memory pattern — per-user fact storage with remember/recall tools:

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
    "github.com/camilbinas/gude-agents/agent/rag"
    "github.com/camilbinas/gude-agents/agent/tool"
)

func main() {
    ctx := context.Background()

    provider, err := bedrock.Standard()
    if err != nil {
        log.Fatal(err)
    }

    embedder, err := bedrock.TitanEmbedV2()
    if err != nil {
        log.Fatal(err)
    }

    // 1. Create a VectorStore and wrap it with scoping.
    vectorStore := rag.NewMemoryStore()
    scopedStore := rag.NewScopedStore(vectorStore)

    // 2. Create composable remember/recall tools.
    tools := []tool.Tool{
        memory.NewRememberTool(scopedStore, embedder),
        memory.NewRecallTool(scopedStore, embedder),
    }

    // 3. Build the agent.
    a, err := agent.Default(
        provider,
        prompt.Text("You are a helpful assistant with long-term memory."),
        tools,
    )
    if err != nil {
        log.Fatal(err)
    }

    // 4. Set the identifier and invoke.
    ctx = agent.WithIdentifier(ctx, "user-123")

    result, _, err := a.Invoke(ctx, "I prefer dark mode and use PostgreSQL 16.")
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println(result)
}
```

This is equivalent to the `Memory` interface approach but gives you direct control over the VectorStore backend and tool configuration.

### Multi-Store Agent (Distinct Tool Names)

An agent with separate memory stores for different domains, each with its own tool names:

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
    "github.com/camilbinas/gude-agents/agent/rag"
    "github.com/camilbinas/gude-agents/agent/tool"
)

func main() {
    ctx := context.Background()

    provider, err := bedrock.Standard()
    if err != nil {
        log.Fatal(err)
    }

    embedder, err := bedrock.TitanEmbedV2()
    if err != nil {
        log.Fatal(err)
    }

    // Preferences store — user settings and preferences.
    prefStore := rag.NewScopedStore(rag.NewMemoryStore())

    // Projects store — project-specific context and decisions.
    projStore := rag.NewScopedStore(rag.NewMemoryStore())

    tools := []tool.Tool{
        // Preferences tools
        memory.NewRememberTool(prefStore, embedder,
            memory.WithToolName("remember_preferences"),
            memory.WithToolDescription("Store a user preference or setting."),
        ),
        memory.NewRecallTool(prefStore, embedder,
            memory.WithToolName("recall_preferences"),
            memory.WithToolDescription("Retrieve user preferences and settings."),
        ),
        // Projects tools
        memory.NewRememberTool(projStore, embedder,
            memory.WithToolName("remember_projects"),
            memory.WithToolDescription("Store a project decision or context."),
        ),
        memory.NewRecallTool(projStore, embedder,
            memory.WithToolName("recall_projects"),
            memory.WithToolDescription("Retrieve project decisions and context."),
        ),
    }

    a, err := agent.Default(
        provider,
        prompt.Text("You are a project assistant. Use remember_preferences/recall_preferences "+
            "for user settings. Use remember_projects/recall_projects for project context."),
        tools,
    )
    if err != nil {
        log.Fatal(err)
    }

    ctx = agent.WithIdentifier(ctx, "user-456")

    result, _, err := a.Invoke(ctx, "I prefer Go 1.23 and always use table-driven tests.")
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println(result)
}
```

Each tool pair operates on its own `ScopedStore`, so preferences and project facts are stored and retrieved independently. The LLM sees four distinct tools and decides which to use based on the descriptions.

### Semantic Search (VectorStore + Retriever, No Scoping)

Not all memory patterns need scoping. For document retrieval without per-user partitioning, use the [RAG pipeline](rag.md) directly:

```go
import (
    "github.com/camilbinas/gude-agents/agent/rag"
)

// Semantic search — no scoping needed.
vectorStore := rag.NewMemoryStore()
retriever := rag.NewRetriever(vectorStore, embedder)

// Use with agent.WithRetriever or agent.NewRetrieverTool.
```

### Adapter for Backward Compatibility

If you have existing code that expects a `Memory` interface, use the `Adapter` to bridge the composable layer:

```go
import (
    "github.com/camilbinas/gude-agents/agent/memory"
    "github.com/camilbinas/gude-agents/agent/rag"
)

// Create composable building blocks.
vectorStore := rag.NewMemoryStore()
scopedStore := rag.NewScopedStore(vectorStore)

// Bridge into the Memory interface.
adapter := memory.NewAdapter(scopedStore, embedder)

// Use adapter anywhere Memory is expected.
tools := []tool.Tool{
    memory.RememberTool(adapter),
    memory.RecallTool(adapter),
}
```

This is exactly what the built-in `memory.NewStore(embedder)` does internally — it creates a `MemoryStore`, wraps it in a `ScopedStore`, and creates an `Adapter`.

## Core Types

### Entry

A single unit of stored knowledge:

```go
type Entry struct {
    Fact      string            `json:"fact"`
    Metadata  map[string]string `json:"metadata"`
    CreatedAt time.Time         `json:"created_at"`
    Score     float64           `json:"score"`
}
```

| Field | Type | Description |
|-------|------|-------------|
| `Fact` | `string` | The stored knowledge text |
| `Metadata` | `map[string]string` | Optional categorization tags (nil serializes as JSON `null`) |
| `CreatedAt` | `time.Time` | When the entry was stored (RFC 3339 in JSON) |
| `Score` | `float64` | Cosine similarity score, populated only on Recall results |

Entries round-trip through JSON — `json.Marshal` followed by `json.Unmarshal` produces an equivalent value, including nil metadata.

### Memory Interface

```go
type Memory interface {
    Remember(ctx context.Context, identifier, fact string, metadata map[string]string) error
    Recall(ctx context.Context, identifier, query string, limit int) ([]Entry, error)
}
```

`Remember` stores a fact for an identifier. `Recall` retrieves the most relevant entries by semantic similarity to a query, returning at most `limit` results ordered by descending score.

Validation rules (all implementations must follow these):

| Condition | Method | Behavior |
|-----------|--------|----------|
| Empty identifier | `Remember` | Returns error |
| Empty fact | `Remember` | Returns error |
| Empty identifier | `Recall` | Returns error |
| Limit < 1 | `Recall` | Returns error |
| No entries for identifier | `Recall` | Returns empty non-nil slice, no error |

## In-Memory Store

Import: `github.com/camilbinas/gude-agents/agent/memory`

`Store` is a thread-safe in-memory `Memory` backed by an `agent.Embedder` for cosine similarity search. Good for prototyping and tests — for production, use a persistent backend like the [AgentCore Backend](#agentcore-backend) or see [Backends](#backends) for all options.

```go
func NewStore(embedder agent.Embedder) *Store
```

The embedder computes embedding vectors for both `Remember` (embed the fact) and `Recall` (embed the query, then rank stored entries by cosine similarity). Internally, `NewStore` creates a `rag.MemoryStore`, wraps it in a `rag.ScopedStore`, and uses an `Adapter` — so it benefits from the unified storage layer while preserving the familiar API.

```go
import (
    "github.com/camilbinas/gude-agents/agent/memory"
    "github.com/camilbinas/gude-agents/agent/provider/bedrock"
)

embedder, err := bedrock.TitanEmbedV2()
if err != nil {
    log.Fatal(err)
}

store := memory.NewStore(embedder)
```

Concurrency: reads use `sync.RWMutex` read locks, writes use exclusive locks. Safe for use from multiple goroutines.

### Error Wrapping

Embedder errors are wrapped with context:

- `Remember`: `fmt.Errorf("memory: embed fact: %w", err)`
- `Recall`: `fmt.Errorf("memory: embed query: %w", err)`

## RememberTool

```go
func RememberTool(m Memory) tool.Tool
```

Returns a `tool.Tool` that stores facts into long-term memory. The tool uses `tool.NewRaw` with a hand-crafted JSON Schema (the `metadata` field is a `map[string]string` which benefits from explicit schema control).

- **Name**: `"remember"`
- **Description**: Instructs the LLM to store facts, preferences, and decisions for later recall.

**Input schema:**

```json
{
  "type": "object",
  "properties": {
    "fact": {
      "type": "string",
      "description": "The fact, preference, or decision to remember for later."
    },
    "metadata": {
      "type": "object",
      "description": "Optional key-value pairs for categorization.",
      "additionalProperties": { "type": "string" }
    }
  },
  "required": ["fact"]
}
```

The handler extracts the identifier from the context via `agent.GetIdentifier`, calls `m.Remember`, and returns `"Remembered."` on success. Errors from the underlying `Memory` are propagated directly.

For the composable equivalent with customizable names, see [NewRememberTool](#newremembertool).

## RecallTool

```go
func RecallTool(m Memory) tool.Tool
```

Returns a `tool.Tool` that retrieves facts from long-term memory by semantic similarity.

- **Name**: `"recall"`
- **Description**: Instructs the LLM to retrieve previously stored facts and context.

**Input schema:**

```json
{
  "type": "object",
  "properties": {
    "query": {
      "type": "string",
      "description": "A natural-language query describing what to recall."
    },
    "limit": {
      "type": "integer",
      "description": "Maximum number of results to return. Defaults to 5."
    }
  },
  "required": ["query"]
}
```

The handler extracts the identifier from the context, calls `m.Recall` with the query and limit (defaulting to 5 when omitted), and formats results as a human-readable string listing each entry's fact, metadata, timestamp, and similarity score. When no entries are found, returns `"No relevant memories found."`.

For the composable equivalent with customizable names, see [NewRecallTool](#newrecalltool).

## AgentCore Backend

Import: `github.com/camilbinas/gude-agents/agent/memory/agentcore`

`Store` is a persistent `Memory` backed by [AWS Bedrock AgentCore Memory](https://docs.aws.amazon.com/bedrock-agentcore/latest/APIReference/). It stores and retrieves facts using AgentCore's managed memory service with built-in semantic search — no embedder setup required. The backend lives in its own Go sub-module, so you only pull in AWS SDK dependencies when you use it.

AgentCore is a standalone managed-service backend. It implements `Memory` directly and does not use `VectorStore` or `ScopedStore` — AgentCore manages its own storage, embeddings, and search internally.

```go
import (
    "github.com/aws/aws-sdk-go-v2/config"
    "github.com/aws/aws-sdk-go-v2/service/bedrockagentcore"
    "github.com/camilbinas/gude-agents/agent/memory/agentcore"
)

cfg, _ := config.LoadDefaultConfig(ctx)
client := bedrockagentcore.NewFromConfig(cfg)
store, err := agentcore.New(client, "your-memory-id")
```

### Store Modes

The backend supports two storage modes, selected via `WithStoreMode`:

| Mode | Constant | Description |
|------|----------|-------------|
| CreateEvent (default) | `CreateEventMode` | Sends facts as conversational events. AgentCore's long-term memory strategies automatically extract and store insights. |
| BatchCreate | `BatchCreateMode` | Writes facts directly as memory records, bypassing automatic extraction. Use when you want precise control over what is stored. |

```go
// Default — CreateEvent mode (automatic extraction)
store, err := agentcore.New(client, "my-memory-id")

// Explicit — BatchCreate mode (direct storage)
store, err := agentcore.New(client, "my-memory-id",
    agentcore.WithStoreMode(agentcore.BatchCreateMode),
)
```

### Configuration Options

| Option | Default | Description |
|--------|---------|-------------|
| `WithStoreMode(mode)` | `CreateEventMode` | Select between `CreateEventMode` and `BatchCreateMode`. |
| `WithNamespaceTemplate(tmpl)` | `"/facts/{{.ActorID}}/"` | Go `text/template` string for generating the namespace from the actor ID. The template receives a struct with an `ActorID` field. |
| `WithSessionIDFunc(fn)` | `uuid.NewString` | Function for generating session IDs used in CreateEvent mode. |

### Error Wrapping

AgentCore API errors are wrapped with context, following the same pattern as the in-memory store:

- `Remember` (CreateEvent): `fmt.Errorf("agentcore memory: create event: %w", err)`
- `Remember` (BatchCreate): `fmt.Errorf("agentcore memory: batch create: %w", err)`
- `Recall`: `fmt.Errorf("agentcore memory: retrieve: %w", err)`

All errors use `%w` so callers can use `errors.Is` and `errors.As`.

### Full Example

Create an AgentCore store, wire the tools into an agent, and run it with an identifier:

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/aws/aws-sdk-go-v2/config"
    "github.com/aws/aws-sdk-go-v2/service/bedrockagentcore"
    "github.com/camilbinas/gude-agents/agent"
    "github.com/camilbinas/gude-agents/agent/memory"
    "github.com/camilbinas/gude-agents/agent/memory/agentcore"
    "github.com/camilbinas/gude-agents/agent/prompt"
    "github.com/camilbinas/gude-agents/agent/provider/bedrock"
    "github.com/camilbinas/gude-agents/agent/tool"
)

func main() {
    ctx := context.Background()

    // 1. Create a provider.
    provider, err := bedrock.Standard()
    if err != nil {
        log.Fatal(err)
    }

    // 2. Create an AgentCore memory store.
    cfg, err := config.LoadDefaultConfig(ctx)
    if err != nil {
        log.Fatal(err)
    }
    client := bedrockagentcore.NewFromConfig(cfg)

    store, err := agentcore.New(client, "your-memory-id")
    if err != nil {
        log.Fatal(err)
    }

    // 3. Build the remember and recall tools.
    tools := []tool.Tool{
        memory.RememberTool(store),
        memory.RecallTool(store),
    }

    // 4. Create the agent with memory tools.
    a, err := agent.Default(
        provider,
        prompt.Text("You are a helpful assistant with long-term memory. "+
            "Use the remember tool to store important facts the user shares. "+
            "Use the recall tool to retrieve relevant context from past conversations."),
        tools,
    )
    if err != nil {
        log.Fatal(err)
    }

    // 5. Set the identifier and invoke.
    ctx = agent.WithIdentifier(ctx, "user-123")

    result, _, err := a.Invoke(ctx, "I prefer dark mode and use PostgreSQL 16 for all my projects.")
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println(result)
}
```


## Redis Backend

Import: `github.com/camilbinas/gude-agents/agent/memory/redis`

`Store` is a persistent `Memory` backed by [Redis Stack](https://redis.io/docs/stack/) (RediSearch) with HNSW-indexed embedding vectors for KNN similarity search. It requires an `agent.Embedder` for computing embedding vectors. The backend lives in its own Go sub-module, so you only pull in Redis dependencies when you use it.

Redis Stack is required — standard community Redis does not include the RediSearch module. Run it locally with Docker:

```bash
docker run -p 6379:6379 redis/redis-stack-server:latest
```

```go
import (
    memoryredis "github.com/camilbinas/gude-agents/agent/memory/redis"
    "github.com/camilbinas/gude-agents/agent/provider/bedrock"
)

embedder, err := bedrock.TitanEmbedV2()
if err != nil {
    log.Fatal(err)
}

store, err := memoryredis.New(
    memoryredis.Options{Addr: "127.0.0.1:6379"},
    embedder,
    1024, // Titan Embed V2 dimension
)
if err != nil {
    log.Fatal(err)
}
defer store.Close()
```

### Configuration Options

| Option | Default | Description |
|--------|---------|-------------|
| `WithIndexName(name)` | `"gude_memory_idx"` | RediSearch index name |
| `WithKeyPrefix(prefix)` | `"gude:memory:"` | Redis key prefix for all stored hashes |
| `WithHNSWM(m)` | `16` | HNSW M parameter (graph connectivity) |
| `WithHNSWEFConstruction(ef)` | `200` | HNSW EF_CONSTRUCTION parameter (index build quality) |
| `WithDropExisting()` | `false` | Drop the index and its documents before creating a fresh one. Dev/examples only. |

### Error Wrapping

Redis and embedder errors are wrapped with context, following the same pattern as the other backends:

- Remember: `fmt.Errorf("redis memory: embed fact: %w", err)`, `fmt.Errorf("redis memory: marshal metadata: %w", err)`, `fmt.Errorf("redis memory: remember: %w", err)`
- Recall: `fmt.Errorf("redis memory: embed query: %w", err)`, `fmt.Errorf("redis memory: recall: %w", err)`
- Constructor: `fmt.Errorf("redis memory: ping: %w", err)`, `fmt.Errorf("redis memory: create index: %w", err)`

All errors use `%w` so callers can use `errors.Is` and `errors.As`.

### Full Example

Create a Redis store, wire the tools into an agent, and run it with an identifier:

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/camilbinas/gude-agents/agent"
    "github.com/camilbinas/gude-agents/agent/memory"
    memoryredis "github.com/camilbinas/gude-agents/agent/memory/redis"
    "github.com/camilbinas/gude-agents/agent/prompt"
    "github.com/camilbinas/gude-agents/agent/provider/bedrock"
    "github.com/camilbinas/gude-agents/agent/tool"
)

func main() {
    ctx := context.Background()

    // 1. Create a provider and embedder.
    provider, err := bedrock.Standard()
    if err != nil {
        log.Fatal(err)
    }

    embedder, err := bedrock.TitanEmbedV2()
    if err != nil {
        log.Fatal(err)
    }

    // 2. Create a Redis memory store (1024-dim for Titan Embed V2).
    store, err := memoryredis.New(
        memoryredis.Options{Addr: "127.0.0.1:6379"},
        embedder,
        1024,
        memoryredis.WithDropExisting(), // clean slate for the example
    )
    if err != nil {
        log.Fatal(err)
    }
    defer store.Close()

    // 3. Build the remember and recall tools.
    tools := []tool.Tool{
        memory.RememberTool(store),
        memory.RecallTool(store),
    }

    // 4. Create the agent with memory tools.
    a, err := agent.Default(
        provider,
        prompt.Text("You are a helpful assistant with long-term memory. "+
            "Use the remember tool to store important facts the user shares. "+
            "Use the recall tool to retrieve relevant context from past conversations."),
        tools,
    )
    if err != nil {
        log.Fatal(err)
    }

    // 5. Set the identifier and invoke.
    ctx = agent.WithIdentifier(ctx, "user-123")

    result, _, err := a.Invoke(ctx, "I prefer dark mode and use PostgreSQL 16 for all my projects.")
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println("Turn 1:", result)

    result, _, err = a.Invoke(ctx, "What database do I use?")
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println("Turn 2:", result)
}
```

## Identifier Context Helpers

The application sets the identifier on the context before invoking the agent. The tools extract it automatically — the LLM never needs to know or fabricate identifiers. The identifier can represent any scoping entity: a user ID, team ID, project ID, tenant ID, etc.

```go
func WithIdentifier(ctx context.Context, id string) context.Context
func GetIdentifier(ctx context.Context) string
```

`WithIdentifier` attaches an identifier to the context. `GetIdentifier` retrieves it, returning an empty string if none is set.

These follow the same pattern as `WithImages`/`GetImages` and `WithConversationID` in `agent/context.go`.

If neither tool finds an identifier on the context, it returns an error: `"memory: identifier not found in context; use agent.WithIdentifier"`.

## Code Example

Full example — create an in-memory store, wire the tools into an agent, and run it with an identifier on the context:

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
    "github.com/camilbinas/gude-agents/agent/tool"
)

func main() {
    ctx := context.Background()

    // 1. Create a provider and embedder.
    provider, err := bedrock.Standard()
    if err != nil {
        log.Fatal(err)
    }

    embedder, err := bedrock.TitanEmbedV2()
    if err != nil {
        log.Fatal(err)
    }

    // 2. Create an in-memory store.
    store := memory.NewStore(embedder)

    // 3. Build the remember and recall tools.
    tools := []tool.Tool{
        memory.RememberTool(store),
        memory.RecallTool(store),
    }

    // 4. Create the agent with memory tools.
    a, err := agent.Default(
        provider,
        prompt.Text("You are a helpful assistant with long-term memory. "+
            "Use the remember tool to store important facts the user shares. "+
            "Use the recall tool to retrieve relevant context from past conversations."),
        tools,
    )
    if err != nil {
        log.Fatal(err)
    }

    // 5. Set the identifier on the context.
    ctx = agent.WithIdentifier(ctx, "user-123")

    // 6. First conversation — the agent stores a preference.
    result, _, err := a.Invoke(ctx, "I prefer dark mode and use PostgreSQL 16 for all my projects.")
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println("Turn 1:", result)

    // 7. Later conversation — the agent recalls stored facts.
    result, _, err = a.Invoke(ctx, "What database do I use?")
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println("Turn 2:", result)
}
```

### HTTP Multi-Tenant Pattern

For HTTP servers where each request has a different scope, set the identifier per-request:

```go
func handleChat(a *agent.Agent) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        userID := r.Header.Get("X-User-ID")

        ctx := agent.WithIdentifier(r.Context(), userID)

        result, _, err := a.Invoke(ctx, r.FormValue("message"))
        if err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
            return
        }

        fmt.Fprint(w, result)
    }
}
```

## Long-Term Memory vs Conversation Memory

| | Conversation Memory | Long-Term Memory |
|---|---|---|
| **Stores** | Ordered message sequences | Discrete facts |
| **Keyed by** | Conversation ID | Identifier (user, team, project, etc.) |
| **Retrieval** | Load all messages for a conversation | Semantic similarity search |
| **Scope** | Single conversation | Across all conversations for an identifier |
| **Interface** | `agent.Conversation` (Load/Save) | `memory.Memory` (Remember/Recall) |
| **Wiring** | `agent.WithConversation` option | Tools added to the agent's tool list |

The two systems are complementary. Use conversation memory for multi-turn context within a session. Use long-term memory for knowledge that should persist across sessions — preferences, decisions, project context.

## Backends

Each backend lives in its own sub-module with its own `go.mod`, so you only import the dependencies you need:

| Backend | Package | Status |
|---------|---------|--------|
| In-memory | `agent/memory` | Available |
| AgentCore | `agent/memory/agentcore` | Available |
| PostgreSQL | `agent/memory/postgres` | Planned |
| Redis | `agent/memory/redis` | Available |

The PostgreSQL backend will use [pgvector](https://github.com/pgvector/pgvector) for vector similarity search.

All VectorStore-based backends (in-memory, Redis) can be used with either the composable tools (`NewRememberTool`/`NewRecallTool`) or the `Memory` interface via the `Adapter`. The AgentCore backend implements `Memory` directly and works with `RememberTool`/`RecallTool`.

## See Also

- [Conversation System](conversation.md) — conversation memory (Load/Save per conversation)
- [RAG Pipeline](rag.md) — document retrieval, vector stores, and `ScopedStore`
- [Tool System](tools.md) — how tools work in the agent loop, including `tool.NewRaw`
- [Agent API Reference](agent-api.md) — `agent.WithIdentifier` and agent construction
- [Getting Started](getting-started.md) — installation and first agent