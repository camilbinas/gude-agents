package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// Document holds a text chunk and associated metadata.
// Documented in docs/message-types.md and docs/rag.md — update when changing fields.
type Document struct {
	Content  string
	Metadata map[string]string
}

// ScoredDocument pairs a Document with its similarity score.
// Documented in docs/message-types.md and docs/rag.md — update when changing fields.
type ScoredDocument struct {
	Document Document
	Score    float64
}

// Embedder converts text into a float vector.
// Documented in docs/rag.md — update when changing interface.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float64, error)
}

// VectorStore stores document embeddings and performs similarity search.
// Documented in docs/rag.md — update when changing interface.
type VectorStore interface {
	Add(ctx context.Context, docs []Document, embeddings [][]float64) error
	Search(ctx context.Context, queryEmbedding []float64, topK int) ([]ScoredDocument, error)
}

// Retriever retrieves relevant documents for a query.
// Documented in docs/rag.md — update when changing interface.
type Retriever interface {
	Retrieve(ctx context.Context, query string) ([]Document, error)
}

// Reranker re-scores a candidate set of documents for a query.
// Documented in docs/rag.md — update when changing interface.
type Reranker interface {
	Rerank(ctx context.Context, query string, docs []Document) ([]Document, error)
}

// ContextFormatter formats retrieved documents into a string for prompt injection.
// Documented in docs/rag.md — update when changing type or DefaultContextFormatter.
type ContextFormatter func(docs []Document) string

// DefaultContextFormatter formats documents as numbered items wrapped in
// <retrieved_context> tags. The tags signal to the model that this content
// is external data and should not be interpreted as instructions.
var DefaultContextFormatter ContextFormatter = func(docs []Document) string {
	if len(docs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<retrieved_context>\n")
	for i, doc := range docs {
		fmt.Fprintf(&b, "[%d] %s\n", i+1, doc.Content)
	}
	b.WriteString("</retrieved_context>")
	return b.String()
}

// NewRetrieverTool wraps a Retriever as a tool.Tool so the LLM can
// decide when to retrieve. An optional ContextFormatter controls how
// documents are rendered in the tool result; DefaultContextFormatter is
// used when none is provided.
// Documented in docs/rag.md — update when changing signature or behavior.
func NewRetrieverTool(name, description string, r Retriever, formatter ...ContextFormatter) tool.Tool {
	fmtFn := DefaultContextFormatter
	if len(formatter) > 0 && formatter[0] != nil {
		fmtFn = formatter[0]
	}

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string"},
		},
		"required": []any{"query"},
	}

	return tool.NewRaw(name, description, schema, func(ctx context.Context, input json.RawMessage) (string, error) {
		var params struct {
			Query string `json:"query"`
		}
		if err := json.Unmarshal(input, &params); err != nil {
			return "", err
		}

		docs, err := r.Retrieve(ctx, params.Query)
		if err != nil {
			return "", err
		}

		if len(docs) == 0 {
			return "No relevant documents found.", nil
		}

		return fmtFn(docs), nil
	})
}
