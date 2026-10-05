# Retrieval-augmented generation

The `agent/rag` package owns all portable retrieval types:

```go
type Document struct {
    ID       string
    Content  string
    Metadata map[string]string
}

type Embedder interface { Embed(context.Context, string) ([]float64, error) }
type Retriever interface { Retrieve(context.Context, string) ([]rag.Document, error) }
type Reranker interface { Rerank(context.Context, string, []rag.Document) ([]rag.Document, error) }
```

`rag.Store` provides `Upsert`, vector `Search`, and `Delete`. `rag.Manager` adds `Find` and `DeleteByMetadata`. The package also defines `ScoredDocument`, filtered/full-text capabilities, and `ContextFormatter`.

## Automatic retrieval

```go
retriever := rag.NewRetriever(
    embedder,
    store,
    rag.WithMaxResults(6),
    rag.WithScoreThreshold(0.75),
)

a, err := agent.RAGAgent(
    provider,
    "Answer using the retrieved documentation.",
    retriever,
)
```

`agent.RAGAgent` is for Agents whose defining behavior is automatic retrieval. The retriever is mandatory: a nil retriever returns `agent.ErrRetrieverRequired` at construction instead of producing an Agent that silently skips context injection. It is not a preset — iterations, tool execution, and every other setting use the same defaults as `agent.New`. Pass further options as usual, for example `agent.WithContextFormatter(...)`.

The equivalent lower-level form is useful when retrieval is one optional feature among many:

```go
a, err := agent.New(
    provider,
    instructions,
    agent.WithRetriever(retriever),
)
```

The engine retrieves before the provider call and injects formatted context transiently. Retrieved context is not persisted into conversation history.

## Model-directed retrieval

```go
retrieve := rag.NewRetrieverTool(
    "retrieve_docs",
    "Retrieve relevant product documentation",
    retriever,
)
a, err := agent.New(prov, instructions, agent.WithTools(retrieve))
```

## Ingestion

```go
chunks, err := rag.SplitText(text, 1200, 150)
if err != nil { return err }
docs := make([]rag.Document, len(chunks))
for i, chunk := range chunks {
    docs[i] = rag.Document{Content: chunk, Metadata: map[string]string{"source": path}}
}
```

`SplitText` counts runes. `chunkSize` must be positive and `overlap` must be in `[0, chunkSize)`.

The package includes an in-memory manager plus PostgreSQL and Redis stores; embedder integrations include Bedrock, Gemini, and OpenAI. Bedrock also provides Knowledge Base retrieval and reranking. Choose a backend that explicitly supports any metadata/full-text capability you use.

## Backend and managed retriever setup

[`examples/rag`](../examples/rag/) is the in-memory learning workflow. For a durable vector store, replace only the store constructor:

```go
store, err := postgres.New(pool, dimensions) // agent/rag/postgres
// or: redis.New(client, dimensions)          // agent/rag/redis
retriever := rag.NewRetriever(embedder, store)
```

Managed retrievers such as Bedrock Knowledge Bases and OpenAI vector stores implement `rag.Retriever` directly. The Agent setup remains `agent.RAGAgent(provider, instructions, retriever)`.
