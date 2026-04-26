package redis

import (
	"context"
	"math"
	"os"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/rag"
	"pgregory.net/rapid"
)

func skipIfNoRedis(t *testing.T) string {
	t.Helper()
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		t.Skip("REDIS_ADDR not set, skipping integration test")
	}
	return addr
}

// genDocument generates a random agent.Document with non-empty Content and 0–3 metadata entries.
func genDocument(t *rapid.T) agent.Document {
	content := rapid.StringMatching(`[a-zA-Z0-9 ]{1,100}`).Draw(t, "content")

	numMeta := rapid.IntRange(0, 3).Draw(t, "numMeta")
	meta := make(map[string]string, numMeta)
	for i := 0; i < numMeta; i++ {
		key := rapid.StringMatching(`[a-z]{1,10}`).Draw(t, "metaKey")
		val := rapid.StringMatching(`[a-zA-Z0-9]{0,20}`).Draw(t, "metaVal")
		meta[key] = val
	}

	return agent.Document{Content: content, Metadata: meta}
}

// genEmbedding generates a random unit-normalised float64 embedding of the given dimension.
func genEmbedding(t *rapid.T, dim int) []float64 {
	emb := make([]float64, dim)
	var norm float64
	for i := 0; i < dim; i++ {
		v := rapid.Float64Range(-1.0, 1.0).Draw(t, "embVal")
		emb[i] = v
		norm += v * v
	}
	if mag := math.Sqrt(norm); mag > 0 {
		for i := range emb {
			emb[i] /= mag
		}
	}
	return emb
}

func TestProperty_VectorStoreAddSearchRoundTrip(t *testing.T) {
	addr := skipIfNoRedis(t)

	const dim = 128
	indexName := "testidx-roundtrip-" + rapid.StringMatching(`[a-z0-9]{8}`).Example()

	store, err := New(Options{Addr: addr}, indexName, dim)
	if err != nil {
		t.Fatalf("failed to create VectorStore: %v", err)
	}
	defer store.Close()
	defer store.client.Do(context.Background(), "FT.DROPINDEX", indexName, "DD").Err()

	rapid.Check(t, func(t *rapid.T) {
		doc := genDocument(t)
		emb := genEmbedding(t, dim)

		ctx := context.Background()

		if err := store.Add(ctx, []agent.Document{doc}, [][]float64{emb}); err != nil {
			t.Fatalf("Add failed: %v", err)
		}

		results, err := store.Search(ctx, emb, 1)
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}
		if len(results) == 0 {
			t.Fatal("expected at least one result, got none")
		}

		got := results[0].Document
		if got.Content != doc.Content {
			t.Fatalf("content mismatch:\n  expected: %q\n  got:      %q", doc.Content, got.Content)
		}
		for k, v := range doc.Metadata {
			if gotV, ok := got.Metadata[k]; !ok || gotV != v {
				t.Fatalf("metadata[%q] mismatch: expected %q, got %q", k, v, gotV)
			}
		}
	})
}

func TestProperty_VectorStoreAddRejectsMismatchedLengths(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 10).Draw(t, "n")
		m := rapid.IntRange(1, 10).Draw(t, "m")
		if n == m {
			if m < 10 {
				m++
			} else {
				m--
			}
		}

		docs := make([]agent.Document, n)
		for i := range docs {
			docs[i] = genDocument(t)
		}
		embeddings := make([][]float64, m)
		for i := range embeddings {
			embeddings[i] = genEmbedding(t, 8)
		}

		store := &VectorStore{}
		if err := store.Add(context.Background(), docs, embeddings); err == nil {
			t.Fatalf("expected error for mismatched lengths (docs=%d, embeddings=%d), got nil", n, m)
		}
	})
}

func TestNew_UnreachableAddr(t *testing.T) {
	_, err := New(Options{Addr: "localhost:1"}, "testidx", 128)
	if err == nil {
		t.Fatal("expected error for unreachable address, got nil")
	}
	if !contains(err.Error(), "ping") {
		t.Fatalf("expected error to contain 'ping', got: %v", err)
	}
}

func TestVectorStore_SearchTopKZero(t *testing.T) {
	store := &VectorStore{}
	_, err := store.Search(context.Background(), []float64{1.0, 2.0}, 0)
	if err == nil {
		t.Fatal("expected error for topK=0, got nil")
	}
}

func TestVectorStore_AddEmptySlice(t *testing.T) {
	store := &VectorStore{}
	if err := store.Add(context.Background(), []agent.Document{}, [][]float64{}); err != nil {
		t.Fatalf("expected nil error for empty slices, got: %v", err)
	}
}

func TestVectorStore_DefaultHNSWParams(t *testing.T) {
	addr := skipIfNoRedis(t)
	indexName := "testidx-defaults-hnsw"
	store, err := New(Options{Addr: addr}, indexName, 64)
	if err != nil {
		t.Fatalf("failed to create VectorStore: %v", err)
	}
	defer store.Close()
	defer store.client.Do(context.Background(), "FT.DROPINDEX", indexName, "DD").Err()

	if store.hnswM != 16 {
		t.Fatalf("expected default hnswM=16, got %d", store.hnswM)
	}
	if store.hnswEF != 200 {
		t.Fatalf("expected default hnswEF=200, got %d", store.hnswEF)
	}
}

func TestVectorStore_FTCreateIdempotent(t *testing.T) {
	addr := skipIfNoRedis(t)
	indexName := "testidx-idempotent"

	store1, err := New(Options{Addr: addr}, indexName, 64)
	if err != nil {
		t.Fatalf("first New failed: %v", err)
	}
	defer store1.Close()
	defer store1.client.Do(context.Background(), "FT.DROPINDEX", indexName, "DD").Err()

	store2, err := New(Options{Addr: addr}, indexName, 64)
	if err != nil {
		t.Fatalf("second New failed (should be idempotent): %v", err)
	}
	defer store2.Close()
}

func TestVectorStore_NewRetriever(t *testing.T) {
	addr := skipIfNoRedis(t)
	indexName := "testidx-retriever"
	store, err := New(Options{Addr: addr}, indexName, 64)
	if err != nil {
		t.Fatalf("failed to create VectorStore: %v", err)
	}
	defer store.Close()
	defer store.client.Do(context.Background(), "FT.DROPINDEX", indexName, "DD").Err()

	retriever := rag.NewRetriever(dummyEmbedder{}, store)
	if retriever == nil {
		t.Fatal("expected non-nil retriever from rag.NewRetriever")
	}
}

type dummyEmbedder struct{}

func (dummyEmbedder) Embed(_ context.Context, _ string) ([]float64, error) {
	return make([]float64, 64), nil
}

// --- ScopedSearch unit tests ---

// TestScopedSearch_ReturnsOnlyScopedDocuments verifies that ScopedSearch
// returns only documents matching the given scope value, not documents
// stored under a different scope or without a scope.
func TestScopedSearch_ReturnsOnlyScopedDocuments(t *testing.T) {
	addr := skipIfNoRedis(t)

	const dim = 64
	indexName := "testidx-scopedsearch-filter"

	store, err := New(Options{Addr: addr}, indexName, dim, WithDropExisting())
	if err != nil {
		t.Fatalf("failed to create VectorStore: %v", err)
	}
	defer store.Close()
	defer store.client.Do(context.Background(), "FT.DROPINDEX", indexName, "DD").Err()

	ctx := context.Background()

	// Create a deterministic embedding (all ones, normalised).
	baseEmb := make([]float64, dim)
	for i := range baseEmb {
		baseEmb[i] = 1.0 / math.Sqrt(float64(dim))
	}

	// Store two documents under scope "userA".
	docsA := []agent.Document{
		{Content: "fact for user A first", Metadata: map[string]string{"_scope_id": "userA", "key": "a1"}},
		{Content: "fact for user A second", Metadata: map[string]string{"_scope_id": "userA", "key": "a2"}},
	}
	embsA := [][]float64{baseEmb, baseEmb}
	if err := store.Add(ctx, docsA, embsA); err != nil {
		t.Fatalf("Add for userA failed: %v", err)
	}

	// Store one document under scope "userB".
	docsB := []agent.Document{
		{Content: "fact for user B", Metadata: map[string]string{"_scope_id": "userB", "key": "b1"}},
	}
	embsB := [][]float64{baseEmb}
	if err := store.Add(ctx, docsB, embsB); err != nil {
		t.Fatalf("Add for userB failed: %v", err)
	}

	// ScopedSearch for "userA" should return only userA's documents.
	results, err := store.ScopedSearch(ctx, "_scope_id", "userA", baseEmb, 10)
	if err != nil {
		t.Fatalf("ScopedSearch failed: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results for userA, got %d", len(results))
	}

	for _, r := range results {
		scopeVal := r.Document.Metadata["_scope_id"]
		if scopeVal != "userA" {
			t.Errorf("expected _scope_id=userA in result, got %q (content=%q)", scopeVal, r.Document.Content)
		}
	}

	// ScopedSearch for "userB" should return only userB's document.
	resultsB, err := store.ScopedSearch(ctx, "_scope_id", "userB", baseEmb, 10)
	if err != nil {
		t.Fatalf("ScopedSearch for userB failed: %v", err)
	}

	if len(resultsB) != 1 {
		t.Fatalf("expected 1 result for userB, got %d", len(resultsB))
	}

	if resultsB[0].Document.Content != "fact for user B" {
		t.Errorf("expected content 'fact for user B', got %q", resultsB[0].Document.Content)
	}
}

// TestScopedSearch_EmptyScopeValueReturnsError verifies that ScopedSearch
// returns a descriptive error when called with an empty scopeValue.
func TestScopedSearch_EmptyScopeValueReturnsError(t *testing.T) {
	store := &VectorStore{}
	_, err := store.ScopedSearch(context.Background(), "_scope_id", "", []float64{1.0}, 1)
	if err == nil {
		t.Fatal("expected error for empty scopeValue, got nil")
	}
	if !contains(err.Error(), "scopeValue") {
		t.Fatalf("expected error to mention 'scopeValue', got: %v", err)
	}
}

// TestScopedSearch_TopKLessThanOneReturnsError verifies that ScopedSearch
// returns a descriptive error when topK < 1.
func TestScopedSearch_TopKLessThanOneReturnsError(t *testing.T) {
	store := &VectorStore{}
	_, err := store.ScopedSearch(context.Background(), "_scope_id", "user1", []float64{1.0}, 0)
	if err == nil {
		t.Fatal("expected error for topK=0, got nil")
	}
	if !contains(err.Error(), "topK") {
		t.Fatalf("expected error to mention 'topK', got: %v", err)
	}
}

// TestBackwardCompat_AddSearchWithoutScopeID verifies that the existing
// non-scoped Add/Search workflow continues to work when documents do not
// contain a _scope_id metadata key. This ensures the TAG field addition
// does not break backward compatibility.
func TestBackwardCompat_AddSearchWithoutScopeID(t *testing.T) {
	addr := skipIfNoRedis(t)

	const dim = 64
	indexName := "testidx-backcompat-noscope"

	store, err := New(Options{Addr: addr}, indexName, dim, WithDropExisting())
	if err != nil {
		t.Fatalf("failed to create VectorStore: %v", err)
	}
	defer store.Close()
	defer store.client.Do(context.Background(), "FT.DROPINDEX", indexName, "DD").Err()

	ctx := context.Background()

	// Create a deterministic embedding.
	emb := make([]float64, dim)
	for i := range emb {
		emb[i] = 1.0 / math.Sqrt(float64(dim))
	}

	// Store a document WITHOUT _scope_id — plain RAG usage.
	doc := agent.Document{
		Content:  "plain rag document without scope",
		Metadata: map[string]string{"source": "test"},
	}
	if err := store.Add(ctx, []agent.Document{doc}, [][]float64{emb}); err != nil {
		t.Fatalf("Add without _scope_id failed: %v", err)
	}

	// Standard Search should still find the document.
	results, err := store.Search(ctx, emb, 1)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result from non-scoped Search, got none")
	}
	if results[0].Document.Content != doc.Content {
		t.Errorf("content mismatch: expected %q, got %q", doc.Content, results[0].Document.Content)
	}
	if results[0].Document.Metadata["source"] != "test" {
		t.Errorf("metadata[source] mismatch: expected 'test', got %q", results[0].Document.Metadata["source"])
	}
}

// TestVectorStore_ImplementsScopedSearcher verifies at test time that
// *VectorStore satisfies the rag.ScopedSearcher interface.
func TestVectorStore_ImplementsScopedSearcher(t *testing.T) {
	var _ rag.ScopedSearcher = (*VectorStore)(nil)
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
