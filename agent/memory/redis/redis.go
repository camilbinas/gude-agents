// Package redis provides a Redis Stack (RediSearch) memory backend
// for use with the gude-agents memory system.
//
// Requires Redis Stack — NOT standard community Redis. The store uses
// RediSearch commands (FT.CREATE, FT.SEARCH) that are only available in
// Redis Stack.
package redis

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/memory"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

// Compile-time checks.
var (
	_ memory.MemoryStore = (*Store)(nil)
	_ memory.Memory      = (*Store)(nil)
)

// Store implements both memory.MemoryStore and memory.Memory using Redis
// Stack (RediSearch) for vector similarity search. It uses a native Redis
// TAG field for identifier-based filtering instead of metadata post-filtering.
//
// Documented in docs/memory.md
type Store struct {
	client    *goredis.Client
	indexName string
	keyPrefix string
	dim       int
	embedder  agent.Embedder
	adapter   *memory.Adapter
	hnswM     int
	hnswEF    int
}

// New creates a new Store. It pings Redis to verify connectivity, then creates
// the RediSearch HNSW index via FT.CREATE if it doesn't already exist.
//
// Parameters:
//   - opts: Redis connection configuration
//   - embedder: used to compute embedding vectors for Remember and Recall
//   - dim: embedding dimension (must be >= 1)
//   - sopts: optional functional options (WithIndexName, WithKeyPrefix, etc.)
//
// Returns an error if the embedder is nil, dim < 1, Redis is unreachable,
// or the index cannot be created.
func New(opts Options, embedder agent.Embedder, dim int, sopts ...StoreOption) (*Store, error) {
	if embedder == nil {
		return nil, errors.New("redis memory: embedder is required")
	}
	if dim < 1 {
		return nil, errors.New("redis memory: dim must be at least 1")
	}

	// Apply store options to collect configuration.
	cfg := &storeConfig{
		indexName: "gude_memory_idx",
		keyPrefix: "gude:memory:",
		hnswM:     16,
		hnswEF:    200,
	}
	for _, o := range sopts {
		o(cfg)
	}

	// Create go-redis client directly.
	addr := opts.Addr
	if addr == "" {
		addr = "127.0.0.1:6379"
	}
	client := goredis.NewClient(&goredis.Options{
		Addr:      addr,
		Password:  opts.Password,
		DB:        opts.DB,
		TLSConfig: opts.TLSConfig,
	})

	if err := client.Ping(context.Background()).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("redis memory: ping: %w", err)
	}

	// Drop existing index if requested.
	if cfg.dropExisting {
		_ = client.Do(context.Background(), "FT.DROPINDEX", cfg.indexName, "DD").Err()
	}

	// Create the HNSW index with a dedicated identifier TAG field.
	err := client.Do(context.Background(), "FT.CREATE", cfg.indexName,
		"ON", "HASH",
		"PREFIX", "1", cfg.keyPrefix,
		"SCHEMA",
		"content", "TEXT",
		"metadata", "TEXT",
		"identifier", "TAG",
		"embedding", "VECTOR", "HNSW", "10",
		"TYPE", "FLOAT32",
		"DIM", dim,
		"DISTANCE_METRIC", "COSINE",
		"M", cfg.hnswM,
		"EF_CONSTRUCTION", cfg.hnswEF,
	).Err()
	if err != nil && !strings.Contains(err.Error(), "Index already exists") {
		_ = client.Close()
		return nil, fmt.Errorf("redis memory: create index: %w", err)
	}

	s := &Store{
		client:    client,
		indexName: cfg.indexName,
		keyPrefix: cfg.keyPrefix,
		dim:       dim,
		embedder:  embedder,
		hnswM:     cfg.hnswM,
		hnswEF:    cfg.hnswEF,
	}

	// Create an internal Adapter wrapping this MemoryStore for Remember/Recall.
	s.adapter = memory.NewAdapter(s, embedder)

	return s, nil
}

// Add implements memory.MemoryStore. It stores documents and their embeddings
// as Redis hashes with the identifier as a top-level TAG field.
func (s *Store) Add(ctx context.Context, identifier string, docs []agent.Document, embeddings [][]float64) error {
	if identifier == "" {
		return errors.New("redis memory: identifier must not be empty")
	}
	if len(docs) != len(embeddings) {
		return fmt.Errorf("redis memory: docs and embeddings length mismatch: %d vs %d", len(docs), len(embeddings))
	}
	if len(docs) == 0 {
		return nil
	}

	for i, doc := range docs {
		metaJSON, err := json.Marshal(doc.Metadata)
		if err != nil {
			return fmt.Errorf("redis memory: add: %w", err)
		}

		key := s.keyPrefix + uuid.New().String()
		embeddingBytes := float64sToFloat32Bytes(embeddings[i])

		fields := map[string]interface{}{
			"content":    doc.Content,
			"metadata":   string(metaJSON),
			"identifier": identifier,
			"embedding":  embeddingBytes,
		}

		if err := s.client.HSet(ctx, key, fields).Err(); err != nil {
			return fmt.Errorf("redis memory: add: %w", err)
		}
	}

	return nil
}

// Search implements memory.MemoryStore. It performs a KNN similarity search
// filtered by the identifier TAG field.
func (s *Store) Search(ctx context.Context, identifier string, queryEmbedding []float64, topK int) ([]agent.ScoredDocument, error) {
	if identifier == "" {
		return nil, errors.New("redis memory: identifier must not be empty")
	}
	if topK < 1 {
		return nil, fmt.Errorf("redis memory: topK must be >= 1, got %d", topK)
	}

	blob := float64sToFloat32Bytes(queryEmbedding)
	escaped := escapeTag(identifier)
	query := fmt.Sprintf("@identifier:{%s}=>[KNN %d @embedding $BLOB AS score]", escaped, topK)

	res, err := s.client.Do(ctx, "FT.SEARCH", s.indexName,
		query,
		"PARAMS", "2", "BLOB", blob,
		"SORTBY", "score",
		"LIMIT", "0", fmt.Sprintf("%d", topK),
		"DIALECT", "2",
	).Result()
	if err != nil {
		return nil, fmt.Errorf("redis memory: search: %w", err)
	}

	// go-redis v9 returns RESP3 map format from Redis Stack 7+.
	// Older versions or RESP2 connections return a flat []interface{}.
	// Handle both.
	switch v := res.(type) {
	case map[interface{}]interface{}:
		return parseRESP3(v)
	case []interface{}:
		return parseRESP2(v)
	default:
		return []agent.ScoredDocument{}, nil
	}
}

// Remember stores a fact for the given identifier with an embedding vector
// computed by the configured embedder.
//
// Returns an error if identifier or fact is empty, if the embedder fails,
// or if the Redis HSET command fails.
func (s *Store) Remember(ctx context.Context, identifier, fact string, metadata map[string]string) error {
	return s.adapter.Remember(ctx, identifier, fact, metadata)
}

// Recall retrieves the top entries for the given identifier by semantic
// similarity to the query.
//
// Returns at most limit results ordered by descending similarity score.
// Returns an empty non-nil slice when no entries match.
// Returns an error if identifier is empty, limit < 1, the embedder fails,
// or the FT.SEARCH command fails.
func (s *Store) Recall(ctx context.Context, identifier, query string, limit int) ([]memory.Entry, error) {
	return s.adapter.Recall(ctx, identifier, query, limit)
}

// Close closes the underlying Redis client connection.
func (s *Store) Close() error {
	return s.client.Close()
}

// ---------------------------------------------------------------------------
// Helpers (copied from rag/redis/vectorstore.go)
// ---------------------------------------------------------------------------

// float64sToFloat32Bytes converts a []float64 slice to a little-endian float32 binary blob.
func float64sToFloat32Bytes(v []float64) []byte {
	buf := make([]byte, len(v)*4)
	for i, f := range v {
		bits := math.Float32bits(float32(f))
		binary.LittleEndian.PutUint32(buf[i*4:], bits)
	}
	return buf
}

// escapeTag escapes RediSearch TAG special characters with a backslash so that
// arbitrary strings can be used safely in @field:{...} queries.
func escapeTag(s string) string {
	const special = `,.<>{}[]"':;!@#$%^&*()-+=~/ `
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if strings.ContainsRune(special, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// parseRESP3 handles the map-based response from Redis Stack 7+ / RESP3.
func parseRESP3(m map[interface{}]interface{}) ([]agent.ScoredDocument, error) {
	resultsRaw, ok := m["results"]
	if !ok {
		return []agent.ScoredDocument{}, nil
	}
	items, ok := resultsRaw.([]interface{})
	if !ok || len(items) == 0 {
		return []agent.ScoredDocument{}, nil
	}

	var scored []agent.ScoredDocument
	for _, item := range items {
		entry, ok := item.(map[interface{}]interface{})
		if !ok {
			continue
		}
		attrsRaw, ok := entry["extra_attributes"]
		if !ok {
			continue
		}
		attrs, ok := attrsRaw.(map[interface{}]interface{})
		if !ok {
			continue
		}

		content, _ := attrs["content"].(string)
		metadataJSON, _ := attrs["metadata"].(string)
		scoreStr, _ := attrs["score"].(string)

		distance, err := strconv.ParseFloat(scoreStr, 64)
		if err != nil {
			continue
		}
		similarity := 1 - distance

		var metadata map[string]string
		if metadataJSON != "" {
			_ = json.Unmarshal([]byte(metadataJSON), &metadata)
		}

		scored = append(scored, agent.ScoredDocument{
			Document: agent.Document{
				Content:  content,
				Metadata: metadata,
			},
			Score: similarity,
		})
	}

	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})

	if scored == nil {
		return []agent.ScoredDocument{}, nil
	}
	return scored, nil
}

// parseRESP2 handles the flat array response from RESP2 connections.
// Format: [total, key1, [field, val, ...], key2, [field, val, ...], ...]
func parseRESP2(results []interface{}) ([]agent.ScoredDocument, error) {
	if len(results) < 1 {
		return []agent.ScoredDocument{}, nil
	}

	var scored []agent.ScoredDocument
	for i := 1; i+1 < len(results); i += 2 {
		fields, ok := results[i+1].([]interface{})
		if !ok {
			continue
		}

		var content string
		var metadataJSON string
		var scoreStr string

		for j := 0; j+1 < len(fields); j += 2 {
			fieldName, _ := fields[j].(string)
			fieldVal, _ := fields[j+1].(string)
			switch fieldName {
			case "content":
				content = fieldVal
			case "metadata":
				metadataJSON = fieldVal
			case "score":
				scoreStr = fieldVal
			}
		}

		distance, err := strconv.ParseFloat(scoreStr, 64)
		if err != nil {
			continue
		}
		similarity := 1 - distance

		var metadata map[string]string
		if metadataJSON != "" {
			_ = json.Unmarshal([]byte(metadataJSON), &metadata)
		}

		scored = append(scored, agent.ScoredDocument{
			Document: agent.Document{
				Content:  content,
				Metadata: metadata,
			},
			Score: similarity,
		})
	}

	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})

	if scored == nil {
		return []agent.ScoredDocument{}, nil
	}
	return scored, nil
}
