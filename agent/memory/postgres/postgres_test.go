package postgres

import (
	"context"
	"strings"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/memory"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------------------------------------------------------------------------
// Unit Tests (no PostgreSQL required)
// ---------------------------------------------------------------------------

// TestNew_NilPool verifies that New returns an error when pool is nil.
func TestNew_NilPool(t *testing.T) {
	emb := &hashEmbedder{dim: 8}

	_, err := New(nil, emb, 8)
	if err == nil {
		t.Fatal("expected error for nil pool, got nil")
	}
	if !strings.Contains(err.Error(), "pool is required") {
		t.Fatalf("expected error containing 'pool is required', got: %v", err)
	}
}

// TestNew_NilEmbedder verifies that New returns an error when embedder is nil.
func TestNew_NilEmbedder(t *testing.T) {
	pool := new(pgxpool.Pool)

	_, err := New(pool, nil, 8)
	if err == nil {
		t.Fatal("expected error for nil embedder, got nil")
	}
	if !strings.Contains(err.Error(), "embedder is required") {
		t.Fatalf("expected error containing 'embedder is required', got: %v", err)
	}
}

// TestNew_InvalidDim verifies that New returns an error when dim < 1.
func TestNew_InvalidDim(t *testing.T) {
	pool := new(pgxpool.Pool)
	emb := &hashEmbedder{dim: 8}

	// dim = 0
	_, err := New(pool, emb, 0)
	if err == nil {
		t.Fatal("expected error for dim=0, got nil")
	}
	if !strings.Contains(err.Error(), "dim must be at least 1") {
		t.Fatalf("expected error containing 'dim must be at least 1', got: %v", err)
	}

	// dim = -1
	_, err = New(pool, emb, -1)
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
	var _ memory.MemoryStore = (*Store)(nil)
	var _ memory.Memory = (*Store)(nil)
}

// TestDistanceMetric_OperatorMapping verifies that distanceOp returns the
// correct pgvector operator for each supported distance metric.
func TestDistanceMetric_OperatorMapping(t *testing.T) {
	tests := []struct {
		metric string
		wantOp string
	}{
		{"cosine", "<=>"},
		{"l2", "<->"},
		{"inner_product", "<#>"},
		{"", "<=>"}, // default is cosine
	}

	for _, tc := range tests {
		t.Run(tc.metric, func(t *testing.T) {
			s := &Store{distMetric: tc.metric}
			got := s.distanceOp()
			if got != tc.wantOp {
				t.Fatalf("distanceOp() for metric %q = %q, want %q", tc.metric, got, tc.wantOp)
			}
		})
	}
}

// TestOpsClass verifies that opsClass returns the correct pgvector operator
// class for each supported distance metric.
func TestOpsClass(t *testing.T) {
	tests := []struct {
		metric string
		want   string
	}{
		{"cosine", "vector_cosine_ops"},
		{"l2", "vector_l2_ops"},
		{"inner_product", "vector_ip_ops"},
		{"", "vector_cosine_ops"}, // default is cosine
	}

	for _, tc := range tests {
		t.Run(tc.metric, func(t *testing.T) {
			got := opsClass(tc.metric)
			if got != tc.want {
				t.Fatalf("opsClass(%q) = %q, want %q", tc.metric, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Schema Creation Tests (require PostgreSQL with pgvector)
// ---------------------------------------------------------------------------

// connectPool creates a *pgxpool.Pool from the POSTGRES_URL returned by
// skipIfNoPostgres. Used by integration tests that need direct pool access.
func connectPool(t *testing.T, pgURL string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), pgURL)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool
}

// TestSchemaCreation_AutoMigrate verifies that WithAutoMigrate creates a table
// with the expected columns: id, identifier, content, metadata, embedding,
// created_at.
func TestSchemaCreation_AutoMigrate(t *testing.T) {
	pgURL := skipIfNoPostgres(t)
	store := newTestStore(t, pgURL)
	ctx := t.Context()

	pool := connectPool(t, pgURL)

	// Query information_schema to verify columns exist.
	rows, err := pool.Query(ctx, `
		SELECT column_name, data_type
		FROM information_schema.columns
		WHERE table_name = $1
		ORDER BY ordinal_position
	`, store.tableName)
	if err != nil {
		t.Fatalf("query information_schema: %v", err)
	}
	defer rows.Close()

	columns := make(map[string]string)
	for rows.Next() {
		var name, dtype string
		if err := rows.Scan(&name, &dtype); err != nil {
			t.Fatalf("scan: %v", err)
		}
		columns[name] = dtype
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	expectedColumns := []string{"id", "identifier", "content", "metadata", "embedding", "created_at"}
	for _, col := range expectedColumns {
		if _, ok := columns[col]; !ok {
			t.Errorf("missing column %q in table %q; got columns: %v", col, store.tableName, columns)
		}
	}

	// Verify specific data types.
	if dt, ok := columns["metadata"]; ok && dt != "jsonb" {
		t.Errorf("metadata column type = %q, want jsonb", dt)
	}
	if dt, ok := columns["identifier"]; ok && dt != "text" {
		t.Errorf("identifier column type = %q, want text", dt)
	}
	if dt, ok := columns["content"]; ok && dt != "text" {
		t.Errorf("content column type = %q, want text", dt)
	}
}

// TestSchemaCreation_IdentifierIndex verifies that auto-migrate creates an
// index on the identifier column.
func TestSchemaCreation_IdentifierIndex(t *testing.T) {
	pgURL := skipIfNoPostgres(t)
	store := newTestStore(t, pgURL)
	ctx := t.Context()

	pool := connectPool(t, pgURL)

	var count int
	err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM pg_indexes
		WHERE tablename = $1 AND indexname = $2
	`, store.tableName, store.tableName+"_identifier_idx").Scan(&count)
	if err != nil {
		t.Fatalf("query pg_indexes: %v", err)
	}
	if count == 0 {
		t.Errorf("identifier index %q not found for table %q", store.tableName+"_identifier_idx", store.tableName)
	}
}

// TestSchemaCreation_EmbeddingIndex verifies that auto-migrate creates a
// vector similarity index on the embedding column.
func TestSchemaCreation_EmbeddingIndex(t *testing.T) {
	pgURL := skipIfNoPostgres(t)
	store := newTestStore(t, pgURL)
	ctx := t.Context()

	pool := connectPool(t, pgURL)

	var count int
	err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM pg_indexes
		WHERE tablename = $1 AND indexname = $2
	`, store.tableName, store.tableName+"_embedding_idx").Scan(&count)
	if err != nil {
		t.Fatalf("query pg_indexes: %v", err)
	}
	if count == 0 {
		t.Errorf("embedding index %q not found for table %q", store.tableName+"_embedding_idx", store.tableName)
	}
}

// ---------------------------------------------------------------------------
// MemoryStore.Add / MemoryStore.Search Integration Tests
// ---------------------------------------------------------------------------

// TestAdd_EmptyIdentifier verifies that Add returns an error for an empty
// identifier.
func TestAdd_EmptyIdentifier(t *testing.T) {
	pgURL := skipIfNoPostgres(t)
	store := newTestStore(t, pgURL)

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
	pgURL := skipIfNoPostgres(t)
	store := newTestStore(t, pgURL)

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
	pgURL := skipIfNoPostgres(t)
	store := newTestStore(t, pgURL)

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
	pgURL := skipIfNoPostgres(t)
	store := newTestStore(t, pgURL)

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
	pgURL := skipIfNoPostgres(t)
	store := newTestStore(t, pgURL)
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

// ---------------------------------------------------------------------------
// WHERE-based Filtering Tests
// ---------------------------------------------------------------------------

// TestAddSearch_WHEREFiltering verifies that documents stored under different
// identifiers are isolated: Search for identifier A does not return documents
// stored under identifier B. This validates the SQL WHERE clause scoping.
func TestAddSearch_WHEREFiltering(t *testing.T) {
	pgURL := skipIfNoPostgres(t)
	store := newTestStore(t, pgURL)
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
			t.Fatal("Search for alice returned bob's document — WHERE filtering broken")
		}
	}
	if len(results) == 0 {
		t.Fatal("Search for alice returned no results")
	}

	// Search under "bob" — should only find bob's document.
	results, err = store.Search(ctx, "bob", vecBob, 10)
	if err != nil {
		t.Fatalf("Search bob: %v", err)
	}
	for _, r := range results {
		if r.Document.Content == "alice fact" {
			t.Fatal("Search for bob returned alice's document — WHERE filtering broken")
		}
	}
	if len(results) == 0 {
		t.Fatal("Search for bob returned no results")
	}
}

// TestSearch_EmptyResults verifies that Search returns a non-nil empty slice
// when no documents match the identifier.
func TestSearch_EmptyResults(t *testing.T) {
	pgURL := skipIfNoPostgres(t)
	store := newTestStore(t, pgURL)
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
// Distance Metric Options Tests
// ---------------------------------------------------------------------------

// newTestStoreWithMetric creates a Store with a specific distance metric.
// It creates its own pool and table to avoid conflicts with other tests.
func newTestStoreWithMetric(t *testing.T, pgURL, metric string) *Store {
	t.Helper()

	pool, err := pgxpool.New(context.Background(), pgURL)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}

	// Sanitize t.Name() for table name.
	sanitized := strings.ReplaceAll(t.Name(), "/", "_")
	sanitized = strings.ReplaceAll(sanitized, " ", "_")
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
		WithDistanceMetric(metric),
	)
	if err != nil {
		pool.Close()
		t.Fatalf("newTestStoreWithMetric: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// TestDistanceMetric_Cosine verifies that a store created with
// WithDistanceMetric("cosine") can Add and Search successfully.
func TestDistanceMetric_Cosine(t *testing.T) {
	pgURL := skipIfNoPostgres(t)
	store := newTestStoreWithMetric(t, pgURL, "cosine")
	testDistanceMetricAddSearch(t, store)
}

// TestDistanceMetric_L2 verifies that a store created with
// WithDistanceMetric("l2") can Add and Search successfully.
func TestDistanceMetric_L2(t *testing.T) {
	pgURL := skipIfNoPostgres(t)
	store := newTestStoreWithMetric(t, pgURL, "l2")
	testDistanceMetricAddSearch(t, store)
}

// TestDistanceMetric_InnerProduct verifies that a store created with
// WithDistanceMetric("inner_product") can Add and Search successfully.
func TestDistanceMetric_InnerProduct(t *testing.T) {
	pgURL := skipIfNoPostgres(t)
	store := newTestStoreWithMetric(t, pgURL, "inner_product")
	testDistanceMetricAddSearch(t, store)
}

// testDistanceMetricAddSearch is a helper that verifies Add/Search works for
// a store configured with any distance metric.
func testDistanceMetricAddSearch(t *testing.T, store *Store) {
	t.Helper()
	ctx := t.Context()

	emb := &hashEmbedder{dim: 32}

	vec, err := emb.Embed(ctx, "distance metric test")
	if err != nil {
		t.Fatalf("embed: %v", err)
	}

	docs := []agent.Document{{Content: "distance metric test"}}
	err = store.Add(ctx, "user-1", docs, [][]float64{vec})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	results, err := store.Search(ctx, "user-1", vec, 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("Search returned empty results after Add")
	}

	found := false
	for _, r := range results {
		if r.Document.Content == "distance metric test" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Search results do not contain the stored document; got %+v", results)
	}
}

// ---------------------------------------------------------------------------
// Remember / Recall Integration Tests (via internal Adapter)
// ---------------------------------------------------------------------------

// TestRemember_EmptyIdentifier verifies that Remember returns an error for
// an empty identifier.
func TestRemember_EmptyIdentifier(t *testing.T) {
	pgURL := skipIfNoPostgres(t)
	store := newTestStore(t, pgURL)

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
	pgURL := skipIfNoPostgres(t)
	store := newTestStore(t, pgURL)

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
	pgURL := skipIfNoPostgres(t)
	store := newTestStore(t, pgURL)

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
	pgURL := skipIfNoPostgres(t)
	store := newTestStore(t, pgURL)

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

// TestRememberRecall_BasicFlow verifies the Remember/Recall round-trip
// through the internal Adapter.
func TestRememberRecall_BasicFlow(t *testing.T) {
	pgURL := skipIfNoPostgres(t)
	store := newTestStore(t, pgURL)
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
	pgURL := skipIfNoPostgres(t)
	store := newTestStore(t, pgURL)
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
// WithDropExisting Tests
// ---------------------------------------------------------------------------

// TestWithDropExisting verifies that WithDropExisting drops and recreates the
// table, removing previously stored data.
func TestWithDropExisting(t *testing.T) {
	pgURL := skipIfNoPostgres(t)
	ctx := t.Context()

	pool, err := pgxpool.New(context.Background(), pgURL)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(func() { pool.Close() })

	tableName := "test_drop_existing"
	emb := &hashEmbedder{dim: 32}

	// Create a store and add a document.
	store1, err := New(pool, emb, 32,
		WithTableName(tableName),
		WithAutoMigrate(),
		WithDropExisting(),
	)
	if err != nil {
		t.Fatalf("New store1: %v", err)
	}

	vec, _ := emb.Embed(ctx, "old data")
	docs := []agent.Document{{Content: "old data"}}
	if err := store1.Add(ctx, "user-1", docs, [][]float64{vec}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Verify the document exists.
	results, err := store1.Search(ctx, "user-1", vec, 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results after Add, got none")
	}

	// Create a new store with WithDropExisting — should drop the table.
	store2, err := New(pool, emb, 32,
		WithTableName(tableName),
		WithAutoMigrate(),
		WithDropExisting(),
	)
	if err != nil {
		t.Fatalf("New store2: %v", err)
	}

	// The old data should be gone.
	results, err = store2.Search(ctx, "user-1", vec, 5)
	if err != nil {
		t.Fatalf("Search after drop: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected empty results after WithDropExisting, got %d", len(results))
	}
}
