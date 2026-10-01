package postgres

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/camilbinas/gude-agents/agent/rag"
	"github.com/jackc/pgx/v5/pgxpool"
)

func skipIfNoPostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("POSTGRES_URL")
	if url == "" {
		t.Skip("POSTGRES_URL not set, skipping postgres test")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("failed to connect to postgres: %v", err)
	}
	return pool
}

func newTestStore(t *testing.T, dim int) *Store {
	t.Helper()
	pool := skipIfNoPostgres(t)
	table := fmt.Sprintf("test_docs_%d", os.Getpid())

	ctx := context.Background()
	if _, err := pool.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS vector"); err != nil {
		t.Fatalf("create extension: %v", err)
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", table)); err != nil {
		t.Fatalf("drop table: %v", err)
	}
	ddl := fmt.Sprintf(`CREATE TABLE %s (
		id TEXT PRIMARY KEY, content TEXT NOT NULL, metadata JSONB, embedding vector(%d) NOT NULL
	)`, table, dim)
	if _, err := pool.Exec(ctx, ddl); err != nil {
		t.Fatalf("create table: %v", err)
	}

	s, err := New(pool, dim, WithTableName(table))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	t.Cleanup(func() {
		pool.Exec(context.Background(), fmt.Sprintf("DROP TABLE IF EXISTS %s", table))
		s.Close()
	})

	return s
}

func TestNew_NilPool(t *testing.T) {
	_, err := New(nil, 3)
	if err == nil {
		t.Fatal("expected error for nil pool")
	}
}

func TestNew_InvalidDim(t *testing.T) {
	pool := skipIfNoPostgres(t)
	defer pool.Close()

	_, err := New(pool, 0)
	if err == nil {
		t.Fatal("expected error for dim=0")
	}
}

func TestNew_CreatesTable(t *testing.T) {
	s := newTestStore(t, 3)
	if s == nil {
		t.Fatal("expected non-nil Store")
	}
}

func TestUpsertAndSearch(t *testing.T) {
	s := newTestStore(t, 3)
	ctx := context.Background()

	docs := []rag.Document{
		{Content: "Go is a compiled language", Metadata: map[string]string{"lang": "go"}},
		{Content: "Python is interpreted", Metadata: map[string]string{"lang": "python"}},
		{Content: "Rust focuses on safety", Metadata: map[string]string{"lang": "rust"}},
	}
	embeddings := [][]float64{
		{1.0, 0.0, 0.0},
		{0.0, 1.0, 0.0},
		{0.0, 0.0, 1.0},
	}

	if _, err := s.Upsert(ctx, docs, embeddings); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Search for something close to the first document.
	results, err := s.Search(ctx, []float64{0.9, 0.1, 0.0}, 2)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Top result should be the Go document.
	if results[0].Document.Content != "Go is a compiled language" {
		t.Errorf("expected top result to be Go doc, got %q", results[0].Document.Content)
	}
	if results[0].Document.Metadata["lang"] != "go" {
		t.Errorf("expected metadata lang=go, got %q", results[0].Document.Metadata["lang"])
	}
	if results[0].Score <= 0 {
		t.Errorf("expected positive score, got %f", results[0].Score)
	}
}

func TestSearch_Empty(t *testing.T) {
	s := newTestStore(t, 3)
	ctx := context.Background()

	results, err := s.Search(ctx, []float64{1.0, 0.0, 0.0}, 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results from empty store, got %d", len(results))
	}
}

func TestSearch_InvalidTopK(t *testing.T) {
	s := newTestStore(t, 3)
	ctx := context.Background()

	_, err := s.Search(ctx, []float64{1.0, 0.0, 0.0}, 0)
	if err == nil {
		t.Fatal("expected error for topK=0")
	}
}

func TestUpsert_LengthMismatch(t *testing.T) {
	s := newTestStore(t, 3)
	ctx := context.Background()

	docs := []rag.Document{{Content: "hello"}}
	embeddings := [][]float64{{1.0, 0.0, 0.0}, {0.0, 1.0, 0.0}}

	_, err := s.Upsert(ctx, docs, embeddings)
	if err == nil {
		t.Fatal("expected error for length mismatch")
	}
}

func TestUpsert_Empty(t *testing.T) {
	s := newTestStore(t, 3)
	ctx := context.Background()

	_, err := s.Upsert(ctx, nil, nil)
	if err != nil {
		t.Fatalf("expected nil error for empty upsert, got: %v", err)
	}
}

func TestCustomTableName(t *testing.T) {
	pool := skipIfNoPostgres(t)
	table := fmt.Sprintf("custom_docs_%d", os.Getpid())

	ctx := context.Background()
	pool.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS vector")
	pool.Exec(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", table))
	pool.Exec(ctx, fmt.Sprintf(`CREATE TABLE %s (id TEXT PRIMARY KEY, content TEXT NOT NULL, metadata JSONB, embedding vector(3) NOT NULL)`, table))

	s, err := New(pool, 3, WithTableName(table))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() {
		pool.Exec(context.Background(), fmt.Sprintf("DROP TABLE IF EXISTS %s", table))
		s.Close()
	}()

	// Identifiers are sanitized (double-quoted) at construction time.
	wantSanitized := `"` + table + `"`
	if s.tableName != wantSanitized {
		t.Fatalf("expected sanitized table %q, got %q", wantSanitized, s.tableName)
	}
}

func TestWithDistanceMetric_L2(t *testing.T) {
	pool := skipIfNoPostgres(t)
	table := fmt.Sprintf("test_l2_%d", os.Getpid())

	ctx := context.Background()
	pool.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS vector")
	pool.Exec(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", table))
	pool.Exec(ctx, fmt.Sprintf(`CREATE TABLE %s (id TEXT PRIMARY KEY, content TEXT NOT NULL, metadata JSONB, embedding vector(3) NOT NULL)`, table))

	s, err := New(pool, 3, WithTableName(table), WithDistanceMetric("l2"))
	if err != nil {
		t.Fatalf("New with L2: %v", err)
	}
	defer func() {
		pool.Exec(context.Background(), fmt.Sprintf("DROP TABLE IF EXISTS %s", table))
		s.Close()
	}()

	ctx = context.Background()

	docs := []rag.Document{
		{Content: "near"},
		{Content: "far"},
	}
	embeddings := [][]float64{
		{1.0, 0.0, 0.0},
		{0.0, 0.0, 1.0},
	}

	if _, err := s.Upsert(ctx, docs, embeddings); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	results, err := s.Search(ctx, []float64{1.0, 0.0, 0.0}, 2)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Document.Content != "near" {
		t.Errorf("expected 'near' as top result, got %q", results[0].Document.Content)
	}
}

func TestStore_ManagerLifecycle(t *testing.T) {
	s := newTestStore(t, 3)
	ctx := context.Background()

	ids, err := s.Upsert(ctx, []rag.Document{
		{ID: "document-1", Content: "first", Metadata: map[string]string{"group": "keep"}},
		{Content: "generated", Metadata: map[string]string{"group": "delete"}},
	}, [][]float64{{1, 0, 0}, {0, 1, 0}})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if len(ids) != 2 || ids[0] != "document-1" || ids[1] == "" {
		t.Fatalf("Upsert IDs = %v, want explicit and generated logical IDs", ids)
	}

	found, err := s.Find(ctx, ids[1], "missing", ids[0])
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(found) != 2 || found[0].ID != ids[1] || found[1].ID != ids[0] {
		t.Fatalf("Find result = %+v, want existing documents in input-ID order", found)
	}

	if err := s.DeleteByMetadata(ctx, map[string]string{"group": "delete"}); err != nil {
		t.Fatalf("DeleteByMetadata: %v", err)
	}
	found, err = s.Find(ctx, ids...)
	if err != nil {
		t.Fatalf("Find after DeleteByMetadata: %v", err)
	}
	if len(found) != 1 || found[0].ID != ids[0] {
		t.Fatalf("Find after DeleteByMetadata = %+v, want only %q", found, ids[0])
	}

	if err := s.Delete(ctx, ids[0]); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	found, err = s.Find(ctx, ids[0])
	if err != nil {
		t.Fatalf("Find after Delete: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("Find after Delete = %+v, want no documents", found)
	}
}

// ---------------------------------------------------------------------------
// Metric-correct scoring regression tests
// ---------------------------------------------------------------------------

// TestSearch_CosineScoreMatchesFormula verifies the cosine score returned by
// Search equals 1 - cosine_distance, and stays ordered nearest-first.
func TestSearch_CosineScoreMatchesFormula(t *testing.T) {
	s := newTestStore(t, 2)
	ctx := context.Background()

	docs := []rag.Document{{Content: "aligned"}, {Content: "orthogonal"}}
	embeddings := [][]float64{
		{1.0, 0.0},
		{0.0, 1.0},
	}
	if _, err := s.Upsert(ctx, docs, embeddings); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	results, err := s.Search(ctx, []float64{1.0, 0.0}, 2)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	// Query is identical to "aligned" -> cosine distance 0 -> score 1.
	if results[0].Document.Content != "aligned" {
		t.Fatalf("expected 'aligned' first, got %q", results[0].Document.Content)
	}
	if diff := results[0].Score - 1.0; diff > 1e-6 || diff < -1e-6 {
		t.Errorf("aligned score = %v, want ~1.0", results[0].Score)
	}
	// Query is orthogonal to "orthogonal" -> cosine distance 1 -> score 0.
	if diff := results[1].Score - 0.0; diff > 1e-6 || diff < -1e-6 {
		t.Errorf("orthogonal score = %v, want ~0.0", results[1].Score)
	}
}

// TestSearch_InnerProductScoreIsPlainInnerProduct verifies that with the
// inner_product metric, the returned score is the plain (positive) inner
// product rather than pgvector's raw negative-inner-product operator
// result, and that ordering remains nearest-first (largest inner product).
func TestSearch_InnerProductScoreIsPlainInnerProduct(t *testing.T) {
	pool := skipIfNoPostgres(t)
	table := fmt.Sprintf("test_ip_%d", os.Getpid())

	ctx := context.Background()
	pool.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS vector")
	pool.Exec(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", table))
	pool.Exec(ctx, fmt.Sprintf(`CREATE TABLE %s (id TEXT PRIMARY KEY, content TEXT NOT NULL, metadata JSONB, embedding vector(2) NOT NULL)`, table))

	s, err := New(pool, 2, WithTableName(table), WithDistanceMetric("inner_product"))
	if err != nil {
		t.Fatalf("New with inner_product: %v", err)
	}
	defer func() {
		pool.Exec(context.Background(), fmt.Sprintf("DROP TABLE IF EXISTS %s", table))
		s.Close()
	}()

	docs := []rag.Document{{Content: "strong"}, {Content: "weak"}}
	embeddings := [][]float64{
		{2.0, 0.0}, // inner product with query (1,0) = 2
		{0.5, 0.0}, // inner product with query (1,0) = 0.5
	}
	if _, err := s.Upsert(ctx, docs, embeddings); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	results, err := s.Search(ctx, []float64{1.0, 0.0}, 2)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Document.Content != "strong" {
		t.Fatalf("expected 'strong' (higher inner product) first, got %q", results[0].Document.Content)
	}
	// Plain inner product, not pgvector's negated <#> result: strong=2.0, weak=0.5.
	if diff := results[0].Score - 2.0; diff > 1e-6 || diff < -1e-6 {
		t.Errorf("strong score = %v, want ~2.0 (plain inner product)", results[0].Score)
	}
	if diff := results[1].Score - 0.5; diff > 1e-6 || diff < -1e-6 {
		t.Errorf("weak score = %v, want ~0.5 (plain inner product)", results[1].Score)
	}
}

// TestSearch_L2ScoreIsBoundedAndOrdered verifies that with the l2 metric,
// the score is bounded to (0, 1] (not a naive "1 - distance" that can go
// negative or exceed 1 for large distances) and preserves nearest-first
// ordering.
func TestSearch_L2ScoreIsBoundedAndOrdered(t *testing.T) {
	pool := skipIfNoPostgres(t)
	table := fmt.Sprintf("test_l2_score_%d", os.Getpid())

	ctx := context.Background()
	pool.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS vector")
	pool.Exec(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", table))
	pool.Exec(ctx, fmt.Sprintf(`CREATE TABLE %s (id TEXT PRIMARY KEY, content TEXT NOT NULL, metadata JSONB, embedding vector(2) NOT NULL)`, table))

	s, err := New(pool, 2, WithTableName(table), WithDistanceMetric("l2"))
	if err != nil {
		t.Fatalf("New with l2: %v", err)
	}
	defer func() {
		pool.Exec(context.Background(), fmt.Sprintf("DROP TABLE IF EXISTS %s", table))
		s.Close()
	}()

	docs := []rag.Document{{Content: "near"}, {Content: "far"}}
	embeddings := [][]float64{
		{1.0, 0.0},  // distance from query (1,0) = 0
		{1.0, 10.0}, // distance from query (1,0) = 10
	}
	if _, err := s.Upsert(ctx, docs, embeddings); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	results, err := s.Search(ctx, []float64{1.0, 0.0}, 2)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Document.Content != "near" {
		t.Fatalf("expected 'near' first, got %q", results[0].Document.Content)
	}
	// distance 0 -> score = 1/(1+0) = 1.
	if diff := results[0].Score - 1.0; diff > 1e-6 || diff < -1e-6 {
		t.Errorf("near score = %v, want ~1.0", results[0].Score)
	}
	// distance 10 -> score = 1/(1+10) ≈ 0.0909, must be within (0, 1] — not
	// negative, which a naive "1 - distance" would produce for distance > 1.
	if results[1].Score <= 0 || results[1].Score >= 1 {
		t.Errorf("far score = %v, want in (0, 1)", results[1].Score)
	}
	if results[0].Score <= results[1].Score {
		t.Errorf("expected near score (%v) > far score (%v)", results[0].Score, results[1].Score)
	}
}
