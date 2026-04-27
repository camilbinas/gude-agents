package memory

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
)

// TestInMemoryStore_Add_EmptyIdentifier verifies that Add returns a descriptive
// error when the identifier is empty.
// Requirements: 1.3
func TestInMemoryStore_Add_EmptyIdentifier(t *testing.T) {
	store := NewInMemoryStore()
	ctx := context.Background()

	docs := []agent.Document{{Content: "hello"}}
	embeddings := [][]float64{{1.0, 2.0}}

	err := store.Add(ctx, "", docs, embeddings)
	if err == nil {
		t.Fatal("expected error for empty identifier, got nil")
	}
	const want = "memory: identifier must not be empty"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}

// TestInMemoryStore_Search_EmptyIdentifier verifies that Search returns a
// descriptive error when the identifier is empty.
// Requirements: 1.4
func TestInMemoryStore_Search_EmptyIdentifier(t *testing.T) {
	store := NewInMemoryStore()
	ctx := context.Background()

	_, err := store.Search(ctx, "", []float64{1.0}, 5)
	if err == nil {
		t.Fatal("expected error for empty identifier, got nil")
	}
	const want = "memory: identifier must not be empty"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}

// TestInMemoryStore_Search_TopKLessThanOne verifies that Search returns a
// descriptive error when topK < 1.
// Requirements: 1.5
func TestInMemoryStore_Search_TopKLessThanOne(t *testing.T) {
	store := NewInMemoryStore()
	ctx := context.Background()

	for _, topK := range []int{0, -1, -100} {
		t.Run(fmt.Sprintf("topK=%d", topK), func(t *testing.T) {
			_, err := store.Search(ctx, "user-1", []float64{1.0}, topK)
			if err == nil {
				t.Fatalf("expected error for topK=%d, got nil", topK)
			}
			want := fmt.Sprintf("memory: topK must be >= 1, got %d", topK)
			if err.Error() != want {
				t.Fatalf("error = %q, want %q", err.Error(), want)
			}
		})
	}
}

// TestInMemoryStore_Add_DocsEmbeddingsMismatch verifies that Add returns an
// error when the docs and embeddings slices have different lengths.
// Requirements: 2.1
func TestInMemoryStore_Add_DocsEmbeddingsMismatch(t *testing.T) {
	store := NewInMemoryStore()
	ctx := context.Background()

	docs := []agent.Document{{Content: "a"}, {Content: "b"}}
	embeddings := [][]float64{{1.0, 2.0}} // only 1 embedding for 2 docs

	err := store.Add(ctx, "user-1", docs, embeddings)
	if err == nil {
		t.Fatal("expected error for docs/embeddings mismatch, got nil")
	}
	const want = "memory: docs and embeddings length mismatch: 2 vs 1"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}

// TestInMemoryStore_Search_NoMatchingIdentifier verifies that Search returns
// a non-nil empty slice (not nil) when no documents match the identifier.
// Requirements: 1.5, 2.2
func TestInMemoryStore_Search_NoMatchingIdentifier(t *testing.T) {
	store := NewInMemoryStore()
	ctx := context.Background()

	results, err := store.Search(ctx, "nonexistent", []float64{1.0}, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results == nil {
		t.Fatal("expected non-nil empty slice, got nil")
	}
	if len(results) != 0 {
		t.Fatalf("expected empty slice, got %d results", len(results))
	}
}

// TestInMemoryStore_AddThenSearch verifies the basic flow: adding documents
// under an identifier and then searching returns correct results ordered by
// descending cosine similarity.
// Requirements: 2.1, 2.2, 2.3
func TestInMemoryStore_AddThenSearch(t *testing.T) {
	store := NewInMemoryStore()
	ctx := context.Background()

	// Use a known embedding space: 2D vectors.
	docs := []agent.Document{
		{Content: "close to query", Metadata: map[string]string{"idx": "0"}},
		{Content: "far from query", Metadata: map[string]string{"idx": "1"}},
		{Content: "medium distance", Metadata: map[string]string{"idx": "2"}},
	}
	// Query will be [1, 0]. Cosine similarity:
	// doc0 [1, 0]   → cos = 1.0
	// doc1 [0, 1]   → cos = 0.0
	// doc2 [1, 1]   → cos ≈ 0.707
	embeddings := [][]float64{
		{1.0, 0.0}, // close to query
		{0.0, 1.0}, // orthogonal to query
		{1.0, 1.0}, // 45 degrees from query
	}

	err := store.Add(ctx, "user-1", docs, embeddings)
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	queryEmb := []float64{1.0, 0.0}
	results, err := store.Search(ctx, "user-1", queryEmb, 3)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// Verify descending score order.
	for i := 1; i < len(results); i++ {
		if results[i-1].Score < results[i].Score {
			t.Fatalf("results not sorted: results[%d].Score=%v < results[%d].Score=%v",
				i-1, results[i-1].Score, i, results[i].Score)
		}
	}

	// The closest document should be first.
	if results[0].Document.Content != "close to query" {
		t.Fatalf("expected first result to be 'close to query', got %q", results[0].Document.Content)
	}

	// The orthogonal document should be last.
	if results[2].Document.Content != "far from query" {
		t.Fatalf("expected last result to be 'far from query', got %q", results[2].Document.Content)
	}
}

// TestInMemoryStore_Search_TopKClamping verifies that Search returns at most
// topK results, clamped to the number of stored documents.
// Requirements: 2.3
func TestInMemoryStore_Search_TopKClamping(t *testing.T) {
	store := NewInMemoryStore()
	ctx := context.Background()

	docs := []agent.Document{
		{Content: "a"},
		{Content: "b"},
		{Content: "c"},
	}
	embeddings := [][]float64{
		{1.0, 0.0},
		{0.0, 1.0},
		{1.0, 1.0},
	}

	if err := store.Add(ctx, "user-1", docs, embeddings); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Request more than available.
	results, err := store.Search(ctx, "user-1", []float64{1.0, 0.0}, 100)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results (clamped), got %d", len(results))
	}

	// Request fewer than available.
	results, err = store.Search(ctx, "user-1", []float64{1.0, 0.0}, 1)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
}

// TestInMemoryStore_ConcurrentAccess verifies that InMemoryStore is safe for
// concurrent use from multiple goroutines. This test should be run with -race.
// Requirements: 2.4
func TestInMemoryStore_ConcurrentAccess(t *testing.T) {
	store := NewInMemoryStore()
	ctx := context.Background()
	embedder := &hashEmbedder{dim: 8}

	const numGoroutines = 20
	const numOps = 50

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for g := range numGoroutines {
		go func(id int) {
			defer wg.Done()
			identifier := fmt.Sprintf("user-%d", id%5) // share identifiers across goroutines

			for i := range numOps {
				content := fmt.Sprintf("fact-%d-%d", id, i)
				emb, err := embedder.Embed(ctx, content)
				if err != nil {
					t.Errorf("Embed failed: %v", err)
					return
				}

				doc := agent.Document{Content: content}
				if err := store.Add(ctx, identifier, []agent.Document{doc}, [][]float64{emb}); err != nil {
					t.Errorf("Add failed: %v", err)
					return
				}

				queryEmb, err := embedder.Embed(ctx, "query")
				if err != nil {
					t.Errorf("Embed query failed: %v", err)
					return
				}

				results, err := store.Search(ctx, identifier, queryEmb, 5)
				if err != nil {
					t.Errorf("Search failed: %v", err)
					return
				}
				if results == nil {
					t.Errorf("Search returned nil results")
					return
				}
			}
		}(g)
	}

	wg.Wait()
}
