package memory

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"

	"github.com/camilbinas/gude-agents/agent"
)

// vsEntry pairs a document with its embedding vector.
type vsEntry struct {
	doc       agent.Document
	embedding []float64
}

// InMemoryStore implements MemoryStore using an in-memory map partitioned
// by identifier. Each identifier gets its own slice of entries. Search uses
// brute-force cosine similarity. Safe for concurrent use.
type InMemoryStore struct {
	mu      sync.RWMutex
	entries map[string][]vsEntry // keyed by identifier
}

// NewInMemoryStore returns an empty InMemoryStore.
func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{
		entries: make(map[string][]vsEntry),
	}
}

// Add stores documents and their embeddings scoped to the given identifier.
// Returns an error if identifier is empty or if docs and embeddings have
// different lengths.
func (s *InMemoryStore) Add(ctx context.Context, identifier string, docs []agent.Document, embeddings [][]float64) error {
	if identifier == "" {
		return fmt.Errorf("memory: identifier must not be empty")
	}
	if len(docs) != len(embeddings) {
		return fmt.Errorf("memory: docs and embeddings length mismatch: %d vs %d", len(docs), len(embeddings))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, doc := range docs {
		s.entries[identifier] = append(s.entries[identifier], vsEntry{doc: doc, embedding: embeddings[i]})
	}
	return nil
}

// Search returns the top-K documents for the given identifier by cosine
// similarity to queryEmbedding. Results are ordered by descending score.
// Returns a non-nil empty slice when no documents match the identifier.
// Returns an error if identifier is empty or topK < 1.
func (s *InMemoryStore) Search(ctx context.Context, identifier string, queryEmbedding []float64, topK int) ([]agent.ScoredDocument, error) {
	if identifier == "" {
		return nil, fmt.Errorf("memory: identifier must not be empty")
	}
	if topK < 1 {
		return nil, fmt.Errorf("memory: topK must be >= 1, got %d", topK)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	bucket := s.entries[identifier]
	if len(bucket) == 0 {
		return []agent.ScoredDocument{}, nil
	}

	scored := make([]agent.ScoredDocument, len(bucket))
	for i, e := range bucket {
		scored[i] = agent.ScoredDocument{
			Document: e.doc,
			Score:    cosineSimilarity(queryEmbedding, e.embedding),
		}
	}

	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})

	if topK > len(scored) {
		topK = len(scored)
	}
	return scored[:topK], nil
}

// cosineSimilarity computes dot(a,b) / (norm(a) * norm(b)).
func cosineSimilarity(a, b []float64) float64 {
	n := len(a)
	if n != len(b) {
		return 0.0
	}
	var dot, normA, normB float64
	for i := 0; i < n; i++ {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	magA := math.Sqrt(normA)
	magB := math.Sqrt(normB)
	if magA == 0 || magB == 0 {
		return 0.0
	}
	return dot / (magA * magB)
}

// InMemory is an in-memory Memory backed by an InMemoryStore and Adapter.
// It provides the same public API as before but delegates all storage and
// similarity logic to the InMemoryStore.
type InMemory struct {
	adapter *Adapter
}

// NewInMemory creates a new in-memory Store. The embedder is used to compute
// embedding vectors for Remember and Recall operations.
func NewInMemory(embedder agent.Embedder) *InMemory {
	store := NewInMemoryStore()
	adapter := NewAdapter(store, embedder)
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
