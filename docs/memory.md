# Long-Term Memory

Long-term memory gives agents identifier-scoped fact storage and semantic retrieval. The identifier can represent a user, team, project, tenant, or any other entity. Unlike the [conversation system](conversation.md) which stores message history, long-term memory stores discrete facts — preferences, past decisions, project context — that persist across conversations and are retrieved by semantic similarity.

## Quick Start

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

    provider, err := bedrock.Standard()
    if err != nil {
        log.Fatal(err)
    }

    embedder, err := bedrock.TitanEmbedV2()
    if err != nil {
        log.Fatal(err)
    }

    // 1. Create an in-memory store.
    memStore := memory.NewInMemoryStore()

    // 2. Create composable remember/recall tools.
    tools := []tool.Tool{
        memory.NewRememberTool(memStore, embedder),
        memory.NewRecallTool(memStore, embedder),
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

Create a `MemoryStore`, wrap it with `NewRememberTool` and `NewRecallTool`, and pass the tools to the agent. The identifier (set via `agent.WithIdentifier`) scopes all storage and retrieval — the LLM never sees or fabricates identifiers.

## Persistent Backends

The in-memory store is good for prototyping. For production, use a persistent backend.

### Redis Backend

Import: `github.com/camilbinas/gude-agents/agent/memory/redis`

Requires Redis Stack (not standard community Redis) for RediSearch vector search.

```go
import memredis "github.com/camilbinas/gude-agents/agent/memory/redis"

store, err := memredis.New(
    memredis.Options{Addr: "127.0.0.1:6379"},
    embedder,
    1024, // embedding dimension
)
```

| Option | Default | Description |
|--------|---------|-------------|
| `WithIndexName(name)` | `"gude_memory_idx"` | RediSearch index name |
| `WithKeyPrefix(prefix)` | `"gude:memory:"` | Redis key prefix for hash entries |
| `WithHNSWM(m)` | `16` | HNSW M parameter (max outgoing edges per node) |
| `WithHNSWEFConstruction(ef)` | `200` | HNSW EF_CONSTRUCTION parameter (search width during index build) |
| `WithDropExisting()` | disabled | Drop and recreate the index on construction (dev/testing only) |

The store uses native TAG filtering for identifier scoping — no post-search metadata filtering. It implements both `MemoryStore` and `Memory`, so it works with both the composable tools and the backward-compatible interface.

### Postgres Backend

Import: `github.com/camilbinas/gude-agents/agent/memory/postgres`

Requires PostgreSQL with the pgvector extension.

```go
import (
    "github.com/jackc/pgx/v5/pgxpool"
    mempg "github.com/camilbinas/gude-agents/agent/memory/postgres"
)

pool, err := pgxpool.New(ctx, "postgres://postgres:postgres@localhost:5432/postgres")
if err != nil {
    log.Fatal(err)
}

store, err := mempg.New(pool, embedder, 1024,
    mempg.WithAutoMigrate(),
)
```

| Option | Default | Description |
|--------|---------|-------------|
| `WithTableName(name)` | `"memory_entries"` | PostgreSQL table name |
| `WithAutoMigrate()` | disabled | Create extension, table, and indexes on construction |
| `WithHNSW(m, efConstruction)` | `m=16, ef=200` | HNSW index parameters (used with `WithAutoMigrate`) |
| `WithIVFFlat(lists)` | disabled | Use IVFFlat index instead of HNSW (used with `WithAutoMigrate`) |
| `WithDistanceMetric(metric)` | `"cosine"` | Distance metric: `"cosine"`, `"l2"`, or `"inner_product"` |
| `WithDropExisting()` | disabled | Drop and recreate the table on construction (dev/testing only) |

Uses a dedicated `identifier` column with a SQL `WHERE` clause for scoping. Like the Redis backend, it implements both `MemoryStore` and `Memory`.

## Typed Memory

`TypedMemory[T]` provides type-safe memory for user-defined Go structs. Instead of flattening domain data into `Entry{Fact, Metadata}`, you define your own struct and get full type safety through generics.

```go
type Preference struct {
    Category string `json:"category"`
    Detail   string `json:"detail"`
}

// Create a typed store over any MemoryStore backend.
memStore := memory.NewInMemoryStore()
typedStore := memory.NewTypedMemoryStore[Preference](memStore)

contentFunc := func(p Preference) string {
    return fmt.Sprintf("%s: %s", p.Category, p.Detail)
}

schemaFunc := func() map[string]any {
    return map[string]any{
        "type": "object",
        "properties": map[string]any{
            "category": map[string]any{"type": "string"},
            "detail":   map[string]any{"type": "string"},
        },
        "required": []any{"category", "detail"},
    }
}

tools := []tool.Tool{
    memory.NewTypedRememberTool(typedStore, embedder, contentFunc, schemaFunc),
    memory.NewTypedRecallTool(typedStore, embedder),
}
```

The `contentFunc` extracts the text used for embedding from each value. The `schemaFunc` returns the JSON Schema so the LLM understands the structure of your type. Use `memory.NewTypedInMemory[T](embedder, contentFunc)` as a convenience shortcut for in-memory typed memory.

## Multiple Stores

Use `WithToolName` and `WithToolDescription` to give tools distinct names when running multiple stores in the same agent:

```go
prefStore := memory.NewInMemoryStore()
projStore := memory.NewInMemoryStore()

tools := []tool.Tool{
    memory.NewRememberTool(prefStore, embedder,
        memory.WithToolName("remember_preferences"),
        memory.WithToolDescription("Store a user preference or setting."),
    ),
    memory.NewRecallTool(prefStore, embedder,
        memory.WithToolName("recall_preferences"),
        memory.WithToolDescription("Retrieve user preferences and settings."),
    ),
    memory.NewRememberTool(projStore, embedder,
        memory.WithToolName("remember_projects"),
        memory.WithToolDescription("Store a project decision or context."),
    ),
    memory.NewRecallTool(projStore, embedder,
        memory.WithToolName("recall_projects"),
        memory.WithToolDescription("Retrieve project decisions and context."),
    ),
}
```

Each tool pair operates on its own store, so preferences and project facts are stored and retrieved independently. The LLM sees four distinct tools and decides which to use based on the descriptions.

## Memory Interface (Backward Compatible)

The original `Memory` interface (`Remember`/`Recall`) still works and is backed by the composable layer internally. Use `NewInMemory` for the simplest setup, or `RememberTool`/`RecallTool` to wrap any `Memory` as agent tools:

```go
// Simplest path — in-memory store with the Memory interface.
store := memory.NewInMemory(embedder)

tools := []tool.Tool{
    memory.RememberTool(store),
    memory.RecallTool(store),
}
```

If you have a composable `MemoryStore` but need to pass it to code expecting the `Memory` interface, use the `Adapter`:

```go
memStore := memory.NewInMemoryStore()
adapter := memory.NewAdapter(memStore, embedder)
// adapter implements Memory — use it anywhere Memory is expected.
```

This is exactly what `memory.NewInMemory(embedder)` does internally.

## See Also

- [Agent API](agent-api.md) — `WithIdentifier` for scoping memory to a user/tenant
- [RAG Pipeline](rag.md) — document retrieval without per-user scoping
- [Providers](providers.md) — embedder setup (Bedrock, OpenAI, Gemini)
