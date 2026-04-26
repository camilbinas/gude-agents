package memory

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/rag"
)

// Compile-time check that Adapter implements Memory.
var _ Memory = (*Adapter)(nil)

// Adapter implements Memory using a ScopedStore and Embedder.
// It bridges the composable building blocks (VectorStore + ScopedStore +
// Embedder) into the existing Memory interface for backward
// compatibility.
// Documented in docs/memory.md — update when changing behavior.
type Adapter struct {
	store    *rag.ScopedStore
	embedder agent.Embedder
}

// NewAdapter creates a Memory adapter backed by a ScopedStore
// and Embedder.
func NewAdapter(store *rag.ScopedStore, embedder agent.Embedder) *Adapter {
	return &Adapter{
		store:    store,
		embedder: embedder,
	}
}

// Remember implements Memory.Remember. It validates inputs, embeds
// the fact, constructs a Document with created_at and user metadata, and
// stores it in the scoped store.
func (a *Adapter) Remember(ctx context.Context, identifier, fact string, metadata map[string]string) error {
	if identifier == "" {
		return errors.New("memory: identifier must not be empty")
	}
	if fact == "" {
		return errors.New("memory: fact must not be empty")
	}

	embedding, err := a.embedder.Embed(ctx, fact)
	if err != nil {
		return fmt.Errorf("memory: embed fact: %w", err)
	}

	meta := make(map[string]string, len(metadata)+1)
	for k, v := range metadata {
		meta[k] = v
	}
	meta["created_at"] = time.Now().UTC().Format(time.RFC3339)

	doc := agent.Document{
		Content:  fact,
		Metadata: meta,
	}

	if err := a.store.Add(ctx, identifier, []agent.Document{doc}, [][]float64{embedding}); err != nil {
		return fmt.Errorf("memory: store add: %w", err)
	}

	return nil
}

// Recall implements Memory.Recall. It validates inputs, embeds the
// query, searches the scoped store, and converts ScoredDocuments to Entries.
// Internal metadata keys (_scope_id and created_at) are excluded from the
// returned Entry.Metadata. Returns an empty non-nil slice when no results
// are found.
func (a *Adapter) Recall(ctx context.Context, identifier, query string, limit int) ([]Entry, error) {
	if identifier == "" {
		return nil, errors.New("memory: identifier must not be empty")
	}
	if limit < 1 {
		return nil, errors.New("memory: limit must be at least 1")
	}

	embedding, err := a.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("memory: embed query: %w", err)
	}

	results, err := a.store.Search(ctx, identifier, embedding, limit)
	if err != nil {
		return nil, fmt.Errorf("memory: store search: %w", err)
	}

	entries := make([]Entry, 0, len(results))
	for _, sd := range results {
		entry := Entry{
			Fact:  sd.Document.Content,
			Score: sd.Score,
		}

		// Parse created_at timestamp from metadata.
		if ts, ok := sd.Document.Metadata["created_at"]; ok {
			if t, err := time.Parse(time.RFC3339, ts); err == nil {
				entry.CreatedAt = t
			}
		}

		// Build user metadata, filtering out internal keys.
		userMeta := make(map[string]string, len(sd.Document.Metadata))
		for k, v := range sd.Document.Metadata {
			if k == rag.ScopeMetadataKey || k == "created_at" {
				continue
			}
			userMeta[k] = v
		}
		entry.Metadata = userMeta

		entries = append(entries, entry)
	}

	return entries, nil
}
