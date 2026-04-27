package memory

import (
	"context"
	"reflect"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"pgregory.net/rapid"
)

// ---------------------------------------------------------------------------
// Feature: memory-rag-separation, Property 3: Adapter Delegation Preserves Arguments
// ---------------------------------------------------------------------------

// recordingStore is a mock MemoryStore that records the arguments passed to
// Add and Search. Search returns configurable dummy results so that
// Adapter.Recall can process them.
type recordingStore struct {
	// Recorded Add arguments.
	addIdentifier string
	addDocs       []agent.Document
	addEmbeddings [][]float64

	// Recorded Search arguments.
	searchIdentifier     string
	searchQueryEmbedding []float64
	searchTopK           int

	// Configurable Search return values.
	searchResults []agent.ScoredDocument
	searchErr     error
}

func (r *recordingStore) Add(_ context.Context, identifier string, docs []agent.Document, embeddings [][]float64) error {
	r.addIdentifier = identifier
	r.addDocs = docs
	r.addEmbeddings = embeddings
	return nil
}

func (r *recordingStore) Search(_ context.Context, identifier string, queryEmbedding []float64, topK int) ([]agent.ScoredDocument, error) {
	r.searchIdentifier = identifier
	r.searchQueryEmbedding = queryEmbedding
	r.searchTopK = topK
	return r.searchResults, r.searchErr
}

// TestProperty_AdapterDelegation verifies that for any valid identifier, fact
// string, and metadata map, when Adapter.Remember is called, the underlying
// MemoryStore.Add receives the same identifier, a document whose Content
// equals the fact and whose Metadata contains all user-provided keys, and an
// embedding vector matching what the embedder produces. Similarly, for any
// valid identifier, query string, and limit, when Adapter.Recall is called,
// the underlying MemoryStore.Search receives the same identifier, an embedding
// of the query, and the same limit.
//
// **Validates: Requirements 3.2, 3.3**
func TestProperty_AdapterDelegation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		const dim = 16
		embedder := &hashEmbedder{dim: dim}
		ctx := context.Background()

		// Generate random inputs for Remember.
		identifier := genNonEmptyString(rt, "identifier")
		fact := genNonEmptyString(rt, "fact")
		metadata := genMetadata(rt)

		// --- Test Remember delegation ---

		recStore := &recordingStore{}
		adapter := NewAdapter(recStore, embedder)

		err := adapter.Remember(ctx, identifier, fact, metadata)
		if err != nil {
			rt.Fatalf("Adapter.Remember failed: %v", err)
		}

		// Verify identifier was passed through.
		if recStore.addIdentifier != identifier {
			rt.Fatalf("Add received identifier %q, want %q", recStore.addIdentifier, identifier)
		}

		// Verify exactly one document was passed.
		if len(recStore.addDocs) != 1 {
			rt.Fatalf("Add received %d docs, want 1", len(recStore.addDocs))
		}

		// Verify the document's Content equals the fact.
		if recStore.addDocs[0].Content != fact {
			rt.Fatalf("Add doc.Content = %q, want %q", recStore.addDocs[0].Content, fact)
		}

		// Verify all user-provided metadata keys are present in the document.
		for k, v := range metadata {
			got, ok := recStore.addDocs[0].Metadata[k]
			if !ok {
				rt.Fatalf("Add doc.Metadata missing user key %q", k)
			}
			if got != v {
				rt.Fatalf("Add doc.Metadata[%q] = %q, want %q", k, got, v)
			}
		}

		// Verify the embedding matches what the embedder produces for the fact.
		expectedEmb, err := embedder.Embed(ctx, fact)
		if err != nil {
			rt.Fatalf("Embed(fact) failed: %v", err)
		}
		if len(recStore.addEmbeddings) != 1 {
			rt.Fatalf("Add received %d embeddings, want 1", len(recStore.addEmbeddings))
		}
		if !reflect.DeepEqual(recStore.addEmbeddings[0], expectedEmb) {
			rt.Fatalf("Add embedding does not match embedder output for fact %q", fact)
		}

		// --- Test Recall delegation ---

		query := genNonEmptyString(rt, "query")
		limit := rapid.IntRange(1, 50).Draw(rt, "limit")

		// Prepare dummy search results so Recall can process them.
		dummyResults := []agent.ScoredDocument{
			{
				Document: agent.Document{
					Content:  "dummy fact",
					Metadata: map[string]string{"created_at": "2025-01-01T00:00:00Z"},
				},
				Score: 0.95,
			},
		}

		recStore2 := &recordingStore{searchResults: dummyResults}
		adapter2 := NewAdapter(recStore2, embedder)

		_, err = adapter2.Recall(ctx, identifier, query, limit)
		if err != nil {
			rt.Fatalf("Adapter.Recall failed: %v", err)
		}

		// Verify identifier was passed through.
		if recStore2.searchIdentifier != identifier {
			rt.Fatalf("Search received identifier %q, want %q", recStore2.searchIdentifier, identifier)
		}

		// Verify the embedding matches what the embedder produces for the query.
		expectedQueryEmb, err := embedder.Embed(ctx, query)
		if err != nil {
			rt.Fatalf("Embed(query) failed: %v", err)
		}
		if !reflect.DeepEqual(recStore2.searchQueryEmbedding, expectedQueryEmb) {
			rt.Fatalf("Search queryEmbedding does not match embedder output for query %q", query)
		}

		// Verify the limit was passed through.
		if recStore2.searchTopK != limit {
			rt.Fatalf("Search received topK %d, want %d", recStore2.searchTopK, limit)
		}
	})
}
