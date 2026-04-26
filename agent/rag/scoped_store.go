package rag

import (
	"context"
	"fmt"

	"github.com/camilbinas/gude-agents/agent"
)

// ScopeMetadataKey is the reserved metadata key used by ScopedStore to
// partition documents by identifier. The underscore prefix avoids
// collisions with user-defined metadata keys.
const ScopeMetadataKey = "_scope_id"

// ScopedSearcher is an optional interface that VectorStore backends can
// implement to support native scoped search (e.g., Redis TAG filtering).
// When the underlying store implements this, ScopedStore uses it instead
// of post-search metadata filtering.
type ScopedSearcher interface {
	ScopedSearch(ctx context.Context, scopeKey, scopeValue string,
		queryEmbedding []float64, topK int) ([]agent.ScoredDocument, error)
}

// ScopedStore wraps a VectorStore to partition documents by identifier.
// It injects a reserved metadata key into each document on Add and filters
// results by that key on Search. If the underlying store implements
// ScopedSearcher, native filtering is used; otherwise ScopedStore
// over-fetches and post-filters.
type ScopedStore struct {
	store          agent.VectorStore
	scopedSearcher ScopedSearcher // nil if store doesn't implement ScopedSearcher
}

// NewScopedStore creates a ScopedStore wrapping the given VectorStore.
// If the store implements ScopedSearcher, scoped searches use the native
// implementation. Otherwise, post-search metadata filtering is used.
func NewScopedStore(store agent.VectorStore) *ScopedStore {
	ss, _ := store.(ScopedSearcher)
	return &ScopedStore{store: store, scopedSearcher: ss}
}

// Add stores documents scoped to the given identifier. It clones each
// document's metadata map (to avoid mutating the caller's map), injects
// ScopeMetadataKey with the identifier value, then delegates to the
// underlying VectorStore. An empty identifier returns a descriptive error.
func (s *ScopedStore) Add(ctx context.Context, identifier string,
	docs []agent.Document, embeddings [][]float64) error {

	if identifier == "" {
		return fmt.Errorf("scopedstore: identifier must not be empty")
	}

	scoped := make([]agent.Document, len(docs))
	for i, doc := range docs {
		meta := make(map[string]string, len(doc.Metadata)+1)
		for k, v := range doc.Metadata {
			meta[k] = v
		}
		meta[ScopeMetadataKey] = identifier
		scoped[i] = agent.Document{
			Content:  doc.Content,
			Metadata: meta,
		}
	}

	if err := s.store.Add(ctx, scoped, embeddings); err != nil {
		return fmt.Errorf("scopedstore: add: %w", err)
	}
	return nil
}

// Search returns documents matching the given identifier. If the underlying
// store implements ScopedSearcher, native scoped search is used. Otherwise,
// ScopedStore over-fetches 3x from the underlying store and post-filters
// results by the ScopeMetadataKey. An empty identifier returns a descriptive
// error. When no documents match, a non-nil empty slice is returned.
func (s *ScopedStore) Search(ctx context.Context, identifier string,
	queryEmbedding []float64, topK int) ([]agent.ScoredDocument, error) {

	if identifier == "" {
		return nil, fmt.Errorf("scopedstore: identifier must not be empty")
	}

	if s.scopedSearcher != nil {
		results, err := s.scopedSearcher.ScopedSearch(ctx, ScopeMetadataKey, identifier, queryEmbedding, topK)
		if err != nil {
			return nil, fmt.Errorf("scopedstore: scoped search: %w", err)
		}
		if results == nil {
			return []agent.ScoredDocument{}, nil
		}
		return results, nil
	}

	// Fallback: over-fetch 3x and post-filter by metadata.
	overFetch := topK * 3
	all, err := s.store.Search(ctx, queryEmbedding, overFetch)
	if err != nil {
		return nil, fmt.Errorf("scopedstore: search: %w", err)
	}

	filtered := make([]agent.ScoredDocument, 0, topK)
	for _, sd := range all {
		if sd.Document.Metadata[ScopeMetadataKey] == identifier {
			filtered = append(filtered, sd)
			if len(filtered) >= topK {
				break
			}
		}
	}

	return filtered, nil
}
