package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/memory"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pgvector "github.com/pgvector/pgvector-go"
)

// Store is a PostgreSQL memory store that maps Go struct fields directly
// to table columns using `db` struct tags. Unlike the base Store (which stores
// everything in content + metadata JSONB), Store gives each field its own
// column for native SQL filtering and indexing.
//
// Tag format: `db:"column_name,role"`
//
// Roles:
//   - pk: primary key column
//   - identifier: scoping column (used in WHERE for multi-tenant isolation)
//   - content: field whose value is embedded (passed to the embedder)
//   - jsonb: serialize the field as JSONB (for slices, maps, nested structs)
//
// The table must be created by the caller. The embedding column name is
// configured via StoreOption.
type Store[T any] struct {
	pool         *pgxpool.Pool
	embedder     agent.Embedder
	dim          int
	tableName    string
	embeddingCol string
	distMetric   string
	schema       *tableSchema
}

// StoreOption configures a Store.
type StoreOption func(*storeConfig)

type storeConfig struct {
	tableName    string
	embeddingCol string
	distMetric   string
}

// WithTableName sets the table name. Default: inferred from struct name (lowercased + "s").
func WithTableName(name string) StoreOption {
	return func(c *storeConfig) {
		if name != "" {
			c.tableName = name
		}
	}
}

// WithEmbeddingColumn sets the name of the vector embedding column.
// Default: "embedding".
func WithEmbeddingColumn(name string) StoreOption {
	return func(c *storeConfig) {
		if name != "" {
			c.embeddingCol = name
		}
	}
}

// WithDistanceMetric sets the distance metric for vector queries.
// Supported: "cosine" (default), "l2", "inner_product".
func WithDistanceMetric(metric string) StoreOption {
	return func(c *storeConfig) {
		if metric != "" {
			c.distMetric = metric
		}
	}
}

// NewStore creates a Store for the given struct type T.
// It parses `db` struct tags to build the column mapping.
func NewStore[T any](pool *pgxpool.Pool, embedder agent.Embedder, dim int, opts ...StoreOption) (*Store[T], error) {
	if pool == nil {
		return nil, errors.New("postgres: pool is required")
	}
	if embedder == nil {
		return nil, errors.New("postgres: embedder is required")
	}
	if dim < 1 {
		return nil, errors.New("postgres: dim must be at least 1")
	}

	// Infer default table name from type.
	var zero T
	typeName := reflect.TypeOf(zero).Name()
	defaultTable := strings.ToLower(typeName) + "s"

	cfg := &storeConfig{
		tableName:    defaultTable,
		embeddingCol: "embedding",
		distMetric:   "cosine",
	}
	for _, o := range opts {
		o(cfg)
	}

	schema, err := parseSchema[T](cfg.embeddingCol)
	if err != nil {
		return nil, err
	}

	// Validate and sanitize identifiers for safe SQL interpolation.
	allIdents := []string{cfg.tableName, cfg.embeddingCol}
	for _, col := range schema.Columns {
		allIdents = append(allIdents, col.Column)
	}
	for _, name := range allIdents {
		if name == "" || strings.ContainsRune(name, 0) {
			return nil, fmt.Errorf("postgres: identifier %q is invalid", name)
		}
	}
	cfg.tableName = pgx.Identifier{cfg.tableName}.Sanitize()
	cfg.embeddingCol = pgx.Identifier{cfg.embeddingCol}.Sanitize()
	for i := range schema.Columns {
		schema.Columns[i].Column = pgx.Identifier{schema.Columns[i].Column}.Sanitize()
	}
	schema.EmbeddingCol = cfg.embeddingCol

	return &Store[T]{
		pool:         pool,
		embedder:     embedder,
		dim:          dim,
		tableName:    cfg.tableName,
		embeddingCol: cfg.embeddingCol,
		distMetric:   cfg.distMetric,
		schema:       schema,
	}, nil
}

// Remember stores a value for the given identifier. It sets the identifier
// field on the struct, extracts the content field, embeds it, and inserts a
// row with all struct fields mapped to their respective columns.
//
// Implements memory.TypedMemory[T].
func (s *Store[T]) Remember(ctx context.Context, identifier string, value T) error {
	if identifier == "" {
		return errors.New("postgres: identifier must not be empty")
	}

	// Set the identifier field on the struct.
	setIdentifierField(&value, s.schema, identifier)

	// Extract content for embedding.
	content := s.extractContent(value)
	if content == "" {
		return errors.New("postgres: content field is empty")
	}

	// Embed the content.
	embedding, err := s.embedder.Embed(ctx, content)
	if err != nil {
		return fmt.Errorf("postgres: embed: %w", err)
	}

	// Build INSERT (skip PK if empty — let postgres DEFAULT handle it).
	cols, args, err := s.extractInsertValues(value)
	if err != nil {
		return fmt.Errorf("postgres: extract values: %w", err)
	}

	// Append embedding as the last argument.
	vec := float64sToFloat32(embedding)
	args = append(args, pgvector.NewVector(vec))
	cols = append(cols, s.schema.EmbeddingCol)

	query := fmt.Sprintf(
		"INSERT INTO %s (%s) VALUES (%s)",
		s.tableName,
		strings.Join(cols, ", "),
		placeholders(len(args)),
	)

	if _, err := s.pool.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("postgres: insert: %w", err)
	}

	return nil
}

// Recall retrieves values by semantic similarity to the query, scoped to the
// identifier extracted from the provided value or passed explicitly.
// Supports filtering and sorting via RecallOption.
func (s *Store[T]) Recall(ctx context.Context, identifier string, query string, limit int, opts ...RecallOption) ([]memory.Entry[T], error) {
	if identifier == "" {
		return nil, errors.New("postgres: identifier must not be empty")
	}
	if limit < 1 {
		return nil, errors.New("postgres: limit must be at least 1")
	}

	// Embed the query.
	embedding, err := s.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("postgres: embed query: %w", err)
	}

	// Apply options.
	rc := &recallConfig{}
	for _, o := range opts {
		o(rc)
	}

	// Build the query.
	op := s.distanceOp()
	vec := float64sToFloat32(embedding)

	// Base args: embedding vector, identifier, limit
	identifierCol := s.schema.Columns[s.schema.IdentifierIdx].Column
	selectCols := strings.Join(s.schema.columnNames(), ", ")

	// Start building query.
	var sb strings.Builder
	args := []any{pgvector.NewVector(vec), identifier}
	paramIdx := 3 // next available param

	sb.WriteString(fmt.Sprintf(
		"SELECT %s, 1 - (%s %s $1) AS _similarity FROM %s WHERE %s = $2",
		selectCols, s.embeddingCol, op, s.tableName, identifierCol,
	))

	// Min similarity filter.
	if rc.minSimilarity != nil {
		sb.WriteString(fmt.Sprintf(" AND 1 - (%s %s $1) >= $%d", s.embeddingCol, op, paramIdx))
		args = append(args, *rc.minSimilarity)
		paramIdx++
	}

	// Additional filters.
	if len(rc.filters) > 0 {
		whereExtra, filterArgs, nextParam := rc.buildWhereClause(paramIdx)
		sb.WriteString(whereExtra)
		args = append(args, filterArgs...)
		paramIdx = nextParam
	}

	// ORDER BY.
	orderExtra := rc.buildOrderClause()
	if orderExtra != "" {
		sb.WriteString(fmt.Sprintf(" ORDER BY %s", orderExtra))
	} else {
		sb.WriteString(fmt.Sprintf(" ORDER BY %s %s $1", s.embeddingCol, op))
	}

	// LIMIT.
	sb.WriteString(fmt.Sprintf(" LIMIT $%d", paramIdx))
	args = append(args, limit)

	// Execute.
	rows, err := s.pool.Query(ctx, sb.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: query: %w", err)
	}
	defer rows.Close()

	var results []memory.Entry[T]
	for rows.Next() {
		value, similarity, err := s.scanRow(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scan: %w", err)
		}
		id := s.extractPK(value)
		results = append(results, memory.Entry[T]{
			ID:    id,
			Value: value,
			Score: similarity,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: rows: %w", err)
	}

	if results == nil {
		return []memory.Entry[T]{}, nil
	}
	return results, nil
}

// distanceOp returns the pgvector operator for the configured distance metric.
func (s *Store[T]) distanceOp() string {
	return pgvectorOp(s.distMetric)
}

// extractContent gets the value of the content-tagged field as a string.
func (s *Store[T]) extractContent(value T) string {
	v := reflect.ValueOf(value)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	col := s.schema.Columns[s.schema.ContentIdx]
	field := v.Field(col.FieldIndex)
	return fmt.Sprintf("%v", field.Interface())
}

// extractPK gets the value of the pk-tagged field as a string.
func (s *Store[T]) extractPK(value T) string {
	if s.schema.PKIndex < 0 {
		return ""
	}
	v := reflect.ValueOf(value)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	col := s.schema.Columns[s.schema.PKIndex]
	return fmt.Sprintf("%v", v.Field(col.FieldIndex).Interface())
}

// extractValues extracts all column values from the struct in schema order.
func (s *Store[T]) extractValues(value T) ([]any, error) {
	v := reflect.ValueOf(value)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	args := make([]any, len(s.schema.Columns))
	for i, col := range s.schema.Columns {
		field := v.Field(col.FieldIndex)
		val := field.Interface()

		if col.IsJSONB {
			data, err := json.Marshal(val)
			if err != nil {
				return nil, fmt.Errorf("marshal jsonb field %s: %w", col.Column, err)
			}
			args[i] = data
		} else {
			args[i] = val
		}
	}
	return args, nil
}

// extractInsertValues extracts column names and values for INSERT, skipping
// PK and noinput columns when their value is zero (lets postgres DEFAULT handle them).
func (s *Store[T]) extractInsertValues(value T) ([]string, []any, error) {
	v := reflect.ValueOf(value)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	var cols []string
	var args []any
	for i, col := range s.schema.Columns {
		field := v.Field(col.FieldIndex)

		// Skip PK or noinput fields if zero — let the DB default handle them.
		if (i == s.schema.PKIndex || col.NoInput) && field.IsZero() {
			continue
		}

		val := field.Interface()
		if col.IsJSONB {
			data, err := json.Marshal(val)
			if err != nil {
				return nil, nil, fmt.Errorf("marshal jsonb field %s: %w", col.Column, err)
			}
			args = append(args, data)
		} else {
			args = append(args, val)
		}
		cols = append(cols, col.Column)
	}
	return cols, args, nil
}

// scanRow scans a single row into a T value plus the similarity score.
func (s *Store[T]) scanRow(rows pgx.Rows) (T, float64, error) {
	var zero T

	// Create scan destinations for each column + similarity.
	numCols := len(s.schema.Columns)
	dests := make([]any, numCols+1) // +1 for _similarity

	// We need typed destinations for scanning.
	var result T
	rv := reflect.ValueOf(&result).Elem()

	for i, col := range s.schema.Columns {
		field := rv.Field(col.FieldIndex)

		if col.IsJSONB {
			// Scan JSONB as []byte, then unmarshal.
			var raw []byte
			dests[i] = &raw
		} else {
			dests[i] = field.Addr().Interface()
		}
	}

	var similarity float64
	dests[numCols] = &similarity

	if err := rows.Scan(dests...); err != nil {
		return zero, 0, err
	}

	// Post-process JSONB fields.
	for i, col := range s.schema.Columns {
		if col.IsJSONB {
			raw := *(dests[i].(*[]byte))
			if len(raw) > 0 {
				field := rv.Field(col.FieldIndex)
				ptr := reflect.New(field.Type())
				if err := json.Unmarshal(raw, ptr.Interface()); err != nil {
					return zero, 0, fmt.Errorf("unmarshal jsonb field %s: %w", col.Column, err)
				}
				field.Set(ptr.Elem())
			}
		}
	}

	return result, similarity, nil
}

// ForgetAll removes all stored entries for the given identifier.
func (s *Store[T]) ForgetAll(ctx context.Context, identifier string) error {
	if identifier == "" {
		return errors.New("postgres: identifier must not be empty")
	}
	identifierCol := s.schema.Columns[s.schema.IdentifierIdx].Column
	query := fmt.Sprintf(`DELETE FROM %s WHERE %s = $1`, s.tableName, identifierCol)
	if _, err := s.pool.Exec(ctx, query, identifier); err != nil {
		return fmt.Errorf("postgres: forget all: %w", err)
	}
	return nil
}

// Forget removes a single entry by its primary key, scoped to the identifier.
func (s *Store[T]) Forget(ctx context.Context, identifier, id string) error {
	if identifier == "" {
		return errors.New("postgres: identifier must not be empty")
	}
	if id == "" {
		return errors.New("postgres: id must not be empty")
	}
	if s.schema.PKIndex < 0 {
		return errors.New("postgres: struct has no pk field; cannot forget by id")
	}
	pkCol := s.schema.Columns[s.schema.PKIndex].Column
	identifierCol := s.schema.Columns[s.schema.IdentifierIdx].Column
	query := fmt.Sprintf(`DELETE FROM %s WHERE %s = $1 AND %s = $2`, s.tableName, pkCol, identifierCol)
	if _, err := s.pool.Exec(ctx, query, id, identifier); err != nil {
		return fmt.Errorf("postgres: forget: %w", err)
	}
	return nil
}

// Close closes the underlying connection pool.
func (s *Store[T]) Close() {
	s.pool.Close()
}

// setIdentifierField sets the identifier-tagged field on a struct value.
func setIdentifierField[T any](value *T, schema *tableSchema, id string) {
	if schema.IdentifierIdx < 0 {
		return
	}
	col := schema.Columns[schema.IdentifierIdx]
	v := reflect.ValueOf(value).Elem()
	field := v.Field(col.FieldIndex)
	if field.CanSet() && field.Kind() == reflect.String {
		field.SetString(id)
	}
}

// pgvectorOp returns the pgvector distance operator for the given metric.
func pgvectorOp(metric string) string {
	switch metric {
	case "l2":
		return "<->"
	case "inner_product":
		return "<#>"
	default:
		return "<=>"
	}
}

// float64sToFloat32 converts []float64 to []float32.
func float64sToFloat32(v []float64) []float32 {
	out := make([]float32, len(v))
	for i, f := range v {
		out[i] = float32(f)
	}
	return out
}
