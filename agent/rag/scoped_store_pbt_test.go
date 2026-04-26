package rag

import (
	"context"
	"hash/fnv"
	"math"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"pgregory.net/rapid"
)

// ---------------------------------------------------------------------------
// Deterministic mock Embedder (hash-based vectors)
// ---------------------------------------------------------------------------

// hashEmbedder is a deterministic mock Embedder that produces consistent
// embedding vectors from strings using FNV hashing. The same input always
// yields the same vector, making cosine similarity results predictable.
type hashEmbedder struct {
	dim int
}

// Embed returns a deterministic float64 vector derived from the FNV-64a hash
// of the input text. Each dimension is seeded by hashing the text concatenated
// with the dimension index, then normalized to unit length.
func (e *hashEmbedder) Embed(_ context.Context, text string) ([]float64, error) {
	vec := make([]float64, e.dim)
	for i := range vec {
		h := fnv.New64a()
		h.Write([]byte(text))
		h.Write([]byte{byte(i), byte(i >> 8)})
		vec[i] = float64(int64(h.Sum64())) / float64(math.MaxInt64)
	}
	return vec, nil
}

// ---------------------------------------------------------------------------
// Generators
// ---------------------------------------------------------------------------

// genNonEmptyString generates a random non-empty alphanumeric string (1–100 chars).
func genNonEmptyString(t *rapid.T, name string) string {
	return rapid.StringMatching(`[a-zA-Z0-9 ]{1,100}`).Draw(t, name)
}

// genMetadata generates a random metadata map with 0–5 entries.
func genMetadata(t *rapid.T) map[string]string {
	n := rapid.IntRange(0, 5).Draw(t, "metadata_len")
	m := make(map[string]string, n)
	for i := range n {
		key := rapid.StringMatching(`[a-z]{1,10}`).Draw(t, "metadata_key")
		val := rapid.StringMatching(`[a-zA-Z0-9]{1,20}`).Draw(t, "metadata_val")
		_ = i
		m[key] = val
	}
	return m
}

// ---------------------------------------------------------------------------
// Property 1: ScopedStore Add injects scope metadata
// ---------------------------------------------------------------------------

// Feature: unified-memory-providers, Property 1: ScopedStore Add injects scope metadata
//
// TestProperty_ScopedStoreAddInjectsScope verifies that for any non-empty
// identifier string and any list of documents with arbitrary metadata
// (including pre-existing _scope_id values), after calling
// ScopedStore.Add(ctx, identifier, docs, embeddings), every document stored
// in the underlying VectorStore has metadata["_scope_id"] equal to the
// provided identifier. Pre-existing _scope_id values are overwritten.
//
// **Validates: Requirements 2.2, 8.1, 8.2, 8.4**
func TestProperty_ScopedStoreAddInjectsScope(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		embedder := &hashEmbedder{dim: 16}
		identifier := genNonEmptyString(rt, "identifier")

		// Generate 1–10 documents with random metadata.
		numDocs := rapid.IntRange(1, 10).Draw(rt, "num_docs")
		docs := make([]agent.Document, numDocs)
		embeddings := make([][]float64, numDocs)

		for i := range numDocs {
			meta := genMetadata(rt)

			// Randomly inject a pre-existing _scope_id to verify it gets overwritten.
			if rapid.Bool().Draw(rt, "has_preexisting_scope") {
				meta[ScopeMetadataKey] = genNonEmptyString(rt, "old_scope_id")
			}

			content := genNonEmptyString(rt, "content")
			docs[i] = agent.Document{
				Content:  content,
				Metadata: meta,
			}

			emb, err := embedder.Embed(context.Background(), content)
			if err != nil {
				rt.Fatalf("Embed failed: %v", err)
			}
			embeddings[i] = emb
		}

		// Use a recording store to capture what ScopedStore passes to the backend.
		backend := &recordingScopedStore{}
		scoped := NewScopedStore(backend)

		ctx := context.Background()
		err := scoped.Add(ctx, identifier, docs, embeddings)
		if err != nil {
			rt.Fatalf("ScopedStore.Add failed: %v", err)
		}

		// Verify every stored document has _scope_id == identifier.
		if len(backend.addedDocs) != numDocs {
			rt.Fatalf("expected %d stored docs, got %d", numDocs, len(backend.addedDocs))
		}

		for i, storedDoc := range backend.addedDocs {
			scopeVal, ok := storedDoc.Metadata[ScopeMetadataKey]
			if !ok {
				rt.Fatalf("stored doc[%d] missing %q metadata key", i, ScopeMetadataKey)
			}
			if scopeVal != identifier {
				rt.Fatalf("stored doc[%d] has %q = %q, want %q",
					i, ScopeMetadataKey, scopeVal, identifier)
			}
		}

		// Verify original documents' metadata was not mutated by ScopedStore.
		for i, originalDoc := range docs {
			storedDoc := backend.addedDocs[i]
			// The stored doc's metadata should be a separate map from the original.
			if originalDoc.Metadata != nil {
				for k, v := range originalDoc.Metadata {
					if k == ScopeMetadataKey {
						continue // _scope_id may differ (overwritten)
					}
					if storedDoc.Metadata[k] != v {
						rt.Fatalf("stored doc[%d] metadata[%q] = %q, want %q (from original)",
							i, k, storedDoc.Metadata[k], v)
					}
				}
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Recording VectorStore for test observation
// ---------------------------------------------------------------------------

// recordingScopedStore captures documents passed to Add for test assertions.
type recordingScopedStore struct {
	addedDocs       []agent.Document
	addedEmbeddings [][]float64
}

func (r *recordingScopedStore) Add(_ context.Context, docs []agent.Document, embeddings [][]float64) error {
	r.addedDocs = append(r.addedDocs, docs...)
	r.addedEmbeddings = append(r.addedEmbeddings, embeddings...)
	return nil
}

func (r *recordingScopedStore) Search(_ context.Context, _ []float64, topK int) ([]agent.ScoredDocument, error) {
	return []agent.ScoredDocument{}, nil
}

// ---------------------------------------------------------------------------
// Property 2: ScopedStore Search isolates by identifier
// ---------------------------------------------------------------------------

// Feature: unified-memory-providers, Property 2: ScopedStore Search isolates by identifier
//
// TestProperty_ScopedStoreSearchIsolation verifies that for any two distinct
// non-empty identifier strings A and B, and any documents stored under each
// identifier via ScopedStore.Add, calling ScopedStore.Search(ctx, A, ...)
// returns only documents whose _scope_id metadata equals A — no documents
// stored under identifier B appear in the results.
//
// **Validates: Requirements 2.3, 8.3, 9.3**
func TestProperty_ScopedStoreSearchIsolation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		const dim = 16
		embedder := &hashEmbedder{dim: dim}
		backend := NewMemoryStore()
		scoped := NewScopedStore(backend)
		ctx := context.Background()

		// Generate two distinct identifiers.
		identA := genNonEmptyString(rt, "identA")
		identB := genNonEmptyString(rt, "identB")
		for identB == identA {
			identB = genNonEmptyString(rt, "identB_retry")
		}

		// Generate 1–10 documents for identifier A.
		numDocsA := rapid.IntRange(1, 10).Draw(rt, "num_docs_A")
		docsA := make([]agent.Document, numDocsA)
		embsA := make([][]float64, numDocsA)
		for i := range numDocsA {
			content := genNonEmptyString(rt, "contentA")
			docsA[i] = agent.Document{
				Content:  content,
				Metadata: genMetadata(rt),
			}
			emb, err := embedder.Embed(ctx, content)
			if err != nil {
				rt.Fatalf("Embed failed: %v", err)
			}
			embsA[i] = emb
		}

		// Generate 1–10 documents for identifier B.
		numDocsB := rapid.IntRange(1, 10).Draw(rt, "num_docs_B")
		docsB := make([]agent.Document, numDocsB)
		embsB := make([][]float64, numDocsB)
		for i := range numDocsB {
			content := genNonEmptyString(rt, "contentB")
			docsB[i] = agent.Document{
				Content:  content,
				Metadata: genMetadata(rt),
			}
			emb, err := embedder.Embed(ctx, content)
			if err != nil {
				rt.Fatalf("Embed failed: %v", err)
			}
			embsB[i] = emb
		}

		// Add documents under both identifiers.
		if err := scoped.Add(ctx, identA, docsA, embsA); err != nil {
			rt.Fatalf("ScopedStore.Add(A) failed: %v", err)
		}
		if err := scoped.Add(ctx, identB, docsB, embsB); err != nil {
			rt.Fatalf("ScopedStore.Add(B) failed: %v", err)
		}

		// Search for identifier A using the first A-document's embedding as query.
		topK := numDocsA + numDocsB // request more than exist to get all possible matches
		resultsA, err := scoped.Search(ctx, identA, embsA[0], topK)
		if err != nil {
			rt.Fatalf("ScopedStore.Search(A) failed: %v", err)
		}

		// Every result must have _scope_id == identA.
		for i, sd := range resultsA {
			scopeVal, ok := sd.Document.Metadata[ScopeMetadataKey]
			if !ok {
				rt.Fatalf("result[%d] missing %q metadata key", i, ScopeMetadataKey)
			}
			if scopeVal != identA {
				rt.Fatalf("result[%d] has %q = %q, want %q (identifier A)",
					i, ScopeMetadataKey, scopeVal, identA)
			}
		}

		// Search for identifier B using the first B-document's embedding as query.
		resultsB, err := scoped.Search(ctx, identB, embsB[0], topK)
		if err != nil {
			rt.Fatalf("ScopedStore.Search(B) failed: %v", err)
		}

		// Every result must have _scope_id == identB.
		for i, sd := range resultsB {
			scopeVal, ok := sd.Document.Metadata[ScopeMetadataKey]
			if !ok {
				rt.Fatalf("result[%d] missing %q metadata key", i, ScopeMetadataKey)
			}
			if scopeVal != identB {
				rt.Fatalf("result[%d] has %q = %q, want %q (identifier B)",
					i, ScopeMetadataKey, scopeVal, identB)
			}
		}

		// Verify A's results contain exactly numDocsA documents.
		if len(resultsA) != numDocsA {
			rt.Fatalf("Search(A) returned %d results, want %d", len(resultsA), numDocsA)
		}

		// Verify B's results contain exactly numDocsB documents.
		if len(resultsB) != numDocsB {
			rt.Fatalf("Search(B) returned %d results, want %d", len(resultsB), numDocsB)
		}
	})
}
