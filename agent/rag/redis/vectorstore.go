// Package redis provides a Redis Stack (RediSearch) vector store for use with
// the gude-agents RAG pipeline.
//
// Requires Redis Stack — NOT standard community Redis. The vector store uses
// RediSearch commands (FT.CREATE, FT.SEARCH) that are only available in
// Redis Stack.
package redis

import (
	"context"
	"crypto/tls"
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

// Compile-time check: VectorStore implements agent.VectorStoreManager.
var _ agent.VectorStoreManager = (*VectorStore)(nil)

// Options holds Redis connection configuration.
type Options struct {
	Addr      string // Default: "127.0.0.1:6379"
	Password  string
	DB        int         // Default: 0
	TLSConfig *tls.Config // Optional
}

// newClient creates a go-redis client from Options, applying defaults.
func newClient(opts Options) *goredis.Client {
	addr := opts.Addr
	if addr == "" {
		addr = "127.0.0.1:6379"
	}
	return goredis.NewClient(&goredis.Options{
		Addr:      addr,
		Password:  opts.Password,
		DB:        opts.DB,
		TLSConfig: opts.TLSConfig,
	})
}

// VectorStoreOption configures a VectorStore instance.
type VectorStoreOption func(*VectorStore)

// WithHNSWM sets the HNSW M parameter. Default: 16.
func WithHNSWM(m int) VectorStoreOption {
	return func(s *VectorStore) {
		s.hnswM = m
	}
}

// WithHNSWEFConstruction sets the HNSW EF_CONSTRUCTION parameter. Default: 200.
func WithHNSWEFConstruction(ef int) VectorStoreOption {
	return func(s *VectorStore) {
		s.hnswEF = ef
	}
}

// WithDropExisting drops the index and its documents before creating a fresh
// one. Useful for examples and development where you want a clean slate on
// every run. Do not use in production — it deletes all indexed data.
func WithDropExisting() VectorStoreOption {
	return func(s *VectorStore) {
		s.dropExisting = true
	}
}

// VectorStore implements agent.VectorStore using Redis Stack (RediSearch).
type VectorStore struct {
	client       *goredis.Client
	indexName    string
	dim          int
	hnswM        int
	hnswEF       int
	dropExisting bool
}

// New creates a new VectorStore. Pings Redis, then creates the HNSW index via
// FT.CREATE if it doesn't already exist.
func New(opts Options, indexName string, dim int, vopts ...VectorStoreOption) (*VectorStore, error) {
	client := newClient(opts)

	s := &VectorStore{
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
	if s.dropExisting {
		// FT.DROPINDEX with DD deletes the index and all associated hashes.
		_ = client.Do(context.Background(), "FT.DROPINDEX", indexName, "DD").Err()
	}

	err := client.Do(context.Background(), "FT.CREATE", indexName,
		"ON", "HASH",
		"PREFIX", "1", indexName+":",
		"SCHEMA",
		"content", "TEXT",
		"metadata", "TEXT",
		"embedding", "VECTOR", "HNSW", "10",
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

// Upsert stores documents and their embeddings as Redis hashes. If a document's
// ID is empty, the store generates one. If a document's ID already exists, the
// store replaces the existing content, metadata, and embedding (HSET is
// natively an upsert operation in Redis).
// Returns the IDs of all stored documents in input order.
func (s *VectorStore) Upsert(ctx context.Context, docs []agent.Document, embeddings [][]float64) ([]string, error) {
	if len(docs) != len(embeddings) {
		return nil, fmt.Errorf("redis vectorstore: docs and embeddings length mismatch: %d vs %d", len(docs), len(embeddings))
	}
	if len(docs) == 0 {
		return []string{}, nil
	}

	ids := make([]string, len(docs))
	for i, doc := range docs {
		metaJSON, err := json.Marshal(doc.Metadata)
		if err != nil {
			return nil, fmt.Errorf("redis vectorstore: upsert: %w", err)
		}

		id := doc.ID
		if id == "" {
			id = uuid.New().String()
		}
		key := s.indexName + ":" + id
		ids[i] = key
		embeddingBytes := float64sToFloat32Bytes(embeddings[i])

		err = s.client.HSet(ctx, key, map[string]interface{}{
			"content":   doc.Content,
			"metadata":  string(metaJSON),
			"embedding": embeddingBytes,
		}).Err()
		if err != nil {
			return nil, fmt.Errorf("redis vectorstore: upsert: %w", err)
		}
	}

	return ids, nil
}

// Search performs KNN similarity search using FT.SEARCH.
func (s *VectorStore) Search(ctx context.Context, queryEmbedding []float64, topK int) ([]agent.ScoredDocument, error) {
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

	// go-redis v9 returns RESP3 map format from Redis Stack 7+.
	// Older versions or RESP2 connections return a flat []interface{}.
	// Handle both.
	switch v := res.(type) {
	case map[interface{}]interface{}:
		return s.parseRESP3(v)
	case []interface{}:
		return s.parseRESP2(v)
	default:
		return nil, nil
	}
}

// parseRESP3 handles the map-based response from Redis Stack 7+ / RESP3.
//
//	{
//	  "total_results": int64,
//	  "results": [ { "id": ..., "extra_attributes": { "content": ..., "score": ... } }, ... ]
//	}
func (s *VectorStore) parseRESP3(m map[interface{}]interface{}) ([]agent.ScoredDocument, error) {
	resultsRaw, ok := m["results"]
	if !ok {
		return nil, nil
	}
	items, ok := resultsRaw.([]interface{})
	if !ok || len(items) == 0 {
		return nil, nil
	}

	var scored []agent.ScoredDocument
	for _, item := range items {
		entry, ok := item.(map[interface{}]interface{})
		if !ok {
			continue
		}

		key, _ := entry["id"].(string)

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
				ID:       key,
				Content:  content,
				Metadata: metadata,
			},
			Score: similarity,
		})
	}

	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})
	return scored, nil
}

// parseRESP2 handles the flat array response from RESP2 connections.
// Format: [total, key1, [field, val, ...], key2, [field, val, ...], ...]
func (s *VectorStore) parseRESP2(results []interface{}) ([]agent.ScoredDocument, error) {
	if len(results) < 1 {
		return nil, nil
	}

	var scored []agent.ScoredDocument
	for i := 1; i+1 < len(results); i += 2 {
		key, _ := results[i].(string)

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
				ID:       key,
				Content:  content,
				Metadata: metadata,
			},
			Score: similarity,
		})
	}

	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})

	return scored, nil
}

// Find retrieves documents by their IDs. It uses a Redis pipeline to batch
// HGETALL calls for each requested key. Keys that don't exist are omitted from
// the result. Returns documents in the same order as the input IDs.
// Returns an empty slice and nil error for an empty input.
func (s *VectorStore) Find(ctx context.Context, ids ...string) ([]agent.Document, error) {
	if len(ids) == 0 {
		return []agent.Document{}, nil
	}

	pipe := s.client.Pipeline()
	cmds := make([]*goredis.MapStringStringCmd, len(ids))
	for i, id := range ids {
		key := s.indexName + ":" + id
		cmds[i] = pipe.HGetAll(ctx, key)
	}

	_, err := pipe.Exec(ctx)
	if err != nil && err != goredis.Nil {
		return nil, fmt.Errorf("redis vectorstore: find: %w", err)
	}

	var docs []agent.Document
	for i, cmd := range cmds {
		result, err := cmd.Result()
		if err != nil {
			continue
		}
		if len(result) == 0 {
			continue
		}

		content := result["content"]
		metadataJSON := result["metadata"]

		var metadata map[string]string
		if metadataJSON != "" {
			_ = json.Unmarshal([]byte(metadataJSON), &metadata)
		}

		docs = append(docs, agent.Document{
			ID:       ids[i],
			Content:  content,
			Metadata: metadata,
		})
	}

	if docs == nil {
		return []agent.Document{}, nil
	}
	return docs, nil
}

// DeleteByMetadata deletes all documents whose metadata contains all key-value
// pairs in the filter (AND semantics). Uses FT.SEARCH to find matching documents,
// then DEL to remove them. Returns an error if the filter is empty.
func (s *VectorStore) DeleteByMetadata(ctx context.Context, filter map[string]string) error {
	if len(filter) == 0 {
		return fmt.Errorf("vectorstore: filter must not be empty")
	}

	// Build a FT.SEARCH query that matches all filter k/v pairs in the metadata
	// TEXT field. Since metadata is stored as a JSON string (e.g. {"source":"readme.md"}),
	// we search for each "key":"value" substring in the field.
	var clauses []string
	for k, v := range filter {
		// Escape special RediSearch characters in the search term.
		term := escapeRedisSearchTerm(fmt.Sprintf(`"%s":"%s"`, k, v))
		clauses = append(clauses, fmt.Sprintf("@metadata:(%s)", term))
	}
	// Sort clauses for deterministic query construction (helps testing).
	sort.Strings(clauses)
	query := strings.Join(clauses, " ")

	// Use a large LIMIT to get all matching documents.
	res, err := s.client.Do(ctx, "FT.SEARCH", s.indexName,
		query,
		"NOCONTENT",
		"LIMIT", "0", "10000",
		"DIALECT", "2",
	).Result()
	if err != nil {
		return fmt.Errorf("redis vectorstore: delete by metadata: search: %w", err)
	}

	// Parse keys from the search results (handle both RESP2 and RESP3 formats).
	keys := s.parseSearchKeys(res)
	if len(keys) == 0 {
		return nil
	}

	// Delete all matching keys.
	if err := s.client.Del(ctx, keys...).Err(); err != nil {
		return fmt.Errorf("redis vectorstore: delete by metadata: del: %w", err)
	}
	return nil
}

// escapeRedisSearchTerm escapes special RediSearch characters in a search term.
func escapeRedisSearchTerm(s string) string {
	// RediSearch special characters that need escaping.
	special := []string{",", ".", "<", ">", "{", "}", "[", "]", "\"", "'", ":", ";", "!", "@", "#", "$", "%", "^", "&", "*", "(", ")", "-", "+", "=", "~", "/", "|"}
	result := s
	for _, ch := range special {
		result = strings.ReplaceAll(result, ch, `\`+ch)
	}
	return result
}

// parseSearchKeys extracts document keys from an FT.SEARCH NOCONTENT response.
// Handles both RESP2 (flat array) and RESP3 (map) formats.
func (s *VectorStore) parseSearchKeys(res interface{}) []string {
	switch v := res.(type) {
	case map[interface{}]interface{}:
		return s.parseSearchKeysRESP3(v)
	case []interface{}:
		return s.parseSearchKeysRESP2(v)
	default:
		return nil
	}
}

// parseSearchKeysRESP3 extracts keys from a RESP3 map response.
// Format: {"total_results": N, "results": [{"id": "key1"}, {"id": "key2"}, ...]}
func (s *VectorStore) parseSearchKeysRESP3(m map[interface{}]interface{}) []string {
	resultsRaw, ok := m["results"]
	if !ok {
		return nil
	}
	items, ok := resultsRaw.([]interface{})
	if !ok || len(items) == 0 {
		return nil
	}

	var keys []string
	for _, item := range items {
		entry, ok := item.(map[interface{}]interface{})
		if !ok {
			continue
		}
		key, _ := entry["id"].(string)
		if key != "" {
			keys = append(keys, key)
		}
	}
	return keys
}

// parseSearchKeysRESP2 extracts keys from a RESP2 flat array response.
// NOCONTENT format: [total, key1, key2, ...]
func (s *VectorStore) parseSearchKeysRESP2(results []interface{}) []string {
	if len(results) < 2 {
		return nil
	}

	var keys []string
	// With NOCONTENT, the format is [total, key1, key2, ...] (no field arrays).
	for i := 1; i < len(results); i++ {
		key, ok := results[i].(string)
		if ok && key != "" {
			keys = append(keys, key)
		}
	}
	return keys
}

// Delete removes documents by their Redis keys.
func (s *VectorStore) Delete(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	if err := s.client.Del(ctx, ids...).Err(); err != nil {
		return fmt.Errorf("redis vectorstore: delete: %w", err)
	}
	return nil
}

// Close closes the underlying Redis client.
func (s *VectorStore) Close() error {
	return s.client.Close()
}
