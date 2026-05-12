# Long-Term Memory

Long-term memory gives agents identifier-scoped fact storage and semantic retrieval. The identifier can represent a user, team, project, tenant, or any other entity. Unlike the [conversation system](conversation.md) which stores message history, long-term memory stores discrete facts — preferences, past decisions, project context — that persist across conversations and are retrieved by semantic similarity.

## Quick Start

Define a struct with `db` tags, create a store, and wire up tools:

```go
type Preference struct {
    ID       string  `json:"id" db:"id,pk"`
    UserID   string  `json:"user_id" db:"user_id,identifier"`
    Title    string  `json:"title" db:"title,content" description:"Short description" required:"true"`
    Category string  `json:"category" db:"category" description:"Category: appearance, workflow, tools" required:"true"`
    Value    string  `json:"value" db:"value" description:"The preference value" required:"true"`
    Priority float64 `json:"priority" db:"priority" description:"Importance 0.0-1.0" required:"true"`
}

store, err := postgres.NewStore[Preference](pool, embedder, 1024,
    postgres.WithTableName("preferences"),
)

tools := []tool.Tool{
    postgres.NewRememberTool(store, postgres.WithToolName("save_preference")),
    postgres.NewUpdateTool(store, postgres.WithToolName("update_preference")),
    postgres.NewRecallTool(store, postgres.WithToolName("get_preferences")),
    postgres.NewForgetTool(store, postgres.WithToolName("forget_preference")),
}

a, _ := agent.Default(provider, systemPrompt, tools)
c := agent.Background().WithIdentifier("user-123")
```

The identifier (set via `c.WithIdentifier()`) scopes all storage and retrieval — the LLM never sees or fabricates identifiers. The tool schema is auto-generated from struct tags.

## Struct Tags

Tag format: `db:"column_name,role"`

| Role | Description |
|------|-------------|
| `pk` | Primary key column. Excluded from LLM input; if zero on insert, lets the DB default handle it. |
| `identifier` | Scoping column (WHERE filter). Excluded from LLM input; set automatically from context. |
| `content` | Field to embed. Its value is passed to the embedder for vector search. |
| `jsonb` | Serialize as JSONB (for slices, maps, nested structs). |
| `noinput` | Exclude from LLM input schema (e.g. server-set timestamps). |

Additional struct tags: `description:"..."` (shown to LLM), `required:"true"` (marked required in schema), `enum:"a,b,c"` (enum constraint).

## Interfaces

The core interface requires only Remember and Recall. Optional capabilities are expressed as separate interfaces that backends opt into.

```go
// Core — every backend implements this.
type Memory[T any] interface {
    Remember(ctx context.Context, identifier string, value T) error
    Recall(ctx context.Context, identifier string, query string, limit int, opts ...RecallOption) ([]Entry[T], error)
}

// Optional capabilities.
type Updater[T any] interface {
    Update(ctx context.Context, identifier, id string, value T) error
}

type Forgetter[T any] interface {
    Forget(ctx context.Context, identifier, id string) error
}

type BulkForgetter[T any] interface {
    ForgetAll(ctx context.Context, identifier string) error
}
```

All built-in backends (in-memory, Postgres, Redis) implement all four interfaces. Custom backends only need to implement `Memory[T]` — add the others as needed.

## Backends

### In-Memory

Import: `github.com/camilbinas/gude-agents/agent/memory`

No external dependencies. Uses brute-force cosine similarity. Good for development, testing, and examples.

```go
store, err := memory.NewStore[MyStruct](embedder)
```

### Postgres

Import: `github.com/camilbinas/gude-agents/agent/memory/postgres`

Requires PostgreSQL with pgvector. The table must be created by the caller.

```go
store, err := postgres.NewStore[MyStruct](pool, embedder, 1024,
    postgres.WithTableName("my_memories"),
)
```

| Option | Default | Description |
|--------|---------|-------------|
| `WithTableName(name)` | lowercased struct name + "s" | PostgreSQL table name |
| `WithEmbeddingColumn(name)` | `"embedding"` | Vector column name |
| `WithDistanceMetric(metric)` | `"cosine"` | `"cosine"`, `"l2"`, or `"inner_product"` |

### Redis

Import: `github.com/camilbinas/gude-agents/agent/memory/redis`

Requires Redis Stack (RediSearch). The index is created automatically.

```go
store, err := memredis.NewStore[MyStruct](
    memredis.Options{Addr: "127.0.0.1:6379"},
    embedder, 1024,
    memredis.WithIndexName("my_idx"),
)
```

| Option | Default | Description |
|--------|---------|-------------|
| `WithIndexName(name)` | `"gude_typed_idx"` | RediSearch index name |
| `WithKeyPrefix(prefix)` | `"gude:typed:"` | Redis key prefix |

Strings become TAG fields, numbers become NUMERIC fields (sortable/filterable). Same `RecallOption` filters as Postgres.

## Tools

`NewRememberTool`, `NewUpdateTool`, `NewRecallTool`, and `NewForgetTool` wrap a store as agent tools. The LLM schema is auto-generated from struct tags (fields with `pk`, `identifier`, or `noinput` are excluded). `NewUpdateTool` adds an `id` parameter so the LLM can target an existing entry.

```go
tools := []tool.Tool{
    postgres.NewRememberTool(store,
        postgres.WithToolName("remember_event"),
        postgres.WithToolDescription("Store an event."),
    ),
    postgres.NewUpdateTool(store,
        postgres.WithToolName("update_event"),
        postgres.WithToolDescription("Update an existing event by ID."),
    ),
    postgres.NewRecallTool(store,
        postgres.WithToolName("recall_events"),
        postgres.WithFieldGT("importance", 0.3),
        postgres.WithOrderBy("observed_at", postgres.Desc),
    ),
    postgres.NewForgetTool(store,
        postgres.WithToolName("forget_event"),
    ),
}
```

## Recall Filters

Pass `RecallOption` values to `Recall()` or to `NewRecallTool` (applied to every call).

| Option | Description |
|--------|-------------|
| `WithFieldEquals(col, val)` | WHERE col = val |
| `WithFieldGT(col, val)` | WHERE col > val |
| `WithFieldLT(col, val)` | WHERE col < val |
| `WithFieldIn(col, vals)` | WHERE col = ANY(vals) |
| `WithTimeAfter(col, t)` | WHERE col > t |
| `WithTimeBefore(col, t)` | WHERE col < t |
| `WithMinSimilarity(threshold)` | Minimum vector similarity score |
| `WithOrderBy(col, dir)` | Custom sort (overrides vector similarity) |
| `WithRawFilter(sql, args...)` | Raw SQL WHERE clause (Postgres only) |

## Multi-Scope Memory

By default, memory tools read their identifier from `c.Identifier()`. When an agent needs multiple independent scopes (e.g. user preferences + shared project notes), use `WithScope` to bind a tool to a named scope on the context.

```go
// User tools — default, reads c.Identifier()
memory.NewRememberTool(userMem, memory.WithToolName("save_preference"))

// Project tools — reads c.Scope("project") instead
memory.NewRememberTool(projectMem, memory.WithToolName("save_project_note"), memory.WithScope("project"))
memory.NewRecallTool(projectMem, memory.WithToolName("get_project_notes"), memory.WithScope("project"))
```

Set scopes on the context per-request:

```go
c := agent.Background().
    WithIdentifier("user-alice").
    WithScope("project", "proj-atlas")
```

To switch scopes mid-conversation (e.g. via a tool), use `c.SetScope("project", newID)`.

If a scoped tool's key is not set on the context, it falls back to `c.Identifier()`.

## Update

```go
// Update an existing entry by ID — re-embeds the content automatically.
store.Update(ctx, "user-123", entryID, updatedValue)
```

## Deletion

```go
// Forget a single entry by ID (from a previous recall).
store.Forget(ctx, "user-123", entryID)

// Delete all entries for an identifier (e.g. GDPR).
store.ForgetAll(ctx, "user-123")
```

## See Also

- [Agent API](agent-api.md) — `WithIdentifier` for scoping memory to a user/tenant
- [RAG Pipeline](rag.md) — document retrieval without per-user scoping
- [Providers](providers.md) — embedder setup (Bedrock, OpenAI, Gemini)
