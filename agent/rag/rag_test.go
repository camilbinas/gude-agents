package rag

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/camilbinas/gude-agents/agent"
	"pgregory.net/rapid"
)

// Feature: rag, Property 1: SplitText chunk size bound
// **Validates: Requirements 4.6**
func TestSplitText_ChunkSizeBound(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		text := rapid.StringOf(rapid.Rune()).Draw(t, "text")
		if len([]rune(text)) == 0 {
			t.Skip("empty text")
		}
		chunkSize := rapid.IntRange(1, 200).Draw(t, "chunkSize")
		chunkOverlap := rapid.IntRange(0, chunkSize-1).Draw(t, "chunkOverlap")

		chunks := SplitText(text, chunkSize, chunkOverlap)

		for i, chunk := range chunks {
			runeLen := utf8.RuneCountInString(chunk)
			if runeLen > chunkSize {
				t.Fatalf("chunk[%d] has %d runes, exceeds chunkSize %d", i, runeLen, chunkSize)
			}
		}
	})
}

// Feature: rag, Property 2: SplitText zero-overlap partition
// **Validates: Requirements 4.5**
func TestSplitText_ZeroOverlapPartition(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		text := rapid.StringOf(rapid.Rune()).Draw(t, "text")
		if len([]rune(text)) == 0 {
			t.Skip("empty text")
		}
		chunkSize := rapid.IntRange(1, 200).Draw(t, "chunkSize")

		chunks := SplitText(text, chunkSize, 0)

		concatenated := ""
		for _, chunk := range chunks {
			concatenated += chunk
		}
		if concatenated != text {
			t.Fatalf("concatenation of chunks does not equal original text\noriginal:     %q\nconcatenated: %q", text, concatenated)
		}
	})
}

// --- Unit tests for SplitText edge cases ---
// Requirements: 4.2, 4.3, 4.4

func TestSplitTextE_EmptyInput(t *testing.T) {
	// Requirement 4.4: empty text returns empty slice
	chunks, err := SplitTextE("", 10, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) != 0 {
		t.Fatalf("expected empty slice, got %v", chunks)
	}
}

func TestSplitText_EmptyInput(t *testing.T) {
	// Requirement 4.4: empty text returns empty slice (clamping variant)
	chunks := SplitText("", 10, 0)
	if len(chunks) != 0 {
		t.Fatalf("expected empty slice, got %v", chunks)
	}
}

func TestSplitTextE_ChunkSizeLessThanOne(t *testing.T) {
	// Requirement 4.2: chunkSize < 1 returns error
	tests := []struct {
		name      string
		chunkSize int
	}{
		{"zero", 0},
		{"negative", -1},
		{"very negative", -100},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := SplitTextE("hello", tc.chunkSize, 0)
			if err == nil {
				t.Fatal("expected error for chunkSize < 1, got nil")
			}
			expected := fmt.Sprintf("splittext: chunkSize must be >= 1, got %d", tc.chunkSize)
			if err.Error() != expected {
				t.Fatalf("expected error %q, got %q", expected, err.Error())
			}
		})
	}
}

func TestSplitTextE_OverlapGEChunkSize(t *testing.T) {
	// Requirement 4.3: chunkOverlap >= chunkSize returns error
	tests := []struct {
		name         string
		chunkSize    int
		chunkOverlap int
	}{
		{"equal", 5, 5},
		{"overlap greater", 5, 6},
		{"overlap much greater", 3, 100},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := SplitTextE("hello", tc.chunkSize, tc.chunkOverlap)
			if err == nil {
				t.Fatal("expected error for chunkOverlap >= chunkSize, got nil")
			}
			expected := fmt.Sprintf("splittext: chunkOverlap (%d) must be < chunkSize (%d)", tc.chunkOverlap, tc.chunkSize)
			if err.Error() != expected {
				t.Fatalf("expected error %q, got %q", expected, err.Error())
			}
		})
	}
}

func TestSplitText_ClampsInvalidParams(t *testing.T) {
	// SplitText silently clamps invalid params instead of erroring
	tests := []struct {
		name         string
		text         string
		chunkSize    int
		chunkOverlap int
	}{
		{"chunkSize zero", "abc", 0, 0},
		{"chunkSize negative", "abc", -5, 0},
		{"overlap equals chunkSize", "abcdef", 3, 3},
		{"overlap exceeds chunkSize", "abcdef", 3, 10},
		{"negative overlap", "abcdef", 3, -1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Should not panic and should return a valid result
			chunks := SplitText(tc.text, tc.chunkSize, tc.chunkOverlap)
			if chunks == nil {
				t.Fatal("expected non-nil result from SplitText with clamped params")
			}
			// Every chunk should be non-empty for non-empty input
			for i, c := range chunks {
				if len(c) == 0 {
					t.Fatalf("chunk[%d] is empty", i)
				}
			}
		})
	}
}

func TestSplitTextE_BoundaryValues(t *testing.T) {
	// Boundary: chunkSize=1, overlap=0 → each rune is its own chunk
	t.Run("chunkSize 1 overlap 0", func(t *testing.T) {
		chunks, err := SplitTextE("abc", 1, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(chunks) != 3 {
			t.Fatalf("expected 3 chunks, got %d: %v", len(chunks), chunks)
		}
		for i, expected := range []string{"a", "b", "c"} {
			if chunks[i] != expected {
				t.Fatalf("chunk[%d] = %q, want %q", i, chunks[i], expected)
			}
		}
	})

	// Boundary: chunkSize equals text length → single chunk
	t.Run("chunkSize equals text length", func(t *testing.T) {
		chunks, err := SplitTextE("hello", 5, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(chunks) != 1 || chunks[0] != "hello" {
			t.Fatalf("expected [\"hello\"], got %v", chunks)
		}
	})

	// Boundary: chunkSize exceeds text length → single chunk
	t.Run("chunkSize exceeds text length", func(t *testing.T) {
		chunks, err := SplitTextE("hi", 100, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(chunks) != 1 || chunks[0] != "hi" {
			t.Fatalf("expected [\"hi\"], got %v", chunks)
		}
	})

	// Boundary: overlap = chunkSize - 1 (maximum valid overlap)
	t.Run("max overlap", func(t *testing.T) {
		chunks, err := SplitTextE("abcde", 3, 2)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// step = 3 - 2 = 1, so chunks start at 0,1,2,3,4 → "abc","bcd","cde","de","e"
		// Each chunk is at most 3 runes
		for i, c := range chunks {
			if len([]rune(c)) > 3 {
				t.Fatalf("chunk[%d] = %q exceeds chunkSize 3", i, c)
			}
			if len(c) == 0 {
				t.Fatalf("chunk[%d] is empty", i)
			}
		}
		if len(chunks) < 3 {
			t.Fatalf("expected at least 3 chunks with max overlap, got %d", len(chunks))
		}
	})

	// Boundary: multi-byte runes (UTF-8)
	t.Run("multi-byte runes", func(t *testing.T) {
		text := "héllo"
		chunks, err := SplitTextE(text, 2, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// "héllo" is 5 runes → chunks of 2: "hé", "ll", "o"
		if len(chunks) != 3 {
			t.Fatalf("expected 3 chunks, got %d: %v", len(chunks), chunks)
		}
		if chunks[0] != "hé" {
			t.Fatalf("chunk[0] = %q, want %q", chunks[0], "hé")
		}
	})
}

// Feature: rag, Property 3: MemoryStore search returns results in descending score order
// **Validates: Requirements 3.4**
func TestMemoryStore_SearchDescendingOrder(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		const dim = 8

		// Generate between 1 and 20 documents with random embeddings.
		n := rapid.IntRange(1, 20).Draw(t, "numDocs")
		docs := make([]agent.Document, n)
		embeddings := make([][]float64, n)
		for i := 0; i < n; i++ {
			docs[i] = agent.Document{
				Content:  fmt.Sprintf("doc-%d", i),
				Metadata: map[string]string{},
			}
			emb := make([]float64, dim)
			for j := 0; j < dim; j++ {
				emb[j] = rapid.Float64Range(-1.0, 1.0).Draw(t, fmt.Sprintf("emb[%d][%d]", i, j))
			}
			embeddings[i] = emb
		}

		store := NewMemoryStore()
		err := store.Add(context.Background(), docs, embeddings)
		if err != nil {
			t.Fatalf("Add failed: %v", err)
		}

		// Generate a random query embedding.
		query := make([]float64, dim)
		for j := 0; j < dim; j++ {
			query[j] = rapid.Float64Range(-1.0, 1.0).Draw(t, fmt.Sprintf("query[%d]", j))
		}

		topK := rapid.IntRange(1, n).Draw(t, "topK")
		results, err := store.Search(context.Background(), query, topK)
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}

		// Assert descending score order.
		for i := 0; i < len(results)-1; i++ {
			if results[i].Score < results[i+1].Score {
				t.Fatalf("results not in descending score order: results[%d].Score=%f < results[%d].Score=%f",
					i, results[i].Score, i+1, results[i+1].Score)
			}
		}
	})
}

// Feature: rag, Property 4: MemoryStore returns all documents when topK exceeds store size
// **Validates: Requirements 3.5**
func TestMemoryStore_SearchReturnsAllWhenTopKExceedsSize(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		const dim = 8

		n := rapid.IntRange(1, 20).Draw(t, "numDocs")
		topK := rapid.IntRange(n+1, n+50).Draw(t, "topK")

		docs := make([]agent.Document, n)
		embeddings := make([][]float64, n)
		for i := 0; i < n; i++ {
			docs[i] = agent.Document{
				Content:  fmt.Sprintf("doc-%d", i),
				Metadata: map[string]string{},
			}
			emb := make([]float64, dim)
			for j := 0; j < dim; j++ {
				emb[j] = rapid.Float64Range(-1.0, 1.0).Draw(t, fmt.Sprintf("emb[%d][%d]", i, j))
			}
			embeddings[i] = emb
		}

		store := NewMemoryStore()
		err := store.Add(context.Background(), docs, embeddings)
		if err != nil {
			t.Fatalf("Add failed: %v", err)
		}

		query := make([]float64, dim)
		for j := 0; j < dim; j++ {
			query[j] = rapid.Float64Range(-1.0, 1.0).Draw(t, fmt.Sprintf("query[%d]", j))
		}

		results, err := store.Search(context.Background(), query, topK)
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}

		if len(results) != n {
			t.Fatalf("expected %d results (all docs), got %d (topK=%d)", n, len(results), topK)
		}
	})
}

// --- Unit tests for MemoryStore error cases ---
// Requirements: 3.2, 3.3, 3.6

func TestMemoryStore_AddLengthMismatch(t *testing.T) {
	// Requirement 3.2: Add with mismatched docs/embeddings lengths returns error
	store := NewMemoryStore()
	ctx := context.Background()

	tests := []struct {
		name       string
		docs       []agent.Document
		embeddings [][]float64
		wantErr    string
	}{
		{
			name:       "more docs than embeddings",
			docs:       []agent.Document{{Content: "a"}, {Content: "b"}},
			embeddings: [][]float64{{1.0}},
			wantErr:    "vectorstore: docs and embeddings length mismatch: 2 vs 1",
		},
		{
			name:       "more embeddings than docs",
			docs:       []agent.Document{{Content: "a"}},
			embeddings: [][]float64{{1.0}, {2.0}, {3.0}},
			wantErr:    "vectorstore: docs and embeddings length mismatch: 1 vs 3",
		},
		{
			name:       "empty docs non-empty embeddings",
			docs:       []agent.Document{},
			embeddings: [][]float64{{1.0}},
			wantErr:    "vectorstore: docs and embeddings length mismatch: 0 vs 1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := store.Add(ctx, tc.docs, tc.embeddings)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if err.Error() != tc.wantErr {
				t.Fatalf("expected error %q, got %q", tc.wantErr, err.Error())
			}
		})
	}
}

func TestMemoryStore_SearchTopKLessThanOne(t *testing.T) {
	// Requirement 3.3: Search with topK < 1 returns error
	store := NewMemoryStore()
	ctx := context.Background()

	// Add a document so the store is non-empty
	err := store.Add(ctx, []agent.Document{{Content: "hello"}}, [][]float64{{1.0, 0.0}})
	if err != nil {
		t.Fatalf("unexpected Add error: %v", err)
	}

	tests := []struct {
		name    string
		topK    int
		wantErr string
	}{
		{"zero", 0, "vectorstore: topK must be >= 1, got 0"},
		{"negative", -1, "vectorstore: topK must be >= 1, got -1"},
		{"very negative", -100, "vectorstore: topK must be >= 1, got -100"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := store.Search(ctx, []float64{1.0, 0.0}, tc.topK)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if err.Error() != tc.wantErr {
				t.Fatalf("expected error %q, got %q", tc.wantErr, err.Error())
			}
		})
	}
}

func TestMemoryStore_ConcurrentAccess(t *testing.T) {
	// Requirement 3.6: MemoryStore is safe for concurrent use.
	// This test should pass with -race flag.
	store := NewMemoryStore()
	ctx := context.Background()
	const goroutines = 10
	const opsPerGoroutine = 50

	done := make(chan struct{})
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer func() { done <- struct{}{} }()
			for i := 0; i < opsPerGoroutine; i++ {
				doc := agent.Document{Content: fmt.Sprintf("doc-%d-%d", id, i)}
				emb := []float64{float64(id), float64(i), 1.0}
				_ = store.Add(ctx, []agent.Document{doc}, [][]float64{emb})
			}
		}(g)
		go func(id int) {
			defer func() { done <- struct{}{} }()
			for i := 0; i < opsPerGoroutine; i++ {
				query := []float64{float64(id), float64(i), 1.0}
				_, _ = store.Search(ctx, query, 5)
			}
		}(g)
	}

	// Wait for all goroutines (2 per iteration: one Add, one Search)
	for i := 0; i < goroutines*2; i++ {
		<-done
	}
}

// --- Mock helpers for Retriever tests ---

// mockEmbedder returns a fixed embedding for any input.
type mockEmbedder struct {
	embedding []float64
	err       error
}

func (m *mockEmbedder) Embed(_ context.Context, _ string) ([]float64, error) {
	return m.embedding, m.err
}

// mockVectorStore returns a pre-configured list of ScoredDocuments from Search.
type mockVectorStore struct {
	searchResults []agent.ScoredDocument
	searchErr     error
}

func (m *mockVectorStore) Add(_ context.Context, _ []agent.Document, _ [][]float64) error {
	return nil
}

func (m *mockVectorStore) Search(_ context.Context, _ []float64, _ int) ([]agent.ScoredDocument, error) {
	return m.searchResults, m.searchErr
}

// Feature: rag, Property 7: Retriever returns documents in descending score order
// **Validates: Requirements 6.3**
func TestRetriever_DescendingOrder(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate between 1 and 20 scored documents in descending score order
		// (simulating what a real VectorStore would return).
		n := rapid.IntRange(1, 20).Draw(t, "numDocs")
		scores := make([]float64, n)
		for i := 0; i < n; i++ {
			scores[i] = rapid.Float64Range(-1.0, 1.0).Draw(t, fmt.Sprintf("score[%d]", i))
		}
		// Sort scores descending to simulate store output.
		sort.Slice(scores, func(i, j int) bool {
			return scores[i] > scores[j]
		})

		scoredDocs := make([]agent.ScoredDocument, n)
		for i, s := range scores {
			scoredDocs[i] = agent.ScoredDocument{
				Document: agent.Document{
					Content:  fmt.Sprintf("doc-%d", i),
					Metadata: map[string]string{},
				},
				Score: s,
			}
		}

		store := &mockVectorStore{searchResults: scoredDocs}
		embedder := &mockEmbedder{embedding: []float64{1.0}}
		retriever := NewRetriever(embedder, store, WithTopK(n))

		docs, err := retriever.Retrieve(context.Background(), "test query")
		if err != nil {
			t.Fatalf("Retrieve failed: %v", err)
		}

		// Map returned docs back to their scores via the original scoredDocs order.
		// Since the store returns docs in descending score order and no reranker is
		// configured, the retriever must preserve that order.
		if len(docs) > len(scoredDocs) {
			t.Fatalf("got %d docs, expected at most %d", len(docs), len(scoredDocs))
		}

		// Verify the returned documents preserve the descending order from the store.
		// Build a content→score lookup from the mock store results.
		scoreByContent := make(map[string]float64, len(scoredDocs))
		for _, sd := range scoredDocs {
			scoreByContent[sd.Document.Content] = sd.Score
		}

		for i := 0; i < len(docs)-1; i++ {
			scoreI := scoreByContent[docs[i].Content]
			scoreJ := scoreByContent[docs[i+1].Content]
			if scoreI < scoreJ {
				t.Fatalf("docs not in descending score order: docs[%d] score=%f < docs[%d] score=%f",
					i, scoreI, i+1, scoreJ)
			}
		}
	})
}

// Feature: rag, Property 8: Retriever score threshold filters low-scoring documents
// **Validates: Requirements 6.4**
func TestRetriever_ScoreThreshold(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		threshold := rapid.Float64Range(0, 1).Draw(t, "threshold")

		// Generate between 1 and 20 scored documents with random scores in [0, 1].
		n := rapid.IntRange(1, 20).Draw(t, "numDocs")
		scoredDocs := make([]agent.ScoredDocument, n)
		for i := 0; i < n; i++ {
			score := rapid.Float64Range(0, 1).Draw(t, fmt.Sprintf("score[%d]", i))
			scoredDocs[i] = agent.ScoredDocument{
				Document: agent.Document{
					Content:  fmt.Sprintf("doc-%d", i),
					Metadata: map[string]string{},
				},
				Score: score,
			}
		}
		// Sort descending to simulate real VectorStore output.
		sort.Slice(scoredDocs, func(i, j int) bool {
			return scoredDocs[i].Score > scoredDocs[j].Score
		})

		store := &mockVectorStore{searchResults: scoredDocs}
		embedder := &mockEmbedder{embedding: []float64{1.0}}
		retriever := NewRetriever(embedder, store,
			WithTopK(n),
			WithScoreThreshold(threshold),
		)

		docs, err := retriever.Retrieve(context.Background(), "test query")
		if err != nil {
			t.Fatalf("Retrieve failed: %v", err)
		}

		// Build a content→score lookup from the original scored docs.
		scoreByContent := make(map[string]float64, len(scoredDocs))
		for _, sd := range scoredDocs {
			scoreByContent[sd.Document.Content] = sd.Score
		}

		// Assert all returned docs have score >= threshold.
		for i, doc := range docs {
			score := scoreByContent[doc.Content]
			if score < threshold {
				t.Fatalf("doc[%d] (content=%q) has score %f which is below threshold %f",
					i, doc.Content, score, threshold)
			}
		}

		// Also verify no qualifying document was dropped: count how many original
		// docs meet the threshold and compare with the returned count.
		expectedCount := 0
		for _, sd := range scoredDocs {
			if sd.Score >= threshold {
				expectedCount++
			}
		}
		if len(docs) != expectedCount {
			t.Fatalf("expected %d docs with score >= %f, got %d", expectedCount, threshold, len(docs))
		}
	})
}

// mockReranker reverses the document order (deterministic shuffle).
type mockReranker struct{}

func (m *mockReranker) Rerank(_ context.Context, _ string, docs []agent.Document) ([]agent.Document, error) {
	reversed := make([]agent.Document, len(docs))
	for i, doc := range docs {
		reversed[len(docs)-1-i] = doc
	}
	return reversed, nil
}

// Feature: rag, Property 13: Reranker output is used as the final retrieval result
// **Validates: Requirements 9.3**
func TestRetriever_RerankerOutputUsed(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate between 1 and 20 scored documents with scores in [0, 1].
		n := rapid.IntRange(1, 20).Draw(t, "numDocs")
		scoredDocs := make([]agent.ScoredDocument, n)
		for i := 0; i < n; i++ {
			score := rapid.Float64Range(0, 1).Draw(t, fmt.Sprintf("score[%d]", i))
			scoredDocs[i] = agent.ScoredDocument{
				Document: agent.Document{
					Content:  fmt.Sprintf("doc-%d", i),
					Metadata: map[string]string{},
				},
				Score: score,
			}
		}
		// Sort descending to simulate real VectorStore output.
		sort.Slice(scoredDocs, func(i, j int) bool {
			return scoredDocs[i].Score > scoredDocs[j].Score
		})

		store := &mockVectorStore{searchResults: scoredDocs}
		embedder := &mockEmbedder{embedding: []float64{1.0}}
		reranker := &mockReranker{}
		retriever := NewRetriever(embedder, store,
			WithTopK(n),
			WithReranker(reranker),
		)

		docs, err := retriever.Retrieve(context.Background(), "test query")
		if err != nil {
			t.Fatalf("Retrieve failed: %v", err)
		}

		// Compute expected reranker output: the store docs (after threshold filter)
		// reversed by the mock reranker.
		var storeDocs []agent.Document
		for _, sd := range scoredDocs {
			storeDocs = append(storeDocs, sd.Document)
		}
		expectedDocs := make([]agent.Document, len(storeDocs))
		for i, doc := range storeDocs {
			expectedDocs[len(storeDocs)-1-i] = doc
		}

		// Assert Retrieve returns exactly the reranker's output in same order.
		if len(docs) != len(expectedDocs) {
			t.Fatalf("expected %d docs, got %d", len(expectedDocs), len(docs))
		}
		for i := range docs {
			if docs[i].Content != expectedDocs[i].Content {
				t.Fatalf("doc[%d].Content = %q, want %q", i, docs[i].Content, expectedDocs[i].Content)
			}
		}
	})
}

// --- Unit tests for Retriever error cases ---
// Requirements: 6.4, 6.5, 9.4

// errReranker is a mock reranker that always returns an error.
type errReranker struct {
	err error
}

func (m *errReranker) Rerank(_ context.Context, _ string, _ []agent.Document) ([]agent.Document, error) {
	return nil, m.err
}

func TestRetriever_EmptyQueryError(t *testing.T) {
	// Requirement 6.5: empty query returns descriptive error
	store := &mockVectorStore{}
	embedder := &mockEmbedder{embedding: []float64{1.0}}
	retriever := NewRetriever(embedder, store)

	_, err := retriever.Retrieve(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty query, got nil")
	}
	expected := "retrieve: query must not be empty"
	if err.Error() != expected {
		t.Fatalf("expected error %q, got %q", expected, err.Error())
	}
}

func TestRetriever_RerankerErrorWrapping(t *testing.T) {
	// Requirement 9.4: reranker error is wrapped with "reranker: " prefix
	store := &mockVectorStore{
		searchResults: []agent.ScoredDocument{
			{Document: agent.Document{Content: "doc1"}, Score: 0.9},
		},
	}
	embedder := &mockEmbedder{embedding: []float64{1.0}}
	innerErr := fmt.Errorf("connection refused")
	reranker := &errReranker{err: innerErr}
	retriever := NewRetriever(embedder, store, WithReranker(reranker))

	_, err := retriever.Retrieve(context.Background(), "test query")
	if err == nil {
		t.Fatal("expected error from reranker, got nil")
	}
	expected := "reranker: connection refused"
	if err.Error() != expected {
		t.Fatalf("expected error %q, got %q", expected, err.Error())
	}
}

func TestRetriever_ScoreThresholdExactMatch(t *testing.T) {
	// Requirement 6.4: document with score exactly equal to threshold is included
	threshold := 0.75
	store := &mockVectorStore{
		searchResults: []agent.ScoredDocument{
			{Document: agent.Document{Content: "above"}, Score: 0.9},
			{Document: agent.Document{Content: "exact"}, Score: threshold},
			{Document: agent.Document{Content: "below"}, Score: 0.5},
		},
	}
	embedder := &mockEmbedder{embedding: []float64{1.0}}
	retriever := NewRetriever(embedder, store,
		WithTopK(10),
		WithScoreThreshold(threshold),
	)

	docs, err := retriever.Retrieve(context.Background(), "test query")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should include "above" (0.9 >= 0.75) and "exact" (0.75 >= 0.75), exclude "below" (0.5 < 0.75)
	if len(docs) != 2 {
		t.Fatalf("expected 2 docs (above + exact threshold), got %d", len(docs))
	}
	if docs[0].Content != "above" {
		t.Fatalf("expected first doc to be %q, got %q", "above", docs[0].Content)
	}
	if docs[1].Content != "exact" {
		t.Fatalf("expected second doc to be %q, got %q", "exact", docs[1].Content)
	}
}

func TestRetriever_ScoreThresholdBelowExcluded(t *testing.T) {
	// Requirement 6.4: document with score just below threshold is excluded
	threshold := 0.75
	justBelow := 0.7499999999999999
	store := &mockVectorStore{
		searchResults: []agent.ScoredDocument{
			{Document: agent.Document{Content: "above"}, Score: 0.9},
			{Document: agent.Document{Content: "just-below"}, Score: justBelow},
		},
	}
	embedder := &mockEmbedder{embedding: []float64{1.0}}
	retriever := NewRetriever(embedder, store,
		WithTopK(10),
		WithScoreThreshold(threshold),
	)

	docs, err := retriever.Retrieve(context.Background(), "test query")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should include only "above" (0.9 >= 0.75), exclude "just-below" (< 0.75)
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc (only above threshold), got %d", len(docs))
	}
	if docs[0].Content != "above" {
		t.Fatalf("expected doc to be %q, got %q", "above", docs[0].Content)
	}
}

// recordingVectorStore records all documents passed to Add.
type recordingVectorStore struct {
	docs []agent.Document
}

func (r *recordingVectorStore) Add(_ context.Context, docs []agent.Document, _ [][]float64) error {
	r.docs = append(r.docs, docs...)
	return nil
}

func (r *recordingVectorStore) Search(_ context.Context, _ []float64, _ int) ([]agent.ScoredDocument, error) {
	return nil, nil
}

// Feature: rag, Property 5: Ingest stores the correct number of chunks
// **Validates: Requirements 5.2**
func TestIngest_ChunkCount(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		texts := rapid.SliceOfN(rapid.StringMatching("[a-z]{1,100}"), 1, 5).Draw(t, "texts")
		chunkSize := rapid.IntRange(1, 50).Draw(t, "chunkSize")
		chunkOverlap := rapid.IntRange(0, chunkSize-1).Draw(t, "chunkOverlap")

		embedder := &mockEmbedder{embedding: []float64{1.0}}
		store := &recordingVectorStore{}

		err := Ingest(
			context.Background(),
			store,
			embedder,
			texts,
			nil,
			WithChunkSize(chunkSize),
			WithChunkOverlap(chunkOverlap),
		)
		if err != nil {
			t.Fatalf("Ingest failed: %v", err)
		}

		// Compute expected total chunks.
		expectedTotal := 0
		for _, text := range texts {
			expectedTotal += len(SplitText(text, chunkSize, chunkOverlap))
		}

		if len(store.docs) != expectedTotal {
			t.Fatalf("stored %d docs, expected %d (sum of SplitText chunks across %d texts)",
				len(store.docs), expectedTotal, len(texts))
		}
	})
}

// Feature: rag, Property 6: Ingest propagates chunk_index and source_index metadata
// **Validates: Requirements 5.7**
func TestIngest_MetadataPropagation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate non-empty texts to ensure chunks are produced.
		texts := rapid.SliceOfN(rapid.StringMatching("[a-z]{1,100}"), 1, 5).Draw(t, "texts")

		embedder := &mockEmbedder{embedding: []float64{1.0}}
		store := &recordingVectorStore{}

		err := Ingest(
			context.Background(),
			store,
			embedder,
			texts,
			nil,
			WithChunkSize(20),
			WithChunkOverlap(0),
		)
		if err != nil {
			t.Fatalf("Ingest failed: %v", err)
		}

		// Assert every stored Document has non-empty "chunk_index" and "source_index" keys.
		for i, doc := range store.docs {
			ci, ok := doc.Metadata["chunk_index"]
			if !ok || ci == "" {
				t.Fatalf("doc[%d] missing or empty chunk_index metadata", i)
			}
			si, ok := doc.Metadata["source_index"]
			if !ok || si == "" {
				t.Fatalf("doc[%d] missing or empty source_index metadata", i)
			}
		}
	})
}

// --- Unit tests for Ingest error handling ---
// Requirements: 5.3, 5.4, 5.5

// failingEmbedder returns an error after a configured number of successful calls.
type failingEmbedder struct {
	succeedCount int // number of Embed calls that succeed before failing
	callCount    int
	err          error
}

func (f *failingEmbedder) Embed(_ context.Context, _ string) ([]float64, error) {
	if f.callCount >= f.succeedCount {
		return nil, f.err
	}
	f.callCount++
	return []float64{1.0}, nil
}

// failingVectorStore returns an error from Add.
type failingVectorStore struct {
	err error
}

func (f *failingVectorStore) Add(_ context.Context, _ []agent.Document, _ [][]float64) error {
	return f.err
}

func (f *failingVectorStore) Search(_ context.Context, _ []float64, _ int) ([]agent.ScoredDocument, error) {
	return nil, nil
}

func TestIngest_EmbedFailureWrapping(t *testing.T) {
	// Requirement 5.4: embed failure wraps error with "ingest: embed chunk %d: %w"
	innerErr := fmt.Errorf("model unavailable")
	embedder := &failingEmbedder{
		succeedCount: 0, // fail on the very first chunk
		err:          innerErr,
	}
	store := &recordingVectorStore{}

	err := Ingest(
		context.Background(),
		store,
		embedder,
		[]string{"some text to embed"},
		nil,
		WithChunkSize(5),
		WithChunkOverlap(0),
	)
	if err == nil {
		t.Fatal("expected error from Ingest, got nil")
	}
	if !strings.Contains(err.Error(), "ingest: embed chunk") {
		t.Fatalf("expected error to contain %q, got %q", "ingest: embed chunk", err.Error())
	}
	// The first chunk index is 0.
	if !strings.Contains(err.Error(), "embed chunk 0") {
		t.Fatalf("expected error to reference chunk index 0, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "model unavailable") {
		t.Fatalf("expected error to wrap inner error, got %q", err.Error())
	}
}

func TestIngest_EmbedFailureSecondChunk(t *testing.T) {
	// Requirement 5.4: embed failure on a later chunk includes the correct chunk index
	innerErr := fmt.Errorf("rate limited")
	embedder := &failingEmbedder{
		succeedCount: 2, // succeed on chunks 0 and 1, fail on chunk 2
		err:          innerErr,
	}
	store := &recordingVectorStore{}

	err := Ingest(
		context.Background(),
		store,
		embedder,
		[]string{"abcdefghijklmnop"}, // with chunkSize=5, overlap=0 → 4 chunks
		nil,
		WithChunkSize(5),
		WithChunkOverlap(0),
	)
	if err == nil {
		t.Fatal("expected error from Ingest, got nil")
	}
	if !strings.Contains(err.Error(), "embed chunk 2") {
		t.Fatalf("expected error to reference chunk index 2, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("expected error to wrap inner error, got %q", err.Error())
	}
}

func TestIngest_StoreFailureWrapping(t *testing.T) {
	// Requirement 5.5: store failure wraps error with "ingest: store.Add: %w"
	innerErr := fmt.Errorf("disk full")
	embedder := &mockEmbedder{embedding: []float64{1.0}}
	store := &failingVectorStore{err: innerErr}

	err := Ingest(
		context.Background(),
		store,
		embedder,
		[]string{"hello world"},
		nil,
	)
	if err == nil {
		t.Fatal("expected error from Ingest, got nil")
	}
	if !strings.Contains(err.Error(), "ingest: store.Add:") {
		t.Fatalf("expected error to contain %q, got %q", "ingest: store.Add:", err.Error())
	}
	if !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("expected error to wrap inner error, got %q", err.Error())
	}
}

func TestIngest_NilMetadata(t *testing.T) {
	// Requirement 5.3: nil metadata does not panic, documents get source_index/chunk_index
	embedder := &mockEmbedder{embedding: []float64{1.0}}
	store := &recordingVectorStore{}

	err := Ingest(
		context.Background(),
		store,
		embedder,
		[]string{"hello", "world"},
		nil, // nil metadata
		WithChunkSize(100),
		WithChunkOverlap(0),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(store.docs) != 2 {
		t.Fatalf("expected 2 docs, got %d", len(store.docs))
	}
	for i, doc := range store.docs {
		if doc.Metadata == nil {
			t.Fatalf("doc[%d] has nil metadata", i)
		}
		if _, ok := doc.Metadata["source_index"]; !ok {
			t.Fatalf("doc[%d] missing source_index", i)
		}
		if _, ok := doc.Metadata["chunk_index"]; !ok {
			t.Fatalf("doc[%d] missing chunk_index", i)
		}
	}
}

func TestIngest_ShortMetadata(t *testing.T) {
	// Requirement 5.3: metadata shorter than texts — texts without corresponding
	// metadata get source_index/chunk_index but no user-supplied keys.
	embedder := &mockEmbedder{embedding: []float64{1.0}}
	store := &recordingVectorStore{}

	meta := []map[string]string{
		{"author": "alice"},
		// no entry for second text
	}

	err := Ingest(
		context.Background(),
		store,
		embedder,
		[]string{"first text", "second text"},
		meta,
		WithChunkSize(100),
		WithChunkOverlap(0),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(store.docs) != 2 {
		t.Fatalf("expected 2 docs, got %d", len(store.docs))
	}

	// First doc should have author metadata merged in.
	if store.docs[0].Metadata["author"] != "alice" {
		t.Fatalf("doc[0] expected author=alice, got %q", store.docs[0].Metadata["author"])
	}
	if store.docs[0].Metadata["source_index"] != "0" {
		t.Fatalf("doc[0] expected source_index=0, got %q", store.docs[0].Metadata["source_index"])
	}

	// Second doc should have source_index/chunk_index but no author key.
	if _, ok := store.docs[1].Metadata["author"]; ok {
		t.Fatalf("doc[1] should not have author metadata, got %q", store.docs[1].Metadata["author"])
	}
	if store.docs[1].Metadata["source_index"] != "1" {
		t.Fatalf("doc[1] expected source_index=1, got %q", store.docs[1].Metadata["source_index"])
	}
	if store.docs[1].Metadata["chunk_index"] != "0" {
		t.Fatalf("doc[1] expected chunk_index=0, got %q", store.docs[1].Metadata["chunk_index"])
	}
}
