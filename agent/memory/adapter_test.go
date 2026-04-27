package memory

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
)

// TestAdapter_ValidationErrors verifies that the Adapter returns descriptive
// errors for invalid inputs: empty identifier, empty fact, and limit < 1.
//
// Requirements: 3.1, 3.4
func TestAdapter_ValidationErrors(t *testing.T) {
	store := NewInMemoryStore()
	adapter := NewAdapter(store, &hashEmbedder{dim: 16})
	ctx := context.Background()

	t.Run("Remember_EmptyIdentifier", func(t *testing.T) {
		err := adapter.Remember(ctx, "", "some fact", nil)
		if err == nil {
			t.Fatal("expected error for empty identifier, got nil")
		}
		const want = "memory: identifier must not be empty"
		if err.Error() != want {
			t.Fatalf("error = %q, want %q", err.Error(), want)
		}
	})

	t.Run("Remember_EmptyFact", func(t *testing.T) {
		err := adapter.Remember(ctx, "user-1", "", nil)
		if err == nil {
			t.Fatal("expected error for empty fact, got nil")
		}
		const want = "memory: fact must not be empty"
		if err.Error() != want {
			t.Fatalf("error = %q, want %q", err.Error(), want)
		}
	})

	t.Run("Recall_EmptyIdentifier", func(t *testing.T) {
		entries, err := adapter.Recall(ctx, "", "query", 5)
		if err == nil {
			t.Fatal("expected error for empty identifier, got nil")
		}
		const want = "memory: identifier must not be empty"
		if err.Error() != want {
			t.Fatalf("error = %q, want %q", err.Error(), want)
		}
		if entries != nil {
			t.Fatalf("expected nil entries on error, got %v", entries)
		}
	})

	t.Run("Recall_LimitZero", func(t *testing.T) {
		entries, err := adapter.Recall(ctx, "user-1", "query", 0)
		if err == nil {
			t.Fatal("expected error for limit 0, got nil")
		}
		const want = "memory: limit must be at least 1"
		if err.Error() != want {
			t.Fatalf("error = %q, want %q", err.Error(), want)
		}
		if entries != nil {
			t.Fatalf("expected nil entries on error, got %v", entries)
		}
	})

	t.Run("Recall_LimitNegative", func(t *testing.T) {
		for _, limit := range []int{-1, -100} {
			t.Run(fmt.Sprintf("limit=%d", limit), func(t *testing.T) {
				entries, err := adapter.Recall(ctx, "user-1", "query", limit)
				if err == nil {
					t.Fatalf("expected error for limit %d, got nil", limit)
				}
				const want = "memory: limit must be at least 1"
				if err.Error() != want {
					t.Fatalf("error = %q, want %q", err.Error(), want)
				}
				if entries != nil {
					t.Fatalf("expected nil entries on error, got %v", entries)
				}
			})
		}
	})
}

// TestAdapter_EmbedderFailurePropagation verifies that embedder errors are
// wrapped and propagated by both Remember and Recall.
//
// Requirements: 3.2, 3.3
func TestAdapter_EmbedderFailurePropagation(t *testing.T) {
	sentinel := errors.New("embed boom")
	store := NewInMemoryStore()
	adapter := NewAdapter(store, &errorEmbedder{err: sentinel})
	ctx := context.Background()

	t.Run("Remember_EmbedderError", func(t *testing.T) {
		err := adapter.Remember(ctx, "user-1", "some fact", nil)
		if err == nil {
			t.Fatal("expected error from embedder, got nil")
		}
		if !errors.Is(err, sentinel) {
			t.Fatalf("error does not wrap sentinel: %v", err)
		}
		if got := err.Error(); got != "memory: embed fact: embed boom" {
			t.Fatalf("error = %q, want %q", got, "memory: embed fact: embed boom")
		}
	})

	t.Run("Recall_EmbedderError", func(t *testing.T) {
		entries, err := adapter.Recall(ctx, "user-1", "query", 5)
		if err == nil {
			t.Fatal("expected error from embedder, got nil")
		}
		if !errors.Is(err, sentinel) {
			t.Fatalf("error does not wrap sentinel: %v", err)
		}
		if got := err.Error(); got != "memory: embed query: embed boom" {
			t.Fatalf("error = %q, want %q", got, "memory: embed query: embed boom")
		}
		if entries != nil {
			t.Fatalf("expected nil entries on error, got %v", entries)
		}
	})
}

// failingStore is a mock MemoryStore that returns configurable errors from
// Add and Search.
type failingStore struct {
	addErr    error
	searchErr error
}

func (f *failingStore) Add(_ context.Context, _ string, _ []agent.Document, _ [][]float64) error {
	return f.addErr
}

func (f *failingStore) Search(_ context.Context, _ string, _ []float64, _ int) ([]agent.ScoredDocument, error) {
	return nil, f.searchErr
}

// TestAdapter_StoreFailurePropagation verifies that store errors are wrapped
// and propagated by both Remember and Recall.
//
// Requirements: 3.2, 3.3
func TestAdapter_StoreFailurePropagation(t *testing.T) {
	ctx := context.Background()

	t.Run("Remember_StoreAddError", func(t *testing.T) {
		sentinel := errors.New("store add boom")
		adapter := NewAdapter(
			&failingStore{addErr: sentinel},
			&hashEmbedder{dim: 16},
		)
		err := adapter.Remember(ctx, "user-1", "some fact", nil)
		if err == nil {
			t.Fatal("expected error from store, got nil")
		}
		if !errors.Is(err, sentinel) {
			t.Fatalf("error does not wrap sentinel: %v", err)
		}
		if got := err.Error(); got != "memory: store add: store add boom" {
			t.Fatalf("error = %q, want %q", got, "memory: store add: store add boom")
		}
	})

	t.Run("Recall_StoreSearchError", func(t *testing.T) {
		sentinel := errors.New("store search boom")
		adapter := NewAdapter(
			&failingStore{searchErr: sentinel},
			&hashEmbedder{dim: 16},
		)
		entries, err := adapter.Recall(ctx, "user-1", "query", 5)
		if err == nil {
			t.Fatal("expected error from store, got nil")
		}
		if !errors.Is(err, sentinel) {
			t.Fatalf("error does not wrap sentinel: %v", err)
		}
		if got := err.Error(); got != "memory: store search: store search boom" {
			t.Fatalf("error = %q, want %q", got, "memory: store search: store search boom")
		}
		if entries != nil {
			t.Fatalf("expected nil entries on error, got %v", entries)
		}
	})
}

// TestAdapter_CreatedAtMetadataInjection verifies that Remember injects a
// created_at metadata key and that Recall parses it into Entry.CreatedAt,
// excluding it from the returned Entry.Metadata.
//
// Requirements: 3.2, 3.5
func TestAdapter_CreatedAtMetadataInjection(t *testing.T) {
	store := NewInMemoryStore()
	embedder := &hashEmbedder{dim: 16}
	adapter := NewAdapter(store, embedder)
	ctx := context.Background()

	userMeta := map[string]string{"source": "test", "priority": "high"}
	err := adapter.Remember(ctx, "user-1", "important fact", userMeta)
	if err != nil {
		t.Fatalf("Remember failed: %v", err)
	}

	entries, err := adapter.Recall(ctx, "user-1", "important fact", 1)
	if err != nil {
		t.Fatalf("Recall failed: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one entry, got none")
	}

	entry := entries[0]

	// Verify created_at was parsed into the CreatedAt field.
	if entry.CreatedAt.IsZero() {
		t.Fatal("expected non-zero CreatedAt, got zero")
	}

	// Verify created_at is NOT in the returned metadata.
	if _, ok := entry.Metadata["created_at"]; ok {
		t.Fatal("created_at should not be present in returned Entry.Metadata")
	}

	// Verify user-provided metadata keys are present.
	for k, v := range userMeta {
		got, ok := entry.Metadata[k]
		if !ok {
			t.Fatalf("expected metadata key %q, not found", k)
		}
		if got != v {
			t.Fatalf("metadata[%q] = %q, want %q", k, got, v)
		}
	}
}

// TestAdapter_ScopeIDNotInReturnedMetadata verifies that _scope_id is NOT
// present in the metadata returned by Recall. Since MemoryStore backends
// handle identifier scoping natively, _scope_id is never injected.
//
// Requirements: 3.5
func TestAdapter_ScopeIDNotInReturnedMetadata(t *testing.T) {
	store := NewInMemoryStore()
	embedder := &hashEmbedder{dim: 16}
	adapter := NewAdapter(store, embedder)
	ctx := context.Background()

	err := adapter.Remember(ctx, "user-1", "a fact about scope", map[string]string{"tag": "test"})
	if err != nil {
		t.Fatalf("Remember failed: %v", err)
	}

	entries, err := adapter.Recall(ctx, "user-1", "a fact about scope", 1)
	if err != nil {
		t.Fatalf("Recall failed: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one entry, got none")
	}

	entry := entries[0]

	// _scope_id must NOT be present — MemoryStore handles scoping natively.
	if _, ok := entry.Metadata["_scope_id"]; ok {
		t.Fatal("_scope_id should not be present in returned Entry.Metadata")
	}

	// Verify the user-provided metadata is still there.
	if got := entry.Metadata["tag"]; got != "test" {
		t.Fatalf("metadata[tag] = %q, want %q", got, "test")
	}
}
