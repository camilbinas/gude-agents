package postgres

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/camilbinas/gude-agents/agent/memory"
	"github.com/jackc/pgx/v5/pgxpool"
	"pgregory.net/rapid"
)

// ---------------------------------------------------------------------------
// Test Infrastructure
// ---------------------------------------------------------------------------

// skipIfNoPostgres checks the POSTGRES_URL environment variable and skips the
// test if it is not set. Returns the URL when available.
func skipIfNoPostgres(t *testing.T) string {
	t.Helper()
	url := os.Getenv("POSTGRES_URL")
	if url == "" {
		t.Skip("POSTGRES_URL not set, skipping integration test")
	}
	return url
}

// hashEmbedder is a deterministic mock Embedder that produces consistent
// embedding vectors from strings using FNV hashing. The same input always
// yields the same vector, making similarity results predictable in tests.
type hashEmbedder struct {
	dim int
}

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

// failingEmbedder is a mock Embedder that always returns an error.
type failingEmbedder struct {
	err error
}

func (e *failingEmbedder) Embed(_ context.Context, _ string) ([]float64, error) {
	return nil, e.err
}

// ---------------------------------------------------------------------------
// Generators
// ---------------------------------------------------------------------------

// genIdentifier generates a random alphanumeric identifier (1-50 chars).
func genIdentifier(t *rapid.T) string {
	return rapid.StringMatching("[a-zA-Z0-9]{1,50}").Draw(t, "identifier")
}

// genFact generates a random fact string (1-200 chars).
func genFact(t *rapid.T) string {
	return rapid.StringMatching("[a-zA-Z0-9 ]{1,200}").Draw(t, "fact")
}

// genMetadata generates a randomly nil or non-nil metadata map with 0-5 entries.
func genMetadata(t *rapid.T) map[string]string {
	if rapid.Bool().Draw(t, "metadata_present") {
		n := rapid.IntRange(0, 5).Draw(t, "metadata_len")
		if n == 0 {
			return map[string]string{}
		}
		m := make(map[string]string, n)
		for i := 0; i < n; i++ {
			key := rapid.StringMatching("[a-z]{1,10}").Draw(t, "metadata_key")
			val := rapid.StringMatching("[a-zA-Z0-9]{1,20}").Draw(t, "metadata_val")
			_ = i
			m[key] = val
		}
		return m
	}
	return nil
}

// newTestStore creates a Store backed by a real PostgreSQL instance with a
// fresh table. Each test gets its own table name derived from t.Name() to
// avoid interference between tests.
func newTestStore(t *testing.T, pgURL string) *Store {
	t.Helper()

	pool, err := pgxpool.New(context.Background(), pgURL)
	if err != nil {
		t.Fatalf("newTestStore: pgxpool.New: %v", err)
	}

	// Sanitize t.Name(): table names cannot contain '/' or special chars.
	sanitized := strings.ReplaceAll(t.Name(), "/", "_")
	sanitized = strings.ReplaceAll(sanitized, " ", "_")
	// Lowercase and truncate to a reasonable length for PostgreSQL.
	sanitized = strings.ToLower(sanitized)
	if len(sanitized) > 60 {
		sanitized = sanitized[:60]
	}

	store, err := New(
		pool,
		&hashEmbedder{dim: 32},
		32,
		WithTableName(sanitized),
		WithAutoMigrate(),
		WithDropExisting(),
	)
	if err != nil {
		pool.Close()
		t.Fatalf("newTestStore: New: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// ---------------------------------------------------------------------------
// Property 1: Remember validation
// Feature: postgres-memory, Property 1: Remember validation
// ---------------------------------------------------------------------------

// TestProperty_RememberValidation verifies that Remember returns an error when
// the identifier or fact is empty, and succeeds for valid non-empty inputs.
//
// **Validates: Requirements 4.3, 4.4**
func TestProperty_RememberValidation(t *testing.T) {
	pgURL := skipIfNoPostgres(t)
	store := newTestStore(t, pgURL)
	ctx := context.Background()

	rapid.Check(t, func(rt *rapid.T) {
		id := genIdentifier(rt)
		fact := genFact(rt)
		meta := genMetadata(rt)

		// Empty identifier → error.
		err := store.Remember(ctx, "", fact, meta)
		if err == nil {
			rt.Fatal("Remember with empty identifier should return error")
		}
		if !strings.Contains(err.Error(), "identifier must not be empty") {
			rt.Fatalf("expected error containing 'identifier must not be empty', got: %v", err)
		}

		// Empty fact → error.
		err = store.Remember(ctx, id, "", meta)
		if err == nil {
			rt.Fatal("Remember with empty fact should return error")
		}
		if !strings.Contains(err.Error(), "fact must not be empty") {
			rt.Fatalf("expected error containing 'fact must not be empty', got: %v", err)
		}

		// Valid inputs → no error.
		err = store.Remember(ctx, id, fact, meta)
		if err != nil {
			rt.Fatalf("Remember with valid inputs should not return error, got: %v", err)
		}
	})
}

// ---------------------------------------------------------------------------
// Property 2: Recall validation
// Feature: postgres-memory, Property 2: Recall validation
// ---------------------------------------------------------------------------

// TestProperty_RecallValidation verifies that Recall returns an error when
// the identifier is empty or the limit is less than 1.
//
// **Validates: Requirements 5.4, 5.5**
func TestProperty_RecallValidation(t *testing.T) {
	pgURL := skipIfNoPostgres(t)
	store := newTestStore(t, pgURL)
	ctx := context.Background()

	rapid.Check(t, func(rt *rapid.T) {
		id := genIdentifier(rt)
		query := genFact(rt)
		limit := rapid.IntRange(1, 100).Draw(rt, "limit")

		// Empty identifier → error.
		_, err := store.Recall(ctx, "", query, limit)
		if err == nil {
			rt.Fatal("Recall with empty identifier should return error")
		}
		if !strings.Contains(err.Error(), "identifier must not be empty") {
			rt.Fatalf("expected error containing 'identifier must not be empty', got: %v", err)
		}

		// Limit < 1 → error.
		badLimit := rapid.IntRange(-100, 0).Draw(rt, "badLimit")
		_, err = store.Recall(ctx, id, query, badLimit)
		if err == nil {
			rt.Fatalf("Recall with limit=%d should return error", badLimit)
		}
		if !strings.Contains(err.Error(), "limit must be at least 1") {
			rt.Fatalf("expected error containing 'limit must be at least 1', got: %v", err)
		}
	})
}

// ---------------------------------------------------------------------------
// Property 3: Remember-then-Recall round-trip
// Feature: postgres-memory, Property 3: Remember-then-Recall round-trip
// ---------------------------------------------------------------------------

// TestProperty_RememberThenRecall verifies that after storing a fact via
// Remember, calling Recall with the same fact as query returns a non-empty
// slice containing an Entry whose Fact field matches the stored fact.
//
// **Validates: Requirements 4.1, 4.2, 5.1, 5.2**
func TestProperty_RememberThenRecall(t *testing.T) {
	pgURL := skipIfNoPostgres(t)
	store := newTestStore(t, pgURL)
	ctx := context.Background()

	rapid.Check(t, func(rt *rapid.T) {
		id := genIdentifier(rt)
		fact := genFact(rt)
		meta := genMetadata(rt)

		err := store.Remember(ctx, id, fact, meta)
		if err != nil {
			rt.Fatalf("Remember failed: %v", err)
		}

		results, err := store.Recall(ctx, id, fact, 10)
		if err != nil {
			rt.Fatalf("Recall failed: %v", err)
		}

		if len(results) == 0 {
			rt.Fatal("Recall returned empty slice after Remember")
		}

		found := false
		for _, ep := range results {
			if ep.Fact == fact {
				found = true
				break
			}
		}
		if !found {
			rt.Fatalf("Recall results do not contain the stored fact %q; got %+v", fact, results)
		}
	})
}

// ---------------------------------------------------------------------------
// Property 4: Recall results ordered by descending score
// Feature: postgres-memory, Property 4: Recall results ordered by descending score
// ---------------------------------------------------------------------------

// TestProperty_RecallOrderedByScore verifies that for any identifier with
// multiple stored entries, the slice returned by Recall is ordered by
// descending Score.
//
// **Validates: Requirements 5.7**
func TestProperty_RecallOrderedByScore(t *testing.T) {
	pgURL := skipIfNoPostgres(t)
	store := newTestStore(t, pgURL)
	ctx := context.Background()

	rapid.Check(t, func(rt *rapid.T) {
		id := genIdentifier(rt)
		query := genFact(rt)

		// Store 3-10 distinct facts (append index suffix for uniqueness).
		n := rapid.IntRange(3, 10).Draw(rt, "num_facts")
		for i := 0; i < n; i++ {
			fact := genFact(rt) + fmt.Sprintf("_%d", i)
			err := store.Remember(ctx, id, fact, nil)
			if err != nil {
				rt.Fatalf("Remember[%d] failed: %v", i, err)
			}
		}

		results, err := store.Recall(ctx, id, query, n)
		if err != nil {
			rt.Fatalf("Recall failed: %v", err)
		}

		if len(results) == 0 {
			rt.Fatal("Recall returned empty slice after storing entries")
		}

		// Verify descending score order.
		for i := 1; i < len(results); i++ {
			if results[i-1].Score < results[i].Score {
				rt.Fatalf("results not sorted by descending score: results[%d].Score=%v < results[%d].Score=%v",
					i-1, results[i-1].Score, i, results[i].Score)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Property 5: Recall limit clamping
// Feature: postgres-memory, Property 5: Recall limit clamping
// ---------------------------------------------------------------------------

// TestProperty_RecallLimitClamping verifies that for any identifier with N
// stored entries and any positive limit L, Recall returns at most min(L, N)
// entries.
//
// **Validates: Requirements 5.7**
func TestProperty_RecallLimitClamping(t *testing.T) {
	pgURL := skipIfNoPostgres(t)
	store := newTestStore(t, pgURL)
	ctx := context.Background()

	rapid.Check(t, func(rt *rapid.T) {
		id := genIdentifier(rt)
		query := genFact(rt)

		// Store N facts (2-10).
		n := rapid.IntRange(2, 10).Draw(rt, "num_facts")
		for i := 0; i < n; i++ {
			fact := genFact(rt) + fmt.Sprintf("_%d", i)
			err := store.Remember(ctx, id, fact, nil)
			if err != nil {
				rt.Fatalf("Remember[%d] failed: %v", i, err)
			}
		}

		// Recall with limit L (1-15).
		limit := rapid.IntRange(1, 15).Draw(rt, "limit")
		results, err := store.Recall(ctx, id, query, limit)
		if err != nil {
			rt.Fatalf("Recall failed: %v", err)
		}

		// Expected: len(results) <= min(L, N).
		maxExpected := limit
		if n < maxExpected {
			maxExpected = n
		}
		if len(results) > maxExpected {
			rt.Fatalf("len(results) = %d, want <= min(%d, %d) = %d", len(results), limit, n, maxExpected)
		}
	})
}

// ---------------------------------------------------------------------------
// Property 6: Recall empty for unknown identifier
// Feature: postgres-memory, Property 6: Recall empty for unknown identifier
// ---------------------------------------------------------------------------

// TestProperty_RecallEmptyForUnknownIdentifier verifies that Recall returns
// a non-nil, length-0 slice when called with an identifier that has no stored
// entries.
//
// **Validates: Requirements 5.6**
func TestProperty_RecallEmptyForUnknownIdentifier(t *testing.T) {
	pgURL := skipIfNoPostgres(t)
	store := newTestStore(t, pgURL)
	ctx := context.Background()

	rapid.Check(t, func(rt *rapid.T) {
		id := genIdentifier(rt)
		query := genFact(rt)

		results, err := store.Recall(ctx, id, query, 10)
		if err != nil {
			rt.Fatalf("Recall failed: %v", err)
		}

		if results == nil {
			rt.Fatal("Recall returned nil, want empty non-nil slice")
		}
		if len(results) != 0 {
			rt.Fatalf("Recall returned %d results for unknown identifier, want 0", len(results))
		}
	})
}

// ---------------------------------------------------------------------------
// Property 7: Identifier isolation
// Feature: postgres-memory, Property 7: Identifier isolation
// ---------------------------------------------------------------------------

// TestProperty_IdentifierIsolation verifies that entries stored under
// identifier A are never returned by Recall for identifier B (where A ≠ B).
//
// **Validates: Requirements 4.2, 5.2**
func TestProperty_IdentifierIsolation(t *testing.T) {
	pgURL := skipIfNoPostgres(t)
	store := newTestStore(t, pgURL)
	ctx := context.Background()

	rapid.Check(t, func(rt *rapid.T) {
		idA := genIdentifier(rt)
		idB := genIdentifier(rt)
		// Ensure A ≠ B.
		if idA == idB {
			idB = idB + "x"
		}

		fact := genFact(rt)

		// Store fact under id A.
		err := store.Remember(ctx, idA, fact, nil)
		if err != nil {
			rt.Fatalf("Remember failed: %v", err)
		}

		// Recall under id B.
		results, err := store.Recall(ctx, idB, fact, 10)
		if err != nil {
			rt.Fatalf("Recall failed: %v", err)
		}

		// No results from A should appear in B's recall.
		for _, ep := range results {
			if ep.Fact == fact {
				rt.Fatalf("fact %q stored under %q was returned for %q", fact, idA, idB)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Property 8: Metadata round-trip
// Feature: postgres-memory, Property 8: Metadata round-trip
// ---------------------------------------------------------------------------

// TestProperty_MetadataRoundTrip verifies that metadata stored via Remember
// is faithfully returned by Recall after serialization/deserialization,
// including the nil metadata case.
//
// **Validates: Requirements 5.3**
func TestProperty_MetadataRoundTrip(t *testing.T) {
	pgURL := skipIfNoPostgres(t)
	store := newTestStore(t, pgURL)
	ctx := context.Background()

	rapid.Check(t, func(rt *rapid.T) {
		id := genIdentifier(rt)
		fact := genFact(rt)
		meta := genMetadata(rt)

		err := store.Remember(ctx, id, fact, meta)
		if err != nil {
			rt.Fatalf("Remember failed: %v", err)
		}

		results, err := store.Recall(ctx, id, fact, 10)
		if err != nil {
			rt.Fatalf("Recall failed: %v", err)
		}

		if len(results) == 0 {
			rt.Fatal("Recall returned empty slice after Remember")
		}

		// Find the entry with the matching fact.
		var found *memory.Entry
		for i := range results {
			if results[i].Fact == fact {
				found = &results[i]
				break
			}
		}
		if found == nil {
			rt.Fatalf("Recall results do not contain fact %q", fact)
		}

		// Verify metadata matches. The adapter filters out internal keys
		// (_scope_id, created_at), so we only check user-provided metadata.
		if meta == nil {
			// nil metadata: the adapter may return an empty map since it
			// always creates a metadata map for created_at. After filtering
			// internal keys, the result should be an empty map.
			if len(found.Metadata) != 0 {
				rt.Fatalf("expected empty metadata for nil input, got %v", found.Metadata)
			}
		} else {
			if found.Metadata == nil {
				rt.Fatalf("expected metadata %v, got nil", meta)
			}
			for k, v := range meta {
				if got, ok := found.Metadata[k]; !ok || got != v {
					rt.Fatalf("metadata[%q]: want %q, got %q (ok=%v)", k, v, got, ok)
				}
			}
		}

		// Verify internal keys are NOT exposed.
		if _, ok := found.Metadata["_scope_id"]; ok {
			rt.Fatal("internal key _scope_id should not be in returned metadata")
		}
		if _, ok := found.Metadata["created_at"]; ok {
			rt.Fatal("internal key created_at should not be in returned metadata")
		}
	})
}

// ---------------------------------------------------------------------------
// Property 9: Embedder error propagation
// Feature: postgres-memory, Property 9: Embedder error propagation
// ---------------------------------------------------------------------------

// TestProperty_EmbedderErrorPropagation verifies that when the embedder
// returns an error, both Remember and Recall propagate the error.
//
// **Validates: Requirements 4.5, 8.2**
func TestProperty_EmbedderErrorPropagation(t *testing.T) {
	pgURL := skipIfNoPostgres(t)

	rapid.Check(t, func(rt *rapid.T) {
		errMsg := rapid.StringMatching("[a-zA-Z0-9 ]{1,50}").Draw(rt, "errMsg")

		pool, err := pgxpool.New(context.Background(), pgURL)
		if err != nil {
			rt.Fatalf("pgxpool.New: %v", err)
		}

		// Sanitize t.Name(): table names cannot contain '/' or special chars.
		sanitized := strings.ReplaceAll(t.Name(), "/", "_")
		sanitized = strings.ReplaceAll(sanitized, " ", "_")
		sanitized = strings.ToLower(sanitized)
		if len(sanitized) > 60 {
			sanitized = sanitized[:60]
		}

		// Create a Store with a failing embedder via the public constructor.
		store, err := New(
			pool,
			&failingEmbedder{err: fmt.Errorf("%s", errMsg)},
			32,
			WithTableName("errprop_"+sanitized),
			WithAutoMigrate(),
			WithDropExisting(),
		)
		if err != nil {
			pool.Close()
			rt.Fatalf("New failed: %v", err)
		}
		defer store.Close()

		ctx := context.Background()
		id := genIdentifier(rt)
		fact := genFact(rt)

		// Remember should propagate the embedder error.
		err = store.Remember(ctx, id, fact, nil)
		if err == nil {
			rt.Fatal("Remember should return error when embedder fails")
		}
		if !strings.Contains(err.Error(), errMsg) {
			rt.Fatalf("Remember error should contain %q, got: %v", errMsg, err)
		}

		// Recall should propagate the embedder error.
		_, err = store.Recall(ctx, id, fact, 5)
		if err == nil {
			rt.Fatal("Recall should return error when embedder fails")
		}
		if !strings.Contains(err.Error(), errMsg) {
			rt.Fatalf("Recall error should contain %q, got: %v", errMsg, err)
		}
	})
}
