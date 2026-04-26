package rag

import (
	"context"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
)

// ---------------------------------------------------------------------------
// TestScopedStore_EmptyIdentifierError — Req 2.6
// Verifies that an empty identifier returns a descriptive error on both
// Add and Search.
// ---------------------------------------------------------------------------

func TestScopedStore_EmptyIdentifierError(t *testing.T) {
	backend := NewMemoryStore()
	scoped := NewScopedStore(backend)
	ctx := context.Background()

	embedder := &hashEmbedder{dim: 16}

	t.Run("Add with empty identifier", func(t *testing.T) {
		docs := []agent.Document{{Content: "hello", Metadata: map[string]string{}}}
		emb, err := embedder.Embed(ctx, "hello")
		if err != nil {
			t.Fatalf("Embed failed: %v", err)
		}
		err = scoped.Add(ctx, "", docs, [][]float64{emb})
		if err == nil {
			t.Fatal("expected error for empty identifier on Add, got nil")
		}
	})

	t.Run("Search with empty identifier", func(t *testing.T) {
		emb, err := embedder.Embed(ctx, "query")
		if err != nil {
			t.Fatalf("Embed failed: %v", err)
		}
		_, err = scoped.Search(ctx, "", emb, 5)
		if err == nil {
			t.Fatal("expected error for empty identifier on Search, got nil")
		}
	})
}

// ---------------------------------------------------------------------------
// TestScopedStore_EmptyResultNonNil — Req 2.4
// Verifies that Search for an unknown identifier returns a non-nil empty
// slice ([]ScoredDocument{}) rather than nil.
// ---------------------------------------------------------------------------

func TestScopedStore_EmptyResultNonNil(t *testing.T) {
	backend := NewMemoryStore()
	scoped := NewScopedStore(backend)
	ctx := context.Background()

	embedder := &hashEmbedder{dim: 16}

	// Add a document under identifier "alice".
	docs := []agent.Document{{Content: "alice data", Metadata: map[string]string{}}}
	emb, err := embedder.Embed(ctx, "alice data")
	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}
	if err := scoped.Add(ctx, "alice", docs, [][]float64{emb}); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Search for an identifier that has no documents.
	queryEmb, err := embedder.Embed(ctx, "some query")
	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}
	results, err := scoped.Search(ctx, "unknown-user", queryEmb, 5)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if results == nil {
		t.Fatal("expected non-nil empty slice, got nil")
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results for unknown identifier, got %d", len(results))
	}
}

// ---------------------------------------------------------------------------
// TestScopedStore_OverwriteExistingScope — Req 8.4
// Verifies that if a document's metadata already contains the reserved
// _scope_id key, ScopedStore.Add overwrites it with the provided identifier.
// ---------------------------------------------------------------------------

func TestScopedStore_OverwriteExistingScope(t *testing.T) {
	backend := &recordingScopedStore{}
	scoped := NewScopedStore(backend)
	ctx := context.Background()

	embedder := &hashEmbedder{dim: 16}

	// Document with a pre-existing _scope_id that should be overwritten.
	docs := []agent.Document{
		{
			Content: "important fact",
			Metadata: map[string]string{
				ScopeMetadataKey: "old-scope-value",
				"user_key":       "user_val",
			},
		},
	}
	emb, err := embedder.Embed(ctx, "important fact")
	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}

	newIdentifier := "new-scope-value"
	if err := scoped.Add(ctx, newIdentifier, docs, [][]float64{emb}); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Verify the stored document has the new identifier, not the old one.
	if len(backend.addedDocs) != 1 {
		t.Fatalf("expected 1 stored doc, got %d", len(backend.addedDocs))
	}

	stored := backend.addedDocs[0]
	scopeVal, ok := stored.Metadata[ScopeMetadataKey]
	if !ok {
		t.Fatalf("stored doc missing %q metadata key", ScopeMetadataKey)
	}
	if scopeVal != newIdentifier {
		t.Fatalf("expected %q = %q, got %q (old value was not overwritten)",
			ScopeMetadataKey, newIdentifier, scopeVal)
	}

	// Verify other metadata is preserved.
	if stored.Metadata["user_key"] != "user_val" {
		t.Fatalf("expected user_key = %q, got %q", "user_val", stored.Metadata["user_key"])
	}

	// Verify the original document's metadata was not mutated.
	if docs[0].Metadata[ScopeMetadataKey] != "old-scope-value" {
		t.Fatalf("original document metadata was mutated: _scope_id = %q, want %q",
			docs[0].Metadata[ScopeMetadataKey], "old-scope-value")
	}
}
