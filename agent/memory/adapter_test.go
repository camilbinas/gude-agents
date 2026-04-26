package memory

import (
	"context"
	"fmt"
	"testing"

	"github.com/camilbinas/gude-agents/agent/rag"
)

// TestAdapter_ValidationErrors verifies that the Adapter returns descriptive
// errors for invalid inputs: empty identifier, empty fact, and limit < 1.
//
// Validates: Requirements 6.1, 6.2
func TestAdapter_ValidationErrors(t *testing.T) {
	memStore := rag.NewMemoryStore()
	scopedStore := rag.NewScopedStore(memStore)
	adapter := NewAdapter(scopedStore, &hashEmbedder{dim: 16})
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
