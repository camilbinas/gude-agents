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

`SplitText` counts runes. `chunkSize` must be positive and `overlap` must be in `[0, chunkSize)`. With a positive `overlap`, the final chunk always reaches the end of the input and is never a sub-overlap remnant of the previous chunk, so no duplicate near-empty chunk is emitted.

The package includes an in-memory manager plus PostgreSQL and Redis stores; embedder integrations include Bedrock and OpenAI. Bedrock also provides Knowledge Base retrieval and reranking. Choose a backend that explicitly supports any metadata/full-text capability you use.

### Idempotent re-ingest requires a `source`

`rag.Ingest` only replaces existing chunks when the store implements `rag.Manager` **and** the per-text metadata carries a `source` key. For each distinct `source` being ingested, Ingest first calls `DeleteByMetadata({"source": ...})`, so re-ingesting the same source replaces its chunks. A text with no `source` (or an empty one) is never deduplicated: because generated chunk IDs are random, every re-ingest appends a fresh set of chunks. `document.LoadFiles`/`LoadDir` set `source` automatically; set it yourself for direct `Ingest` calls that may run more than once.

### Embedding dimension enforcement

The Postgres and Redis vector stores reject an `Upsert`/`Search` embedding whose length does not match the store's configured `dim`, and reject any non-finite (`NaN`/`Inf`) component, rather than surfacing an opaque engine error or writing corrupt vectors. `redis.New` also validates `dim >= 1` and a non-empty index name, matching `postgres.New`.

### Redis `DeleteByMetadata` (experimental)

The Redis vector store stores metadata as a single JSON string in a RediSearch `TEXT` field, and `DeleteByMetadata` matches each filter pair by substring. This is **experimental**: substring matching can over-match and is sensitive to tokenization. Filter keys/values containing `"`, `:`, or `\` are rejected with `redis.ErrUnsafeFilterValue`; delete such documents by ID instead. For exact, injection-safe filtering, promote the keys you filter on to dedicated indexed fields. The in-memory and Postgres managers match metadata exactly.

### Bedrock Titan output dimensions

`bedrock.WithDimensions` now applies to Amazon Titan Embeddings V2 as well as Cohere Embed v4. For Titan, allowed values are 256, 512, and 1024 (default 1024 when unset); an unsupported value is rejected by `Embed`. The value has no effect on Cohere v3 models.

## Backend and managed retriever setup

[`examples/rag`](../examples/rag/) is the in-memory learning workflow. For a durable vector store, replace only the store constructor:

```go
store, err := postgres.New(pool, dimensions) // agent/rag/postgres
// or: redis.New(client, dimensions)          // agent/rag/redis
retriever := rag.NewRetriever(embedder, store)
```

Managed retrievers such as Bedrock Knowledge Bases and OpenAI vector stores implement `rag.Retriever` directly. The Agent setup remains `agent.RAGAgent(provider, instructions, retriever)`.

## Live embedder smoke tests
The Bedrock and OpenAI embedders each have an opt-in `live_test.go`. It is skipped unless `TEST_LIVE_INFERENCE` is exactly `1`, before any client is built. When enabled, `TEST_EMBEDDING_MODEL_ID` is required (there is no default embedder), and the Bedrock test also requires `AWS_REGION`. `TEST_EMBEDDING_MODEL_ID` or credentials alone never enable live calls. Run from `agent/rag/bedrock` or `agent/rag/openai`:
```sh
TEST_LIVE_INFERENCE=1 TEST_EMBEDDING_MODEL_ID=<embedding-model> go test -run Live -v ./...
```
The tests check that vectors are non-empty, consistent in length across calls, and returned in batch order. Reranking and Knowledge Base retrieval have no live test because they need provisioned resources (a Knowledge Base ID or a rerank model) that this opt-in scheme does not configure. They call a paid API and are not run by CI or the release process.
