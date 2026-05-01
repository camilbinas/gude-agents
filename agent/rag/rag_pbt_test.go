package rag_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/rag"
	"pgregory.net/rapid"
)

// Feature: vector-store-lifecycle, Property 1: Upsert+Find round-trip

// genNonEmptyID generates a non-empty document ID.
func genNonEmptyID(t *rapid.T, label string) string {
	return rapid.StringMatching(`[a-zA-Z0-9_-]{1,32}`).Draw(t, label)
}

// genContent generates random document content.
func genContent(t *rapid.T, label string) string {
	return rapid.StringMatching(`[a-zA-Z0-9 .,!?]{0,100}`).Draw(t, label)
}

// genMetadata generates random metadata with 0–5 key-value pairs.
func genMetadata(t *rapid.T, label string) map[string]string {
	numKeys := rapid.IntRange(0, 5).Draw(t, fmt.Sprintf("%s_numKeys", label))
	if numKeys == 0 {
		return nil
	}
	m := make(map[string]string, numKeys)
	for i := 0; i < numKeys; i++ {
		key := rapid.StringMatching(`[a-z_]{1,10}`).Draw(t, fmt.Sprintf("%s_key_%d", label, i))
		val := rapid.StringMatching(`[a-zA-Z0-9]{0,20}`).Draw(t, fmt.Sprintf("%s_val_%d", label, i))
		m[key] = val
	}
	return m
}

// genEmbedding generates a dummy embedding vector of fixed dimension.
func genEmbedding(t *rapid.T, label string) []float64 {
	dim := 4
	emb := make([]float64, dim)
	for i := 0; i < dim; i++ {
		emb[i] = rapid.Float64Range(-1.0, 1.0).Draw(t, fmt.Sprintf("%s_dim_%d", label, i))
	}
	return emb
}

// TestProperty_UpsertFindRoundTrip verifies that for any valid document with a
// non-empty ID, upserting it into the store and then calling Find with that ID
// returns a document with the same Content and Metadata.
func TestProperty_UpsertFindRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		store := rag.NewMemoryStore()
		ctx := context.Background()

		id := genNonEmptyID(t, "id")
		content := genContent(t, "content")
		metadata := genMetadata(t, "metadata")
		embedding := genEmbedding(t, "embedding")

		doc := agent.Document{
			ID:       id,
			Content:  content,
			Metadata: metadata,
		}

		// Upsert the document.
		ids, err := store.Upsert(ctx, []agent.Document{doc}, [][]float64{embedding})
		if err != nil {
			t.Fatalf("Upsert failed: %v", err)
		}
		if len(ids) != 1 || ids[0] != id {
			t.Fatalf("expected returned ID %q, got %v", id, ids)
		}

		// Find the document by ID.
		found, err := store.Find(ctx, id)
		if err != nil {
			t.Fatalf("Find failed: %v", err)
		}
		if len(found) != 1 {
			t.Fatalf("expected 1 document from Find, got %d", len(found))
		}

		// Assert Content matches.
		if found[0].Content != content {
			t.Fatalf("Content mismatch:\n  expected: %q\n  got:      %q", content, found[0].Content)
		}

		// Assert Metadata matches.
		if !metadataEqual(metadata, found[0].Metadata) {
			t.Fatalf("Metadata mismatch:\n  expected: %v\n  got:      %v", metadata, found[0].Metadata)
		}
	})
}

// metadataEqual compares two metadata maps, treating nil and empty map as equal.
func metadataEqual(a, b map[string]string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// Feature: vector-store-lifecycle, Property 2: Upsert overwrite

// TestProperty_UpsertOverwrite verifies that for any document ID and two distinct
// (content, metadata, embedding) tuples, upserting the first then upserting the
// second with the same ID results in Find returning only the second version's
// content and metadata.
func TestProperty_UpsertOverwrite(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		store := rag.NewMemoryStore()
		ctx := context.Background()

		id := genNonEmptyID(t, "id")

		// Generate two different (content, metadata, embedding) tuples.
		content1 := genContent(t, "content1")
		metadata1 := genMetadata(t, "metadata1")
		embedding1 := genEmbedding(t, "embedding1")

		content2 := genContent(t, "content2")
		metadata2 := genMetadata(t, "metadata2")
		embedding2 := genEmbedding(t, "embedding2")

		// Upsert the first version.
		doc1 := agent.Document{
			ID:       id,
			Content:  content1,
			Metadata: metadata1,
		}
		_, err := store.Upsert(ctx, []agent.Document{doc1}, [][]float64{embedding1})
		if err != nil {
			t.Fatalf("first Upsert failed: %v", err)
		}

		// Upsert the second version with the same ID.
		doc2 := agent.Document{
			ID:       id,
			Content:  content2,
			Metadata: metadata2,
		}
		ids, err := store.Upsert(ctx, []agent.Document{doc2}, [][]float64{embedding2})
		if err != nil {
			t.Fatalf("second Upsert failed: %v", err)
		}
		if len(ids) != 1 || ids[0] != id {
			t.Fatalf("expected returned ID %q, got %v", id, ids)
		}

		// Find by ID and assert it returns only the second version.
		found, err := store.Find(ctx, id)
		if err != nil {
			t.Fatalf("Find failed: %v", err)
		}
		if len(found) != 1 {
			t.Fatalf("expected 1 document from Find, got %d", len(found))
		}

		// Assert Content matches the second version.
		if found[0].Content != content2 {
			t.Fatalf("Content mismatch after overwrite:\n  expected: %q\n  got:      %q", content2, found[0].Content)
		}

		// Assert Metadata matches the second version.
		if !metadataEqual(metadata2, found[0].Metadata) {
			t.Fatalf("Metadata mismatch after overwrite:\n  expected: %v\n  got:      %v", metadata2, found[0].Metadata)
		}
	})
}

// Feature: vector-store-lifecycle, Property 3: Auto-generated IDs unique

// TestProperty_AutoGeneratedIDsUnique verifies that for any batch of documents
// with empty IDs, the IDs returned by Upsert are all non-empty and pairwise distinct.
func TestProperty_AutoGeneratedIDsUnique(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		store := rag.NewMemoryStore()
		ctx := context.Background()

		// Generate a random batch size between 1 and 20.
		batchSize := rapid.IntRange(1, 20).Draw(t, "batchSize")

		// Create documents with empty IDs.
		docs := make([]agent.Document, batchSize)
		embeddings := make([][]float64, batchSize)
		for i := range docs {
			docs[i] = agent.Document{
				ID:       "", // empty ID triggers auto-generation
				Content:  genContent(t, fmt.Sprintf("content_%d", i)),
				Metadata: genMetadata(t, fmt.Sprintf("metadata_%d", i)),
			}
			embeddings[i] = genEmbedding(t, fmt.Sprintf("embedding_%d", i))
		}

		// Upsert the batch.
		ids, err := store.Upsert(ctx, docs, embeddings)
		if err != nil {
			t.Fatalf("Upsert failed: %v", err)
		}

		// Assert returned IDs slice has the correct length.
		if len(ids) != batchSize {
			t.Fatalf("expected %d IDs, got %d", batchSize, len(ids))
		}

		// Assert all returned IDs are non-empty and pairwise distinct.
		seen := make(map[string]struct{}, batchSize)
		for i, id := range ids {
			if id == "" {
				t.Fatalf("returned ID at index %d is empty", i)
			}
			if _, exists := seen[id]; exists {
				t.Fatalf("duplicate ID %q at index %d", id, i)
			}
			seen[id] = struct{}{}
		}
	})
}

// Feature: vector-store-lifecycle, Property 4: Upsert preserves input order

// TestProperty_UpsertPreservesOrder verifies that for any slice of documents
// with non-empty IDs passed to Upsert, the returned IDs slice has the same
// length as the input and each ID corresponds to the document at the same index.
func TestProperty_UpsertPreservesOrder(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		store := rag.NewMemoryStore()
		ctx := context.Background()

		// Generate a random batch size between 1 and 20.
		batchSize := rapid.IntRange(1, 20).Draw(t, "batchSize")

		// Create documents with non-empty IDs.
		docs := make([]agent.Document, batchSize)
		embeddings := make([][]float64, batchSize)
		for i := range docs {
			docs[i] = agent.Document{
				ID:       genNonEmptyID(t, fmt.Sprintf("id_%d", i)),
				Content:  genContent(t, fmt.Sprintf("content_%d", i)),
				Metadata: genMetadata(t, fmt.Sprintf("metadata_%d", i)),
			}
			embeddings[i] = genEmbedding(t, fmt.Sprintf("embedding_%d", i))
		}

		// Upsert the batch.
		ids, err := store.Upsert(ctx, docs, embeddings)
		if err != nil {
			t.Fatalf("Upsert failed: %v", err)
		}

		// Assert returned IDs slice has the same length as input.
		if len(ids) != batchSize {
			t.Fatalf("expected %d IDs, got %d", batchSize, len(ids))
		}

		// Assert each returned ID matches the corresponding input document's ID.
		for i, id := range ids {
			if id != docs[i].ID {
				t.Fatalf("ID mismatch at index %d: expected %q, got %q", i, docs[i].ID, id)
			}
		}
	})
}

// Feature: vector-store-lifecycle, Property 5: Find preserves input order

// TestProperty_FindPreservesOrder verifies that for any set of upserted documents
// and any sequence of IDs (some existing, some not), Find returns documents in
// the same relative order as the input IDs, containing only documents whose IDs
// exist in the store.
func TestProperty_FindPreservesOrder(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		store := rag.NewMemoryStore()
		ctx := context.Background()

		// Generate a batch of documents with unique non-empty IDs.
		batchSize := rapid.IntRange(1, 15).Draw(t, "batchSize")
		docs := make([]agent.Document, batchSize)
		embeddings := make([][]float64, batchSize)
		existingIDs := make(map[string]agent.Document, batchSize)

		for i := range docs {
			id := genNonEmptyID(t, fmt.Sprintf("id_%d", i))
			content := genContent(t, fmt.Sprintf("content_%d", i))
			metadata := genMetadata(t, fmt.Sprintf("metadata_%d", i))
			embedding := genEmbedding(t, fmt.Sprintf("embedding_%d", i))

			docs[i] = agent.Document{
				ID:       id,
				Content:  content,
				Metadata: metadata,
			}
			embeddings[i] = embedding
			existingIDs[id] = docs[i]
		}

		// Upsert all documents.
		_, err := store.Upsert(ctx, docs, embeddings)
		if err != nil {
			t.Fatalf("Upsert failed: %v", err)
		}

		// Generate a sequence of IDs to query: mix of existing and non-existing.
		numQueryIDs := rapid.IntRange(1, 20).Draw(t, "numQueryIDs")
		queryIDs := make([]string, numQueryIDs)
		for i := range queryIDs {
			if rapid.Bool().Draw(t, fmt.Sprintf("useExisting_%d", i)) && len(docs) > 0 {
				// Pick a random existing ID.
				idx := rapid.IntRange(0, len(docs)-1).Draw(t, fmt.Sprintf("existIdx_%d", i))
				queryIDs[i] = docs[idx].ID
			} else {
				// Generate a non-existing ID (prefix with "missing_" to avoid collision).
				queryIDs[i] = "missing_" + genNonEmptyID(t, fmt.Sprintf("missingId_%d", i))
			}
		}

		// Call Find with the mixed sequence.
		found, err := store.Find(ctx, queryIDs...)
		if err != nil {
			t.Fatalf("Find failed: %v", err)
		}

		// Build expected result: iterate queryIDs in order, include only existing ones.
		var expected []agent.Document
		for _, qid := range queryIDs {
			if doc, ok := existingIDs[qid]; ok {
				expected = append(expected, doc)
			}
		}

		// Assert the count matches the number of existing IDs in the query.
		if len(found) != len(expected) {
			t.Fatalf("expected %d documents, got %d", len(expected), len(found))
		}

		// Assert documents are in the same relative order as the input IDs
		// and only existing IDs are returned.
		for i, doc := range found {
			if doc.ID != expected[i].ID {
				t.Fatalf("order mismatch at index %d: expected ID %q, got %q", i, expected[i].ID, doc.ID)
			}
			if doc.Content != expected[i].Content {
				t.Fatalf("content mismatch at index %d: expected %q, got %q", i, expected[i].Content, doc.Content)
			}
			if !metadataEqual(expected[i].Metadata, doc.Metadata) {
				t.Fatalf("metadata mismatch at index %d: expected %v, got %v", i, expected[i].Metadata, doc.Metadata)
			}
		}
	})
}

// Feature: vector-store-lifecycle, Property 6: DeleteByMetadata removes matching

// TestProperty_DeleteByMetadataInvariant verifies that after calling DeleteByMetadata,
// no remaining document matches the filter AND all documents that did NOT match
// the filter are still present.
func TestProperty_DeleteByMetadataInvariant(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		store := rag.NewMemoryStore()
		ctx := context.Background()

		// Generate a non-empty filter (1–3 key-value pairs).
		numFilterKeys := rapid.IntRange(1, 3).Draw(t, "numFilterKeys")
		filter := make(map[string]string, numFilterKeys)
		for i := 0; i < numFilterKeys; i++ {
			key := rapid.StringMatching(`[a-z]{1,8}`).Draw(t, fmt.Sprintf("filterKey_%d", i))
			val := rapid.StringMatching(`[a-zA-Z0-9]{1,10}`).Draw(t, fmt.Sprintf("filterVal_%d", i))
			filter[key] = val
		}

		// Generate documents: some matching the filter, some not.
		numMatching := rapid.IntRange(1, 10).Draw(t, "numMatching")
		numNonMatching := rapid.IntRange(1, 10).Draw(t, "numNonMatching")

		var allDocs []agent.Document
		var allEmbeddings [][]float64
		var nonMatchingIDs []string

		// Create matching documents: metadata contains all filter k/v pairs (plus possibly more).
		for i := 0; i < numMatching; i++ {
			id := genNonEmptyID(t, fmt.Sprintf("matchID_%d", i))
			content := genContent(t, fmt.Sprintf("matchContent_%d", i))
			embedding := genEmbedding(t, fmt.Sprintf("matchEmb_%d", i))

			// Start with the filter metadata, then add extra keys.
			meta := make(map[string]string)
			for k, v := range filter {
				meta[k] = v
			}
			numExtra := rapid.IntRange(0, 3).Draw(t, fmt.Sprintf("matchExtra_%d", i))
			for j := 0; j < numExtra; j++ {
				extraKey := rapid.StringMatching(`extra_[a-z]{1,5}`).Draw(t, fmt.Sprintf("matchExtraKey_%d_%d", i, j))
				extraVal := rapid.StringMatching(`[a-zA-Z0-9]{1,10}`).Draw(t, fmt.Sprintf("matchExtraVal_%d_%d", i, j))
				meta[extraKey] = extraVal
			}

			allDocs = append(allDocs, agent.Document{ID: id, Content: content, Metadata: meta})
			allEmbeddings = append(allEmbeddings, embedding)
		}

		// Create non-matching documents: metadata does NOT contain all filter k/v pairs.
		for i := 0; i < numNonMatching; i++ {
			id := genNonEmptyID(t, fmt.Sprintf("nonMatchID_%d", i))
			content := genContent(t, fmt.Sprintf("nonMatchContent_%d", i))
			embedding := genEmbedding(t, fmt.Sprintf("nonMatchEmb_%d", i))

			// Generate metadata that deliberately does NOT match the filter.
			// Strategy: include some filter keys with wrong values, or omit some filter keys.
			meta := make(map[string]string)
			numKeys := rapid.IntRange(0, 4).Draw(t, fmt.Sprintf("nonMatchNumKeys_%d", i))
			for j := 0; j < numKeys; j++ {
				key := rapid.StringMatching(`[a-z]{1,8}`).Draw(t, fmt.Sprintf("nonMatchKey_%d_%d", i, j))
				val := rapid.StringMatching(`[a-zA-Z0-9]{1,10}`).Draw(t, fmt.Sprintf("nonMatchVal_%d_%d", i, j))
				meta[key] = val
			}
			// Ensure the metadata does NOT match the filter by breaking at least one filter condition.
			// Pick a random filter key and set it to a different value or remove it.
			filterKeys := make([]string, 0, len(filter))
			for k := range filter {
				filterKeys = append(filterKeys, k)
			}
			breakIdx := rapid.IntRange(0, len(filterKeys)-1).Draw(t, fmt.Sprintf("breakIdx_%d", i))
			breakKey := filterKeys[breakIdx]
			// Either delete the key or set it to a guaranteed-different value.
			if rapid.Bool().Draw(t, fmt.Sprintf("deleteOrChange_%d", i)) {
				delete(meta, breakKey)
			} else {
				meta[breakKey] = filter[breakKey] + "_different"
			}

			allDocs = append(allDocs, agent.Document{ID: id, Content: content, Metadata: meta})
			allEmbeddings = append(allEmbeddings, embedding)
			nonMatchingIDs = append(nonMatchingIDs, id)
		}

		// Upsert all documents.
		_, err := store.Upsert(ctx, allDocs, allEmbeddings)
		if err != nil {
			t.Fatalf("Upsert failed: %v", err)
		}

		// Call DeleteByMetadata with the filter.
		err = store.DeleteByMetadata(ctx, filter)
		if err != nil {
			t.Fatalf("DeleteByMetadata failed: %v", err)
		}

		// Retrieve all remaining documents by querying all IDs.
		allIDs := make([]string, len(allDocs))
		for i, doc := range allDocs {
			allIDs[i] = doc.ID
		}
		remaining, err := store.Find(ctx, allIDs...)
		if err != nil {
			t.Fatalf("Find failed: %v", err)
		}

		// Assert: no remaining document has metadata matching the filter.
		for _, doc := range remaining {
			if docMatchesFilter(doc.Metadata, filter) {
				t.Fatalf("document %q still matches filter after DeleteByMetadata:\n  metadata: %v\n  filter: %v",
					doc.ID, doc.Metadata, filter)
			}
		}

		// Assert: all non-matching documents are still present.
		remainingIDSet := make(map[string]struct{}, len(remaining))
		for _, doc := range remaining {
			remainingIDSet[doc.ID] = struct{}{}
		}
		for _, id := range nonMatchingIDs {
			if _, ok := remainingIDSet[id]; !ok {
				t.Fatalf("non-matching document %q was incorrectly deleted", id)
			}
		}
	})
}

// docMatchesFilter returns true if the document's metadata contains all
// key-value pairs in the filter (AND semantics).
func docMatchesFilter(metadata map[string]string, filter map[string]string) bool {
	for k, v := range filter {
		if metadata[k] != v {
			return false
		}
	}
	return true
}

// Feature: vector-store-lifecycle, Property 7: Ingest conditional deletion

// mockEmbedder is a mock Embedder that returns a fixed-dimension dummy embedding.
type mockEmbedder struct {
	dim int
}

func (e *mockEmbedder) Embed(_ context.Context, _ string) ([]float64, error) {
	emb := make([]float64, e.dim)
	for i := range emb {
		emb[i] = 0.1
	}
	return emb, nil
}

// callRecord records the order and type of calls made to a mock store.
type callRecord struct {
	method string
	filter map[string]string // populated for DeleteByMetadata calls
}

// mockVectorStoreManager implements agent.VectorStoreManager and tracks calls.
type mockVectorStoreManager struct {
	calls []callRecord
}

func (m *mockVectorStoreManager) Upsert(_ context.Context, docs []agent.Document, _ [][]float64) ([]string, error) {
	m.calls = append(m.calls, callRecord{method: "Upsert"})
	ids := make([]string, len(docs))
	for i := range docs {
		ids[i] = fmt.Sprintf("id_%d", i)
	}
	return ids, nil
}

func (m *mockVectorStoreManager) Search(_ context.Context, _ []float64, _ int) ([]agent.ScoredDocument, error) {
	return nil, nil
}

func (m *mockVectorStoreManager) Delete(_ context.Context, _ ...string) error {
	return nil
}

func (m *mockVectorStoreManager) Find(_ context.Context, _ ...string) ([]agent.Document, error) {
	return nil, nil
}

func (m *mockVectorStoreManager) DeleteByMetadata(_ context.Context, filter map[string]string) error {
	// Copy the filter to avoid aliasing issues.
	f := make(map[string]string, len(filter))
	for k, v := range filter {
		f[k] = v
	}
	m.calls = append(m.calls, callRecord{method: "DeleteByMetadata", filter: f})
	return nil
}

// mockPlainVectorStore implements only agent.VectorStore (not VectorStoreManager)
// and tracks calls to verify no deletion is attempted.
type mockPlainVectorStore struct {
	calls []callRecord
}

func (m *mockPlainVectorStore) Upsert(_ context.Context, docs []agent.Document, _ [][]float64) ([]string, error) {
	m.calls = append(m.calls, callRecord{method: "Upsert"})
	ids := make([]string, len(docs))
	for i := range docs {
		ids[i] = fmt.Sprintf("id_%d", i)
	}
	return ids, nil
}

func (m *mockPlainVectorStore) Search(_ context.Context, _ []float64, _ int) ([]agent.ScoredDocument, error) {
	return nil, nil
}

func (m *mockPlainVectorStore) Delete(_ context.Context, _ ...string) error {
	return nil
}

// TestProperty_IngestConditionalDeletion verifies that when Ingest is called with
// a VectorStoreManager and metadata containing a "source" key, DeleteByMetadata is
// called with the source filter before Upsert. When called with a plain VectorStore,
// no deletion is attempted.
func TestProperty_IngestConditionalDeletion(t *testing.T) {
	t.Run("VectorStoreManager_deletes_before_upsert", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			ctx := context.Background()
			embedder := &mockEmbedder{dim: 4}
			store := &mockVectorStoreManager{}

			// Generate random source value and text content.
			source := rapid.StringMatching(`[a-zA-Z0-9_/.-]{1,30}`).Draw(t, "source")
			text := rapid.StringMatching(`[a-zA-Z0-9 ]{1,100}`).Draw(t, "text")

			texts := []string{text}
			metadata := []map[string]string{{"source": source}}

			err := rag.Ingest(ctx, store, embedder, texts, metadata)
			if err != nil {
				t.Fatalf("Ingest failed: %v", err)
			}

			// Verify DeleteByMetadata was called before Upsert.
			if len(store.calls) < 2 {
				t.Fatalf("expected at least 2 calls (DeleteByMetadata + Upsert), got %d", len(store.calls))
			}

			// Find the DeleteByMetadata call and verify it comes before Upsert.
			deleteIdx := -1
			upsertIdx := -1
			for i, call := range store.calls {
				if call.method == "DeleteByMetadata" && deleteIdx == -1 {
					deleteIdx = i
				}
				if call.method == "Upsert" && upsertIdx == -1 {
					upsertIdx = i
				}
			}

			if deleteIdx == -1 {
				t.Fatalf("DeleteByMetadata was not called")
			}
			if upsertIdx == -1 {
				t.Fatalf("Upsert was not called")
			}
			if deleteIdx >= upsertIdx {
				t.Fatalf("DeleteByMetadata (index %d) was not called before Upsert (index %d)", deleteIdx, upsertIdx)
			}

			// Verify the filter passed to DeleteByMetadata contains the correct source.
			deleteCall := store.calls[deleteIdx]
			if deleteCall.filter["source"] != source {
				t.Fatalf("DeleteByMetadata filter mismatch: expected source=%q, got %v", source, deleteCall.filter)
			}
		})
	})

	t.Run("PlainVectorStore_no_deletion", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			ctx := context.Background()
			embedder := &mockEmbedder{dim: 4}
			store := &mockPlainVectorStore{}

			// Generate random source value and text content.
			source := rapid.StringMatching(`[a-zA-Z0-9_/.-]{1,30}`).Draw(t, "source")
			text := rapid.StringMatching(`[a-zA-Z0-9 ]{1,100}`).Draw(t, "text")

			texts := []string{text}
			metadata := []map[string]string{{"source": source}}

			err := rag.Ingest(ctx, store, embedder, texts, metadata)
			if err != nil {
				t.Fatalf("Ingest failed: %v", err)
			}

			// Verify no DeleteByMetadata call was made (plain VectorStore doesn't have it).
			for _, call := range store.calls {
				if call.method == "DeleteByMetadata" {
					t.Fatalf("DeleteByMetadata should not be called on a plain VectorStore")
				}
			}

			// Verify Upsert was called.
			upsertCalled := false
			for _, call := range store.calls {
				if call.method == "Upsert" {
					upsertCalled = true
					break
				}
			}
			if !upsertCalled {
				t.Fatalf("Upsert was not called")
			}
		})
	})
}
