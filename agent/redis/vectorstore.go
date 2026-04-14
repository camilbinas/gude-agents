package redis

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

// Compile-time check: RedisVectorStore implements agent.VectorStore.
var _ agent.VectorStore = (*RedisVectorStore)(nil)

// RedisVectorStoreOption configures a RedisVectorStore instance.
type RedisVectorStoreOption func(*RedisVectorStore)

// WithHNSWM sets the HNSW M parameter. Default: 16.
func WithHNSWM(m int) RedisVectorStoreOption {
	return func(s *RedisVectorStore) {
		s.hnswM = m
	}
}

// WithHNSWEFConstruction sets the HNSW EF_CONSTRUCTION parameter. Default: 200.
func WithHNSWEFConstruction(ef int) RedisVectorStoreOption {
	return func(s *RedisVectorStore) {
		s.hnswEF = ef
	}
}

// RedisVectorStore implements agent.VectorStore using Redis Stack (RediSearch).
// Documented in docs/redis.md — update when changing constructor, options, or HNSW config.
type RedisVectorStore struct {
	client    *goredis.Client
	indexName string
	dim       int
	hnswM     int
	hnswEF    int
}

// NewRedisVectorStore creates a new RedisVectorStore. Pings Redis, then
// creates the HNSW index via FT.CREATE if it doesn't already exist.
func NewRedisVectorStore(opts RedisOptions, indexName string, dim int, vopts ...RedisVectorStoreOption) (*RedisVectorStore, error) {
	client := newClient(opts)

	s := &RedisVectorStore{
		client:    client,
		indexName: indexName,
		dim:       dim,
		hnswM:     16,
		hnswEF:    200,
	}

	for _, o := range vopts {
		o(s)
	}

	if err := client.Ping(context.Background()).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("redis vectorstore: ping: %w", err)
	}

	// Create the HNSW index. Silently ignore "Index already exists" errors.
	err := client.Do(context.Background(), "FT.CREATE", indexName,
		"ON", "HASH",
		"PREFIX", "1", indexName+":",
		"SCHEMA",
		"content", "TEXT",
		"metadata", "TEXT",
		"embedding", "VECTOR", "HNSW", "6",
		"TYPE", "FLOAT32",
		"DIM", dim,
		"DISTANCE_METRIC", "COSINE",
		"M", s.hnswM,
		"EF_CONSTRUCTION", s.hnswEF,
	).Err()
	if err != nil && !strings.Contains(err.Error(), "Index already exists") {
		_ = client.Close()
		return nil, fmt.Errorf("redis vectorstore: create index: %w", err)
	}

	return s, nil
}

// float64sToFloat32Bytes converts a []float64 slice to a little-endian float32 binary blob.
func float64sToFloat32Bytes(v []float64) []byte {
	buf := make([]byte, len(v)*4)
	for i, f := range v {
		bits := math.Float32bits(float32(f))
		binary.LittleEndian.PutUint32(buf[i*4:], bits)
	}
	return buf
}

// Add stores documents and their embeddings as Redis hashes.
func (s *RedisVectorStore) Add(ctx context.Context, docs []agent.Document, embeddings [][]float64) error {
	if len(docs) != len(embeddings) {
		return fmt.Errorf("redis vectorstore: docs and embeddings length mismatch: %d vs %d", len(docs), len(embeddings))
	}
	if len(docs) == 0 {
		return nil
	}

	for i, doc := range docs {
		metaJSON, err := json.Marshal(doc.Metadata)
		if err != nil {
			return fmt.Errorf("redis vectorstore: add: %w", err)
		}

		key := s.indexName + ":" + uuid.New().String()
		embeddingBytes := float64sToFloat32Bytes(embeddings[i])

		err = s.client.HSet(ctx, key, map[string]interface{}{
			"content":   doc.Content,
			"metadata":  string(metaJSON),
			"embedding": embeddingBytes,
		}).Err()
		if err != nil {
			return fmt.Errorf("redis vectorstore: add: %w", err)
		}
	}

	return nil
}

// Search performs KNN similarity search using FT.SEARCH.
func (s *RedisVectorStore) Search(ctx context.Context, queryEmbedding []float64, topK int) ([]agent.ScoredDocument, error) {
	if topK < 1 {
		return nil, fmt.Errorf("redis vectorstore: topK must be >= 1, got %d", topK)
	}

	blob := float64sToFloat32Bytes(queryEmbedding)
	query := fmt.Sprintf("*=>[KNN %d @embedding $BLOB AS score]", topK)

	res, err := s.client.Do(ctx, "FT.SEARCH", s.indexName,
		query,
		"PARAMS", "2", "BLOB", blob,
		"SORTBY", "score",
		"LIMIT", "0", fmt.Sprintf("%d", topK),
		"DIALECT", "2",
	).Result()
	if err != nil {
		return nil, fmt.Errorf("redis vectorstore: search: %w", err)
	}

	// Parse FT.SEARCH response: [totalCount, key1, fields1, key2, fields2, ...]
	results, ok := res.([]interface{})
	if !ok || len(results) < 1 {
		return nil, nil
	}

	var scored []agent.ScoredDocument
	// Skip first element (total count), then iterate pairs of (key, fields)
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

	// Sort by descending similarity (should already be sorted, but be explicit)
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})

	return scored, nil
}

// Close closes the underlying Redis client.
func (s *RedisVectorStore) Close() error {
	return s.client.Close()
}
