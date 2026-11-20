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

	"github.com/camilbinas/gude-agents/agent/rag"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

// Compile-time check: Store implements rag.Manager.
var _ rag.Manager = (*Store)(nil)

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

// StoreOption configures a Store instance.
type StoreOption func(*Store)

// WithHNSWM sets the HNSW M parameter. Default: 16.
func WithHNSWM(m int) StoreOption {
	return func(s *Store) {
		s.hnswM = m
	}
}

// WithHNSWEFConstruction sets the HNSW EF_CONSTRUCTION parameter. Default: 200.
func WithHNSWEFConstruction(ef int) StoreOption {
	return func(s *Store) {
		s.hnswEF = ef
	}
}

// WithDropExisting drops the index and its documents before creating a fresh
// one. Useful for examples and development where you want a clean slate on
// every run. Do not use in production — it deletes all indexed data.
func WithDropExisting() StoreOption {
	return func(s *Store) {
		s.dropExisting = true
	}
}

// Store implements rag.Store using Redis Stack (RediSearch).
type Store struct {
	client       *goredis.Client
	indexName    string
	dim          int
	hnswM        int
	hnswEF       int
	dropExisting bool
}

// New creates a new Store. Pings Redis, then creates the HNSW index via
// FT.CREATE if it doesn't already exist. dim must be >= 1 and indexName must
// be non-empty.
func New(opts Options, indexName string, dim int, vopts ...StoreOption) (*Store, error) {
	if dim < 1 {
		return nil, fmt.Errorf("redis vectorstore: dim must be >= 1, got %d", dim)
	}
	if indexName == "" {
		return nil, fmt.Errorf("redis vectorstore: indexName must not be empty")
	}
	client := newClient(opts)

	s := &Store{
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

// documentKey converts a public logical document ID to its Redis hash key.
func (s *Store) documentKey(id string) string {
	return s.indexName + ":" + id
}

// documentID converts an indexed Redis hash key back to its public logical ID.
func (s *Store) documentID(key string) string {
	return strings.TrimPrefix(key, s.indexName+":")
}

// decodeMetadata unmarshals a stored metadata JSON string; an empty string
// yields a nil map, a malformed string an error.
func decodeMetadata(metadataJSON string) (map[string]string, error) {
	if metadataJSON == "" {
		return nil, nil
	}
	var metadata map[string]string
	if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
		return nil, fmt.Errorf("decode metadata: %w", err)
	}
	return metadata, nil
}

// validateEmbedding checks that an embedding matches the store's dimension and
// contains only finite values.
func (s *Store) validateEmbedding(embedding []float64) error {
	if len(embedding) != s.dim {
		return fmt.Errorf("embedding dimension is %d, want %d", len(embedding), s.dim)
	}
	for i, v := range embedding {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("embedding value %d must be finite", i)
		}
	}
	return nil
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
func (s *Store) Upsert(ctx context.Context, docs []rag.Document, embeddings [][]float64) ([]string, error) {
	if len(docs) != len(embeddings) {
		return nil, fmt.Errorf("redis vectorstore: docs and embeddings length mismatch: %d vs %d", len(docs), len(embeddings))
	}
	if len(docs) == 0 {
		return []string{}, nil
	}

	// Validate and encode everything first, then write all hashes in one
	// MULTI/EXEC so a failure cannot leave the batch partially stored.
	ids := make([]string, len(docs))
	pipe := s.client.TxPipeline()
	for i, doc := range docs {
		if err := s.validateEmbedding(embeddings[i]); err != nil {
			return nil, fmt.Errorf("redis vectorstore: upsert: document %d: %w", i, err)
		}
		metaJSON, err := json.Marshal(doc.Metadata)
		if err != nil {
			return nil, fmt.Errorf("redis vectorstore: upsert: %w", err)
		}

		id := doc.ID
		if id == "" {
			id = uuid.New().String()
		}
		ids[i] = id
		pipe.HSet(ctx, s.documentKey(id), map[string]interface{}{
			"content":   doc.Content,
			"metadata":  string(metaJSON),
			"embedding": float64sToFloat32Bytes(embeddings[i]),
		})
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("redis vectorstore: upsert: %w", err)
	}

	return ids, nil
}

// Search performs KNN similarity search using FT.SEARCH.
func (s *Store) Search(ctx context.Context, queryEmbedding []float64, topK int) ([]rag.ScoredDocument, error) {
	if topK < 1 {
		return nil, fmt.Errorf("redis vectorstore: topK must be >= 1, got %d", topK)
	}
	if err := s.validateEmbedding(queryEmbedding); err != nil {
		return nil, fmt.Errorf("redis vectorstore: search: %w", err)
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
func (s *Store) parseRESP3(m map[interface{}]interface{}) ([]rag.ScoredDocument, error) {
	resultsRaw, ok := m["results"]
	if !ok {
		return nil, nil
	}
	items, ok := resultsRaw.([]interface{})
	if !ok || len(items) == 0 {
		return nil, nil
	}

	var scored []rag.ScoredDocument
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

		metadata, err := decodeMetadata(metadataJSON)
		if err != nil {
			return nil, fmt.Errorf("redis vectorstore: document %q: %w", s.documentID(key), err)
		}

		scored = append(scored, rag.ScoredDocument{
			Document: rag.Document{
				ID:       s.documentID(key),
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
func (s *Store) parseRESP2(results []interface{}) ([]rag.ScoredDocument, error) {
	if len(results) < 1 {
		return nil, nil
	}

	var scored []rag.ScoredDocument
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

		metadata, err := decodeMetadata(metadataJSON)
		if err != nil {
			return nil, fmt.Errorf("redis vectorstore: document %q: %w", s.documentID(key), err)
		}

		scored = append(scored, rag.ScoredDocument{
			Document: rag.Document{
				ID:       s.documentID(key),
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
func (s *Store) Find(ctx context.Context, ids ...string) ([]rag.Document, error) {
	if len(ids) == 0 {
		return []rag.Document{}, nil
	}

	pipe := s.client.Pipeline()
	cmds := make([]*goredis.MapStringStringCmd, len(ids))
	for i, id := range ids {
		cmds[i] = pipe.HGetAll(ctx, s.documentKey(id))
	}

	_, err := pipe.Exec(ctx)
	if err != nil && err != goredis.Nil {
		return nil, fmt.Errorf("redis vectorstore: find: %w", err)
	}

	var docs []rag.Document
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

		metadata, err := decodeMetadata(metadataJSON)
		if err != nil {
			return nil, fmt.Errorf("redis vectorstore: find: document %q: %w", ids[i], err)
		}

		docs = append(docs, rag.Document{
			ID:       ids[i],
			Content:  content,
			Metadata: metadata,
		})
	}

	if docs == nil {
		return []rag.Document{}, nil
	}
	return docs, nil
}

// ErrUnsafeFilterValue is returned by DeleteByMetadata for a filter key/value
// containing a double quote, colon, or backslash.
var ErrUnsafeFilterValue = fmt.Errorf("redis vectorstore: unsafe filter key/value")

const (
	deleteByMetadataPageSize    = 1000
	deleteByMetadataDeleteBatch = 1000
)

// DeleteByMetadata deletes all documents whose metadata contains all key-value
// pairs in the filter (AND semantics). Returns an error if the filter is empty.
//
// FT.SEARCH over the metadata TEXT field only selects candidates, because TEXT
// matching is tokenized and can over-match. Every candidate page is read (not
// capped), its stored metadata is decoded, and a document is deleted only when
// the decoded metadata contains every filter pair exactly. Filter keys/values
// containing a double quote, colon, or backslash are rejected with
// ErrUnsafeFilterValue.
func (s *Store) DeleteByMetadata(ctx context.Context, filter map[string]string) error {
	if len(filter) == 0 {
		return fmt.Errorf("vectorstore: filter must not be empty")
	}

	var clauses []string
	for k, v := range filter {
		if !safeFilterComponent(k) || !safeFilterComponent(v) {
			return fmt.Errorf("%w: key %q value %q (contains \" : or \\); delete by ID instead", ErrUnsafeFilterValue, k, v)
		}
		term := escapeRedisSearchTerm(fmt.Sprintf(`"%s":"%s"`, k, v))
		clauses = append(clauses, fmt.Sprintf("@metadata:(%s)", term))
	}
	// Sort clauses for deterministic query construction (helps testing).
	sort.Strings(clauses)
	query := strings.Join(clauses, " ")

	// Collect every match before deleting: deletions shift result offsets.
	var keys []string
	for offset := 0; ; offset += deleteByMetadataPageSize {
		res, err := s.client.Do(ctx, "FT.SEARCH", s.indexName,
			query,
			"RETURN", "1", "metadata",
			"LIMIT", strconv.Itoa(offset), strconv.Itoa(deleteByMetadataPageSize),
			"DIALECT", "2",
		).Result()
		if err != nil {
			return fmt.Errorf("redis vectorstore: delete by metadata: search: %w", err)
		}
		hits := parseSearchMetadata(res)
		for _, hit := range hits {
			if metadataMatches(hit.metadata, filter) {
				keys = append(keys, hit.key)
			}
		}
		if len(hits) < deleteByMetadataPageSize {
			break
		}
	}

	for start := 0; start < len(keys); start += deleteByMetadataDeleteBatch {
		end := min(start+deleteByMetadataDeleteBatch, len(keys))
		if err := s.client.Del(ctx, keys[start:end]...).Err(); err != nil {
			return fmt.Errorf("redis vectorstore: delete by metadata: del: %w", err)
		}
	}
	return nil
}

// metadataMatches reports whether the JSON metadata contains every filter pair
// with exactly equal values. Metadata that does not decode never matches.
func metadataMatches(metadataJSON string, filter map[string]string) bool {
	metadata, err := decodeMetadata(metadataJSON)
	if err != nil {
		return false
	}
	for k, v := range filter {
		got, ok := metadata[k]
		if !ok || got != v {
			return false
		}
	}
	return true
}

type searchHit struct {
	key      string
	metadata string
}

// parseSearchMetadata extracts document keys and their returned metadata field
// from an FT.SEARCH ... RETURN 1 metadata response (RESP2 or RESP3).
func parseSearchMetadata(res interface{}) []searchHit {
	str := func(v interface{}) string { s, _ := v.(string); return s }
	var hits []searchHit
	switch v := res.(type) {
	case map[interface{}]interface{}:
		items, _ := v["results"].([]interface{})
		for _, item := range items {
			doc, ok := item.(map[interface{}]interface{})
			if !ok {
				continue
			}
			attrs, _ := doc["extra_attributes"].(map[interface{}]interface{})
			if key := str(doc["id"]); key != "" {
				hits = append(hits, searchHit{key: key, metadata: str(attrs["metadata"])})
			}
		}
	case map[string]interface{}:
		items, _ := v["results"].([]interface{})
		for _, item := range items {
			doc, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			attrs, _ := doc["extra_attributes"].(map[string]interface{})
			if key := str(doc["id"]); key != "" {
				hits = append(hits, searchHit{key: key, metadata: str(attrs["metadata"])})
			}
		}
	case []interface{}:
		// [total, key1, [field, value, ...], key2, [...], ...]
		for i := 1; i+1 < len(v); i += 2 {
			key := str(v[i])
			fields, _ := v[i+1].([]interface{})
			if key == "" {
				continue
			}
			hit := searchHit{key: key}
			for j := 0; j+1 < len(fields); j += 2 {
				if str(fields[j]) == "metadata" {
					hit.metadata = str(fields[j+1])
				}
			}
			hits = append(hits, hit)
		}
	}
	return hits
}

// safeFilterComponent reports whether a filter key/value is free of double
// quote, colon, and backslash.
func safeFilterComponent(s string) bool {
	return !strings.ContainsAny(s, "\":\\")
}

// escapeRedisSearchTerm escapes special RediSearch characters in a search term.
func escapeRedisSearchTerm(s string) string {
	// Escape backslash first so escapes added below are not re-escaped.
	result := strings.ReplaceAll(s, `\`, `\\`)
	special := []string{",", ".", "<", ">", "{", "}", "[", "]", "\"", "'", ":", ";", "!", "@", "#", "$", "%", "^", "&", "*", "(", ")", "-", "+", "=", "~", "/", "|"}
	for _, ch := range special {
		result = strings.ReplaceAll(result, ch, `\`+ch)
	}
	return result
}

// Delete removes documents by their logical IDs.
func (s *Store) Delete(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = s.documentKey(id)
	}
	if err := s.client.Del(ctx, keys...).Err(); err != nil {
		return fmt.Errorf("redis vectorstore: delete: %w", err)
	}
	return nil
}

// Close closes the underlying Redis client.
func (s *Store) Close() error {
	return s.client.Close()
}
