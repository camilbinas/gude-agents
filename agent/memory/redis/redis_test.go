package redis

import (
	"strings"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/memory"
)

// ---------------------------------------------------------------------------
// Unit Tests (no Redis required)
// ---------------------------------------------------------------------------

// TestNew_NilEmbedder verifies that New returns an error when embedder is nil.
func TestNew_NilEmbedder(t *testing.T) {
	_, err := New(Options{}, nil, 128)
	if err == nil {
		t.Fatal("expected error for nil embedder, got nil")
	}
	if !strings.Contains(err.Error(), "embedder is required") {
		t.Fatalf("expected error containing 'embedder is required', got: %v", err)
	}
}

// TestNew_InvalidDim verifies that New returns an error when dim < 1.
func TestNew_InvalidDim(t *testing.T) {
	emb := &hashEmbedder{dim: 8}

	// dim = 0
	_, err := New(Options{}, emb, 0)
	if err == nil {
		t.Fatal("expected error for dim=0, got nil")
	}
	if !strings.Contains(err.Error(), "dim must be at least 1") {
		t.Fatalf("expected error containing 'dim must be at least 1', got: %v", err)
	}

	// dim = -1
	_, err = New(Options{}, emb, -1)
	if err == nil {
		t.Fatal("expected error for dim=-1, got nil")
	}
	if !strings.Contains(err.Error(), "dim must be at least 1") {
		t.Fatalf("expected error containing 'dim must be at least 1', got: %v", err)
	}
}

// TestInterfaceAssertion verifies at runtime that *Store satisfies both
// memory.MemoryStore and memory.Memory interfaces.
func TestInterfaceAssertion(t *testing.T) {
	var _ memory.Memory = (*Store)(nil)
	var _ memory.MemoryStore = (*Store)(nil)
}

// ---------------------------------------------------------------------------
// escapeTag unit tests (no Redis required)
// ---------------------------------------------------------------------------

// TestEscapeTag_NoSpecialChars verifies that plain alphanumeric strings pass
// through escapeTag unchanged.
func TestEscapeTag_NoSpecialChars(t *testing.T) {
	input := "user123"
	got := escapeTag(input)
	if got != input {
		t.Fatalf("escapeTag(%q) = %q, want %q", input, got, input)
	}
}

// TestEscapeTag_SpecialCharacters verifies that RediSearch TAG special
// characters are escaped with a backslash.
func TestEscapeTag_SpecialCharacters(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"comma", "a,b", `a\,b`},
		{"dot", "a.b", `a\.b`},
		{"angle brackets", "a<b>c", `a\<b\>c`},
		{"curly braces", "a{b}c", `a\{b\}c`},
		{"square brackets", "a[b]c", `a\[b\]c`},
		{"double quote", `a"b`, `a\"b`},
		{"single quote", "a'b", `a\'b`},
		{"colon", "a:b", `a\:b`},
		{"semicolon", "a;b", `a\;b`},
		{"exclamation", "a!b", `a\!b`},
		{"at sign", "a@b", `a\@b`},
		{"hash", "a#b", `a\#b`},
		{"dollar", "a$b", `a\$b`},
		{"percent", "a%b", `a\%b`},
		{"caret", "a^b", `a\^b`},
		{"ampersand", "a&b", `a\&b`},
		{"asterisk", "a*b", `a\*b`},
		{"parens", "a(b)c", `a\(b\)c`},
		{"dash", "a-b", `a\-b`},
		{"plus", "a+b", `a\+b`},
		{"equals", "a=b", `a\=b`},
		{"tilde", "a~b", `a\~b`},
		{"slash", "a/b", `a\/b`},
		{"space", "a b", `a\ b`},
		{"multiple specials", "user@org.com", `user\@org\.com`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := escapeTag(tc.input)
			if got != tc.want {
				t.Fatalf("escapeTag(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestEscapeTag_EmptyString verifies that escapeTag handles an empty string.
func TestEscapeTag_EmptyString(t *testing.T) {
	got := escapeTag("")
	if got != "" {
		t.Fatalf("escapeTag(%q) = %q, want %q", "", got, "")
	}
}

// ---------------------------------------------------------------------------
// Integration Tests (require Redis Stack)
// ---------------------------------------------------------------------------

// TestRemember_EmptyIdentifier verifies that Remember returns an error for
// an empty identifier.
func TestRemember_EmptyIdentifier(t *testing.T) {
	addr := skipIfNoRedis(t)
	store := newTestStore(t, addr)

	err := store.Remember(t.Context(), "", "some fact", nil)
	if err == nil {
		t.Fatal("expected error for empty identifier, got nil")
	}
	if !strings.Contains(err.Error(), "identifier must not be empty") {
		t.Fatalf("expected error containing 'identifier must not be empty', got: %v", err)
	}
}

// TestRemember_EmptyFact verifies that Remember returns an error for
// an empty fact.
func TestRemember_EmptyFact(t *testing.T) {
	addr := skipIfNoRedis(t)
	store := newTestStore(t, addr)

	err := store.Remember(t.Context(), "user-1", "", nil)
	if err == nil {
		t.Fatal("expected error for empty fact, got nil")
	}
	if !strings.Contains(err.Error(), "fact must not be empty") {
		t.Fatalf("expected error containing 'fact must not be empty', got: %v", err)
	}
}

// TestRecall_EmptyIdentifier verifies that Recall returns an error for
// an empty identifier.
func TestRecall_EmptyIdentifier(t *testing.T) {
	addr := skipIfNoRedis(t)
	store := newTestStore(t, addr)

	_, err := store.Recall(t.Context(), "", "query", 5)
	if err == nil {
		t.Fatal("expected error for empty identifier, got nil")
	}
	if !strings.Contains(err.Error(), "identifier must not be empty") {
		t.Fatalf("expected error containing 'identifier must not be empty', got: %v", err)
	}
}

// TestRecall_InvalidLimit verifies that Recall returns an error for limit < 1.
func TestRecall_InvalidLimit(t *testing.T) {
	addr := skipIfNoRedis(t)
	store := newTestStore(t, addr)

	// limit = 0
	_, err := store.Recall(t.Context(), "user-1", "query", 0)
	if err == nil {
		t.Fatal("expected error for limit=0, got nil")
	}
	if !strings.Contains(err.Error(), "limit must be at least 1") {
		t.Fatalf("expected error containing 'limit must be at least 1', got: %v", err)
	}

	// limit = -1
	_, err = store.Recall(t.Context(), "user-1", "query", -1)
	if err == nil {
		t.Fatal("expected error for limit=-1, got nil")
	}
	if !strings.Contains(err.Error(), "limit must be at least 1") {
		t.Fatalf("expected error containing 'limit must be at least 1', got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// MemoryStore.Add / MemoryStore.Search integration tests
// ---------------------------------------------------------------------------

// TestAdd_EmptyIdentifier verifies that Add returns an error for an empty
// identifier.
func TestAdd_EmptyIdentifier(t *testing.T) {
	addr := skipIfNoRedis(t)
	store := newTestStore(t, addr)

	docs := []agent.Document{{Content: "hello"}}
	embs := [][]float64{make([]float64, 32)}

	err := store.Add(t.Context(), "", docs, embs)
	if err == nil {
		t.Fatal("expected error for empty identifier, got nil")
	}
	if !strings.Contains(err.Error(), "identifier must not be empty") {
		t.Fatalf("expected error containing 'identifier must not be empty', got: %v", err)
	}
}

// TestAdd_DocEmbeddingMismatch verifies that Add returns an error when the
// docs and embeddings slices have different lengths.
func TestAdd_DocEmbeddingMismatch(t *testing.T) {
	addr := skipIfNoRedis(t)
	store := newTestStore(t, addr)

	docs := []agent.Document{{Content: "a"}, {Content: "b"}}
	embs := [][]float64{make([]float64, 32)}

	err := store.Add(t.Context(), "user-1", docs, embs)
	if err == nil {
		t.Fatal("expected error for docs/embeddings mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("expected error containing 'mismatch', got: %v", err)
	}
}

// TestSearch_EmptyIdentifier verifies that Search returns an error for an
// empty identifier.
func TestSearch_EmptyIdentifier(t *testing.T) {
	addr := skipIfNoRedis(t)
	store := newTestStore(t, addr)

	_, err := store.Search(t.Context(), "", make([]float64, 32), 5)
	if err == nil {
		t.Fatal("expected error for empty identifier, got nil")
	}
	if !strings.Contains(err.Error(), "identifier must not be empty") {
		t.Fatalf("expected error containing 'identifier must not be empty', got: %v", err)
	}
}

// TestSearch_InvalidTopK verifies that Search returns an error for topK < 1.
func TestSearch_InvalidTopK(t *testing.T) {
	addr := skipIfNoRedis(t)
	store := newTestStore(t, addr)

	_, err := store.Search(t.Context(), "user-1", make([]float64, 32), 0)
	if err == nil {
		t.Fatal("expected error for topK=0, got nil")
	}
	if !strings.Contains(err.Error(), "topK must be >= 1") {
		t.Fatalf("expected error containing 'topK must be >= 1', got: %v", err)
	}
}

// TestAddSearch_BasicFlow verifies the basic Add/Search round-trip: documents
// stored via Add are returned by Search with the same identifier.
func TestAddSearch_BasicFlow(t *testing.T) {
	addr := skipIfNoRedis(t)
	store := newTestStore(t, addr)
	ctx := t.Context()

	emb := &hashEmbedder{dim: 32}

	// Embed and store a document.
	vec, err := emb.Embed(ctx, "the sky is blue")
	if err != nil {
		t.Fatalf("embed: %v", err)
	}

	docs := []agent.Document{{Content: "the sky is blue", Metadata: map[string]string{"source": "test"}}}
	err = store.Add(ctx, "user-1", docs, [][]float64{vec})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Search with the same embedding.
	results, err := store.Search(ctx, "user-1", vec, 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("Search returned empty results after Add")
	}

	found := false
	for _, r := range results {
		if r.Document.Content == "the sky is blue" {
			found = true
			if r.Document.Metadata["source"] != "test" {
				t.Fatalf("metadata mismatch: got %v", r.Document.Metadata)
			}
			break
		}
	}
	if !found {
		t.Fatalf("Search results do not contain the stored document; got %+v", results)
	}
}

// TestAddSearch_TAGFiltering verifies that documents stored under different
// identifiers are isolated: Search for identifier A does not return documents
// stored under identifier B.
func TestAddSearch_TAGFiltering(t *testing.T) {
	addr := skipIfNoRedis(t)
	store := newTestStore(t, addr)
	ctx := t.Context()

	emb := &hashEmbedder{dim: 32}

	// Store a document under "alice".
	vecAlice, _ := emb.Embed(ctx, "alice fact")
	docsAlice := []agent.Document{{Content: "alice fact"}}
	if err := store.Add(ctx, "alice", docsAlice, [][]float64{vecAlice}); err != nil {
		t.Fatalf("Add alice: %v", err)
	}

	// Store a document under "bob".
	vecBob, _ := emb.Embed(ctx, "bob fact")
	docsBob := []agent.Document{{Content: "bob fact"}}
	if err := store.Add(ctx, "bob", docsBob, [][]float64{vecBob}); err != nil {
		t.Fatalf("Add bob: %v", err)
	}

	// Search under "alice" — should only find alice's document.
	results, err := store.Search(ctx, "alice", vecAlice, 10)
	if err != nil {
		t.Fatalf("Search alice: %v", err)
	}
	for _, r := range results {
		if r.Document.Content == "bob fact" {
			t.Fatal("Search for alice returned bob's document — TAG filtering broken")
		}
	}

	// Search under "bob" — should only find bob's document.
	results, err = store.Search(ctx, "bob", vecBob, 10)
	if err != nil {
		t.Fatalf("Search bob: %v", err)
	}
	for _, r := range results {
		if r.Document.Content == "alice fact" {
			t.Fatal("Search for bob returned alice's document — TAG filtering broken")
		}
	}
}

// TestAddSearch_SpecialCharIdentifier verifies that identifiers containing
// RediSearch TAG special characters (e.g., @, ., :) are properly escaped
// and do not break the TAG filter query.
func TestAddSearch_SpecialCharIdentifier(t *testing.T) {
	addr := skipIfNoRedis(t)
	store := newTestStore(t, addr)
	ctx := t.Context()

	emb := &hashEmbedder{dim: 32}

	// Use an identifier with special characters that need escaping.
	specialID := "user@org.com:tenant/123"

	vec, _ := emb.Embed(ctx, "special char fact")
	docs := []agent.Document{{Content: "special char fact"}}
	if err := store.Add(ctx, specialID, docs, [][]float64{vec}); err != nil {
		t.Fatalf("Add with special identifier: %v", err)
	}

	results, err := store.Search(ctx, specialID, vec, 5)
	if err != nil {
		t.Fatalf("Search with special identifier: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("Search returned empty results for identifier with special characters")
	}

	found := false
	for _, r := range results {
		if r.Document.Content == "special char fact" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Search did not return the stored document for special char identifier; got %+v", results)
	}
}

// TestSearch_EmptyResults verifies that Search returns a non-nil empty slice
// when no documents match the identifier.
func TestSearch_EmptyResults(t *testing.T) {
	addr := skipIfNoRedis(t)
	store := newTestStore(t, addr)
	ctx := t.Context()

	results, err := store.Search(ctx, "nonexistent", make([]float64, 32), 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if results == nil {
		t.Fatal("Search returned nil, want non-nil empty slice")
	}
	if len(results) != 0 {
		t.Fatalf("Search returned %d results for nonexistent identifier, want 0", len(results))
	}
}

// ---------------------------------------------------------------------------
// Index creation verification
// ---------------------------------------------------------------------------

// TestIndexCreation verifies that New creates a RediSearch index with the
// expected schema including the identifier TAG field. It uses FT.INFO to
// inspect the created index.
func TestIndexCreation(t *testing.T) {
	addr := skipIfNoRedis(t)
	store := newTestStore(t, addr)
	ctx := t.Context()

	// Use FT.INFO to inspect the index schema.
	res, err := store.client.Do(ctx, "FT.INFO", store.indexName).Result()
	if err != nil {
		t.Fatalf("FT.INFO: %v", err)
	}

	// FT.INFO returns different formats for RESP2 vs RESP3. We check that
	// the response contains the expected field names by converting to string.
	info := flattenToString(res)

	// Verify the index contains the expected fields.
	expectedFields := []string{"content", "metadata", "identifier", "embedding"}
	for _, field := range expectedFields {
		if !strings.Contains(info, field) {
			t.Errorf("FT.INFO output missing field %q", field)
		}
	}

	// Verify the identifier field is a TAG type.
	if !strings.Contains(info, "TAG") {
		t.Error("FT.INFO output missing TAG type for identifier field")
	}

	// Verify the embedding field is a VECTOR type.
	if !strings.Contains(info, "VECTOR") {
		t.Error("FT.INFO output missing VECTOR type for embedding field")
	}
}

// ---------------------------------------------------------------------------
// Remember / Recall integration tests (via internal Adapter)
// ---------------------------------------------------------------------------

// TestRememberRecall_BasicFlow verifies the Remember/Recall round-trip
// through the internal Adapter.
func TestRememberRecall_BasicFlow(t *testing.T) {
	addr := skipIfNoRedis(t)
	store := newTestStore(t, addr)
	ctx := t.Context()

	err := store.Remember(ctx, "user-1", "Go is a compiled language", nil)
	if err != nil {
		t.Fatalf("Remember: %v", err)
	}

	results, err := store.Recall(ctx, "user-1", "Go is a compiled language", 5)
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("Recall returned empty results after Remember")
	}

	found := false
	for _, e := range results {
		if e.Fact == "Go is a compiled language" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Recall results do not contain the stored fact; got %+v", results)
	}
}

// TestRememberRecall_IdentifierIsolation verifies that facts stored under
// one identifier are not returned when recalling under a different identifier.
func TestRememberRecall_IdentifierIsolation(t *testing.T) {
	addr := skipIfNoRedis(t)
	store := newTestStore(t, addr)
	ctx := t.Context()

	err := store.Remember(ctx, "alice", "alice secret", nil)
	if err != nil {
		t.Fatalf("Remember alice: %v", err)
	}

	results, err := store.Recall(ctx, "bob", "alice secret", 10)
	if err != nil {
		t.Fatalf("Recall bob: %v", err)
	}

	for _, e := range results {
		if e.Fact == "alice secret" {
			t.Fatal("Recall for bob returned alice's fact — identifier isolation broken")
		}
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// flattenToString recursively converts a Redis response (which may be nested
// slices, maps, or scalars) into a single string for substring matching.
func flattenToString(v interface{}) string {
	switch val := v.(type) {
	case string:
		return val
	case []interface{}:
		var parts []string
		for _, item := range val {
			parts = append(parts, flattenToString(item))
		}
		return strings.Join(parts, " ")
	case map[interface{}]interface{}:
		var parts []string
		for k, v := range val {
			parts = append(parts, flattenToString(k)+" "+flattenToString(v))
		}
		return strings.Join(parts, " ")
	default:
		return ""
	}
}
