package memory

import (
	"context"
	"testing"

	"github.com/camilbinas/gude-agents/agent/rag"
	"pgregory.net/rapid"
)

//
// TestProperty_AdapterRoundTrip verifies that for any non-empty identifier and
// any non-empty fact string with arbitrary metadata, after calling
// Adapter.Remember(ctx, identifier, fact, metadata), calling
// Adapter.Recall(ctx, identifier, fact, 10) returns a non-empty []Entry
// slice containing an Entry whose Fact field equals the stored fact.
//
func TestProperty_AdapterRoundTrip(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		identifier := genNonEmptyString(rt, "identifier")
		fact := genNonEmptyString(rt, "fact")
		metadata := genMetadata(rt)

		// Build composable stack: MemoryStore → ScopedStore → Adapter.
		memStore := rag.NewMemoryStore()
		scopedStore := rag.NewScopedStore(memStore)
		adapter := NewAdapter(scopedStore, &hashEmbedder{dim: 16})

		ctx := context.Background()

		// Remember the fact.
		err := adapter.Remember(ctx, identifier, fact, metadata)
		if err != nil {
			rt.Fatalf("Adapter.Remember failed: %v", err)
		}

		// Recall using the same fact as the query.
		entries, err := adapter.Recall(ctx, identifier, fact, 10)
		if err != nil {
			rt.Fatalf("Adapter.Recall failed: %v", err)
		}

		// Results must be non-empty.
		if len(entries) == 0 {
			rt.Fatal("Adapter.Recall returned empty slice after Remember")
		}

		// The stored fact must appear in the results.
		found := false
		for _, entry := range entries {
			if entry.Fact == fact {
				found = true
				break
			}
		}
		if !found {
			rt.Fatalf("Adapter.Recall results do not contain the stored fact %q; got %+v", fact, entries)
		}
	})
}
