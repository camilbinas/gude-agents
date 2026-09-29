package redis

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
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
	"unicode"

	"github.com/camilbinas/gude-agents/agent/memory"
	"github.com/camilbinas/gude-agents/agent/rag"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

// Store is a Redis memory store that maps Go struct fields to Redis HASH
// fields using `db` struct tags. Requires Redis Stack (RediSearch).
type Store[T any] struct {
	client    redisClient
	indexName string
	keyPrefix string
	dim       int
	embedder  rag.Embedder
	schema    *redisSchema
}

// redisClient is the subset of go-redis used by Store. It keeps backend
// behavior unit-testable without requiring a Redis service.
type redisClient interface {
	Ping(context.Context) *goredis.StatusCmd
	HSet(context.Context, string, ...any) *goredis.IntCmd
	HGet(context.Context, string, string) *goredis.StringCmd
	Del(context.Context, ...string) *goredis.IntCmd
	Do(context.Context, ...any) *goredis.Cmd
	Close() error
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

func defaultStoreConfig[T any]() *storeConfig {
	namespace := defaultNamespace[T]()
	return &storeConfig{
		indexName: "gude_typed_idx_" + namespace,
		keyPrefix: "gude:typed:" + namespace + ":",
		hnswM:     16,
		hnswEF:    200,
	}
}

// defaultNamespace derives a stable, Redis-safe namespace from T so distinct
// types cannot share keys or an index unless callers explicitly configure one.
func defaultNamespace[T any]() string {
	var zero T
	t := reflect.TypeOf(zero)
	return fmt.Sprintf("%x", sha256.Sum256([]byte(t.PkgPath()+":"+t.String())))
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
func NewStore[T any](opts Options, embedder rag.Embedder, dim int, sopts ...StoreOption) (*Store[T], error) {
	if embedder == nil {
		return nil, errors.New("redis: embedder is required")
	}
	if dim < 1 {
		return nil, errors.New("redis: dim must be at least 1")
	}

	cfg := defaultStoreConfig[T]()
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

	if err := createOrValidateIndex(context.Background(), client, cfg, schema, dim); err != nil {
		_ = client.Close()
		return nil, err
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
	if err := s.validateEmbedding(embedding); err != nil {
		return err
	}

	// Build HASH fields.
	fields := s.buildHashFields(value)
	fields["embedding"] = float64sToFloat32Bytes(embedding)

	// Namespace the physical key by identifier so equal primary keys from
	// different identifiers cannot overwrite each other.
	key := s.entryKey(identifier, s.extractPK(value))

	if err := s.client.HSet(ctx, key, fields).Err(); err != nil {
		return fmt.Errorf("redis: hset: %w", err)
	}

	return nil
}

// Recall retrieves values by semantic similarity to the query, scoped to the
// identifier. Portable filters are translated through the parsed Redis schema;
// unsupported fields, operators, and value types are rejected explicitly.
//
// Implements memory.Memory[T].
func (s *Store[T]) Recall(ctx context.Context, identifier string, query memory.RecallQuery) ([]memory.Entry[T], error) {
	if identifier == "" {
		return nil, errors.New("redis: identifier must not be empty")
	}
	if err := validateRecallQuery(query); err != nil {
		return nil, err
	}

	filterQuery, err := s.buildRecallFilter(identifier, query.Filters)
	if err != nil {
		return nil, err
	}
	order, err := s.compileOrder(query.Order)
	if err != nil {
		return nil, err
	}

	limit := query.Limit
	if limit == 0 {
		limit, err = s.recallMatchCount(ctx, filterQuery)
		if err != nil {
			return nil, err
		}
		if limit == 0 {
			return []memory.Entry[T]{}, nil
		}
	}

	embedding, err := s.embedder.Embed(ctx, query.Text)
	if err != nil {
		return nil, fmt.Errorf("redis: embed query: %w", err)
	}
	if err := s.validateEmbedding(embedding); err != nil {
		return nil, err
	}

	ftQuery := fmt.Sprintf("(%s)=>[KNN %d @embedding $BLOB AS score]", filterQuery, limit)
	sortField, sortDirection := "score", "ASC"
	if order != nil {
		sortField = order.field
		sortDirection = order.direction
	}
	args := []any{"FT.SEARCH", s.indexName, ftQuery,
		"PARAMS", "2", "BLOB", float64sToFloat32Bytes(embedding),
		"SORTBY", sortField, sortDirection,
		"LIMIT", "0", strconv.Itoa(limit),
		"DIALECT", "2",
	}

	res, err := s.client.Do(ctx, args...).Result()
	if err != nil {
		return nil, fmt.Errorf("redis: search: %w", err)
	}

	results, err := s.parseResults(res, query.MinSimilarity, order == nil)
	if err != nil {
		return nil, err
	}
	if results == nil {
		return []memory.Entry[T]{}, nil
	}
	return results, nil
}

// Update replaces an existing entry by its Redis key, re-embedding the content.
func (s *Store[T]) Update(ctx context.Context, identifier, id string, value T) error {
	if identifier == "" {
		return errors.New("redis: identifier must not be empty")
	}
	if id == "" {
		return errors.New("redis: id must not be empty")
	}

	owned, err := s.entryOwnedBy(ctx, identifier, id)
	if err != nil {
		return err
	}
	if !owned {
		return fmt.Errorf("redis: entry %q not found", id)
	}

	setRedisIdentifier(&value, s.schema, identifier)

	content := s.extractContent(value)
	if content == "" {
		return errors.New("redis: content field is empty")
	}

	embedding, err := s.embedder.Embed(ctx, content)
	if err != nil {
		return fmt.Errorf("redis: embed: %w", err)
	}
	if err := s.validateEmbedding(embedding); err != nil {
		return err
	}

	fields := s.buildHashFields(value)
	fields["embedding"] = float64sToFloat32Bytes(embedding)

	if err := s.client.HSet(ctx, id, fields).Err(); err != nil {
		return fmt.Errorf("redis: hset: %w", err)
	}

	return nil
}

func (s *Store[T]) entryOwnedBy(ctx context.Context, identifier, id string) (bool, error) {
	identField := s.schema.Fields[s.schema.IdentifierIdx].HashField
	storedIdentifier, err := s.client.HGet(ctx, id, identField).Result()
	if errors.Is(err, goredis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("redis: ownership check: %w", err)
	}
	return storedIdentifier == identifier, nil
}

const (
	forgetAllPageSize    = 1000
	forgetAllDeleteBatch = 1000
)

// ForgetAll removes all stored entries for the given identifier.
func (s *Store[T]) ForgetAll(ctx context.Context, identifier string) error {
	if identifier == "" {
		return errors.New("redis: identifier must not be empty")
	}

	identField := s.schema.Fields[s.schema.IdentifierIdx].HashField
	query := fmt.Sprintf("@%s:{%s}", escapeQueryField(identField), escapeTag(identifier))
	keys, err := collectPagedKeys(forgetAllPageSize, func(offset, limit int) ([]string, error) {
		res, err := s.client.Do(ctx, "FT.SEARCH", s.indexName,
			query,
			"NOCONTENT",
			"LIMIT", strconv.Itoa(offset), strconv.Itoa(limit),
		).Result()
		if err != nil {
			return nil, fmt.Errorf("redis: forget all search: %w", err)
		}
		return extractTypedKeys(res), nil
	})
	if err != nil {
		return err
	}

	for start := 0; start < len(keys); start += forgetAllDeleteBatch {
		end := min(start+forgetAllDeleteBatch, len(keys))
		if err := s.client.Del(ctx, keys[start:end]...).Err(); err != nil {
			return fmt.Errorf("redis: forget all delete: %w", err)
		}
	}
	return nil
}

// collectPagedKeys reads every page before deletion changes the search results.
func collectPagedKeys(pageSize int, fetch func(offset, limit int) ([]string, error)) ([]string, error) {
	var keys []string
	for offset := 0; ; {
		page, err := fetch(offset, pageSize)
		if err != nil {
			return nil, err
		}
		keys = append(keys, page...)
		if len(page) < pageSize {
			return keys, nil
		}
		offset += len(page)
	}
}

// Forget removes a single entry by its Redis key.
func (s *Store[T]) Forget(ctx context.Context, identifier, id string) error {
	if identifier == "" {
		return errors.New("redis: identifier must not be empty")
	}
	if id == "" {
		return errors.New("redis: id must not be empty")
	}
	owned, err := s.entryOwnedBy(ctx, identifier, id)
	if err != nil {
		return err
	}
	if !owned {
		return fmt.Errorf("redis: entry %q not found", id)
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
		return nil, fmt.Errorf("redis: T must be a non-pointer struct; got %s", t)
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

func createOrValidateIndex(ctx context.Context, client redisClient, cfg *storeConfig, schema *redisSchema, dim int) error {
	createArgs := buildFTCreate(cfg, schema, dim)
	if err := client.Do(ctx, createArgs...).Err(); err == nil {
		return nil
	} else if !strings.Contains(strings.ToLower(err.Error()), "index already exists") {
		return fmt.Errorf("redis: create index: %w", err)
	}

	info, err := client.Do(ctx, "FT.INFO", cfg.indexName).Result()
	if err != nil {
		return fmt.Errorf("redis: inspect existing index %q: %w", cfg.indexName, err)
	}
	return validateExistingIndex(info, cfg, schema, dim)
}

// validateExistingIndex checks that a pre-existing RediSearch index can safely
// serve the schema requested by this store. It accepts both RESP2 key/value
// arrays and RESP3 maps returned by FT.INFO.
func validateExistingIndex(info any, cfg *storeConfig, schema *redisSchema, dim int) error {
	incompatible := func(format string, args ...any) error {
		return fmt.Errorf("redis: existing index %q is incompatible: %s", cfg.indexName, fmt.Sprintf(format, args...))
	}

	definition := redisInfoField(info, "index_definition")
	if keyType := strings.ToUpper(redisInfoString(redisInfoField(definition, "key_type"))); keyType != "HASH" {
		return incompatible("key type is %q, want HASH", keyType)
	}
	prefixes := redisInfoStrings(redisInfoField(definition, "prefixes"))
	if len(prefixes) != 1 || prefixes[0] != cfg.keyPrefix {
		return incompatible("prefixes are %q, want [%q]", prefixes, cfg.keyPrefix)
	}

	attributes := redisInfoList(redisInfoField(info, "attributes"))
	if attributes == nil {
		return incompatible("attributes are missing")
	}
	for _, field := range schema.Fields {
		attribute := redisAttribute(attributes, field.HashField)
		if attribute == nil {
			return incompatible("field %q is missing", field.HashField)
		}
		wantType := redisFieldTypeName(field.FieldType)
		if gotType := strings.ToUpper(redisInfoString(redisInfoField(attribute, "type"))); gotType != wantType {
			return incompatible("field %q type is %q, want %s", field.HashField, gotType, wantType)
		}
	}

	embedding := redisAttribute(attributes, "embedding")
	if embedding == nil {
		return incompatible("embedding vector field is missing")
	}
	if got := strings.ToUpper(redisInfoString(redisInfoField(embedding, "type"))); got != "VECTOR" {
		return incompatible("embedding type is %q, want VECTOR", got)
	}
	if got := strings.ToUpper(redisInfoString(redisInfoField(embedding, "algorithm"))); got != "HNSW" {
		return incompatible("embedding algorithm is %q, want HNSW", got)
	}
	if got := strings.ToUpper(redisInfoString(redisInfoField(embedding, "data_type"))); got != "FLOAT32" {
		return incompatible("embedding data type is %q, want FLOAT32", got)
	}
	if got := strings.ToUpper(redisInfoString(redisInfoField(embedding, "distance_metric"))); got != "COSINE" {
		return incompatible("embedding distance metric is %q, want COSINE", got)
	}
	gotDim, err := strconv.Atoi(redisInfoString(redisInfoField(embedding, "dim")))
	if err != nil || gotDim != dim {
		return incompatible("embedding DIM is %q, want %d", redisInfoString(redisInfoField(embedding, "dim")), dim)
	}
	return nil
}

func redisFieldTypeName(fieldType redisFieldType) string {
	switch fieldType {
	case fieldTAG:
		return "TAG"
	case fieldNUMERIC:
		return "NUMERIC"
	case fieldTEXT:
		return "TEXT"
	default:
		return ""
	}
}

func redisAttribute(attributes []any, name string) any {
	for _, attribute := range attributes {
		attributeName := redisInfoString(redisInfoField(attribute, "attribute"))
		if attributeName == "" {
			attributeName = redisInfoString(redisInfoField(attribute, "identifier"))
		}
		if attributeName == name {
			return attribute
		}
	}
	return nil
}

func redisInfoField(value any, key string) any {
	switch v := value.(type) {
	case map[string]any:
		for candidate, field := range v {
			if strings.EqualFold(candidate, key) {
				return field
			}
		}
	case map[any]any:
		for candidate, field := range v {
			if strings.EqualFold(redisInfoString(candidate), key) {
				return field
			}
		}
	case []any:
		for i := 0; i+1 < len(v); i += 2 {
			if strings.EqualFold(redisInfoString(v[i]), key) {
				return v[i+1]
			}
		}
	}
	return nil
}

func redisInfoString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

func redisInfoList(value any) []any {
	switch v := value.(type) {
	case []any:
		return v
	case []string:
		result := make([]any, len(v))
		for i := range v {
			result[i] = v[i]
		}
		return result
	default:
		return nil
	}
}

func redisInfoStrings(value any) []string {
	if values := redisInfoList(value); values != nil {
		result := make([]string, len(values))
		for i, item := range values {
			result[i] = redisInfoString(item)
		}
		return result
	}
	if value == nil {
		return nil
	}
	return []string{redisInfoString(value)}
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

// entryKey builds a type-prefixed Redis key with a delimiter-safe identifier
// component. The key remains the storage ID returned by Recall.
func (s *Store[T]) entryKey(identifier, pk string) string {
	encodedIdentifier := base64.RawURLEncoding.EncodeToString([]byte(identifier))
	return s.keyPrefix + "id:" + encodedIdentifier + ":" + pk
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

func validateRecallQuery(query memory.RecallQuery) error {
	if query.Text == "" {
		return fmt.Errorf("%w: text must not be empty", memory.ErrInvalidRecallQuery)
	}
	if query.Limit < 0 {
		return fmt.Errorf("%w: limit must not be negative", memory.ErrInvalidRecallQuery)
	}
	if query.MinSimilarity != 0 && (math.IsNaN(query.MinSimilarity) || math.IsInf(query.MinSimilarity, 0) || query.MinSimilarity <= 0 || query.MinSimilarity > 1) {
		return fmt.Errorf("%w: minimum similarity must be in (0, 1]", memory.ErrInvalidRecallQuery)
	}
	return nil
}

type compiledOrder struct {
	field     string
	direction string
}

func (s *Store[T]) buildRecallFilter(identifier string, filters []memory.Filter) (string, error) {
	identField := s.schema.Fields[s.schema.IdentifierIdx].HashField
	parts := []string{fmt.Sprintf("(@%s:{%s})", escapeQueryField(identField), escapeTag(identifier))}
	for _, filter := range filters {
		clause, err := s.compileFilter(filter)
		if err != nil {
			return "", err
		}
		parts = append(parts, "("+clause+")")
	}
	return strings.Join(parts, " "), nil
}

func (s *Store[T]) compileFilter(filter memory.Filter) (string, error) {
	field, ok := s.schemaField(filter.Field)
	if !ok {
		return "", fmt.Errorf("%w: unknown field %q", memory.ErrUnsupportedFilter, filter.Field)
	}

	var (
		clause string
		err    error
	)
	switch field.FieldType {
	case fieldTAG:
		clause, err = compileTagFilter(field.HashField, filter.Operator, filter.Value)
	case fieldNUMERIC:
		clause, err = compileNumericFilter(field.HashField, filter.Operator, filter.Value)
	default:
		err = fmt.Errorf("field type %s cannot be filtered", redisFieldTypeName(field.FieldType))
	}
	if err != nil {
		return "", fmt.Errorf("%w: field %q: %v", memory.ErrUnsupportedFilter, filter.Field, err)
	}
	return clause, nil
}

func (s *Store[T]) schemaField(name string) (redisFieldInfo, bool) {
	for _, field := range s.schema.Fields {
		if field.HashField == name {
			return field, true
		}
	}
	return redisFieldInfo{}, false
}

func compileTagFilter(field string, operator memory.FilterOperator, value any) (string, error) {
	field = escapeQueryField(field)
	switch operator {
	case memory.FilterEqual, memory.FilterNotEqual:
		literal, err := tagLiteral(value)
		if err != nil {
			return "", err
		}
		prefix := ""
		if operator == memory.FilterNotEqual {
			prefix = "-"
		}
		return fmt.Sprintf("%s@%s:{%s}", prefix, field, literal), nil
	case memory.FilterIn:
		values, err := filterValues(value, tagLiteral)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("@%s:{%s}", field, strings.Join(values, "|")), nil
	default:
		return "", fmt.Errorf("operator %q is not supported for TAG fields", operator)
	}
}

func compileNumericFilter(field string, operator memory.FilterOperator, value any) (string, error) {
	field = escapeQueryField(field)
	if operator == memory.FilterIn {
		values, err := filterValues(value, numericLiteral)
		if err != nil {
			return "", err
		}
		clauses := make([]string, len(values))
		for i, literal := range values {
			clauses[i] = fmt.Sprintf("@%s:[%s %s]", field, literal, literal)
		}
		return "(" + strings.Join(clauses, "|") + ")", nil
	}

	literal, err := numericLiteral(value)
	if err != nil {
		return "", err
	}
	switch operator {
	case memory.FilterEqual:
		return fmt.Sprintf("@%s:[%s %s]", field, literal, literal), nil
	case memory.FilterNotEqual:
		return fmt.Sprintf("-@%s:[%s %s]", field, literal, literal), nil
	case memory.FilterGreaterThan:
		return fmt.Sprintf("@%s:[(%s +inf]", field, literal), nil
	case memory.FilterGreaterThanOrEqual:
		return fmt.Sprintf("@%s:[%s +inf]", field, literal), nil
	case memory.FilterLessThan:
		return fmt.Sprintf("@%s:[-inf (%s]", field, literal), nil
	case memory.FilterLessThanOrEqual:
		return fmt.Sprintf("@%s:[-inf %s]", field, literal), nil
	default:
		return "", fmt.Errorf("operator %q is not supported for NUMERIC fields", operator)
	}
}

func filterValues(value any, encode func(any) (string, error)) ([]string, error) {
	rv := reflect.ValueOf(value)
	if !rv.IsValid() || (rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array) || rv.Len() == 0 {
		return nil, errors.New("IN requires a non-empty slice or array")
	}
	values := make([]string, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		literal, err := encode(rv.Index(i).Interface())
		if err != nil {
			return nil, fmt.Errorf("IN value %d: %w", i, err)
		}
		values[i] = literal
	}
	return values, nil
}

func tagLiteral(value any) (string, error) {
	rv := reflect.ValueOf(value)
	if !rv.IsValid() || rv.Kind() != reflect.String {
		return "", fmt.Errorf("TAG value must be a string, got %T", value)
	}
	return escapeTag(rv.String()), nil
}

func numericLiteral(value any) (string, error) {
	if timestamp, ok := value.(time.Time); ok {
		return strconv.FormatInt(timestamp.Unix(), 10), nil
	}
	rv := reflect.ValueOf(value)
	if !rv.IsValid() {
		return "", errors.New("NUMERIC value must not be nil")
	}
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(rv.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(rv.Uint(), 10), nil
	case reflect.Float32, reflect.Float64:
		value := rv.Float()
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return "", errors.New("NUMERIC value must be finite")
		}
		return strconv.FormatFloat(value, 'g', -1, rv.Type().Bits()), nil
	default:
		return "", fmt.Errorf("NUMERIC value must be a number or time.Time, got %T", value)
	}
}

func (s *Store[T]) compileOrder(orders []memory.Order) (*compiledOrder, error) {
	if len(orders) == 0 {
		return nil, nil
	}
	if len(orders) > 1 {
		return nil, fmt.Errorf("%w: Redis supports one order clause, got %d", memory.ErrUnsupportedOrder, len(orders))
	}
	order := orders[0]
	field, ok := s.schemaField(order.Field)
	if !ok {
		return nil, fmt.Errorf("%w: unknown field %q", memory.ErrUnsupportedOrder, order.Field)
	}
	if field.FieldType != fieldNUMERIC {
		return nil, fmt.Errorf("%w: field %q is not sortable", memory.ErrUnsupportedOrder, order.Field)
	}

	direction := ""
	switch order.Direction {
	case memory.OrderAscending:
		direction = "ASC"
	case memory.OrderDescending:
		direction = "DESC"
	default:
		return nil, fmt.Errorf("%w: direction %q", memory.ErrUnsupportedOrder, order.Direction)
	}
	return &compiledOrder{field: field.HashField, direction: direction}, nil
}

func (s *Store[T]) recallMatchCount(ctx context.Context, filterQuery string) (int, error) {
	result, err := s.client.Do(ctx, "FT.SEARCH", s.indexName, filterQuery,
		"NOCONTENT", "LIMIT", "0", "0", "DIALECT", "2").Result()
	if err != nil {
		return 0, fmt.Errorf("redis: count recall matches: %w", err)
	}
	count, ok := searchResultCount(result)
	if !ok || count < 0 {
		return 0, fmt.Errorf("redis: count recall matches: unexpected response %T", result)
	}
	return count, nil
}

func searchResultCount(result any) (int, bool) {
	var raw any
	switch value := result.(type) {
	case []any:
		if len(value) == 0 {
			return 0, false
		}
		raw = value[0]
	case map[string]any:
		raw = redisInfoField(value, "total_results")
	case map[any]any:
		raw = redisInfoField(value, "total_results")
	default:
		return 0, false
	}
	switch value := raw.(type) {
	case int:
		return value, true
	case int64:
		return int(value), int64(int(value)) == value
	case uint64:
		return int(value), uint64(int(value)) == value
	case string:
		parsed, err := strconv.Atoi(value)
		return parsed, err == nil
	case []byte:
		parsed, err := strconv.Atoi(string(value))
		return parsed, err == nil
	default:
		return 0, false
	}
}

func (s *Store[T]) validateEmbedding(embedding []float64) error {
	if len(embedding) != s.dim {
		return fmt.Errorf("redis: embedding dimension is %d, want %d", len(embedding), s.dim)
	}
	for i, value := range embedding {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("redis: embedding value %d must be finite", i)
		}
	}
	return nil
}

func (s *Store[T]) parseResults(res any, minSimilarity float64, sortBySimilarity bool) ([]memory.Entry[T], error) {
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
	if minSimilarity > 0 {
		filtered := entries[:0]
		for _, e := range entries {
			if e.Score >= minSimilarity {
				filtered = append(filtered, e)
			}
		}
		entries = filtered
	}

	if sortBySimilarity {
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Score > entries[j].Score
		})
	}

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

// escapeTag escapes every non-alphanumeric character except underscore so a
// value remains one literal RediSearch TAG token. In particular, backslash and
// pipe must be escaped to prevent a caller-controlled value from changing the
// tenant predicate.
func escapeTag(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	for _, r := range value {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// escapeQueryField protects schema-defined HASH field names when used in a
// RediSearch query. Caller-provided field names are never emitted directly.
func escapeQueryField(value string) string {
	return escapeTag(value)
}
