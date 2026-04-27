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

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
