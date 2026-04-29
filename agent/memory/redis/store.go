package redis

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/memory"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

// Store is a Redis memory store that maps Go struct fields to Redis HASH
// fields using `db` struct tags. Requires Redis Stack (RediSearch).
type Store[T any] struct {
	client    *goredis.Client
	indexName string
	keyPrefix string
	dim       int
	embedder  agent.Embedder
	schema    *redisSchema
}

// StoreOption configures a Store.
type StoreOption func(*storeConfig)

type storeConfig struct {
	indexName    string
	keyPrefix    string
	hnswM        int
	hnswEF       int
	dropExisting bool
}

// WithIndexName sets the RediSearch index name.
func WithIndexName(name string) StoreOption {
	return func(c *storeConfig) {
		if name != "" {
			c.indexName = name
		}
	}
}

// WithKeyPrefix sets the Redis key prefix.
func WithKeyPrefix(prefix string) StoreOption {
	return func(c *storeConfig) {
		if prefix != "" {
			c.keyPrefix = prefix
		}
	}
}

// WithDropExisting is deprecated and will be removed in a future release.
// Use ForgetAll to clear entries for a specific identifier, or manage index
// lifecycle externally via FT.DROPINDEX.
//
// Deprecated: Manage index lifecycle externally.
func WithDropExisting() StoreOption {
	return func(c *storeConfig) {
		c.dropExisting = true
	}
}

// redisFieldType is the RediSearch field type.
type redisFieldType int

const (
	fieldTAG redisFieldType = iota
	fieldNUMERIC
	fieldTEXT
)

// redisFieldInfo describes a struct field → HASH field mapping.
type redisFieldInfo struct {
	FieldIndex int
	HashField  string
	FieldType  redisFieldType
	IsJSONB    bool
	NoInput    bool
	IsPK       bool
	IsIdent    bool
	IsContent  bool
}

// redisSchema holds the parsed schema for a struct type.
type redisSchema struct {
	Fields        []redisFieldInfo
	PKIdx         int // -1 if none
	IdentifierIdx int // -1 if none
	ContentIdx    int // -1 if none
}

// NewStore creates a Store for the given struct type T.
func NewStore[T any](opts Options, embedder agent.Embedder, dim int, sopts ...StoreOption) (*Store[T], error) {
	if embedder == nil {
		return nil, errors.New("redis: embedder is required")
	}
	if dim < 1 {
		return nil, errors.New("redis: dim must be at least 1")
	}

	cfg := &storeConfig{
		indexName: "gude_typed_idx",
		keyPrefix: "gude:typed:",
		hnswM:     16,
		hnswEF:    200,
	}
	for _, o := range sopts {
		o(cfg)
	}

	// Parse schema from struct tags.
	schema, err := parseRedisSchema[T]()
	if err != nil {
		return nil, err
	}

	// Create Redis client.
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
		return nil, fmt.Errorf("redis: ping: %w", err)
	}

	// Drop existing index if requested.
	if cfg.dropExisting {
		_ = client.Do(context.Background(), "FT.DROPINDEX", cfg.indexName, "DD").Err()
	}

	// Build FT.CREATE args.
	createArgs := buildFTCreate(cfg, schema, dim)
	err = client.Do(context.Background(), createArgs...).Err()
	if err != nil && !strings.Contains(err.Error(), "Index already exists") {
		_ = client.Close()
		return nil, fmt.Errorf("redis: create index: %w", err)
	}

	return &Store[T]{
		client:    client,
		indexName: cfg.indexName,
		keyPrefix: cfg.keyPrefix,
		dim:       dim,
		embedder:  embedder,
		schema:    schema,
	}, nil
}

// Remember stores a value for the given identifier.
func (s *Store[T]) Remember(ctx context.Context, identifier string, value T) error {
	if identifier == "" {
		return errors.New("redis: identifier must not be empty")
	}

	// Set identifier on the struct.
	setRedisIdentifier(&value, s.schema, identifier)

	// Extract content for embedding.
	content := s.extractContent(value)
	if content == "" {
		return errors.New("redis: content field is empty")
	}

	// Embed.
	embedding, err := s.embedder.Embed(ctx, content)
	if err != nil {
		return fmt.Errorf("redis: embed: %w", err)
	}

	// Build HASH fields.
	fields := s.buildHashFields(value)
	fields["embedding"] = float64sToFloat32Bytes(embedding)

	// Generate key.
	key := s.keyPrefix + s.extractPK(value)

	if err := s.client.HSet(ctx, key, fields).Err(); err != nil {
		return fmt.Errorf("redis: hset: %w", err)
	}

	return nil
}

// Recall retrieves values by semantic similarity to the query, scoped to the
// identifier. Supports filtering and sorting via RecallOption.
//
// Implements memory.Memory[T].
func (s *Store[T]) Recall(ctx context.Context, identifier string, query string, limit int, opts ...memory.RecallOption) ([]memory.Entry[T], error) {
	if identifier == "" {
		return nil, errors.New("redis: identifier must not be empty")
	}
	if limit < 1 {
		return nil, errors.New("redis: limit must be at least 1")
	}

	// Embed query.
	embedding, err := s.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("redis: embed query: %w", err)
	}

	// Apply options.
	rc := &recallConfig{}
	for _, o := range opts {
		if ro, ok := o.(RecallOption); ok {
			ro(rc)
		}
	}

	// Build FT.SEARCH query.
	identField := s.schema.Fields[s.schema.IdentifierIdx].HashField
	filterStr := s.buildFilterQuery(rc, identField, identifier)

	// KNN query with pre-filter.
	ftQuery := fmt.Sprintf("(%s)=>[KNN %d @embedding $BLOB AS score]", filterStr, limit)

	args := []any{"FT.SEARCH", s.indexName, ftQuery,
		"PARAMS", "2", "BLOB", float64sToFloat32Bytes(embedding),
		"SORTBY", "score",
		"LIMIT", "0", fmt.Sprintf("%d", limit),
		"DIALECT", "2",
	}

	// Add SORTBY override if specified.
	if len(rc.orderBy) > 0 {
		// Replace the default SORTBY score with the user's sort.
		o := rc.orderBy[0] // RediSearch only supports one SORTBY
		dir := "ASC"
		if o.dir == Desc {
			dir = "DESC"
		}
		// Rebuild args with custom SORTBY.
		args = []any{"FT.SEARCH", s.indexName, ftQuery,
			"PARAMS", "2", "BLOB", float64sToFloat32Bytes(embedding),
			"SORTBY", o.column, dir,
			"LIMIT", "0", fmt.Sprintf("%d", limit),
			"DIALECT", "2",
		}
	}

	res, err := s.client.Do(ctx, args...).Result()
	if err != nil {
		return nil, fmt.Errorf("redis: search: %w", err)
	}

	results, err := s.parseResults(res, rc.minSimilarity)
	if err != nil {
		return nil, err
	}

	if results == nil {
		return []memory.Entry[T]{}, nil
	}
	return results, nil
}

// ForgetAll removes all stored entries for the given identifier.
func (s *Store[T]) ForgetAll(ctx context.Context, identifier string) error {
	if identifier == "" {
		return errors.New("redis: identifier must not be empty")
	}

	identField := s.schema.Fields[s.schema.IdentifierIdx].HashField
	query := fmt.Sprintf("@%s:{%s}", identField, escapeTag(identifier))

	res, err := s.client.Do(ctx, "FT.SEARCH", s.indexName,
		query,
		"NOCONTENT",
		"LIMIT", "0", "10000",
	).Result()
	if err != nil {
		return fmt.Errorf("redis: forget all search: %w", err)
	}

	keys := extractTypedKeys(res)
	if len(keys) == 0 {
		return nil
	}

	if err := s.client.Del(ctx, keys...).Err(); err != nil {
		return fmt.Errorf("redis: forget all delete: %w", err)
	}
	return nil
}

// Forget removes a single entry by its Redis key.
func (s *Store[T]) Forget(ctx context.Context, identifier, id string) error {
	if identifier == "" {
		return errors.New("redis: identifier must not be empty")
	}
	if id == "" {
		return errors.New("redis: id must not be empty")
	}
	if err := s.client.Del(ctx, id).Err(); err != nil {
		return fmt.Errorf("redis: forget: %w", err)
	}
	return nil
}

// extractTypedKeys pulls key names from an FT.SEARCH NOCONTENT response.
func extractTypedKeys(res any) []string {
	switch v := res.(type) {
	case map[interface{}]interface{}:
		resultsRaw, ok := v["results"]
		if !ok {
			return nil
		}
		items, ok := resultsRaw.([]interface{})
		if !ok {
			return nil
		}
		keys := make([]string, 0, len(items))
		for _, item := range items {
			entry, ok := item.(map[interface{}]interface{})
			if !ok {
				continue
			}
			if id, ok := entry["id"].(string); ok {
				keys = append(keys, id)
			}
		}
		return keys
	case []interface{}:
		if len(v) < 2 {
			return nil
		}
		keys := make([]string, 0, len(v)-1)
		for i := 1; i < len(v); i++ {
			if key, ok := v[i].(string); ok {
				keys = append(keys, key)
			}
		}
		return keys
	default:
		return nil
	}
}

// Close closes the Redis client.
func (s *Store[T]) Close() error {
	return s.client.Close()
}

// --- Internal helpers ---

func parseRedisSchema[T any]() (*redisSchema, error) {
	var zero T
	t := reflect.TypeOf(zero)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("redis: T must be a struct, got %s", t.Kind())
	}

	schema := &redisSchema{PKIdx: -1, IdentifierIdx: -1, ContentIdx: -1}

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}

		tag := field.Tag.Get("db")
		if tag == "" || tag == "-" {
			continue
		}

		parts := strings.Split(tag, ",")
		hashField := parts[0]
		if hashField == "" {
			continue
		}

		info := redisFieldInfo{
			FieldIndex: i,
			HashField:  hashField,
			FieldType:  inferRedisFieldType(field.Type),
		}

		for _, p := range parts[1:] {
			switch strings.TrimSpace(p) {
			case "pk":
				info.IsPK = true
				info.NoInput = true
				schema.PKIdx = len(schema.Fields)
			case "identifier":
				info.IsIdent = true
				info.NoInput = true
				info.FieldType = fieldTAG
				schema.IdentifierIdx = len(schema.Fields)
			case "content":
				info.IsContent = true
				info.FieldType = fieldTEXT
				schema.ContentIdx = len(schema.Fields)
			case "jsonb":
				info.IsJSONB = true
				info.FieldType = fieldTEXT
			case "noinput":
				info.NoInput = true
			case "tag":
				info.FieldType = fieldTAG
			case "numeric":
				info.FieldType = fieldNUMERIC
			}
		}

		schema.Fields = append(schema.Fields, info)
	}

	if schema.IdentifierIdx == -1 {
		return nil, fmt.Errorf("redis: struct must have a field with db:\"...,identifier\" tag")
	}
	if schema.ContentIdx == -1 {
		return nil, fmt.Errorf("redis: struct must have a field with db:\"...,content\" tag")
	}

	return schema, nil
}

func inferRedisFieldType(t reflect.Type) redisFieldType {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return fieldNUMERIC
	default:
		// Check for time.Time.
		if t == reflect.TypeOf(time.Time{}) {
			return fieldNUMERIC
		}
		return fieldTAG
	}
}

func buildFTCreate(cfg *storeConfig, schema *redisSchema, dim int) []any {
	args := []any{"FT.CREATE", cfg.indexName,
		"ON", "HASH",
		"PREFIX", "1", cfg.keyPrefix,
		"SCHEMA",
	}

	for _, f := range schema.Fields {
		switch f.FieldType {
		case fieldTAG:
			args = append(args, f.HashField, "TAG")
		case fieldNUMERIC:
			args = append(args, f.HashField, "NUMERIC", "SORTABLE")
		case fieldTEXT:
			args = append(args, f.HashField, "TEXT")
		}
	}

	// Add embedding vector field.
	args = append(args, "embedding", "VECTOR", "HNSW", "10",
		"TYPE", "FLOAT32",
		"DIM", dim,
		"DISTANCE_METRIC", "COSINE",
		"M", cfg.hnswM,
		"EF_CONSTRUCTION", cfg.hnswEF,
	)

	return args
}

func (s *Store[T]) extractContent(value T) string {
	v := reflect.ValueOf(value)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	f := s.schema.Fields[s.schema.ContentIdx]
	return fmt.Sprintf("%v", v.Field(f.FieldIndex).Interface())
}

func (s *Store[T]) extractPK(value T) string {
	v := reflect.ValueOf(value)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	if s.schema.PKIdx >= 0 {
		f := s.schema.Fields[s.schema.PKIdx]
		pk := fmt.Sprintf("%v", v.Field(f.FieldIndex).Interface())
		if pk != "" {
			return pk
		}
	}
	return uuid.New().String()
}

func setRedisIdentifier[T any](value *T, schema *redisSchema, id string) {
	if schema.IdentifierIdx < 0 {
		return
	}
	f := schema.Fields[schema.IdentifierIdx]
	v := reflect.ValueOf(value).Elem()
	field := v.Field(f.FieldIndex)
	if field.CanSet() && field.Kind() == reflect.String {
		field.SetString(id)
	}
}

func (s *Store[T]) buildHashFields(value T) map[string]any {
	v := reflect.ValueOf(value)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	fields := make(map[string]any, len(s.schema.Fields))
	for _, f := range s.schema.Fields {
		fieldVal := v.Field(f.FieldIndex).Interface()

		if f.IsJSONB {
			data, _ := json.Marshal(fieldVal)
			fields[f.HashField] = string(data)
		} else if f.FieldType == fieldNUMERIC {
			// Convert time.Time to Unix epoch.
			if t, ok := fieldVal.(time.Time); ok {
				fields[f.HashField] = t.Unix()
			} else {
				fields[f.HashField] = fieldVal
			}
		} else {
			fields[f.HashField] = fmt.Sprintf("%v", fieldVal)
		}
	}
	return fields
}

func (s *Store[T]) buildFilterQuery(rc *recallConfig, identField, identifier string) string {
	// Always filter by identifier.
	parts := []string{fmt.Sprintf("@%s:{%s}", identField, escapeTag(identifier))}

	// Add additional filters.
	for _, f := range rc.filters {
		parts = append(parts, f.expr) // For Redis, we build the filter string directly
	}

	// Min similarity is handled post-search (Redis KNN doesn't support pre-filter on score).

	return strings.Join(parts, " ")
}

func (s *Store[T]) parseResults(res any, minSimilarity *float64) ([]memory.Entry[T], error) {
	var entries []memory.Entry[T]

	switch v := res.(type) {
	case map[interface{}]interface{}:
		entries = s.parseRESP3Results(v)
	case []interface{}:
		entries = s.parseRESP2Results(v)
	default:
		return []memory.Entry[T]{}, nil
	}

	// Apply min similarity filter (post-search).
	if minSimilarity != nil {
		filtered := entries[:0]
		for _, e := range entries {
			if e.Score >= *minSimilarity {
				filtered = append(filtered, e)
			}
		}
		entries = filtered
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Score > entries[j].Score
	})

	return entries, nil
}

func (s *Store[T]) parseRESP3Results(m map[interface{}]interface{}) []memory.Entry[T] {
	resultsRaw, ok := m["results"]
	if !ok {
		return nil
	}
	items, ok := resultsRaw.([]interface{})
	if !ok {
		return nil
	}

	var entries []memory.Entry[T]
	for _, item := range items {
		entry, ok := item.(map[interface{}]interface{})
		if !ok {
			continue
		}

		// Extract the Redis key (document ID).
		key, _ := entry["id"].(string)

		attrsRaw, ok := entry["extra_attributes"]
		if !ok {
			continue
		}
		attrs, ok := attrsRaw.(map[interface{}]interface{})
		if !ok {
			continue
		}

		value, score := s.scanAttrs(attrs)
		entries = append(entries, memory.Entry[T]{ID: key, Value: value, Score: score})
	}
	return entries
}

func (s *Store[T]) parseRESP2Results(results []interface{}) []memory.Entry[T] {
	if len(results) < 1 {
		return nil
	}

	var entries []memory.Entry[T]
	for i := 1; i+1 < len(results); i += 2 {
		// In RESP2, results[i] is the key, results[i+1] is the field array.
		key, _ := results[i].(string)

		fields, ok := results[i+1].([]interface{})
		if !ok {
			continue
		}

		attrs := make(map[interface{}]interface{}, len(fields)/2)
		for j := 0; j+1 < len(fields); j += 2 {
			attrs[fields[j]] = fields[j+1]
		}

		value, score := s.scanAttrs(attrs)
		entries = append(entries, memory.Entry[T]{ID: key, Value: value, Score: score})
	}
	return entries
}

func (s *Store[T]) scanAttrs(attrs map[interface{}]interface{}) (T, float64) {
	var result T
	rv := reflect.ValueOf(&result).Elem()

	var score float64
	if scoreStr, ok := attrs["score"].(string); ok {
		if d, err := strconv.ParseFloat(scoreStr, 64); err == nil {
			score = 1 - d // convert distance to similarity
		}
	}

	for _, f := range s.schema.Fields {
		raw, ok := attrs[f.HashField]
		if !ok {
			continue
		}
		rawStr := fmt.Sprintf("%v", raw)
		field := rv.Field(f.FieldIndex)

		if f.IsJSONB {
			ptr := reflect.New(field.Type())
			if err := json.Unmarshal([]byte(rawStr), ptr.Interface()); err == nil {
				field.Set(ptr.Elem())
			}
		} else if f.FieldType == fieldNUMERIC {
			if field.Type() == reflect.TypeOf(time.Time{}) {
				if epoch, err := strconv.ParseInt(rawStr, 10, 64); err == nil {
					field.Set(reflect.ValueOf(time.Unix(epoch, 0)))
				}
			} else {
				switch field.Kind() {
				case reflect.Int, reflect.Int64:
					if n, err := strconv.ParseInt(rawStr, 10, 64); err == nil {
						field.SetInt(n)
					}
				case reflect.Float64:
					if n, err := strconv.ParseFloat(rawStr, 64); err == nil {
						field.SetFloat(n)
					}
				}
			}
		} else {
			if field.Kind() == reflect.String {
				field.SetString(rawStr)
			}
		}
	}

	return result, score
}

// --- RecallOption support ---
// Redis RecallOption reuses the same type from recall_options.go pattern.

// RecallOption configures filtering for typed Redis Recall queries.
// It satisfies memory.RecallOption so it can be passed to the interface method.
type RecallOption func(*recallConfig)

func (RecallOption) IsRecallOption() {}

// Implement Option interface for tool compatibility.
func (r RecallOption) applyTool(c *toolConfig) {
	c.recallOpts = append(c.recallOpts, r)
}

type recallConfig struct {
	filters       []filter
	orderBy       []orderClause
	minSimilarity *float64
}

type filter struct {
	expr string // RediSearch filter expression
}

type orderClause struct {
	column string
	dir    SortDir
}

// SortDir is the sort direction.
type SortDir string

const (
	Asc  SortDir = "ASC"
	Desc SortDir = "DESC"
)

// WithFieldEquals adds a TAG filter: @field:{value}.
func WithFieldEquals(column string, value any) RecallOption {
	return func(c *recallConfig) {
		c.filters = append(c.filters, filter{
			expr: fmt.Sprintf("@%s:{%s}", column, escapeTag(fmt.Sprintf("%v", value))),
		})
	}
}

// WithFieldGT adds a NUMERIC filter: @field:[(value +inf].
func WithFieldGT(column string, value any) RecallOption {
	return func(c *recallConfig) {
		c.filters = append(c.filters, filter{
			expr: fmt.Sprintf("@%s:[(%v +inf]", column, value),
		})
	}
}

// WithFieldLT adds a NUMERIC filter: @field:[-inf (value].
func WithFieldLT(column string, value any) RecallOption {
	return func(c *recallConfig) {
		c.filters = append(c.filters, filter{
			expr: fmt.Sprintf("@%s:[-inf (%v]", column, value),
		})
	}
}

// WithTimeAfter adds a NUMERIC filter for timestamps stored as Unix epoch.
func WithTimeAfter(column string, t time.Time) RecallOption {
	return func(c *recallConfig) {
		c.filters = append(c.filters, filter{
			expr: fmt.Sprintf("@%s:[(%d +inf]", column, t.Unix()),
		})
	}
}

// WithTimeBefore adds a NUMERIC filter for timestamps stored as Unix epoch.
func WithTimeBefore(column string, t time.Time) RecallOption {
	return func(c *recallConfig) {
		c.filters = append(c.filters, filter{
			expr: fmt.Sprintf("@%s:[-inf (%d]", column, t.Unix()),
		})
	}
}

// WithMinSimilarity sets a minimum similarity threshold (applied post-search).
func WithMinSimilarity(threshold float64) RecallOption {
	return func(c *recallConfig) {
		c.minSimilarity = &threshold
	}
}

// WithOrderBy sets the SORTBY field. RediSearch supports one SORTBY per query.
func WithOrderBy(column string, dir SortDir) RecallOption {
	return func(c *recallConfig) {
		c.orderBy = []orderClause{{column: column, dir: dir}}
	}
}

// Options holds Redis connection configuration.
type Options struct {
	Addr      string // Default: "127.0.0.1:6379"
	Password  string
	DB        int         // Default: 0
	TLSConfig *tls.Config // Optional
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
