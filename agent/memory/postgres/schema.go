package postgres

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
)

// columnRole marks special roles for columns extracted from struct tags.
type columnRole int

const (
	roleNone       columnRole = iota
	rolePK                    // primary key
	roleIdentifier            // scoping column (WHERE identifier = ?)
	roleContent               // field to embed (passed to embedder)
	roleEmbedding             // vector column
	roleJSONB                 // serialize as JSONB (slices, maps)
)

// columnInfo describes a single struct field → column mapping.
type columnInfo struct {
	FieldIndex int        // index in the struct
	Column     string     // SQL column name
	Role       columnRole // special role (pk, identifier, content, etc.)
	IsJSONB    bool       // serialize as JSONB
	NoInput    bool       // exclude from LLM input schema
}

// tableSchema holds the extracted schema for a struct type.
type tableSchema struct {
	Columns       []columnInfo
	PKIndex       int // index into Columns for the PK field (-1 if none)
	IdentifierIdx int // index into Columns for the identifier field (-1 if none)
	ContentIdx    int // index into Columns for the content field (-1 if none)
	EmbeddingCol  string
}

// schemaCache caches parsed schemas by type.
var (
	schemaCacheMu sync.RWMutex
	schemaCache   = make(map[reflect.Type]*tableSchema)
)

// parseSchema extracts column mappings from struct tags.
// Tag format: `db:"column_name,role1,role2"`
// Roles: pk, identifier, content, jsonb
func parseSchema[T any](embeddingCol string) (*tableSchema, error) {
	var zero T
	t := reflect.TypeOf(zero)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("postgres: T must be a struct, got %s", t.Kind())
	}

	// Check cache.
	schemaCacheMu.RLock()
	if cached, ok := schemaCache[t]; ok {
		schemaCacheMu.RUnlock()
		return cached, nil
	}
	schemaCacheMu.RUnlock()

	schema := &tableSchema{
		PKIndex:       -1,
		IdentifierIdx: -1,
		ContentIdx:    -1,
		EmbeddingCol:  embeddingCol,
	}

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
		colName := parts[0]
		if colName == "" {
			continue
		}

		info := columnInfo{
			FieldIndex: i,
			Column:     colName,
		}

		// Parse roles from remaining parts.
		for _, part := range parts[1:] {
			switch strings.TrimSpace(part) {
			case "pk":
				info.Role = rolePK
				info.NoInput = true // PK is system-managed
				schema.PKIndex = len(schema.Columns)
			case "identifier":
				info.Role = roleIdentifier
				info.NoInput = true // identifier comes from context
				schema.IdentifierIdx = len(schema.Columns)
			case "content":
				info.Role = roleContent
				schema.ContentIdx = len(schema.Columns)
			case "jsonb":
				info.IsJSONB = true
			case "noinput":
				info.NoInput = true
			}
		}

		schema.Columns = append(schema.Columns, info)
	}

	if schema.IdentifierIdx == -1 {
		return nil, fmt.Errorf("postgres: struct %s has no field with db:\"...,identifier\" tag", t.Name())
	}
	if schema.ContentIdx == -1 {
		return nil, fmt.Errorf("postgres: struct %s has no field with db:\"...,content\" tag", t.Name())
	}

	// Cache it.
	schemaCacheMu.Lock()
	schemaCache[t] = schema
	schemaCacheMu.Unlock()

	return schema, nil
}

// columnNames returns all column names in order.
func (s *tableSchema) columnNames() []string {
	names := make([]string, len(s.Columns))
	for i, c := range s.Columns {
		names[i] = c.Column
	}
	return names
}

// insertColumns returns column names for INSERT (all columns + embedding).
func (s *tableSchema) insertColumns() string {
	cols := s.columnNames()
	cols = append(cols, s.EmbeddingCol)
	return strings.Join(cols, ", ")
}

// placeholders returns $1, $2, ... $n for the given count.
func placeholders(n int) string {
	parts := make([]string, n)
	for i := range n {
		parts[i] = fmt.Sprintf("$%d", i+1)
	}
	return strings.Join(parts, ", ")
}

// GenerateInputSchema produces a JSON Schema for the LLM tool input,
// excluding fields tagged with pk, identifier, or noinput.
func GenerateInputSchema[T any]() map[string]any {
	var zero T
	t := reflect.TypeOf(zero)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	properties := make(map[string]any)
	var required []string

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}

		// Check db tag for noinput/pk/identifier — skip those.
		dbTag := field.Tag.Get("db")
		if dbTag == "-" {
			continue
		}
		if dbTag != "" {
			parts := strings.Split(dbTag, ",")
			skip := false
			for _, p := range parts[1:] {
				switch strings.TrimSpace(p) {
				case "pk", "identifier", "noinput":
					skip = true
				}
			}
			if skip {
				continue
			}
		}

		// Get JSON field name.
		name := field.Name
		if jsonTag := field.Tag.Get("json"); jsonTag != "" {
			jsonParts := strings.Split(jsonTag, ",")
			if jsonParts[0] == "-" {
				continue
			}
			if jsonParts[0] != "" {
				name = jsonParts[0]
			}
		}

		prop := goTypeToJSONSchema(field.Type)

		if desc := field.Tag.Get("description"); desc != "" {
			prop["description"] = desc
		}
		if enumTag := field.Tag.Get("enum"); enumTag != "" {
			values := strings.Split(enumTag, ",")
			enumSlice := make([]any, len(values))
			for j, v := range values {
				enumSlice[j] = strings.TrimSpace(v)
			}
			prop["enum"] = enumSlice
		}
		if field.Tag.Get("required") == "true" {
			required = append(required, name)
		}

		properties[name] = prop
	}

	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// goTypeToJSONSchema maps a Go type to a JSON Schema type descriptor.
func goTypeToJSONSchema(t reflect.Type) map[string]any {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return map[string]any{"type": "integer"}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Slice, reflect.Array:
		return map[string]any{
			"type":  "array",
			"items": goTypeToJSONSchema(t.Elem()),
		}
	default:
		return map[string]any{"type": "string"}
	}
}
