# Redis Providers

The `redis` package provides persistent, Redis-backed implementations of `agent.Memory` and `agent.VectorStore`. Use `RedisMemory` for multi-turn conversation storage that survives restarts, and `RedisVectorStore` for similarity search powered by Redis Stack's HNSW indexing.

Both types live in `github.com/camilbinas/gude-agents/agent/redis`.

## RedisOptions

All Redis providers share a common connection configuration struct:

```go
type RedisOptions struct {
    Addr      string      // Redis address. Default: "localhost:6379"
    Password  string      // AUTH password. Empty string means no auth.
    DB        int         // Database number. Default: 0
    TLSConfig *tls.Config // Optional TLS configuration for encrypted connections.
}
```

If `Addr` is empty, it defaults to `"localhost:6379"`. Pass a `*tls.Config` to enable TLS — leave it `nil` for unencrypted connections.

## RedisMemory

`RedisMemory` implements `agent.Memory` and `memory.MemoryManager`. It stores conversation history as JSON in Redis string keys, with optional TTL and key prefix configuration.

### NewRedisMemory

```go
func NewRedisMemory(opts RedisOptions, mopts ...RedisMemoryOption) (*RedisMemory, error)
```

Creates a new `RedisMemory`. Pings Redis on creation to verify connectivity — returns an error if the connection fails. The default key prefix is `"gude:memory:"` and TTL is 0 (no expiration).

### Options

#### WithTTL

```go
func WithTTL(d time.Duration) RedisMemoryOption
```

Sets the TTL for conversation keys. Each `Save` call resets the TTL. Pass `0` to disable expiration (the default).

#### WithKeyPrefix

```go
func WithKeyPrefix(prefix string) RedisMemoryOption
```

Sets the key prefix used for all conversation keys. Default: `"gude:memory:"`. The final Redis key is `prefix + conversationID`.

### Methods

`RedisMemory` satisfies both `agent.Memory` and `memory.MemoryManager`:

- `Load(ctx, conversationID)` — retrieves the message history. Returns an empty slice if the key doesn't exist.
- `Save(ctx, conversationID, messages)` — persists the full message slice as JSON. Resets the TTL if one is configured.
- `List(ctx)` — scans all keys matching the prefix and returns conversation IDs (with the prefix stripped).
- `Delete(ctx, conversationID)` — removes the conversation key from Redis.

### Close

```go
func (m *RedisMemory) Close() error
```

Closes the underlying Redis client. Call this when you're done with the memory (typically via `defer`).

## RedisVectorStore

`RedisVectorStore` implements `agent.VectorStore` using Redis Stack's RediSearch module. It stores document embeddings as Redis hashes and creates an HNSW index for KNN similarity search.

> **Requirement:** `RedisVectorStore` requires [Redis Stack](https://redis.io/docs/stack/) (or the RediSearch module). Standard Redis does not support `FT.CREATE` / `FT.SEARCH` commands.

### NewRedisVectorStore

```go
func NewRedisVectorStore(opts RedisOptions, indexName string, dim int, vopts ...RedisVectorStoreOption) (*RedisVectorStore, error)
```

Creates a new `RedisVectorStore`. Pings Redis, then creates an HNSW index via `FT.CREATE` if it doesn't already exist. Parameters:

- `opts` — Redis connection configuration
- `indexName` — name of the RediSearch index. Also used as the hash key prefix (`indexName + ":"`)
- `dim` — embedding dimension (must match your embedder's output, e.g. 1024 for Titan Embed V2)
- `vopts` — optional HNSW tuning parameters

The index is created with COSINE distance metric and FLOAT32 vector type.

### HNSW Options

#### WithHNSWM

```go
func WithHNSWM(m int) RedisVectorStoreOption
```

Sets the HNSW `M` parameter — the number of bi-directional links per node. Higher values improve recall at the cost of memory. Default: `16`.

#### WithHNSWEFConstruction

```go
func WithHNSWEFConstruction(ef int) RedisVectorStoreOption
```

Sets the HNSW `EF_CONSTRUCTION` parameter — the size of the dynamic candidate list during index building. Higher values improve index quality at the cost of build time. Default: `200`.

### Methods

- `Add(ctx, docs, embeddings)` — stores documents and their embeddings as Redis hashes. Each document gets a UUID-based key under the index prefix.
- `Search(ctx, queryEmbedding, topK)` — performs KNN similarity search using `FT.SEARCH`. Returns results sorted by descending cosine similarity (score = 1 - cosine distance).

### Close

```go
func (s *RedisVectorStore) Close() error
```

Closes the underlying Redis client.

## Code Example: Redis-Backed Memory

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
	"github.com/camilbinas/gude-agents/agent/redis"
)

func main() {
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}

	mem, err := redis.NewRedisMemory(
		redis.RedisOptions{Addr: redisAddr},
		redis.WithTTL(1*time.Hour),
		redis.WithKeyPrefix("example:memory:"),
	)
	if err != nil {
		log.Fatalf("redis memory: %v", err)
	}
	defer mem.Close()

	provider, err := bedrock.ClaudeSonnet4_6()
	if err != nil {
		log.Fatal(err)
	}

	a, err := agent.Default(
		provider,
		prompt.Text("You are a helpful assistant. Be concise."),
		nil,
		agent.WithMemory(mem, "demo-conversation"),
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
	"github.com/camilbinas/gude-agents/agent/redis"
	"github.com/camilbinas/gude-agents/agent/rag"
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

	store, err := redis.NewRedisVectorStore(
		redis.RedisOptions{Addr: redisAddr},
		"example-docs", // index name
		1024,           // dimension (Titan Embed V2 outputs 1024)
	)
	if err != nil {
		log.Fatalf("redis vectorstore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Ingest some documents.
	docs := []string{
		"Go is a statically typed, compiled language designed at Google.",
		"Redis is an in-memory data structure store used as a database, cache, and message broker.",
		"Kubernetes automates deployment, scaling, and management of containerized applications.",
	}

	err = rag.Ingest(ctx, store, embedder, docs, nil)
	if err != nil {
		log.Fatalf("ingest: %v", err)
	}
	fmt.Printf("Ingested %d documents\n", len(docs))

	// Create a retriever-backed agent.
	provider, err := bedrock.ClaudeSonnet4_6()
	if err != nil {
		log.Fatal(err)
	}

	retriever := rag.NewRetriever(embedder, store, rag.WithTopK(2))

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

- [Memory System](memory.md) — in-memory store and composable strategies (Window, Token, Filter, Summary)
- [RAG Pipeline](rag.md) — embedders, retrievers, ingest pipeline, and integration patterns
- [Agent API Reference](agent-api.md) — `WithMemory` and `WithRetriever` options
- [Providers](providers.md) — Bedrock, Anthropic, and OpenAI provider configuration
