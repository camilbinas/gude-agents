package memory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// Store is an in-memory memory store that maps Go struct fields using `db`
// struct tags. It uses brute-force cosine similarity for vector search.
// Safe for concurrent use. Useful for development, testing, and examples.
type Store[T any] struct {
	mu       sync.RWMutex
	entries  map[string][]memEntry[T] // keyed by identifier
	embedder agent.Embedder
	schema   *memSchema
}

type memEntry[T any] struct {
	id        string
	value     T
	embedding []float64
}

type memSchema struct {
	pkIdx      int
	identIdx   int
	contentIdx int
}

// NewStore creates an in-memory Store for the given struct type T.
// It parses `db` struct tags to identify the pk, identifier, and content fields.
func NewStore[T any](embedder agent.Embedder) (*Store[T], error) {
	if embedder == nil {
		return nil, errors.New("memory: embedder is required")
	}
	schema, err := parseMemSchema[T]()
	if err != nil {
		return nil, err
	}
	return &Store[T]{
		entries:  make(map[string][]memEntry[T]),
		embedder: embedder,
		schema:   schema,
	}, nil
}

// Remember stores a value for the given identifier.
func (s *Store[T]) Remember(ctx context.Context, identifier string, value T) error {
	if identifier == "" {
		return errors.New("memory: identifier must not be empty")
	}
	setMemIdentifier(&value, s.schema, identifier)

	content := s.extractContent(value)
	if content == "" {
		return errors.New("memory: content field is empty")
	}

	embedding, err := s.embedder.Embed(ctx, content)
	if err != nil {
		return fmt.Errorf("memory: embed: %w", err)
	}

	id := s.extractPK(value)
	if id == "" {
		id = randomID()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[identifier] = append(s.entries[identifier], memEntry[T]{
		id: id, value: value, embedding: embedding,
	})
	return nil
}

// Recall retrieves values by semantic similarity to the query.
func (s *Store[T]) Recall(ctx context.Context, identifier string, query string, limit int, opts ...RecallOption) ([]Entry[T], error) {
	if identifier == "" {
		return nil, errors.New("memory: identifier must not be empty")
	}
	if limit < 1 {
		return nil, errors.New("memory: limit must be at least 1")
	}

	embedding, err := s.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("memory: embed query: %w", err)
	}

	s.mu.RLock()
	bucket := s.entries[identifier]
	s.mu.RUnlock()

	if len(bucket) == 0 {
		return []Entry[T]{}, nil
	}

	type scored struct {
		entry memEntry[T]
		score float64
	}
	results := make([]scored, len(bucket))
	for i, e := range bucket {
		results[i] = scored{entry: e, score: cosineSimilarity(embedding, e.embedding)}
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})
	if limit > len(results) {
		limit = len(results)
	}

	entries := make([]Entry[T], limit)
	for i := 0; i < limit; i++ {
		entries[i] = Entry[T]{ID: results[i].entry.id, Value: results[i].entry.value, Score: results[i].score}
	}
	return entries, nil
}

// Update replaces an existing entry by ID with new content and re-embeds it.
func (s *Store[T]) Update(ctx context.Context, identifier, id string, value T) error {
	if identifier == "" {
		return errors.New("memory: identifier must not be empty")
	}
	if id == "" {
		return errors.New("memory: id must not be empty")
	}
	setMemIdentifier(&value, s.schema, identifier)

	content := s.extractContent(value)
	if content == "" {
		return errors.New("memory: content field is empty")
	}

	embedding, err := s.embedder.Embed(ctx, content)
	if err != nil {
		return fmt.Errorf("memory: embed: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	bucket := s.entries[identifier]
	for i, e := range bucket {
		if e.id == id {
			s.entries[identifier][i] = memEntry[T]{id: id, value: value, embedding: embedding}
			return nil
		}
	}
	return fmt.Errorf("memory: entry %q not found", id)
}

// ForgetAll removes all entries for the given identifier.
func (s *Store[T]) ForgetAll(ctx context.Context, identifier string) error {
	if identifier == "" {
		return errors.New("memory: identifier must not be empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, identifier)
	return nil
}

// Forget removes a single entry by ID.
func (s *Store[T]) Forget(ctx context.Context, identifier, id string) error {
	if identifier == "" {
		return errors.New("memory: identifier must not be empty")
	}
	if id == "" {
		return errors.New("memory: id must not be empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	bucket := s.entries[identifier]
	for i, e := range bucket {
		if e.id == id {
			s.entries[identifier] = append(bucket[:i], bucket[i+1:]...)
			return nil
		}
	}
	return nil
}

// --- Tools ---

// ToolOption configures tool metadata.
type ToolOption func(*toolCfg)
type toolCfg struct{ name, description string }

// WithToolName sets the tool name.
func WithToolName(name string) ToolOption {
	return func(c *toolCfg) {
		if name != "" {
			c.name = name
		}
	}
}

// WithToolDescription sets the tool description.
func WithToolDescription(desc string) ToolOption {
	return func(c *toolCfg) {
		if desc != "" {
			c.description = desc
		}
	}
}

// NewRememberTool creates a tool that stores values into an in-memory Store.
func NewRememberTool[T any](store *Store[T], opts ...ToolOption) tool.Tool {
	cfg := &toolCfg{name: "remember", description: "Store a memory entry for later recall."}
	for _, o := range opts {
		o(cfg)
	}
	schema := generateMemSchema[T]()
	return tool.NewRaw(cfg.name, cfg.description, schema,
		func(ctx context.Context, input json.RawMessage) (string, error) {
			id := identifierFromContext(ctx)
			if id == "" {
				return "", errors.New("memory: identifier not found in context; use c.WithIdentifier")
			}
			var value T
			if err := json.Unmarshal(input, &value); err != nil {
				return "", fmt.Errorf("memory: unmarshal: %w", err)
			}
			return "Remembered.", store.Remember(ctx, id, value)
		},
	)
}

// NewUpdateTool creates a tool that updates an existing entry by ID.
func NewUpdateTool[T any](store *Store[T], opts ...ToolOption) tool.Tool {
	cfg := &toolCfg{name: "update", description: "Update an existing memory entry by its ID."}
	for _, o := range opts {
		o(cfg)
	}
	schema := generateMemSchema[T]()
	props := schema["properties"].(map[string]any)
	props["id"] = map[string]any{"type": "string", "description": "The ID of the entry to update (from a previous recall)."}
	if req, ok := schema["required"].([]string); ok {
		schema["required"] = append(req, "id")
	} else {
		schema["required"] = []any{"id"}
	}
	return tool.NewRaw(cfg.name, cfg.description, schema,
		func(ctx context.Context, input json.RawMessage) (string, error) {
			identifier := identifierFromContext(ctx)
			if identifier == "" {
				return "", errors.New("memory: identifier not found in context; use c.WithIdentifier")
			}
			var params struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return "", err
			}
			var value T
			if err := json.Unmarshal(input, &value); err != nil {
				return "", fmt.Errorf("memory: unmarshal: %w", err)
			}
			if err := store.Update(ctx, identifier, params.ID, value); err != nil {
				return "", err
			}
			return "Updated.", nil
		},
	)
}

// NewRecallTool creates a tool that retrieves values from an in-memory Store.
func NewRecallTool[T any](store *Store[T], opts ...ToolOption) tool.Tool {
	cfg := &toolCfg{name: "recall", description: "Retrieve relevant memory entries by semantic similarity."}
	for _, o := range opts {
		o(cfg)
	}
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string", "description": "A natural-language query describing what to recall."},
			"limit": map[string]any{"type": "integer", "description": "Maximum number of results. Defaults to 5."},
		},
		"required": []any{"query"},
	}
	return tool.NewRaw(cfg.name, cfg.description, schema,
		func(ctx context.Context, input json.RawMessage) (string, error) {
			id := identifierFromContext(ctx)
			if id == "" {
				return "", errors.New("memory: identifier not found in context; use c.WithIdentifier")
			}
			var params struct {
				Query string `json:"query"`
				Limit int    `json:"limit"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return "", err
			}
			if params.Limit == 0 {
				params.Limit = 5
			}
			results, err := store.Recall(ctx, id, params.Query, params.Limit)
			if err != nil {
				return "", err
			}
			if len(results) == 0 {
				return "No relevant memories found.", nil
			}
			var b strings.Builder
			for i, r := range results {
				if i > 0 {
					b.WriteString("\n")
				}
				data, _ := json.Marshal(r.Value)
				fmt.Fprintf(&b, "- %s\n  ID: %s\n  Score: %.4f", string(data), r.ID, r.Score)
			}
			return b.String(), nil
		},
	)
}

// NewForgetTool creates a tool that removes a single entry by ID.
func NewForgetTool[T any](store *Store[T], opts ...ToolOption) tool.Tool {
	cfg := &toolCfg{name: "forget", description: "Remove a specific memory entry by its ID."}
	for _, o := range opts {
		o(cfg)
	}
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id": map[string]any{"type": "string", "description": "The ID of the entry to forget."},
		},
		"required": []any{"id"},
	}
	return tool.NewRaw(cfg.name, cfg.description, schema,
		func(ctx context.Context, input json.RawMessage) (string, error) {
			identifier := identifierFromContext(ctx)
			if identifier == "" {
				return "", errors.New("memory: identifier not found in context; use c.WithIdentifier")
			}
			var params struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return "", err
			}
			return "Forgotten.", store.Forget(ctx, identifier, params.ID)
		},
	)
}

// --- Internal helpers ---

// identifierFromContext extracts the identifier from a context.Context.
func identifierFromContext(ctx context.Context) string {
	if c := agent.FromContext(ctx); c != nil {
		return c.Identifier()
	}
	return ""
}

func parseMemSchema[T any]() (*memSchema, error) {
	var zero T
	t := reflect.TypeOf(zero)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("memory: T must be a struct, got %s", t.Kind())
	}
	schema := &memSchema{pkIdx: -1, identIdx: -1, contentIdx: -1}
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
		for _, p := range parts[1:] {
			switch strings.TrimSpace(p) {
			case "pk":
				schema.pkIdx = i
			case "identifier":
				schema.identIdx = i
			case "content":
				schema.contentIdx = i
			}
		}
	}
	if schema.identIdx == -1 {
		return nil, errors.New("memory: struct must have a field with db:\"...,identifier\" tag")
	}
	if schema.contentIdx == -1 {
		return nil, errors.New("memory: struct must have a field with db:\"...,content\" tag")
	}
	return schema, nil
}

func (s *Store[T]) extractContent(value T) string {
	v := reflect.ValueOf(value)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	return fmt.Sprintf("%v", v.Field(s.schema.contentIdx).Interface())
}

func (s *Store[T]) extractPK(value T) string {
	if s.schema.pkIdx < 0 {
		return ""
	}
	v := reflect.ValueOf(value)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	pk := fmt.Sprintf("%v", v.Field(s.schema.pkIdx).Interface())
	if pk == "" {
		return ""
	}
	return pk
}

func setMemIdentifier[T any](value *T, schema *memSchema, id string) {
	v := reflect.ValueOf(value).Elem()
	field := v.Field(schema.identIdx)
	if field.CanSet() && field.Kind() == reflect.String {
		field.SetString(id)
	}
}

func generateMemSchema[T any]() map[string]any {
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
		dbTag := field.Tag.Get("db")
		if dbTag == "" || dbTag == "-" {
			continue
		}
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
		name := field.Name
		if jsonTag := field.Tag.Get("json"); jsonTag != "" {
			jp := strings.Split(jsonTag, ",")
			if jp[0] == "-" {
				continue
			}
			if jp[0] != "" {
				name = jp[0]
			}
		}
		prop := tool.GoTypeToSchema(field.Type)
		if desc := field.Tag.Get("description"); desc != "" {
			prop["description"] = desc
		}
		if field.Tag.Get("required") == "true" {
			required = append(required, name)
		}
		properties[name] = prop
	}
	schema := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func cosineSimilarity(a, b []float64) float64 {
	n := len(a)
	if n != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := 0; i < n; i++ {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	magA := math.Sqrt(normA)
	magB := math.Sqrt(normB)
	if magA == 0 || magB == 0 {
		return 0
	}
	return dot / (magA * magB)
}

func randomID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
