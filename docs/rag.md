# RAG Pipeline

The RAG (Retrieval-Augmented Generation) system lets agents ground their responses in external knowledge. You ingest documents into a vector store, then attach a retriever to your agent — either automatically (the agent retrieves on every call) or as a tool (the LLM decides when to retrieve). The framework provides core interfaces, an in-memory vector store, a text splitting and ingestion pipeline, and embedder implementations for Bedrock and OpenAI.

## Core Interfaces

All RAG components are defined as interfaces in the `agent` package, so you can swap implementations without changing agent code.

### Embedder

Converts text into a float vector:

```go
type Embedder interface {
    Embed(ctx context.Context, text string) ([]float64, error)
}
```

### VectorStore

Stores document embeddings and performs similarity search:

```go
type VectorStore interface {
    Add(ctx context.Context, docs []Document, embeddings [][]float64) error
    Search(ctx context.Context, queryEmbedding []float64, topK int) ([]ScoredDocument, error)
}
```

### Retriever

Retrieves relevant documents for a query string:

```go
type Retriever interface {
    Retrieve(ctx context.Context, query string) ([]Document, error)
}
```

### Reranker

Re-scores a candidate set of documents after initial retrieval:

```go
type Reranker interface {
    Rerank(ctx context.Context, query string, docs []Document) ([]Document, error)
}
```

### ContextFormatter

Formats retrieved documents into a string for injection into the conversation. It's a function type, not an interface:

```go
type ContextFormatter func(docs []Document) string
```

`DefaultContextFormatter` formats documents as numbered items wrapped in `<retrieved_context>` XML tags:

```
<retrieved_context>
[1] First document content
[2] Second document content
</retrieved_context>
```

### Document Types

```go
type Document struct {
    Content  string
    Metadata map[string]string
}

type ScoredDocument struct {
    Document Document
    Score    float64
}
```

## rag.NewRetriever

`NewRetriever` creates a `Retriever` that embeds the query, searches the vector store, filters by score threshold, and optionally reranks results.

```go
func NewRetriever(embedder agent.Embedder, store agent.VectorStore, opts ...RetrieverOption) *Retriever
```

Defaults: `topK=4`, `scoreThreshold=0.0`, no reranker.

### RetrieverOption

| Option | Description |
|--------|-------------|
| `WithTopK(k int)` | Maximum number of documents to retrieve (default: 4) |
| `WithScoreThreshold(t float64)` | Minimum cosine similarity score for returned documents (default: 0.0) |
| `WithReranker(rr agent.Reranker)` | Attaches a reranker that re-scores candidates after retrieval |

```go
retriever := rag.NewRetriever(embedder, store,
    rag.WithTopK(5),
    rag.WithScoreThreshold(0.7),
)
```

The retrieval pipeline runs in order: embed query → vector search → score threshold filter → rerank (if configured).

## Vector Store Implementations

### rag.MemoryStore

`MemoryStore` is a brute-force cosine similarity vector store backed by a Go slice. It's safe for concurrent use via `sync.RWMutex`. Good for prototyping and tests — for production, use a persistent vector store.

```go
store := rag.NewMemoryStore()
```

It implements `agent.VectorStore`:

- `Add(ctx, docs, embeddings)` — appends documents and their embeddings. Returns an error if `docs` and `embeddings` have different lengths.
- `Search(ctx, queryEmbedding, topK)` — returns the top-K documents by cosine similarity. Returns an error if `topK < 1`.

### PostgreSQL + pgvector — agent/rag/postgres

Import: `github.com/camilbinas/gude-agents/agent/rag/postgres`

Uses PostgreSQL with the [pgvector](https://github.com/pgvector/pgvector) extension for approximate nearest-neighbor search. Supports HNSW and IVFFlat indexes with cosine, L2, or inner product distance.

```go
pool, err := pgxpool.New(ctx, "postgres://user:pass@localhost:5432/mydb")

// Use an existing table with default column names:
store, err := ragpg.New(pool, 1536)

// Auto-create everything (development):
store, err := ragpg.New(pool, 1536, ragpg.WithAutoMigrate())

// Point at an existing table with custom columns:
store, err := ragpg.New(pool, 1536,
    ragpg.WithTableName("users"),
    ragpg.WithColumns("id", "bio", "", "embedding"),
)
```

| Option | Default | Description |
|--------|---------|-------------|
| `WithTableName(name string)` | `"documents"` | Table name for document storage |
| `WithColumns(id, content, meta, embed)` | `"id"`, `"content"`, `"metadata"`, `"embedding"` | Map to custom column names. Pass `""` for meta to skip metadata. |
| `WithAutoMigrate()` | off | Create extension, table, and index automatically |
| `WithHNSW(m, efConstruction int)` | m=16, ef=200 | HNSW index parameters (only with `WithAutoMigrate`) |
| `WithIVFFlat(lists int)` | 100 lists | IVFFlat index (only with `WithAutoMigrate`) |
| `WithDistanceMetric(metric string)` | `"cosine"` | Distance metric: `"cosine"`, `"l2"`, `"inner_product"` |

By default, the table must already exist. `WithColumns` lets you point the store at any existing table — for example, a `users` table with a `bio` column and an `embedding` column. Pass `""` for the metadata column if the table doesn't have one.

### Redis Stack — agent/rag/redis

Import: `github.com/camilbinas/gude-agents/agent/rag/redis`

Uses Redis Stack (RediSearch) for vector search. Requires Redis Stack — not standard community Redis.

```go
store, err := ragredis.New(
    ragredis.Options{Addr: "localhost:6379"},
    "my-index", 1536,
)
```

See [Redis Providers](redis.md) for full documentation.

## rag.Ingest

`Ingest` is a convenience pipeline that splits texts into chunks, embeds each chunk, and stores the results in a vector store.

```go
func Ingest(
    ctx context.Context,
    store agent.VectorStore,
    embedder agent.Embedder,
    texts []string,
    metadata []map[string]string,
    opts ...IngestOption,
) error
```

- `texts` — the source documents to ingest
- `metadata` — optional per-text metadata (matched by index). Each chunk inherits its source text's metadata plus auto-generated `source_index` and `chunk_index` keys.

### IngestOption

| Option | Default | Description |
|--------|---------|-------------|
| `WithChunkSize(n int)` | 512 | Maximum chunk size in runes |
| `WithChunkOverlap(n int)` | 64 | Overlap between consecutive chunks in runes |

```go
err := rag.Ingest(ctx, store, embedder, texts, metadata,
    rag.WithChunkSize(256),
    rag.WithChunkOverlap(32),
)
```

### SplitText / SplitTextE

The ingestion pipeline uses `SplitText` internally, but you can call the text splitters directly:

```go
// SplitText silently clamps invalid parameters.
chunks := rag.SplitText(text, 512, 64)

// SplitTextE returns an error for invalid parameters.
chunks, err := rag.SplitTextE(text, 512, 64)
```

Both split text into chunks of at most `chunkSize` runes with `chunkOverlap` runes of overlap between consecutive chunks. `SplitTextE` returns an error if `chunkSize < 1` or `chunkOverlap >= chunkSize`. `SplitText` silently clamps invalid values instead.

## Embedder Implementations

### Bedrock — Titan Embed V2

Import: `github.com/camilbinas/gude-agents/agent/provider/bedrock"`

```go
embedder, err := bedrock.TitanEmbedV2()
```

Uses the `amazon.titan-embed-text-v2:0` model via the Bedrock InvokeModel API. Produces 1024-dimensional normalized vectors.

| Option | Description |
|--------|-------------|
| `WithRegion(region string)` | AWS region (defaults to `AWS_REGION` env var, then `us-east-1`) |

Uses the default AWS credential chain.

Other convenience constructors: `bedrock.CohereEmbedEnglishV3()`, `bedrock.CohereEmbedMultilingualV3()`, `bedrock.CohereEmbedV4()`.

For a custom model ID use `ragbedrock.NewEmbedder(modelID, opts...)`.

### OpenAI — EmbeddingSmall / EmbeddingLarge

Import: `ragopenai "github.com/camilbinas/gude-agents/agent/rag/openai"`

```go
small, err := ragopenai.EmbeddingSmall()
large, err := ragopenai.EmbeddingLarge()
```

These are convenience constructors for `text-embedding-3-small` and `text-embedding-3-large` respectively.

| Option | Description |
|--------|-------------|
| `WithEmbedderAPIKey(key string)` | OpenAI API key (defaults to `OPENAI_API_KEY` env var) |
| `WithEmbedderBaseURL(url string)` | Custom base URL for OpenAI-compatible endpoints |
| `WithEmbedderModel(model string)` | Override the model name |

For a custom model use `ragopenai.NewEmbedder(opts...)` with `WithEmbedderModel`.

### Gemini — gemini-embedding-001

Import: `raggemini "github.com/camilbinas/gude-agents/agent/rag/gemini"`

```go
embedder, err := raggemini.GeminiEmbedding001()
// or:
embedder, err := raggemini.GeminiEmbedding002()
```

Uses `gemini-embedding-001` by default (768 dimensions). `gemini-embedding-002` is the newer multimodal model. Reads the API key from `GEMINI_API_KEY` or `GOOGLE_API_KEY` environment variables.

| Option | Description |
|--------|-------------|
| `WithAPIKey(key string)` | Gemini API key (defaults to `GEMINI_API_KEY` → `GOOGLE_API_KEY` env vars) |
| `WithModel(model string)` | Override the model name (default: `"gemini-embedding-001"`) |

For a custom model use `raggemini.NewEmbedder(raggemini.WithModel("my-model"))`.

## Managed Retrievers

Managed retrievers wrap cloud-hosted vector search services directly — no embedder or vector store setup required. Both implement `agent.Retriever` and work with `NewRetrieverTool` and `WithRetriever`.

### Bedrock Knowledge Base Retriever

Import: `rag "github.com/camilbinas/gude-agents/agent/rag/bedrock"`

Wraps the AWS Bedrock Knowledge Bases `Retrieve` API:

```go
retriever, err := rag.NewKnowledgeBaseRetriever("kb-xxxx",
    rag.WithKnowledgeBaseTopK(5),
    rag.WithKnowledgeBaseScoreThreshold(0.4),
)
```

| Option | Default | Description |
|--------|---------|-------------|
| `WithKnowledgeBaseRegion(region string)` | `AWS_REGION` env → `us-east-1` | AWS region |
| `WithKnowledgeBaseTopK(k int)` | 5 | Max results to fetch |
| `WithKnowledgeBaseScoreThreshold(t float64)` | 0.0 | Min relevance score |

Each returned `Document` has:
- `Content` — the retrieved text chunk
- `Metadata["score"]` — relevance score as a decimal string
- `Metadata["source"]` — S3 URI of the source document (when available)

### OpenAI Vector Store Retriever

Import: `ragopenai "github.com/camilbinas/gude-agents/agent/rag/openai"`

Wraps the OpenAI Vector Store Search API:

```go
retriever, err := ragopenai.NewVectorStoreRetriever("vs-xxxx",
    ragopenai.WithVectorStoreTopK(5),
    ragopenai.WithVectorStoreScoreThreshold(0.4),
)
```

| Option | Default | Description |
|--------|---------|-------------|
| `WithVectorStoreAPIKey(key string)` | `OPENAI_API_KEY` env | OpenAI API key |
| `WithVectorStoreBaseURL(url string)` | — | Custom base URL |
| `WithVectorStoreTopK(k int)` | 5 | Max results to fetch |
| `WithVectorStoreScoreThreshold(t float64)` | 0.0 | Min relevance score |

Each returned `Document` has:
- `Content` — concatenated text content blocks
- `Metadata["score"]` — relevance score as a decimal string
- `Metadata["filename"]` — source file name
- `Metadata["file_id"]` — source file ID

## Integration Patterns

There are two ways to wire RAG into an agent:

### Automatic Retrieval — WithRetriever

Attach a retriever to the agent with the `WithRetriever` option. The agent calls `Retrieve` once per invocation (before the first provider call) and injects the formatted documents as a user/assistant message turn in the conversation.

```go
retriever := rag.NewRetriever(embedder, store, rag.WithTopK(3))

a, err := agent.Default(
    provider,
    prompt.Text("Answer using only the provided context."),
    nil,
    agent.WithRetriever(retriever),
)
```

The retriever is called exactly once per `Invoke`/`InvokeStream` call, regardless of how many tool iterations occur. Retrieved context is injected as a message turn (not into the system prompt) and is not persisted to memory.

Use `WithContextFormatter` to customize how documents are rendered:

```go
agent.WithContextFormatter(func(docs []agent.Document) string {
    var b strings.Builder
    for _, d := range docs {
        fmt.Fprintf(&b, "- %s\n", d.Content)
    }
    return b.String()
})
```

### Manual Retrieval — NewRetrieverTool

Wrap a retriever as a tool so the LLM decides when to search. The tool exposes a single `query` string parameter.

```go
func NewRetrieverTool(name, description string, r Retriever, formatter ...ContextFormatter) tool.Tool
```

```go
searchTool := agent.NewRetrieverTool("search_docs", "Search the knowledge base", retriever)

a, err := agent.Default(
    provider,
    prompt.Text("You are a helpful assistant."),
    []tool.Tool{searchTool},
)
```

When the LLM calls this tool, it receives the formatted document content as the tool result. If no documents are found, the result is `"No relevant documents found."`. An optional `ContextFormatter` argument overrides `DefaultContextFormatter`.

## Code Example

Full RAG pipeline — ingest documents, create a retriever, and query with an agent:

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/camilbinas/gude-agents/agent"
    "github.com/camilbinas/gude-agents/agent/prompt"
    "github.com/camilbinas/gude-agents/agent/provider/bedrock"
    "github.com/camilbinas/gude-agents/agent/rag"
)

func main() {
    ctx := context.Background()

    // 1. Create an embedder.
    embedder, err := bedrock.TitanEmbedV2()
    if err != nil {
        log.Fatal(err)
    }

    // 2. Create an in-memory vector store.
    store := rag.NewMemoryStore()

    // 3. Ingest documents.
    docs := []string{
        "Go is a statically typed, compiled language designed at Google.",
        "Redis is an in-memory data structure store used as a database, cache, and message broker.",
        "Kubernetes automates deployment, scaling, and management of containerized applications.",
    }
    meta := []map[string]string{
        {"source": "go-docs"},
        {"source": "redis-docs"},
        {"source": "k8s-docs"},
    }

    err = rag.Ingest(ctx, store, embedder, docs, meta,
        rag.WithChunkSize(256),
        rag.WithChunkOverlap(32),
    )
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("Ingested %d documents\n", len(docs))

    // 4. Create a retriever.
    retriever := rag.NewRetriever(embedder, store,
        rag.WithTopK(2),
        rag.WithScoreThreshold(0.5),
    )

    // 5. Create an agent with automatic retrieval.
    provider, err := bedrock.ClaudeSonnet4_6()
    if err != nil {
        log.Fatal(err)
    }

    a, err := agent.Default(
        provider,
        prompt.Text("Answer questions using only the provided context. Be concise."),
        nil,
        agent.WithRetriever(retriever),
    )
    if err != nil {
        log.Fatal(err)
    }

    // 6. Query the agent.
    result, _, err := a.Invoke(ctx, "What is Go?")
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println("Answer:", result)
}
```

## See Also

- [Agent API Reference](agent-api.md) — `WithRetriever` and `WithContextFormatter` options
- [Redis Providers](redis.md) — `VectorStore` (`agent/rag/redis`) for production vector search with Redis Stack
- [Providers](providers.md) — Bedrock and OpenAI provider setup
- [Tool System](tools.md) — how tools work in the agent loop
- [Message Types](message-types.md) — `Document` and `ScoredDocument` types
