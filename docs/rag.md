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
retriever := rag.NewRetriever(embedder, store,
    rag.WithMaxResults(6),
    rag.WithScoreThreshold(0.75),
)
a, err := agent.New(prov, instructions,
    agent.WithRetriever(retriever),
    agent.WithContextFormatter(rag.DefaultContextFormatter),
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
