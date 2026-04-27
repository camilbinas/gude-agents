// Package postgres provides a PostgreSQL + pgvector memory backend
// for use with the gude-agents memory system.
//
// Requires PostgreSQL with the pgvector extension installed. The store
// implements memory.MemoryStore directly using pgx SQL queries and pgvector
// operations, with a dedicated identifier column for native WHERE-based
// scoping.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/memory"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pgvector "github.com/pgvector/pgvector-go"
)

// Compile-time checks.
var (
	_ memory.MemoryStore = (*Store)(nil)
	_ memory.Memory      = (*Store)(nil)
)

// Store implements both memory.MemoryStore and memory.Memory using PostgreSQL
// with pgvector for vector similarity search. It uses a dedicated identifier
// column with a SQL WHERE clause for scoping instead of metadata-based
// filtering.
//
type Store struct {
	pool       *pgxpool.Pool
	tableName  string
	dim        int
	distMetric string
	embedder   agent.Embedder
	adapter    *memory.Adapter
}

// distanceOp returns the pgvector operator for the configured distance metric.
func (s *Store) distanceOp() string {
	switch s.distMetric {
	case "l2":
		return "<->"
	case "inner_product":
		return "<#>"
	default: // cosine
		return "<=>"
	}
}

// opsClass returns the pgvector operator class for index creation.
func opsClass(metric string) string {
	switch metric {
	case "l2":
		return "vector_l2_ops"
	case "inner_product":
		return "vector_ip_ops"
	default:
		return "vector_cosine_ops"
	}
}

// New creates a new Store. It validates inputs, applies functional options,
// optionally drops the existing table, then creates the table and indexes
// if auto-migrate is enabled.
//
// Parameters:
//   - pool: a connected pgxpool.Pool for PostgreSQL operations
//   - embedder: used to compute embedding vectors for Remember and Recall
//   - dim: embedding dimension (must be >= 1)
//   - opts: optional functional options (WithTableName, WithAutoMigrate, etc.)
//
// Returns an error if the pool is nil, embedder is nil, dim < 1, or the
// auto-migration fails.
func New(pool *pgxpool.Pool, embedder agent.Embedder, dim int, opts ...StoreOption) (*Store, error) {
	if pool == nil {
		return nil, errors.New("postgres memory: pool is required")
	}
	if embedder == nil {
		return nil, errors.New("postgres memory: embedder is required")
	}
	if dim < 1 {
		return nil, errors.New("postgres memory: dim must be at least 1")
	}

	// Apply store options to collect configuration.
	cfg := &storeConfig{
		tableName:  "memory_entries",
		hnswM:      16,
		hnswEFCon:  200,
		ivfLists:   100,
		distMetric: "cosine",
	}
	for _, o := range opts {
		o(cfg)
	}

	// If WithDropExisting was set, drop the table before creating.
	if cfg.dropExisting {
		query := fmt.Sprintf("DROP TABLE IF EXISTS %s CASCADE", cfg.tableName)
		if _, err := pool.Exec(context.Background(), query); err != nil {
			return nil, fmt.Errorf("postgres memory: drop table: %w", err)
		}
	}

	// Auto-migrate: create extension, table, and indexes.
	if cfg.autoMigrate {
		if err := autoMigrate(pool, cfg, dim); err != nil {
			return nil, err
		}
	}

	s := &Store{
		pool:       pool,
		tableName:  cfg.tableName,
		dim:        dim,
		distMetric: cfg.distMetric,
		embedder:   embedder,
	}

	// Create an internal Adapter wrapping this MemoryStore for Remember/Recall.
	s.adapter = memory.NewAdapter(s, embedder)

	return s, nil
}

// autoMigrate creates the pgvector extension, table with dedicated identifier
// column, and indexes.
func autoMigrate(pool *pgxpool.Pool, cfg *storeConfig, dim int) error {
	ctx := context.Background()

	if _, err := pool.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS vector"); err != nil {
		return fmt.Errorf("postgres memory: create extension: %w", err)
	}

	ddl := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id         TEXT PRIMARY KEY,
			identifier TEXT NOT NULL,
			content    TEXT NOT NULL,
			metadata   JSONB,
			embedding  vector(%d) NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`, cfg.tableName, dim)
	if _, err := pool.Exec(ctx, ddl); err != nil {
		return fmt.Errorf("postgres memory: create table: %w", err)
	}

	// Create index on identifier for WHERE-based filtering.
	identifierIdx := fmt.Sprintf(`
		CREATE INDEX IF NOT EXISTS %s_identifier_idx ON %s (identifier)
	`, cfg.tableName, cfg.tableName)
	if _, err := pool.Exec(ctx, identifierIdx); err != nil {
		return fmt.Errorf("postgres memory: create identifier index: %w", err)
	}

	// Create vector similarity index.
	ops := opsClass(cfg.distMetric)
	var indexDDL string

	switch {
	case cfg.ivfLists > 0 && cfg.hnswM == 0 && cfg.hnswEFCon == 0:
		indexDDL = fmt.Sprintf(`
			CREATE INDEX IF NOT EXISTS %s_embedding_idx ON %s
			USING ivfflat (embedding %s) WITH (lists = %d)
		`, cfg.tableName, cfg.tableName, ops, cfg.ivfLists)
	default:
		m := cfg.hnswM
		if m == 0 {
			m = 16
		}
		ef := cfg.hnswEFCon
		if ef == 0 {
			ef = 200
		}
		indexDDL = fmt.Sprintf(`
			CREATE INDEX IF NOT EXISTS %s_embedding_idx ON %s
			USING hnsw (embedding %s) WITH (m = %d, ef_construction = %d)
		`, cfg.tableName, cfg.tableName, ops, m, ef)
	}

	if _, err := pool.Exec(ctx, indexDDL); err != nil {
		return fmt.Errorf("postgres memory: create embedding index: %w", err)
	}

	return nil
}

// Add implements memory.MemoryStore. It stores documents and their embeddings
// with the identifier as a dedicated column.
func (s *Store) Add(ctx context.Context, identifier string, docs []agent.Document, embeddings [][]float64) error {
	if identifier == "" {
		return errors.New("postgres memory: identifier must not be empty")
	}
	if len(docs) != len(embeddings) {
		return fmt.Errorf("postgres memory: docs and embeddings length mismatch: %d vs %d", len(docs), len(embeddings))
	}
	if len(docs) == 0 {
		return nil
	}

	batch := &pgx.Batch{}
	for i, doc := range docs {
		vec := float64sToFloat32(embeddings[i])

		metaJSON, err := json.Marshal(doc.Metadata)
		if err != nil {
			return fmt.Errorf("postgres memory: marshal metadata: %w", err)
		}

		query := fmt.Sprintf(
			`INSERT INTO %s (id, identifier, content, metadata, embedding) VALUES ($1, $2, $3, $4, $5)`,
			s.tableName,
		)
		batch.Queue(query, uuid.New().String(), identifier, doc.Content, metaJSON, pgvector.NewVector(vec))
	}

	results := s.pool.SendBatch(ctx, batch)
	defer results.Close()

	for range docs {
		if _, err := results.Exec(); err != nil {
			return fmt.Errorf("postgres memory: add: %w", err)
		}
	}

	return nil
}

// Search implements memory.MemoryStore. It performs vector similarity search
// scoped to the given identifier using a SQL WHERE clause.
func (s *Store) Search(ctx context.Context, identifier string, queryEmbedding []float64, topK int) ([]agent.ScoredDocument, error) {
	if identifier == "" {
		return nil, errors.New("postgres memory: identifier must not be empty")
	}
	if topK < 1 {
		return nil, fmt.Errorf("postgres memory: topK must be >= 1, got %d", topK)
	}

	op := s.distanceOp()
	vec := float64sToFloat32(queryEmbedding)

	query := fmt.Sprintf(`
		SELECT content, metadata, 1 - (embedding %s $1) AS similarity
		FROM %s
		WHERE identifier = $2
		ORDER BY embedding %s $1
		LIMIT $3
	`, op, s.tableName, op)

	rows, err := s.pool.Query(ctx, query, pgvector.NewVector(vec), identifier, topK)
	if err != nil {
		return nil, fmt.Errorf("postgres memory: search: %w", err)
	}
	defer rows.Close()

	var results []agent.ScoredDocument
	for rows.Next() {
		var content string
		var similarity float64
		var metaJSON []byte

		if err := rows.Scan(&content, &metaJSON, &similarity); err != nil {
			return nil, fmt.Errorf("postgres memory: scan: %w", err)
		}

		var metadata map[string]string
		if len(metaJSON) > 0 {
			_ = json.Unmarshal(metaJSON, &metadata)
		}

		results = append(results, agent.ScoredDocument{
			Document: agent.Document{
				Content:  content,
				Metadata: metadata,
			},
			Score: similarity,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres memory: rows: %w", err)
	}

	if results == nil {
		return []agent.ScoredDocument{}, nil
	}
	return results, nil
}

// Remember stores a fact for the given identifier with an embedding vector
// computed by the configured embedder.
//
// Returns an error if identifier or fact is empty, if the embedder fails,
// or if the PostgreSQL insert fails.
func (s *Store) Remember(ctx context.Context, identifier, fact string, metadata map[string]string) error {
	return s.adapter.Remember(ctx, identifier, fact, metadata)
}

// Recall retrieves the top entries for the given identifier by semantic
// similarity to the query.
//
// Returns at most limit results ordered by descending similarity score.
// Returns an empty non-nil slice when no entries match.
// Returns an error if identifier is empty, limit < 1, the embedder fails,
// or the PostgreSQL query fails.
func (s *Store) Recall(ctx context.Context, identifier, query string, limit int) ([]memory.Entry, error) {
	return s.adapter.Recall(ctx, identifier, query, limit)
}

// Close closes the underlying connection pool.
func (s *Store) Close() {
	s.pool.Close()
}

// float64sToFloat32 converts []float64 to []float32.
func float64sToFloat32(v []float64) []float32 {
	out := make([]float32, len(v))
	for i, f := range v {
		out[i] = float32(f)
	}
	return out
}
