// Package postgres provides a PostgreSQL + pgvector vector store for use with
// the gude-agents RAG pipeline.
//
// Requires PostgreSQL with the pgvector extension installed. By default, the
// table must already exist. Use WithAutoMigrate to have the driver create the
// extension, table, and index automatically.
//
// The driver supports custom column mapping via WithColumns, so you can point
// it at an existing table (e.g. a users table with an embedding column)
// instead of creating a dedicated documents table.
//
// Expected table schema (when using defaults):
//
//	CREATE EXTENSION IF NOT EXISTS vector;
//	CREATE TABLE documents (
//	    id        TEXT PRIMARY KEY,
//	    content   TEXT NOT NULL,
//	    metadata  JSONB,
//	    embedding vector(1536) NOT NULL
//	);
//	CREATE INDEX ON documents USING hnsw (embedding vector_cosine_ops);
//
// Usage:
//
//	pool, err := pgxpool.New(ctx, "postgres://user:pass@localhost:5432/mydb")
//
//	// Use an existing table:
//	store, err := postgres.New(pool, 1536)
//
//	// Auto-create everything (development):
//	store, err := postgres.New(pool, 1536, postgres.WithAutoMigrate())
//
//	// Point at a custom table:
//	store, err := postgres.New(pool, 1536,
//	    postgres.WithTableName("users"),
//	    postgres.WithColumns("id", "bio", "", "embedding"),
//	)
package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pgvector "github.com/pgvector/pgvector-go"
)

// Compile-time check: VectorStore implements agent.VectorStore.
var _ agent.VectorStore = (*VectorStore)(nil)

// VectorStore implements agent.VectorStore using PostgreSQL with pgvector.
type VectorStore struct {
	pool       *pgxpool.Pool
	tableName  string
	colID      string
	colContent string
	colMeta    string // empty = no metadata column
	colEmbed   string
	dim        int
	distMetric string
}

// distanceOp returns the pgvector operator for the configured distance metric.
func (s *VectorStore) distanceOp() string {
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

// New creates a new VectorStore. The pool should be a connected pgxpool.Pool
// and dim is the embedding dimension (e.g. 1536 for OpenAI text-embedding-3-small).
//
// By default, the table must already exist with the expected columns. Use
// WithAutoMigrate to create the extension, table, and index automatically.
// Use WithColumns to map to custom column names on an existing table.
//
// Returns an error if the pool is nil, dim < 1, or (with WithAutoMigrate)
// schema setup fails.
func New(pool *pgxpool.Pool, dim int, opts ...Option) (*VectorStore, error) {
	if pool == nil {
		return nil, fmt.Errorf("postgres vectorstore: pool is required")
	}
	if dim < 1 {
		return nil, fmt.Errorf("postgres vectorstore: dim must be >= 1, got %d", dim)
	}

	cfg := &pgvConfig{
		tableName:  "documents",
		colID:      "id",
		colContent: "content",
		colMeta:    "metadata",
		colEmbed:   "embedding",
		indexType:  "hnsw",
		hnswM:      16,
		hnswEFCon:  200,
		ivfLists:   100,
		distMetric: "cosine",
	}
	for _, o := range opts {
		o(cfg)
	}

	if cfg.autoMigrate {
		if err := autoMigrate(pool, cfg, dim); err != nil {
			return nil, err
		}
	}

	return &VectorStore{
		pool:       pool,
		tableName:  cfg.tableName,
		colID:      cfg.colID,
		colContent: cfg.colContent,
		colMeta:    cfg.colMeta,
		colEmbed:   cfg.colEmbed,
		dim:        dim,
		distMetric: cfg.distMetric,
	}, nil
}

// autoMigrate creates the pgvector extension, table, and index.
func autoMigrate(pool *pgxpool.Pool, cfg *pgvConfig, dim int) error {
	ctx := context.Background()

	if _, err := pool.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS vector"); err != nil {
		return fmt.Errorf("postgres vectorstore: create extension: %w", err)
	}

	metaCol := ""
	if cfg.colMeta != "" {
		metaCol = fmt.Sprintf("%s JSONB,", cfg.colMeta)
	}

	ddl := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			%s TEXT PRIMARY KEY,
			%s TEXT NOT NULL,
			%s
			%s vector(%d) NOT NULL
		)
	`, cfg.tableName, cfg.colID, cfg.colContent, metaCol, cfg.colEmbed, dim)
	if _, err := pool.Exec(ctx, ddl); err != nil {
		return fmt.Errorf("postgres vectorstore: create table: %w", err)
	}

	indexName := cfg.tableName + "_" + cfg.colEmbed + "_idx"
	ops := opsClass(cfg.distMetric)
	var indexDDL string

	switch cfg.indexType {
	case "ivfflat":
		indexDDL = fmt.Sprintf(`
			CREATE INDEX IF NOT EXISTS %s ON %s
			USING ivfflat (%s %s) WITH (lists = %d)
		`, indexName, cfg.tableName, cfg.colEmbed, ops, cfg.ivfLists)
	default:
		indexDDL = fmt.Sprintf(`
			CREATE INDEX IF NOT EXISTS %s ON %s
			USING hnsw (%s %s) WITH (m = %d, ef_construction = %d)
		`, indexName, cfg.tableName, cfg.colEmbed, ops, cfg.hnswM, cfg.hnswEFCon)
	}

	if _, err := pool.Exec(ctx, indexDDL); err != nil {
		return fmt.Errorf("postgres vectorstore: create index: %w", err)
	}

	return nil
}

// Add stores documents and their embeddings.
func (s *VectorStore) Add(ctx context.Context, docs []agent.Document, embeddings [][]float64) error {
	if len(docs) != len(embeddings) {
		return fmt.Errorf("postgres vectorstore: docs and embeddings length mismatch: %d vs %d", len(docs), len(embeddings))
	}
	if len(docs) == 0 {
		return nil
	}

	batch := &pgx.Batch{}
	for i, doc := range docs {
		vec := float64sToFloat32(embeddings[i])

		if s.colMeta != "" {
			metaJSON, err := json.Marshal(doc.Metadata)
			if err != nil {
				return fmt.Errorf("postgres vectorstore: marshal metadata: %w", err)
			}
			query := fmt.Sprintf(`INSERT INTO %s (%s, %s, %s, %s) VALUES ($1, $2, $3, $4)`,
				s.tableName, s.colID, s.colContent, s.colMeta, s.colEmbed)
			batch.Queue(query, uuid.New().String(), doc.Content, metaJSON, pgvector.NewVector(vec))
		} else {
			query := fmt.Sprintf(`INSERT INTO %s (%s, %s, %s) VALUES ($1, $2, $3)`,
				s.tableName, s.colID, s.colContent, s.colEmbed)
			batch.Queue(query, uuid.New().String(), doc.Content, pgvector.NewVector(vec))
		}
	}

	results := s.pool.SendBatch(ctx, batch)
	defer results.Close()

	for range docs {
		if _, err := results.Exec(); err != nil {
			return fmt.Errorf("postgres vectorstore: add: %w", err)
		}
	}

	return nil
}

// Search performs approximate nearest-neighbor search using pgvector.
func (s *VectorStore) Search(ctx context.Context, queryEmbedding []float64, topK int) ([]agent.ScoredDocument, error) {
	if topK < 1 {
		return nil, fmt.Errorf("postgres vectorstore: topK must be >= 1, got %d", topK)
	}

	op := s.distanceOp()
	vec := float64sToFloat32(queryEmbedding)

	var query string
	if s.colMeta != "" {
		query = fmt.Sprintf(`
			SELECT %s, %s, 1 - (%s %s $1) AS similarity
			FROM %s
			ORDER BY %s %s $1
			LIMIT $2
		`, s.colContent, s.colMeta, s.colEmbed, op, s.tableName, s.colEmbed, op)
	} else {
		query = fmt.Sprintf(`
			SELECT %s, 1 - (%s %s $1) AS similarity
			FROM %s
			ORDER BY %s %s $1
			LIMIT $2
		`, s.colContent, s.colEmbed, op, s.tableName, s.colEmbed, op)
	}

	rows, err := s.pool.Query(ctx, query, pgvector.NewVector(vec), topK)
	if err != nil {
		return nil, fmt.Errorf("postgres vectorstore: search: %w", err)
	}
	defer rows.Close()

	var results []agent.ScoredDocument
	for rows.Next() {
		var content string
		var similarity float64
		var metadata map[string]string

		if s.colMeta != "" {
			var metaJSON []byte
			if err := rows.Scan(&content, &metaJSON, &similarity); err != nil {
				return nil, fmt.Errorf("postgres vectorstore: scan: %w", err)
			}
			if len(metaJSON) > 0 {
				_ = json.Unmarshal(metaJSON, &metadata)
			}
		} else {
			if err := rows.Scan(&content, &similarity); err != nil {
				return nil, fmt.Errorf("postgres vectorstore: scan: %w", err)
			}
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
		return nil, fmt.Errorf("postgres vectorstore: rows: %w", err)
	}

	return results, nil
}

// Close closes the underlying connection pool.
func (s *VectorStore) Close() {
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
