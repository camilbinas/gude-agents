package memory

import (
	"context"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/rag"
)

// InMemory is an in-memory Memory backed by a rag.MemoryStore,
// rag.ScopedStore, and Adapter. It provides the same public API as before
// but delegates all storage and similarity logic to the unified RAG layer.
// Documented in docs/memory.md — update when changing fields or behavior.
type InMemory struct {
	adapter *Adapter
}

// NewInMemory creates a new in-memory Store. The embedder is used to compute
// embedding vectors for Remember and Recall operations.
func NewInMemory(embedder agent.Embedder) *InMemory {
	memStore := rag.NewMemoryStore()
	scopedStore := rag.NewScopedStore(memStore)
	adapter := NewAdapter(scopedStore, embedder)
	return &InMemory{
		adapter: adapter,
	}
}

// Remember stores a fact for the given identifier. Metadata is optional.
// Returns an error if identifier or fact is empty.
func (s *InMemory) Remember(ctx context.Context, identifier, fact string, metadata map[string]string) error {
	return s.adapter.Remember(ctx, identifier, fact, metadata)
}

// Recall retrieves the top entries for the given identifier by semantic
// similarity to the query. Returns at most limit results, ordered by
// descending score.
// Returns an error if identifier is empty or limit < 1.
// Returns an empty non-nil slice (not nil) if no entries exist for the identifier.
func (s *InMemory) Recall(ctx context.Context, identifier, query string, limit int) ([]Entry, error) {
	return s.adapter.Recall(ctx, identifier, query, limit)
}
