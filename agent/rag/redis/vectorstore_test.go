package redis

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"

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

// genDocument generates a random rag.Document with non-empty Content and 0–3 metadata entries.
func genDocument(t *rapid.T) rag.Document {
	content := rapid.StringMatching(`[a-zA-Z0-9 ]{1,100}`).Draw(t, "content")

	numMeta := rapid.IntRange(0, 3).Draw(t, "numMeta")
	meta := make(map[string]string, numMeta)
	for i := 0; i < numMeta; i++ {
		key := rapid.StringMatching(`[a-z]{1,10}`).Draw(t, "metaKey")
		val := rapid.StringMatching(`[a-zA-Z0-9]{0,20}`).Draw(t, "metaVal")
		meta[key] = val
	}

	return rag.Document{Content: content, Metadata: meta}
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

func TestProperty_StoreAddSearchRoundTrip(t *testing.T) {
	addr := skipIfNoRedis(t)

	const dim = 128
	indexName := "testidx-roundtrip-" + rapid.StringMatching(`[a-z0-9]{8}`).Example()

	store, err := New(Options{Addr: addr}, indexName, dim)
	if err != nil {
		t.Fatalf("failed to create Store: %v", err)
	}
	defer store.Close()
	defer store.client.Do(context.Background(), "FT.DROPINDEX", indexName, "DD").Err()

	rapid.Check(t, func(t *rapid.T) {
		doc := genDocument(t)
		emb := genEmbedding(t, dim)

		ctx := context.Background()

		if _, err := store.Upsert(ctx, []rag.Document{doc}, [][]float64{emb}); err != nil {
			t.Fatalf("Upsert failed: %v", err)
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

func TestProperty_StoreAddRejectsMismatchedLengths(t *testing.T) {
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

		docs := make([]rag.Document, n)
		for i := range docs {
			docs[i] = genDocument(t)
		}
		embeddings := make([][]float64, m)
		for i := range embeddings {
			embeddings[i] = genEmbedding(t, 8)
		}

		store := &Store{}
		if _, err := store.Upsert(context.Background(), docs, embeddings); err == nil {
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

func TestStore_SearchTopKZero(t *testing.T) {
	store := &Store{}
	_, err := store.Search(context.Background(), []float64{1.0, 2.0}, 0)
	if err == nil {
		t.Fatal("expected error for topK=0, got nil")
	}
}

func TestStore_AddEmptySlice(t *testing.T) {
	store := &Store{}
	if _, err := store.Upsert(context.Background(), []rag.Document{}, [][]float64{}); err != nil {
		t.Fatalf("expected nil error for empty slices, got: %v", err)
	}
}

func TestStore_DefaultHNSWParams(t *testing.T) {
	addr := skipIfNoRedis(t)
	indexName := "testidx-defaults-hnsw"
	store, err := New(Options{Addr: addr}, indexName, 64)
	if err != nil {
		t.Fatalf("failed to create Store: %v", err)
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

func TestStore_FTCreateIdempotent(t *testing.T) {
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

func TestStore_NewRetriever(t *testing.T) {
	addr := skipIfNoRedis(t)
	indexName := "testidx-retriever"
	store, err := New(Options{Addr: addr}, indexName, 64)
	if err != nil {
		t.Fatalf("failed to create Store: %v", err)
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

func TestStore_DocumentIDRoundTrip(t *testing.T) {
	store := &Store{indexName: "documents"}

	if got := store.documentKey("documents:custom"); got != "documents:documents:custom" {
		t.Fatalf("documentKey() = %q, want %q", got, "documents:documents:custom")
	}
	if got := store.documentID("documents:documents:custom"); got != "documents:custom" {
		t.Fatalf("documentID() = %q, want %q", got, "documents:custom")
	}
}

func TestStore_IDLifecycle(t *testing.T) {
	addr := skipIfNoRedis(t)
	indexName := fmt.Sprintf("testidx-id-lifecycle-%d", os.Getpid())
	store, err := New(Options{Addr: addr}, indexName, 3, WithDropExisting())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer store.Close()
	defer store.client.Do(context.Background(), "FT.DROPINDEX", indexName, "DD").Err()

	ctx := context.Background()
	embedding := []float64{1, 0, 0}
	ids, err := store.Upsert(ctx, []rag.Document{{ID: "document-1", Content: "first"}}, [][]float64{embedding})
	if err != nil {
		t.Fatalf("Upsert explicit ID: %v", err)
	}
	if len(ids) != 1 || ids[0] != "document-1" {
		t.Fatalf("Upsert IDs = %v, want [document-1]", ids)
	}

	found, err := store.Find(ctx, ids[0])
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(found) != 1 || found[0].ID != ids[0] || found[0].Content != "first" {
		t.Fatalf("Find result = %+v, want logical ID %q and first content", found, ids[0])
	}

	results, err := store.Search(ctx, embedding, 1)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].Document.ID != ids[0] {
		t.Fatalf("Search result = %+v, want logical ID %q", results, ids[0])
	}

	if _, err := store.Upsert(ctx, []rag.Document{{ID: results[0].Document.ID, Content: "replacement"}}, [][]float64{embedding}); err != nil {
		t.Fatalf("Upsert replacement: %v", err)
	}
	if exists, err := store.client.Exists(ctx, store.documentKey(store.documentKey(ids[0]))).Result(); err != nil {
		t.Fatalf("check double-prefixed key: %v", err)
	} else if exists != 0 {
		t.Fatalf("double-prefixed key unexpectedly exists for %q", ids[0])
	}
	found, err = store.Find(ctx, ids[0])
	if err != nil {
		t.Fatalf("Find replacement: %v", err)
	}
	if len(found) != 1 || found[0].Content != "replacement" {
		t.Fatalf("replacement result = %+v, want replacement content", found)
	}

	generated, err := store.Upsert(ctx, []rag.Document{{Content: "generated"}}, [][]float64{{0, 1, 0}})
	if err != nil {
		t.Fatalf("Upsert generated ID: %v", err)
	}
	if len(generated) != 1 || generated[0] == "" || strings.HasPrefix(generated[0], indexName+":") {
		t.Fatalf("generated IDs = %v, want one unprefixed logical ID", generated)
	}
	if docs, err := store.Find(ctx, generated[0]); err != nil || len(docs) != 1 || docs[0].ID != generated[0] {
		t.Fatalf("generated ID Find = %+v, %v", docs, err)
	}

	if err := store.Delete(ctx, ids[0], generated[0]); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if docs, err := store.Find(ctx, ids[0], generated[0]); err != nil || len(docs) != 0 {
		t.Fatalf("Find after Delete = %+v, %v; want no documents", docs, err)
	}
}
