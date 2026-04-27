package memory

import (
	"context"
	"time"

	"github.com/camilbinas/gude-agents/agent"
)

// Memory stores and retrieves discrete facts (entries) keyed by an identifier.
// The identifier can represent a user, team, project, tenant, or any other entity.
// Unlike agent.Conversation which stores conversation message history, Memory
// stores long-lived knowledge retrieved by semantic similarity.
// Documented in docs/memory.md — update when changing interface.
type Memory interface {
	// Remember stores a fact for the given identifier. Metadata is optional.
	// Returns an error if identifier or fact is empty.
	Remember(ctx context.Context, identifier, fact string, metadata map[string]string) error

	// Recall retrieves the top entries for the given identifier by semantic
	// similarity to the query. Returns at most limit results, ordered by
	// descending score.
	// Returns an error if identifier is empty or limit < 1.
	// Returns an empty non-nil slice (not nil) if no entries exist for the identifier.
	Recall(ctx context.Context, identifier, query string, limit int) ([]Entry, error)
}

// Entry is a single unit of stored knowledge.
// Documented in docs/memory.md — update when changing fields or JSON tags.
type Entry struct {
	Fact      string            `json:"fact"`
	Metadata  map[string]string `json:"metadata"`
	CreatedAt time.Time         `json:"created_at"`
	Score     float64           `json:"score"`
}

// MemoryStore is the low-level storage interface for memory backends.
// It provides identifier-scoped document storage and vector similarity search.
// Backends implement this interface directly using their native capabilities
// (SQL WHERE clauses, Redis TAG filters, in-memory map partitioning).
//
// MemoryStore uses agent.Document and agent.ScoredDocument — the same types
// used by the RAG system — so that documents have a uniform representation
// across the library. These types are defined in the agent package, not the
// rag package, so importing them creates no coupling to RAG.
//
// Documented in docs/memory.md — update when changing interface.
type MemoryStore interface {
	// Add stores documents and their embeddings scoped to the given identifier.
	// Returns an error if identifier is empty.
	Add(ctx context.Context, identifier string, docs []agent.Document, embeddings [][]float64) error

	// Search returns the top-K documents for the given identifier by vector
	// similarity to queryEmbedding. Results are ordered by descending score.
	// Returns a non-nil empty slice when no documents match the identifier.
	// Returns an error if identifier is empty.
	Search(ctx context.Context, identifier string, queryEmbedding []float64, topK int) ([]agent.ScoredDocument, error)
}
