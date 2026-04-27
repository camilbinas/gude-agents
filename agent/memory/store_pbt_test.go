package memory

import (
	"context"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"pgregory.net/rapid"
)

// ---------------------------------------------------------------------------
// Feature: memory-rag-separation, Property 1: Search Isolation by Identifier
// ---------------------------------------------------------------------------

// TestProperty_MemoryStoreSearchIsolation verifies that for any two distinct
// non-empty identifier strings A and B, and any set of documents stored under
// each identifier via InMemoryStore.Add, calling InMemoryStore.Search(ctx, A, ...)
// returns only documents that were stored under identifier A — no documents
// stored under identifier B appear in the results, and vice versa.
//
// **Validates: Requirements 2.2**
func TestProperty_MemoryStoreSearchIsolation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		const dim = 16
		embedder := &hashEmbedder{dim: dim}
		store := NewInMemoryStore()
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
		contentsA := make(map[string]bool, numDocsA)
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
			contentsA[content] = true
		}

		// Generate 1–10 documents for identifier B.
		numDocsB := rapid.IntRange(1, 10).Draw(rt, "num_docs_B")
		docsB := make([]agent.Document, numDocsB)
		embsB := make([][]float64, numDocsB)
		contentsB := make(map[string]bool, numDocsB)
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
			contentsB[content] = true
		}

		// Add documents under both identifiers.
		if err := store.Add(ctx, identA, docsA, embsA); err != nil {
			rt.Fatalf("InMemoryStore.Add(A) failed: %v", err)
		}
		if err := store.Add(ctx, identB, docsB, embsB); err != nil {
			rt.Fatalf("InMemoryStore.Add(B) failed: %v", err)
		}

		// Search for identifier A — request more than exist to get all possible matches.
		topK := numDocsA + numDocsB
		resultsA, err := store.Search(ctx, identA, embsA[0], topK)
		if err != nil {
			rt.Fatalf("InMemoryStore.Search(A) failed: %v", err)
		}

		// Verify A's results contain exactly numDocsA documents (no B docs leaked).
		if len(resultsA) != numDocsA {
			rt.Fatalf("Search(A) returned %d results, want %d", len(resultsA), numDocsA)
		}

		// Every result from Search(A) must be a document that was stored under A.
		for i, sd := range resultsA {
			if !contentsA[sd.Document.Content] {
				rt.Fatalf("Search(A) result[%d] has content %q which was not stored under identifier A",
					i, sd.Document.Content)
			}
		}

		// Search for identifier B — request more than exist to get all possible matches.
		resultsB, err := store.Search(ctx, identB, embsB[0], topK)
		if err != nil {
			rt.Fatalf("InMemoryStore.Search(B) failed: %v", err)
		}

		// Verify B's results contain exactly numDocsB documents (no A docs leaked).
		if len(resultsB) != numDocsB {
			rt.Fatalf("Search(B) returned %d results, want %d", len(resultsB), numDocsB)
		}

		// Every result from Search(B) must be a document that was stored under B.
		for i, sd := range resultsB {
			if !contentsB[sd.Document.Content] {
				rt.Fatalf("Search(B) result[%d] has content %q which was not stored under identifier B",
					i, sd.Document.Content)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Feature: memory-rag-separation, Property 2: Search Results Ordered by Descending Similarity
// ---------------------------------------------------------------------------

// TestProperty_MemoryStoreSearchOrder verifies that for any non-empty identifier,
// any set of documents with embeddings stored under that identifier, and any query
// embedding, the results returned by InMemoryStore.Search are ordered by descending
// Score — that is, for all consecutive pairs results[i] and results[i+1],
// results[i].Score >= results[i+1].Score.
//
// **Validates: Requirements 2.3**
func TestProperty_MemoryStoreSearchOrder(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		const dim = 8
		store := NewInMemoryStore()
		ctx := context.Background()

		identifier := genNonEmptyString(rt, "identifier")

		// Generate 2–20 documents with random embeddings so similarity scores vary.
		numDocs := rapid.IntRange(2, 20).Draw(rt, "num_docs")
		docs := make([]agent.Document, numDocs)
		embeddings := make([][]float64, numDocs)
		for i := range numDocs {
			docs[i] = agent.Document{
				Content:  genNonEmptyString(rt, "content"),
				Metadata: genMetadata(rt),
			}
			emb := make([]float64, dim)
			for j := range dim {
				emb[j] = rapid.Float64Range(-1.0, 1.0).Draw(rt, "emb_component")
			}
			embeddings[i] = emb
		}

		if err := store.Add(ctx, identifier, docs, embeddings); err != nil {
			rt.Fatalf("InMemoryStore.Add failed: %v", err)
		}

		// Generate a random query embedding.
		queryEmb := make([]float64, dim)
		for j := range dim {
			queryEmb[j] = rapid.Float64Range(-1.0, 1.0).Draw(rt, "query_component")
		}

		// Request all documents back.
		results, err := store.Search(ctx, identifier, queryEmb, numDocs)
		if err != nil {
			rt.Fatalf("InMemoryStore.Search failed: %v", err)
		}

		// Verify descending score order for all consecutive pairs.
		for i := 1; i < len(results); i++ {
			if results[i-1].Score < results[i].Score {
				rt.Fatalf("results not sorted by descending score: results[%d].Score=%v < results[%d].Score=%v",
					i-1, results[i-1].Score, i, results[i].Score)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Feature: memory-rag-separation, Property 5: Remember-then-Recall Round-Trip
// ---------------------------------------------------------------------------

// TestProperty_RememberRecallRoundTrip verifies that for any valid non-empty
// identifier and non-empty fact string, after calling Memory.Remember(ctx,
// identifier, fact, nil), a subsequent call to Memory.Recall(ctx, identifier,
// fact, 1) returns a non-empty result slice where at least one entry has Fact
// equal to the original fact string.
//
// This exercises the full NewInMemory convenience constructor path:
// InMemoryStore → Adapter → InMemory, confirming that Remember stores the
// fact and Recall retrieves it using the fact itself as the semantic query.
//
// **Validates: Requirements 10.3, 7.3**
func TestProperty_RememberRecallRoundTrip(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate random non-empty identifier and fact.
		identifier := genNonEmptyString(rt, "identifier")
		fact := genNonEmptyString(rt, "fact")

		// Create a fresh in-memory Memory instance with a deterministic embedder.
		embedder := &hashEmbedder{dim: 16}
		mem := NewInMemory(embedder)
		ctx := context.Background()

		// Remember the fact.
		err := mem.Remember(ctx, identifier, fact, nil)
		if err != nil {
			rt.Fatalf("Remember failed: %v", err)
		}

		// Recall using the fact itself as the query — should match with high similarity.
		results, err := mem.Recall(ctx, identifier, fact, 1)
		if err != nil {
			rt.Fatalf("Recall failed: %v", err)
		}

		// Results must be non-empty.
		if len(results) == 0 {
			rt.Fatalf("Recall returned empty slice after Remember(identifier=%q, fact=%q)", identifier, fact)
		}

		// At least one entry must have Fact equal to the original fact.
		found := false
		for _, entry := range results {
			if entry.Fact == fact {
				found = true
				break
			}
		}
		if !found {
			rt.Fatalf("Recall results do not contain the stored fact %q; got %+v", fact, results)
		}
	})
}
