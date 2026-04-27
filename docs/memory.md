# Long-Term Memory

Long-term memory gives agents knowledge storage and semantic retrieval, scoped by an identifier. The identifier can represent a user, team, project, tenant, or any other entity. While the [conversation system](conversation.md) stores conversation message history (ordered messages keyed by conversation ID), long-term memory stores discrete facts — preferences, past decisions, project context — that persist across conversations and are retrieved by semantic similarity.

The framework provides two approaches to long-term memory:

1. **Composable building blocks** (recommended for new code) — assemble `MemoryStore`, `NewRememberTool`, `NewRecallTool`, and any backend into the memory pattern you need.
2. **Memory interface** (backward compatible) — the original `Remember`/`Recall` interface, now backed by the composable layer internally.

Both approaches use the same underlying storage and produce identical results. The composable approach gives you more flexibility: custom tool names, multiple stores per agent, and the ability to mix memory patterns.

## Architecture

The memory system is self-contained — it has no dependency on the RAG package. Memory backends implement the `MemoryStore` interface directly, using their native capabilities for identifier-scoped storage and vector similarity search.

```
agent/memory          → agent (for Document, ScoredDocument, Embedder)
agent/memory/redis    → agent/memory (for MemoryStore), go-redis
agent/memory/postgres → agent/memory (for MemoryStore), pgx, pgvector
agent/rag             → agent (for VectorStore, Document, etc.) — no memory concepts
```

```mermaid
graph TD
    subgraph "agent package"
        Doc[agent.Document]
        SDoc[agent.ScoredDocument]
        Emb[agent.Embedder]
    end

    subgraph "memory package"
        MSI[MemoryStore interface]
        IMS[InMemoryStore]
        Adp[Adapter]
        MI[Memory interface]
        TMI[TypedMemory T interface]
        TMS[TypedMemoryStore T]
        TA[TypedAdapter T]
        RT[NewRememberTool]
        RCT[NewRecallTool]
    end

    subgraph "memory backends"
        Redis[memory/redis.Store]
        PG[memory/postgres.Store]
    end

    MSI -->|uses| Doc
    MSI -->|uses| SDoc
    IMS -->|implements| MSI
    Redis -->|implements| MSI
    PG -->|implements| MSI
    Adp -->|wraps| MSI
    Adp -->|uses| Emb
    Adp -->|implements| MI
    TMS -->|wraps| MSI
    TA -->|wraps| TMS
    TA -->|implements| TMI
    RT -->|uses| MSI
    RCT -->|uses| MSI

    style MSI fill:#f9f,stroke:#333,stroke-width:2px
```

Each backend handles identifier scoping natively:

| Backend | Scoping Mechanism | Description |
|---------|-------------------|-------------|
| InMemoryStore | Map partitioning | `map[string][]vsEntry` keyed by identifier — O(1) partition lookup |
| Redis | TAG field filter | `@identifier:{value}` in FT.SEARCH queries — native RediSearch filtering |
| Postgres | SQL WHERE clause | `WHERE identifier = $1` on a dedicated indexed column |

## Composable Building Blocks

The composable approach separates concerns into independent pieces you wire together:

| Building Block | Package | Purpose |
|---|---|---|
| `MemoryStore` | `agent/memory` | Identifier-scoped document storage and vector similarity search |
| `InMemoryStore` | `agent/memory` | In-memory `MemoryStore` implementation (map partitioned by identifier) |
| `NewRememberTool` | `agent/memory` | Tool that stores facts into a MemoryStore |
| `NewRecallTool` | `agent/memory` | Tool that retrieves facts from a MemoryStore |
| `Adapter` | `agent/memory` | Bridges composable blocks into the Memory interface |

### MemoryStore Interface

Import: `github.com/camilbinas/gude-agents/agent/memory`

`MemoryStore` is the low-level storage interface for memory backends. It provides identifier-scoped document storage and vector similarity search. Backends implement this interface directly using their native capabilities — SQL `WHERE` clauses for Postgres, Redis TAG filters for Redis, in-memory map partitioning for the default store.

```go
type MemoryStore interface {
    Add(ctx context.Context, identifier string, docs []agent.Document, embeddings [][]float64) error
    Search(ctx context.Context, identifier string, queryEmbedding []float64, topK int) ([]agent.ScoredDocument, error)
}
```

`MemoryStore` uses `agent.Document` and `agent.ScoredDocument` — the same types used by the RAG system — so that documents have a uniform representation across the library. These types are defined in the `agent` package, not the `rag` package, so importing them creates no coupling to RAG.

| Method | Condition | Behavior |
|--------|-----------|----------|
| `Add` | Empty identifier | Returns error |
| `Add` | docs/embeddings length mismatch | Returns error |
| `Search` | Empty identifier | Returns error |
| `Search` | topK < 1 | Returns error |
| `Search` | No matching documents | Returns non-nil empty slice, no error |

### InMemoryStore

Import: `github.com/camilbinas/gude-agents/agent/memory`

`InMemoryStore` implements `MemoryStore` using an in-memory map partitioned by identifier. Each identifier gets its own slice of entries. Search uses brute-force cosine similarity. Safe for concurrent use.

```go
func NewInMemoryStore() *InMemoryStore
```

```go
import "github.com/camilbinas/gude-agents/agent/memory"

// Create an in-memory store.
memStore := memory.NewInMemoryStore()
```

### NewRememberTool

```go
func NewRememberTool(store MemoryStore, embedder agent.Embedder, opts ...ToolOption) tool.Tool
```

Creates a tool that stores facts into a `MemoryStore`. The tool extracts the identifier from the agent context via `agent.GetIdentifier`, embeds the fact, and stores it with metadata including a `created_at` timestamp and any user-provided key-value pairs.

- **Default name**: `"remember"`
- **Default description**: Instructs the LLM to store facts, preferences, and decisions for later recall.
- **Input schema**: Same as `RememberTool` — accepts `fact` (required) and `metadata` (optional).
- **Returns**: `"Remembered."` on success.

### NewRecallTool

```go
func NewRecallTool(store MemoryStore, embedder agent.Embedder, opts ...ToolOption) tool.Tool
```

Creates a tool that retrieves facts from a `MemoryStore`. The tool extracts the identifier from the agent context, embeds the query, and searches the store.

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
func NewAdapter(store MemoryStore, embedder agent.Embedder) *Adapter
```

`Adapter` implements the `Memory` interface using a `MemoryStore` and `Embedder`. Use it when you want the composable storage layer but need to pass a `Memory` to existing code.

- `Remember` validates inputs, embeds the fact, and stores it in the memory store with `created_at` and user metadata.
- `Recall` validates inputs, embeds the query, searches the memory store, and converts `ScoredDocument` results to `[]Entry`. Internal metadata keys (`created_at`) are excluded from the returned `Entry.Metadata`.

## Composable Patterns

### Long-Term Memory (MemoryStore + Tools)

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

This is equivalent to the `Memory` interface approach but gives you direct control over the storage backend and tool configuration.

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
    prefStore := memory.NewInMemoryStore()

    // Projects store — project-specific context and decisions.
    projStore := memory.NewInMemoryStore()

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

Each tool pair operates on its own `InMemoryStore`, so preferences and project facts are stored and retrieved independently. The LLM sees four distinct tools and decides which to use based on the descriptions.

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
)

// Create an in-memory store.
memStore := memory.NewInMemoryStore()

// Bridge into the Memory interface.
adapter := memory.NewAdapter(memStore, embedder)

// Use adapter anywhere Memory is expected.
tools := []tool.Tool{
    memory.RememberTool(adapter),
    memory.RecallTool(adapter),
}
```

This is exactly what the built-in `memory.NewInMemory(embedder)` does internally — it creates an `InMemoryStore` and wraps it in an `Adapter`.

## Typed Memory

`TypedMemory[T]` provides type-safe memory for user-defined Go structs. Instead of flattening domain data into `Entry{Fact, Metadata}`, you define your own struct and get full type safety through generics.

### TypedMemory Interface

```go
type TypedMemory[T any] interface {
    Remember(ctx context.Context, identifier string, value T) error
    Recall(ctx context.Context, identifier string, query string, limit int) ([]TypedEntry[T], error)
}
```

### TypedEntry

```go
type TypedEntry[T any] struct {
    Value T
    Score float64
}
```

### TypedMemoryStore

`TypedMemoryStore[T]` wraps a `MemoryStore` with typed serialization. It encodes user structs into `Document.Metadata` via the `_typed_data` JSON key on `Add` and decodes them back on `Search`.

```go
func NewTypedMemoryStore[T any](store MemoryStore) *TypedMemoryStore[T]
```

### TypedAdapter

`TypedAdapter[T]` implements `TypedMemory[T]` by composing a `TypedMemoryStore[T]`, an `Embedder`, and a content extraction function. The content function extracts the text used for embedding from each value.

```go
func NewTypedAdapter[T any](store *TypedMemoryStore[T], embedder agent.Embedder, contentFunc func(T) string) *TypedAdapter[T]
```

### NewTypedInMemory

Convenience constructor that creates an in-memory `TypedAdapter` — the typed equivalent of `NewInMemory`:

```go
func NewTypedInMemory[T any](embedder agent.Embedder, contentFunc func(T) string) *TypedAdapter[T]
```

Internally creates `InMemoryStore` → `TypedMemoryStore[T]` → `TypedAdapter[T]`.

### Typed Tools

Composable tools for typed memory, analogous to `NewRememberTool`/`NewRecallTool`:

```go
func NewTypedRememberTool[T any](
    store *TypedMemoryStore[T],
    embedder agent.Embedder,
    contentFunc func(T) string,
    schemaFunc func() map[string]any,
    opts ...ToolOption,
) tool.Tool

func NewTypedRecallTool[T any](
    store *TypedMemoryStore[T],
    embedder agent.Embedder,
    opts ...ToolOption,
) tool.Tool
```

`NewTypedRememberTool` accepts a `schemaFunc` that returns the JSON Schema for the LLM input shape, enabling the LLM to understand the structure of type T. `NewTypedRecallTool` uses the same query/limit schema as `NewRecallTool`.

### Typed Memory Example

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

type Preference struct {
    Category string `json:"category"`
    Detail   string `json:"detail"`
}

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

    // Create a typed in-memory store.
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

    a, err := agent.Default(
        provider,
        prompt.Text("You are a helpful assistant that remembers user preferences."),
        tools,
    )
    if err != nil {
        log.Fatal(err)
    }

    ctx = agent.WithIdentifier(ctx, "user-789")

    result, _, err := a.Invoke(ctx, "I prefer dark mode and use vim as my editor.")
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println(result)
}
```

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

`InMemory` is a thread-safe in-memory `Memory` backed by an `agent.Embedder` for cosine similarity search. Good for prototyping and tests — for production, use a persistent backend like the [Redis Backend](#redis-backend) or [Postgres Backend](#postgres-backend).

```go
func NewInMemory(embedder agent.Embedder) *InMemory
```

The embedder computes embedding vectors for both `Remember` (embed the fact) and `Recall` (embed the query, then rank stored entries by cosine similarity). Internally, `NewInMemory` creates an `InMemoryStore` and wraps it in an `Adapter` — so it benefits from the composable storage layer while preserving the familiar API.

```go
import (
    "github.com/camilbinas/gude-agents/agent/memory"
    "github.com/camilbinas/gude-agents/agent/provider/bedrock"
)

embedder, err := bedrock.TitanEmbedV2()
if err != nil {
    log.Fatal(err)
}

store := memory.NewInMemory(embedder)
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

## Backends

The memory system supports multiple storage backends. Each backend implements `MemoryStore` directly using its native capabilities for identifier scoping — no intermediate abstraction layers.

| Backend | Import | Scoping | Dependencies |
|---------|--------|---------|--------------|
| [InMemoryStore](#inmemorystore) | `agent/memory` | Map partitioning | None |
| [Redis](#redis-backend) | `agent/memory/redis` | TAG field filter | Redis Stack |
| [Postgres](#postgres-backend) | `agent/memory/postgres` | SQL WHERE clause | PostgreSQL + pgvector |

### Redis Backend

Import: `github.com/camilbinas/gude-agents/agent/memory/redis`

`Store` implements both `memory.MemoryStore` and `memory.Memory` using Redis Stack (RediSearch) for vector similarity search. It uses a native Redis TAG field for identifier-based filtering — the identifier is stored as a top-level TAG field in each Redis hash, and search queries use `@identifier:{value}` to filter results at the index level.

Requires Redis Stack (not standard community Redis). The store uses RediSearch commands (`FT.CREATE`, `FT.SEARCH`) that are only available in Redis Stack.

```go
import (
    memredis "github.com/camilbinas/gude-agents/agent/memory/redis"
)

store, err := memredis.New(
    memredis.Options{Addr: "127.0.0.1:6379"},
    embedder,
    1024, // embedding dimension
)
```

The constructor pings Redis to verify connectivity, then creates a RediSearch HNSW index with the following schema:

| Field | Type | Description |
|-------|------|-------------|
| `content` | TEXT | The fact text |
| `metadata` | TEXT | JSON-serialized metadata map |
| `identifier` | TAG | The identifier string (indexed for native filtering) |
| `embedding` | VECTOR (HNSW) | Float32 embedding vector |

Search uses native TAG filtering — no post-search metadata filtering:

```
@identifier:{escaped_value}=>[KNN {topK} @embedding $BLOB AS score]
```

The `Store` also implements `Memory` directly via an internal `Adapter`, so it can be used with both the composable tools and the `Memory` interface:

```go
// Composable approach — pass the MemoryStore to tools.
tools := []tool.Tool{
    memory.NewRememberTool(store, embedder),
    memory.NewRecallTool(store, embedder),
}

// Memory interface approach — use Remember/Recall directly.
err := store.Remember(ctx, "user-123", "prefers dark mode", nil)
entries, err := store.Recall(ctx, "user-123", "theme preferences", 5)
```

#### Redis Configuration Options

| Option | Default | Description |
|--------|---------|-------------|
| `WithIndexName(name)` | `"gude_memory_idx"` | RediSearch index name |
| `WithKeyPrefix(prefix)` | `"gude:memory:"` | Redis key prefix for hash entries |
| `WithHNSWM(m)` | `16` | HNSW M parameter (max outgoing edges per node) |
| `WithHNSWEFConstruction(ef)` | `200` | HNSW EF_CONSTRUCTION parameter (search width during index build) |
| `WithDropExisting()` | disabled | Drop and recreate the index on construction (dev/testing only) |

#### Redis Error Wrapping

| Condition | Error |
|-----------|-------|
| nil embedder | `"redis memory: embedder is required"` |
| dim < 1 | `"redis memory: dim must be at least 1"` |
| Redis unreachable | `"redis memory: ping: <wrapped>"` |
| Index creation failure | `"redis memory: create index: <wrapped>"` |
| Empty identifier (Add/Search) | `"redis memory: identifier must not be empty"` |

### Postgres Backend

Import: `github.com/camilbinas/gude-agents/agent/memory/postgres`

`Store` implements both `memory.MemoryStore` and `memory.Memory` using PostgreSQL with pgvector for vector similarity search. It uses a dedicated `identifier` column with a SQL `WHERE` clause for scoping — the identifier is a first-class indexed column, not a JSONB metadata field.

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

When `WithAutoMigrate` is enabled, the constructor creates the pgvector extension, table, and indexes:

```sql
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS memory_entries (
    id         TEXT PRIMARY KEY,
    identifier TEXT NOT NULL,
    content    TEXT NOT NULL,
    metadata   JSONB,
    embedding  vector(1024) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS memory_entries_identifier_idx ON memory_entries (identifier);
CREATE INDEX IF NOT EXISTS memory_entries_embedding_idx ON memory_entries
    USING hnsw (embedding vector_cosine_ops) WITH (m = 16, ef_construction = 200);
```

Search uses native SQL scoping:

```sql
SELECT content, metadata, 1 - (embedding <=> $1) AS similarity
FROM memory_entries
WHERE identifier = $2
ORDER BY embedding <=> $1
LIMIT $3
```

Like the Redis backend, `Store` implements `Memory` directly via an internal `Adapter`:

```go
// Composable approach.
tools := []tool.Tool{
    memory.NewRememberTool(store, embedder),
    memory.NewRecallTool(store, embedder),
}

// Memory interface approach.
err := store.Remember(ctx, "user-123", "prefers dark mode", nil)
entries, err := store.Recall(ctx, "user-123", "theme preferences", 5)
```

#### Postgres Configuration Options

| Option | Default | Description |
|--------|---------|-------------|
| `WithTableName(name)` | `"memory_entries"` | PostgreSQL table name |
| `WithAutoMigrate()` | disabled | Create extension, table, and indexes on construction |
| `WithHNSW(m, efConstruction)` | `m=16, ef=200` | HNSW index parameters (used with `WithAutoMigrate`) |
| `WithIVFFlat(lists)` | disabled | Use IVFFlat index instead of HNSW (used with `WithAutoMigrate`) |
| `WithDistanceMetric(metric)` | `"cosine"` | Distance metric: `"cosine"`, `"l2"`, or `"inner_product"` |
| `WithDropExisting()` | disabled | Drop and recreate the table on construction (dev/testing only) |

#### Postgres Error Wrapping

| Condition | Error |
|-----------|-------|
| nil pool | `"postgres memory: pool is required"` |
| nil embedder | `"postgres memory: embedder is required"` |
| dim < 1 | `"postgres memory: dim must be at least 1"` |
| DDL failure | `"postgres memory: create table: <wrapped>"` |
| Empty identifier (Add/Search) | `"postgres memory: identifier must not be empty"` |