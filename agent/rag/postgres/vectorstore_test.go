package postgres

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
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

func newTestStore(t *testing.T, dim int) *VectorStore {
	t.Helper()
	pool := skipIfNoPostgres(t)
	table := fmt.Sprintf("test_docs_%d", os.Getpid())

	s, err := New(pool, dim, WithTableName(table), WithAutoMigrate())
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
		t.Fatal("expected non-nil VectorStore")
	}
}

func TestAddAndSearch(t *testing.T) {
	s := newTestStore(t, 3)
	ctx := context.Background()

	docs := []agent.Document{
		{Content: "Go is a compiled language", Metadata: map[string]string{"lang": "go"}},
		{Content: "Python is interpreted", Metadata: map[string]string{"lang": "python"}},
		{Content: "Rust focuses on safety", Metadata: map[string]string{"lang": "rust"}},
	}
	embeddings := [][]float64{
		{1.0, 0.0, 0.0},
		{0.0, 1.0, 0.0},
		{0.0, 0.0, 1.0},
	}

	if err := s.Add(ctx, docs, embeddings); err != nil {
		t.Fatalf("Add: %v", err)
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

func TestAdd_LengthMismatch(t *testing.T) {
	s := newTestStore(t, 3)
	ctx := context.Background()

	docs := []agent.Document{{Content: "hello"}}
	embeddings := [][]float64{{1.0, 0.0, 0.0}, {0.0, 1.0, 0.0}}

	err := s.Add(ctx, docs, embeddings)
	if err == nil {
		t.Fatal("expected error for length mismatch")
	}
}

func TestAdd_Empty(t *testing.T) {
	s := newTestStore(t, 3)
	ctx := context.Background()

	err := s.Add(ctx, nil, nil)
	if err != nil {
		t.Fatalf("expected nil error for empty add, got: %v", err)
	}
}

func TestCustomTableName(t *testing.T) {
	pool := skipIfNoPostgres(t)
	table := fmt.Sprintf("custom_docs_%d", os.Getpid())

	s, err := New(pool, 3, WithTableName(table), WithAutoMigrate())
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

	s, err := New(pool, 3, WithTableName(table), WithDistanceMetric("l2"), WithAutoMigrate())
	if err != nil {
		t.Fatalf("New with L2: %v", err)
	}
	defer func() {
		pool.Exec(context.Background(), fmt.Sprintf("DROP TABLE IF EXISTS %s", table))
		s.Close()
	}()

	ctx := context.Background()

	docs := []agent.Document{
		{Content: "near"},
		{Content: "far"},
	}
	embeddings := [][]float64{
		{1.0, 0.0, 0.0},
		{0.0, 0.0, 1.0},
	}

	if err := s.Add(ctx, docs, embeddings); err != nil {
		t.Fatalf("Add: %v", err)
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
