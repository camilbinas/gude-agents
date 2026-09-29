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

	if s.tableName != table {
		t.Fatalf("expected table %q, got %q", table, s.tableName)
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
