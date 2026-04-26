# Redis Providers

The `agent/conversation/redis` package provides a persistent, Redis-backed implementation of `agent.Conversation`. Use `RedisConversation` for multi-turn conversation storage that survives restarts.

For Redis-backed vector search, see `agent/rag/redis` — it provides `VectorStore` (formerly `RedisVectorStore`) for similarity search powered by Redis Stack's HNSW indexing.

## RedisOptions

Both packages share a common connection configuration struct from `github.com/camilbinas/gude-agents/agent/redis`:

```go
type Options struct {
    Addr      string      // Redis address. Default: "localhost:6379"
    Password  string      // AUTH password. Empty string means no auth.
    DB        int         // Database number. Default: 0
    TLSConfig *tls.Config // Optional TLS configuration for encrypted connections.
}
```

If `Addr` is empty, it defaults to `"localhost:6379"`. Pass a `*tls.Config` to enable TLS — leave it `nil` for unencrypted connections.

## RedisConversation

`RedisConversation` implements `agent.Conversation` and `conversation.ConversationManager`. It stores conversation history as JSON in Redis string keys, with optional TTL and key prefix configuration.

Import: `github.com/camilbinas/gude-agents/agent/conversation/redis`

### New

```go
func New(opts RedisOptions, mopts ...RedisConversationOption) (*RedisConversation, error)
```

Creates a new `RedisConversation`. Pings Redis on creation to verify connectivity — returns an error if the connection fails. The default key prefix is `"gude:"` and TTL is 0 (no expiration).

### Options

#### WithTTL

```go
func WithTTL(d time.Duration) RedisConversationOption
```

Sets the TTL for conversation keys. Each `Save` call resets the TTL. Pass `0` to disable expiration (the default).

#### WithKeyPrefix

```go
func WithKeyPrefix(prefix string) RedisConversationOption
```

Sets the key prefix used for all conversation keys. Default: `"gude:"`. The final Redis key is `prefix + conversationID`.

### Methods

`RedisConversation` satisfies both `agent.Conversation` and `conversation.ConversationManager`:

- `Load(ctx, conversationID)` — retrieves the message history. Returns an empty slice if the key doesn't exist.
- `Save(ctx, conversationID, messages)` — persists the full message slice as JSON. Resets the TTL if one is configured.
- `List(ctx)` — scans all keys matching the prefix and returns conversation IDs (with the prefix stripped).
- `Delete(ctx, conversationID)` — removes the conversation key from Redis.

### Close

```go
func (m *RedisConversation) Close() error
```

Closes the underlying Redis client. Call this when you're done with the conversation store (typically via `defer`).

## VectorStore (Redis RAG)

`VectorStore` implements `agent.VectorStore` using Redis Stack's RediSearch module. It stores document embeddings as Redis hashes and creates an HNSW index for KNN similarity search.

> **Note:** `VectorStore` was previously named `RedisVectorStore` and lived in `agent/redis`. It now lives in `agent/rag/redis` as `VectorStore`. The constructor is `ragredis.New` (using the import alias below).

> **Requirement:** `VectorStore` requires [Redis Stack](https://redis.io/docs/stack/) (or the RediSearch module). Standard Redis does not support `FT.CREATE` / `FT.SEARCH` commands.

Import: `ragredis "github.com/camilbinas/gude-agents/agent/rag/redis"`

### New

```go
func New(opts Options, indexName string, dim int, vopts ...VectorStoreOption) (*VectorStore, error)
```

Creates a new `VectorStore`. Pings Redis, then creates an HNSW index via `FT.CREATE` if it doesn't already exist. Parameters:

- `opts` — Redis connection configuration (`ragredis.Options`, which is an alias for `agent/redis.Options`)
- `indexName` — name of the RediSearch index. Also used as the hash key prefix (`indexName + ":"`)
- `dim` — embedding dimension (must match your embedder's output, e.g. 1024 for Titan Embed V2)
- `vopts` — optional HNSW tuning parameters

The index is created with COSINE distance metric and FLOAT32 vector type.

### HNSW Options

#### WithHNSWM

```go
func WithHNSWM(m int) VectorStoreOption
```

Sets the HNSW `M` parameter — the number of bi-directional links per node. Higher values improve recall at the cost of memory. Default: `16`.

#### WithHNSWEFConstruction

```go
func WithHNSWEFConstruction(ef int) VectorStoreOption
```

Sets the HNSW `EF_CONSTRUCTION` parameter — the size of the dynamic candidate list during index building. Higher values improve index quality at the cost of build time. Default: `200`.

#### WithDropExisting

```go
func WithDropExisting() VectorStoreOption
```

Drops the index and all its documents before creating a fresh one. Useful for examples and development where you want a clean slate on every run. Do not use in production — it deletes all indexed data.

### Methods

- `Add(ctx, docs, embeddings)` — stores documents and their embeddings as Redis hashes. Each document gets a UUID-based key under the index prefix.
- `Search(ctx, queryEmbedding, topK)` — performs KNN similarity search using `FT.SEARCH`. Returns results sorted by descending cosine similarity (score = 1 - cosine distance).

### Close

```go
func (s *VectorStore) Close() error
```

Closes the underlying Redis client.

## ScopedSearch Optimization

When you wrap a Redis `VectorStore` in a [`ScopedStore`](rag.md#scopedstore), scoped searches use native Redis TAG filtering instead of post-search metadata filtering. This happens automatically — no extra configuration required.

### How It Works

The Redis `VectorStore` index schema includes a `_scope_id` TAG field alongside the standard `content`, `metadata`, and `embedding` fields. When `ScopedStore.Add` is called, it injects `_scope_id` into each document's metadata. The Redis `VectorStore.Add` method detects this key and stores it as a top-level TAG field in the Redis hash, making it available for native RediSearch filtering.

At construction time, `NewScopedStore` checks whether the underlying store implements the `ScopedSearcher` interface:

```go
type ScopedSearcher interface {
    ScopedSearch(ctx context.Context, scopeKey, scopeValue string,
        queryEmbedding []float64, topK int) ([]agent.ScoredDocument, error)
}
```

The Redis `VectorStore` implements `ScopedSearcher`. When `ScopedStore.Search` is called, it delegates to `ScopedSearch` which constructs an `FT.SEARCH` query with a TAG pre-filter:

```
@_scope_id:{escapedValue}=>[KNN topK @embedding $BLOB AS score]
```

This filters documents at the index level before the KNN search runs, so Redis only considers vectors that belong to the requested scope.

### Why This Matters

Without the optimization, `ScopedStore` falls back to over-fetching 3× the requested `topK` from the underlying store and post-filtering results by metadata. This works correctly but has two drawbacks at scale:

1. **Wasted computation** — Redis computes similarity scores for documents that will be discarded.
2. **Missed results** — if fewer than `topK` documents survive filtering from the 3× over-fetch, you get fewer results than requested.

With TAG filtering, Redis narrows the candidate set before running KNN, so every returned result belongs to the correct scope and no computation is wasted. The `escapeTag` helper ensures special characters in scope identifiers are properly escaped for RediSearch query syntax.

### Usage

No special setup is needed. Wrap a Redis `VectorStore` in a `ScopedStore` and the optimization is active:

```go
import (
    "github.com/camilbinas/gude-agents/agent/rag"
    ragredis "github.com/camilbinas/gude-agents/agent/rag/redis"
)

store, err := ragredis.New(
    ragredis.Options{Addr: "localhost:6379"},
    "my-index", 1024,
)
if err != nil {
    log.Fatal(err)
}
defer store.Close()

// ScopedStore detects ScopedSearcher and uses native TAG filtering.
scoped := rag.NewScopedStore(store)

// All Add/Search calls through scoped use the Redis TAG optimization.
```

This is the same pattern used by the [long-term memory Redis backend](memory.md#redis-backend) and the [composable memory tools](memory.md#composable-building-blocks) when backed by Redis.

### FT.CREATE Schema

For reference, the full index schema created by `ragredis.New`:

| Field | Type | Description |
|-------|------|-------------|
| `content` | TEXT | Document content |
| `metadata` | TEXT | JSON-serialized metadata map |
| `_scope_id` | TAG | Scope identifier for native filtering (populated when documents are added via `ScopedStore`) |
| `embedding` | VECTOR (HNSW) | Float32 binary blob with configurable dimension, M, and EF_CONSTRUCTION |

Documents added without `ScopedStore` (standard RAG usage) simply have no `_scope_id` TAG value. Existing non-scoped `Add` and `Search` operations are unaffected.

### See Also

- [RAG Pipeline — ScopedStore](rag.md#scopedstore) — full `ScopedStore` API reference and the `ScopedSearcher` interface
- [Long-Term Memory](memory.md) — composable `RememberTool` / `RecallTool` built on `ScopedStore`

## Code Example: Redis-Backed Conversation

This example creates a Redis-backed conversational agent with a 1-hour TTL and custom key prefix:

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	redismemory "github.com/camilbinas/gude-agents/agent/conversation/redis"
)

func main() {
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}

	mem, err := redismemory.New(
		redismemory.RedisOptions{Addr: redisAddr},
		redismemory.WithTTL(1*time.Hour),
		redismemory.WithKeyPrefix("example:"),
	)
	if err != nil {
		log.Fatalf("redis conversation: %v", err)
	}
	defer mem.Close()

	provider, err := bedrock.Standard()
	if err != nil {
		log.Fatal(err)
	}

	a, err := agent.Default(
		provider,
		prompt.Text("You are a helpful assistant. Be concise."),
		nil,
		agent.WithConversation(mem, "demo-conversation"),
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
}
```

## Code Example: Redis-Backed RAG

This example ingests documents into a Redis vector store and queries them using a retriever-backed agent. Requires Redis Stack.

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/rag"
	ragredis "github.com/camilbinas/gude-agents/agent/rag/redis"
)

func main() {
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}

	embedder, err := bedrock.TitanEmbedV2()
	if err != nil {
		log.Fatal(err)
	}

	store, err := ragredis.New(
		ragredis.Options{Addr: redisAddr},
		"example-docs", // index name
		1024,           // dimension (Titan Embed V2 outputs 1024)
	)
	if err != nil {
		log.Fatalf("redis vectorstore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	docs := []string{
		"Go is a statically typed, compiled language designed at Google.",
		"Redis is an in-memory data structure store used as a database, cache, and message broker.",
		"Kubernetes automates deployment, scaling, and management of containerized applications.",
	}

	if err = rag.Ingest(ctx, store, embedder, docs, nil); err != nil {
		log.Fatalf("ingest: %v", err)
	}
	fmt.Printf("Ingested %d documents\n", len(docs))

	provider, err := bedrock.Standard()
	if err != nil {
		log.Fatal(err)
	}

	retriever := rag.NewRetriever(embedder, store, rag.WithMaxResults(2))

	a, err := agent.Default(
		provider,
		prompt.Text("Answer questions using only the provided context. Be concise."),
		nil,
		agent.WithRetriever(retriever),
	)
	if err != nil {
		log.Fatal(err)
	}

	result, _, err := a.Invoke(ctx, "What is Redis used for?")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Answer:", result)
}
```

## See Also

- [Conversation System](conversation.md) — in-memory store and composable strategies (Window, Filter, Summary), plus S3 and DynamoDB drivers
- [Long-Term Memory](memory.md) — long-term knowledge storage with Remember/Recall tools and Redis backend
- [RAG Pipeline](rag.md) — embedders, retrievers, ingest pipeline, and integration patterns
- [Agent API Reference](agent-api.md) — `WithConversation` and `WithRetriever` options
- [Providers](providers.md) — Bedrock, Anthropic, and OpenAI provider configuration
