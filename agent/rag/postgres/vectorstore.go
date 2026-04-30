// Package postgres provides a PostgreSQL + pgvector vector store for use with
// the gude-agents RAG pipeline.
//
// Requires PostgreSQL with the pgvector extension installed. The table must be
// created by the caller. Column names are configurable via WithColumns.
//
// Expected table schema (when using defaults):
//
//	CREATE EXTENSION IF NOT EXISTS vector;
//	CREATE TABLE documents (
//	    id        TEXT PRIMARY KEY,
//	    content   TEXT NOT NULL,
//	    metadata  JSONB,
//	    embedding vector(1024) NOT NULL
//	);
//	CREATE INDEX ON documents USING hnsw (embedding vector_cosine_ops);
//
// Usage:
//
//	pool, err := pgxpool.New(ctx, "postgres://user:pass@localhost:5432/mydb")
//	store, err := postgres.New(pool, 1024)
//
//	// Point at a custom table:
//	store, err := postgres.New(pool, 1024,
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

// Compile-time check: VectorStore implements agent.VectorStoreManager.
var _ agent.VectorStoreManager = (*VectorStore)(nil)

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

// New creates a new VectorStore. The pool should be a connected pgxpool.Pool
// and dim is the embedding dimension (e.g. 1536 for OpenAI text-embedding-3-small).
// The table must already exist with the expected schema.
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
		distMetric: "cosine",
	}
	for _, o := range opts {
		o(cfg)
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

// Upsert stores documents with their embeddings. If a document's ID is empty,
// the store generates one. If a document's ID already exists, the store
// replaces the existing content, metadata, and embedding.
// Returns the IDs of all stored documents in input order.
func (s *VectorStore) Upsert(ctx context.Context, docs []agent.Document, embeddings [][]float64) ([]string, error) {
	if len(docs) != len(embeddings) {
		return nil, fmt.Errorf("postgres vectorstore: docs and embeddings length mismatch: %d vs %d", len(docs), len(embeddings))
	}
	if len(docs) == 0 {
		return []string{}, nil
	}

	batch := &pgx.Batch{}
	ids := make([]string, len(docs))
	for i, doc := range docs {
		vec := float64sToFloat32(embeddings[i])
		id := doc.ID
		if id == "" {
			id = uuid.New().String()
		}
		ids[i] = id

		if s.colMeta != "" {
			metaJSON, err := json.Marshal(doc.Metadata)
			if err != nil {
				return nil, fmt.Errorf("postgres vectorstore: marshal metadata: %w", err)
			}
			query := fmt.Sprintf(
				`INSERT INTO %s (%s, %s, %s, %s) VALUES ($1, $2, $3, $4) ON CONFLICT (%s) DO UPDATE SET %s=EXCLUDED.%s, %s=EXCLUDED.%s, %s=EXCLUDED.%s`,
				s.tableName, s.colID, s.colContent, s.colMeta, s.colEmbed,
				s.colID,
				s.colContent, s.colContent,
				s.colMeta, s.colMeta,
				s.colEmbed, s.colEmbed,
			)
			batch.Queue(query, id, doc.Content, metaJSON, pgvector.NewVector(vec))
		} else {
			query := fmt.Sprintf(
				`INSERT INTO %s (%s, %s, %s) VALUES ($1, $2, $3) ON CONFLICT (%s) DO UPDATE SET %s=EXCLUDED.%s, %s=EXCLUDED.%s`,
				s.tableName, s.colID, s.colContent, s.colEmbed,
				s.colID,
				s.colContent, s.colContent,
				s.colEmbed, s.colEmbed,
			)
			batch.Queue(query, id, doc.Content, pgvector.NewVector(vec))
		}
	}

	results := s.pool.SendBatch(ctx, batch)
	defer results.Close()

	for range docs {
		if _, err := results.Exec(); err != nil {
			return nil, fmt.Errorf("postgres vectorstore: upsert: %w", err)
		}
	}

	return ids, nil
}

// Find retrieves documents by their IDs. Returns documents in the same order
// as the input IDs, omitting IDs that don't exist. Returns an empty slice and
// nil error for an empty input.
func (s *VectorStore) Find(ctx context.Context, ids ...string) ([]agent.Document, error) {
	if len(ids) == 0 {
		return []agent.Document{}, nil
	}

	var query string
	if s.colMeta != "" {
		query = fmt.Sprintf(`SELECT %s, %s, %s FROM %s WHERE %s = ANY($1)`,
			s.colID, s.colContent, s.colMeta, s.tableName, s.colID)
	} else {
		query = fmt.Sprintf(`SELECT %s, %s FROM %s WHERE %s = ANY($1)`,
			s.colID, s.colContent, s.tableName, s.colID)
	}

	rows, err := s.pool.Query(ctx, query, ids)
	if err != nil {
		return nil, fmt.Errorf("postgres vectorstore: find: %w", err)
	}
	defer rows.Close()

	// Collect results into a map for reordering.
	found := make(map[string]agent.Document, len(ids))
	for rows.Next() {
		var id, content string
		var metadata map[string]string

		if s.colMeta != "" {
			var metaJSON []byte
			if err := rows.Scan(&id, &content, &metaJSON); err != nil {
				return nil, fmt.Errorf("postgres vectorstore: find scan: %w", err)
			}
			if len(metaJSON) > 0 {
				_ = json.Unmarshal(metaJSON, &metadata)
			}
		} else {
			if err := rows.Scan(&id, &content); err != nil {
				return nil, fmt.Errorf("postgres vectorstore: find scan: %w", err)
			}
		}

		found[id] = agent.Document{
			ID:       id,
			Content:  content,
			Metadata: metadata,
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres vectorstore: find rows: %w", err)
	}

	// Reorder results to match input ID order, omitting missing IDs.
	results := make([]agent.Document, 0, len(found))
	for _, id := range ids {
		if doc, ok := found[id]; ok {
			results = append(results, doc)
		}
	}

	return results, nil
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
			SELECT %s, %s, %s, 1 - (%s %s $1) AS similarity
			FROM %s
			ORDER BY %s %s $1
			LIMIT $2
		`, s.colID, s.colContent, s.colMeta, s.colEmbed, op, s.tableName, s.colEmbed, op)
	} else {
		query = fmt.Sprintf(`
			SELECT %s, %s, 1 - (%s %s $1) AS similarity
			FROM %s
			ORDER BY %s %s $1
			LIMIT $2
		`, s.colID, s.colContent, s.colEmbed, op, s.tableName, s.colEmbed, op)
	}

	rows, err := s.pool.Query(ctx, query, pgvector.NewVector(vec), topK)
	if err != nil {
		return nil, fmt.Errorf("postgres vectorstore: search: %w", err)
	}
	defer rows.Close()

	var results []agent.ScoredDocument
	for rows.Next() {
		var id string
		var content string
		var similarity float64
		var metadata map[string]string

		if s.colMeta != "" {
			var metaJSON []byte
			if err := rows.Scan(&id, &content, &metaJSON, &similarity); err != nil {
				return nil, fmt.Errorf("postgres vectorstore: scan: %w", err)
			}
			if len(metaJSON) > 0 {
				_ = json.Unmarshal(metaJSON, &metadata)
			}
		} else {
			if err := rows.Scan(&id, &content, &similarity); err != nil {
				return nil, fmt.Errorf("postgres vectorstore: scan: %w", err)
			}
		}

		results = append(results, agent.ScoredDocument{
			Document: agent.Document{
				ID:       id,
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

// DeleteByMetadata deletes all documents whose metadata contains all key-value
// pairs in the filter (AND semantics). Returns an error if the filter is empty.
func (s *VectorStore) DeleteByMetadata(ctx context.Context, filter map[string]string) error {
	if len(filter) == 0 {
		return fmt.Errorf("vectorstore: filter must not be empty")
	}
	if s.colMeta == "" {
		return fmt.Errorf("postgres vectorstore: metadata column not configured")
	}

	filterJSON, err := json.Marshal(filter)
	if err != nil {
		return fmt.Errorf("postgres vectorstore: marshal filter: %w", err)
	}

	query := fmt.Sprintf(`DELETE FROM %s WHERE %s @> $1::jsonb`, s.tableName, s.colMeta)
	if _, err := s.pool.Exec(ctx, query, filterJSON); err != nil {
		return fmt.Errorf("postgres vectorstore: delete by metadata: %w", err)
	}
	return nil
}

// Delete removes documents by their IDs.
func (s *VectorStore) Delete(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	query := fmt.Sprintf(`DELETE FROM %s WHERE %s = ANY($1)`, s.tableName, s.colID)
	if _, err := s.pool.Exec(ctx, query, ids); err != nil {
		return fmt.Errorf("postgres vectorstore: delete: %w", err)
	}
	return nil
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
