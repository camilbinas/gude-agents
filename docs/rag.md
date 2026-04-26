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
| `WithMaxResults(k int)` | Maximum number of documents to retrieve (default: 4) |
| `WithScoreThreshold(t float64)` | Minimum cosine similarity score for returned documents (default: 0.0) |
| `WithReranker(rr agent.Reranker)` | Attaches a reranker that re-scores candidates after retrieval |

```go
retriever := rag.NewRetriever(embedder, store,
    rag.WithMaxResults(5),
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

## ScopedStore

`ScopedStore` wraps any `VectorStore` to partition documents by identifier — a user ID, tenant key, session, or any string you choose. It injects a reserved metadata key (`_scope_id`) into every document on `Add` and filters results by that key on `Search`. This gives you per-entity isolation on top of any backend without the backend needing to know about scoping.

### How It Works

```go
import "github.com/camilbinas/gude-agents/agent/rag"

store := rag.NewMemoryStore()
scoped := rag.NewScopedStore(store)
```

`NewScopedStore` accepts any `agent.VectorStore`. At construction time it checks whether the underlying store implements the optional `ScopedSearcher` interface. If it does (as `rag/redis.VectorStore` does), scoped searches use native filtering (e.g., Redis TAG queries). Otherwise, `ScopedStore` over-fetches 3× from the underlying store and post-filters by metadata — transparent to the caller either way.

### ScopeMetadataKey

```go
const ScopeMetadataKey = "_scope_id"
```

This is the reserved metadata key that `ScopedStore` uses internally. The underscore prefix avoids collisions with user-defined metadata. When `Add` is called, `ScopedStore` clones each document's metadata map (so the caller's map is never mutated) and sets `metadata["_scope_id"]` to the provided identifier. If a document already has a `_scope_id` key, it is overwritten.

### ScopedSearcher

```go
type ScopedSearcher interface {
    ScopedSearch(ctx context.Context, scopeKey, scopeValue string,
        queryEmbedding []float64, topK int) ([]agent.ScoredDocument, error)
}
```

`ScopedSearcher` is an optional interface that `VectorStore` backends can implement to support native scoped search. The Redis `VectorStore` implements this using TAG filtering in `FT.SEARCH`, which is significantly faster than post-filtering at scale. You never call `ScopedSearch` directly — `ScopedStore` detects and uses it automatically.

### API

**Add** stores documents scoped to an identifier:

```go
func (s *ScopedStore) Add(ctx context.Context, identifier string,
    docs []agent.Document, embeddings [][]float64) error
```

**Search** returns documents matching the given identifier:

```go
func (s *ScopedStore) Search(ctx context.Context, identifier string,
    queryEmbedding []float64, topK int) ([]agent.ScoredDocument, error)
```

Both methods return a descriptive error if the identifier is empty. `Search` returns a non-nil empty slice when no documents match.

### Example: Per-Tenant Document Retrieval

This example shows a multi-tenant RAG setup where each tenant's documents are isolated using `ScopedStore`. Documents ingested for one tenant never appear in another tenant's search results.

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/camilbinas/gude-agents/agent"
    "github.com/camilbinas/gude-agents/agent/provider/bedrock"
    "github.com/camilbinas/gude-agents/agent/rag"
)

func main() {
    ctx := context.Background()

    embedder, err := bedrock.TitanEmbedV2()
    if err != nil {
        log.Fatal(err)
    }

    // 1. Create a scoped store wrapping any VectorStore.
    store := rag.NewMemoryStore()
    scoped := rag.NewScopedStore(store)

    // 2. Ingest documents for tenant "acme".
    acmeDocs := []agent.Document{
        {Content: "Acme's Q4 revenue was $12M.", Metadata: map[string]string{"source": "financials"}},
        {Content: "Acme uses Go for backend services.", Metadata: map[string]string{"source": "engineering"}},
    }
    acmeEmbeddings := make([][]float64, len(acmeDocs))
    for i, doc := range acmeDocs {
        acmeEmbeddings[i], err = embedder.Embed(ctx, doc.Content)
        if err != nil {
            log.Fatal(err)
        }
    }
    if err := scoped.Add(ctx, "acme", acmeDocs, acmeEmbeddings); err != nil {
        log.Fatal(err)
    }

    // 3. Ingest documents for tenant "globex".
    globexDocs := []agent.Document{
        {Content: "Globex's Q4 revenue was $8M.", Metadata: map[string]string{"source": "financials"}},
    }
    globexEmbeddings := make([][]float64, len(globexDocs))
    for i, doc := range globexDocs {
        globexEmbeddings[i], err = embedder.Embed(ctx, doc.Content)
        if err != nil {
            log.Fatal(err)
        }
    }
    if err := scoped.Add(ctx, "globex", globexDocs, globexEmbeddings); err != nil {
        log.Fatal(err)
    }

    // 4. Search scoped to "acme" — only Acme's documents are returned.
    queryEmb, err := embedder.Embed(ctx, "What was the revenue?")
    if err != nil {
        log.Fatal(err)
    }
    results, err := scoped.Search(ctx, "acme", queryEmb, 5)
    if err != nil {
        log.Fatal(err)
    }

    fmt.Printf("Results for tenant 'acme' (%d):\n", len(results))
    for _, r := range results {
        fmt.Printf("  [%.4f] %s\n", r.Score, r.Document.Content)
    }
    // Only Acme documents appear — Globex documents are never returned.
}
```

For production deployments with Redis, swap `rag.NewMemoryStore()` for `ragredis.New(...)`. The `ScopedStore` automatically detects the Redis backend's `ScopedSearcher` implementation and uses native TAG filtering instead of post-filtering. See [Redis Providers](redis.md) for details on the TAG-based scoping optimization.

### See Also

- [Long-Term Memory](memory.md) — `ScopedStore` powers the composable `RememberTool` / `RecallTool` and the `Memory` adapter
- [Redis Providers](redis.md) — native `ScopedSearcher` implementation for Redis VectorStore

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
| `WithConcurrency(n int)` | 1 | Number of parallel embedding calls. Higher values speed up ingestion but increase API request rate. |

```go
err := rag.Ingest(ctx, store, embedder, texts, metadata,
    rag.WithChunkSize(256),
    rag.WithChunkOverlap(32),
    rag.WithConcurrency(10),
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
retriever := rag.NewRetriever(embedder, store, rag.WithMaxResults(3))

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

### Choosing Between the Two

| | `WithRetriever` | `NewRetrieverTool` |
|---|---|---|
| **When retrieves** | Every invocation, before the first LLM call | LLM decides when to call it |
| **LLM calls** | 1 | 2+ (one to decide to search, one to answer) |
| **Speed** | Faster — retrieval and LLM call don't chain | Slower — extra round-trip |
| **Token cost** | Always pays retrieval cost | Only when LLM calls the tool |
| **Best for** | Document Q&A where context is always needed | Agents with multiple tools where search is optional |

Use `WithRetriever` when every question needs document context (e.g. a PDF assistant). Use `NewRetrieverTool` when the agent has other tools and retrieval is just one option — the LLM can also formulate a better search query than the raw user message.

## Document Loaders

The `agent/rag/document` package extracts text from files for ingestion. It supports plain text, Markdown, CSV, JSON, YAML, HTML, source code, and DOCX out of the box. PDF support is available via a separate submodule.

### Loading Files

```go
import "github.com/camilbinas/gude-agents/agent/rag/document"

// Load specific files
texts, metadata, err := document.LoadFiles(ctx, []string{"guide.md", "faq.docx"})

// Load a directory (with optional extension filter)
texts, metadata, err := document.LoadDir(ctx, "docs/",
    document.WithExtensions(".md", ".txt", ".pdf"),
)
```

`LoadFiles` and `LoadDir` return parallel slices of text content and metadata ready for `rag.Ingest`:

```go
err = rag.Ingest(ctx, store, embedder, texts, metadata)
```

### PDF Support

PDF parsing requires a separate import — blank-import the `pdf` submodule to register the `.pdf` parser:

```go
import (
    "github.com/camilbinas/gude-agents/agent/rag/document"
    _ "github.com/camilbinas/gude-agents/agent/rag/document/pdf" // enables .pdf
)
```

Install: `go get github.com/camilbinas/gude-agents/agent/rag/document/pdf`

### Custom Parsers

Register a parser for any file extension:

```go
document.RegisterParser(".rtf", document.ParserFunc(func(ctx context.Context, path string) (string, error) {
    // your RTF extraction logic
    return text, nil
}))
```

### Options

| Option | Description |
|--------|-------------|
| `WithExtensions(".md", ".pdf")` | Only load files with these extensions |
| `WithParser(".ext", parser)` | Add a custom parser scoped to this call |
| `WithMaxDepth(n)` | Limit directory recursion depth. 1 = flat (no subdirectories), 2 = one level of subdirs, 0 = unlimited (default) |

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
        rag.WithMaxResults(2),
        rag.WithScoreThreshold(0.5),
    )

    // 5. Create an agent with automatic retrieval.
    provider, err := bedrock.Standard()
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
- [Long-Term Memory](memory.md) — composable `RememberTool` / `RecallTool` built on `ScopedStore`
- [Providers](providers.md) — Bedrock and OpenAI provider setup
- [Tool System](tools.md) — how tools work in the agent loop
- [Message Types](message-types.md) — `Document` and `ScoredDocument` types
- `examples/rag-pdf` — PDF document ingestion and querying
- `examples/rag-basic` — basic RAG with text documents
