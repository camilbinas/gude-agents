# RAG Pipeline

The RAG (Retrieval-Augmented Generation) system lets agents ground their responses in external knowledge. You ingest documents into a vector store, then attach a retriever to your agent — either automatically (the agent retrieves on every call) or as a tool (the LLM decides when to retrieve). The framework provides an in-memory vector store, a text splitting and ingestion pipeline, and embedder implementations for Bedrock, OpenAI, and Gemini.

## rag.NewRetriever

`NewRetriever` creates a `Retriever` that embeds the query, searches the vector store, filters by score threshold, and optionally reranks results.

```go
retriever := rag.NewRetriever(embedder, store,
    rag.WithMaxResults(5),
    rag.WithScoreThreshold(0.7),
)
```

Defaults: `topK=4`, `scoreThreshold=0.0`, no reranker.

| Option | Description |
|--------|-------------|
| `WithMaxResults(k int)` | Maximum number of documents to retrieve (default: 4) |
| `WithScoreThreshold(t float64)` | Minimum cosine similarity score for returned documents (default: 0.0) |
| `WithReranker(rr agent.Reranker)` | Attaches a reranker that re-scores candidates after retrieval |

The retrieval pipeline runs in order: embed query → vector search → score threshold filter → rerank (if configured).

## Vector Store Implementations

### rag.MemoryStore

In-memory brute-force cosine similarity vector store. Good for prototyping and tests — for production, use a persistent vector store.

```go
store := rag.NewMemoryStore()
```

### PostgreSQL + pgvector

Import: `github.com/camilbinas/gude-agents/agent/rag/postgres`

Uses PostgreSQL with the [pgvector](https://github.com/pgvector/pgvector) extension for approximate nearest-neighbor search.

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

### Redis Stack

Import: `github.com/camilbinas/gude-agents/agent/rag/redis`

Uses Redis Stack (RediSearch) for vector search. Requires Redis Stack — not standard community Redis.

```go
store, err := ragredis.New(
    ragredis.Options{Addr: "localhost:6379"},
    "my-index", 1536,
)
```

See [Redis Providers](redis.md) for full documentation.

## Identifier-Scoped Storage (Moved)

Identifier-scoped storage — partitioning documents by user, tenant, or session — has moved to the [`agent/memory`](memory.md) package. If you were using `ScopedStore`, `ScopedSearcher`, or `ScopeMetadataKey` from this package, see the [Long-Term Memory](memory.md) docs for the replacement APIs.

## rag.Ingest

`Ingest` splits texts into chunks, embeds each chunk, and stores the results in a vector store.

```go
err := rag.Ingest(ctx, store, embedder, texts, metadata,
    rag.WithChunkSize(256),
    rag.WithChunkOverlap(32),
    rag.WithConcurrency(10),
)
```

- `texts` — the source documents to ingest
- `metadata` — optional per-text metadata (matched by index). Each chunk inherits its source text's metadata plus auto-generated `source_index` and `chunk_index` keys.

| Option | Default | Description |
|--------|---------|-------------|
| `WithChunkSize(n int)` | 512 | Maximum chunk size in runes |
| `WithChunkOverlap(n int)` | 64 | Overlap between consecutive chunks in runes |
| `WithConcurrency(n int)` | 1 | Number of parallel embedding calls. Higher values speed up ingestion but increase API request rate. |

### SplitText / SplitTextE

The ingestion pipeline uses `SplitText` internally, but you can call the text splitters directly:

```go
chunks := rag.SplitText(text, 512, 64)          // silently clamps invalid parameters
chunks, err := rag.SplitTextE(text, 512, 64)     // returns error for invalid parameters
```

## Embedder Implementations

### Bedrock — Titan Embed V2

Import: `github.com/camilbinas/gude-agents/agent/provider/bedrock`

```go
embedder, err := bedrock.TitanEmbedV2()
```

Uses `amazon.titan-embed-text-v2:0` (1024 dimensions). Uses the default AWS credential chain.

| Option | Description |
|--------|-------------|
| `WithRegion(region string)` | AWS region (defaults to `AWS_REGION` env var, then `us-east-1`) |

Other convenience constructors: `bedrock.CohereEmbedEnglishV3()`, `bedrock.CohereEmbedMultilingualV3()`, `bedrock.CohereEmbedV4()`. For a custom model ID use `ragbedrock.NewEmbedder(modelID, opts...)`.

### OpenAI — EmbeddingSmall / EmbeddingLarge

Import: `ragopenai "github.com/camilbinas/gude-agents/agent/rag/openai"`

```go
small, err := ragopenai.EmbeddingSmall()
large, err := ragopenai.EmbeddingLarge()
```

Convenience constructors for `text-embedding-3-small` and `text-embedding-3-large`.

| Option | Description |
|--------|-------------|
| `WithEmbedderAPIKey(key string)` | OpenAI API key (defaults to `OPENAI_API_KEY` env var) |
| `WithEmbedderBaseURL(url string)` | Custom base URL for OpenAI-compatible endpoints |
| `WithEmbedderModel(model string)` | Override the model name |

For a custom model use `ragopenai.NewEmbedder(opts...)` with `WithEmbedderModel`.

### Gemini

Import: `raggemini "github.com/camilbinas/gude-agents/agent/rag/gemini"`

```go
embedder, err := raggemini.GeminiEmbedding001()
embedder, err := raggemini.GeminiEmbedding002()  // newer multimodal model
```

| Option | Description |
|--------|-------------|
| `WithAPIKey(key string)` | Gemini API key (defaults to `GEMINI_API_KEY` → `GOOGLE_API_KEY` env vars) |
| `WithModel(model string)` | Override the model name |

For a custom model use `raggemini.NewEmbedder(raggemini.WithModel("my-model"))`.

## Managed Retrievers

Managed retrievers wrap cloud-hosted vector search services directly — no embedder or vector store setup required. Both implement `agent.Retriever` and work with `NewRetrieverTool` and `WithRetriever`.

### Bedrock Knowledge Base Retriever

Import: `rag "github.com/camilbinas/gude-agents/agent/rag/bedrock"`

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

Each returned `Document` has `Content` (the text chunk), `Metadata["score"]` (relevance score), and `Metadata["source"]` (S3 URI when available).

### OpenAI Vector Store Retriever

Import: `ragopenai "github.com/camilbinas/gude-agents/agent/rag/openai"`

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

Each returned `Document` has `Content` (concatenated text blocks), `Metadata["score"]`, `Metadata["filename"]`, and `Metadata["file_id"]`.

## Reranker Implementations

### Bedrock — Cohere Rerank 3.5 / Amazon Rerank 1.0

Import: `github.com/camilbinas/gude-agents/agent/provider/bedrock` (re-exported) or `ragbedrock "github.com/camilbinas/gude-agents/agent/rag/bedrock"` (direct)

```go
reranker := bedrock.MustReranker(bedrock.AmazonRerank10())

retriever := rag.NewRetriever(embedder, store,
    rag.WithTopK(20),
    rag.WithScoreThreshold(0.3),
    rag.WithReranker(reranker),
)
```

Convenience constructors: `CohereRerank35()`, `AmazonRerank10()`. For a custom model ID use `ragbedrock.NewReranker(modelID, opts...)`.

| Option | Description |
|--------|-------------|
| `WithRerankerRegion(region string)` | AWS region (defaults to `AWS_REGION` env var, then `us-east-1`) |
| `WithRerankerTopN(n int)` | Max documents to return after reranking (default: 0 = all) |

## Integration Patterns

There are two ways to wire RAG into an agent:

### Automatic Retrieval — WithRetriever

Attach a retriever to the agent. The agent calls `Retrieve` once per invocation (before the first provider call) and injects the formatted documents as context.

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

Wrap a retriever as a tool so the LLM decides when to search:

```go
searchTool := agent.NewRetrieverTool("search_docs", "Search the knowledge base", retriever)

a, err := agent.Default(
    provider,
    prompt.Text("You are a helpful assistant."),
    []tool.Tool{searchTool},
)
```

When the LLM calls this tool, it receives the formatted document content as the tool result. An optional `ContextFormatter` argument overrides the default.

### Choosing Between the Two

| | `WithRetriever` | `NewRetrieverTool` |
|---|---|---|
| **When retrieves** | Every invocation, before the first LLM call | LLM decides when to call it |
| **LLM calls** | 1 | 2+ (one to decide to search, one to answer) |
| **Speed** | Faster — retrieval and LLM call don't chain | Slower — extra round-trip |
| **Token cost** | Always pays retrieval cost | Only when LLM calls the tool |
| **Best for** | Document Q&A where context is always needed | Agents with multiple tools where search is optional |

Use `WithRetriever` when every question needs document context (e.g. a PDF assistant). Use `NewRetrieverTool` when the agent has other tools and retrieval is just one option.

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

- [Agent API](agent-api.md) — `WithRetriever` and `WithContextFormatter` options
- [Redis Providers](redis.md) — `VectorStore` (`agent/rag/redis`) for production vector search with Redis Stack
- [Long-Term Memory](memory.md) — identifier-scoped fact storage with composable `RememberTool` / `RecallTool`
- [Providers](providers.md) — Bedrock and OpenAI provider setup
- [Tool System](tools.md) — how tools work in the agent loop
- [Message Types](message-types.md) — `Document` and `ScoredDocument` types
- `examples/rag-pdf` — PDF document ingestion and querying
- `examples/rag-basic` — basic RAG with text documents
