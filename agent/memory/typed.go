package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/camilbinas/gude-agents/agent"
)

// TypedMemory is the generic memory interface for user-defined struct types.
// Instead of flattening domain data into Entry{Fact, Metadata}, developers
// define their own Go structs and get full type safety through TypedMemory[T].
// Documented in docs/memory.md — update when changing interface.
type TypedMemory[T any] interface {
	// Remember stores a value for the given identifier.
	// Returns an error if identifier is empty or the content extracted from
	// the value is empty.
	Remember(ctx context.Context, identifier string, value T) error

	// Recall retrieves the top entries for the given identifier by semantic
	// similarity to the query. Returns at most limit results, ordered by
	// descending score.
	// Returns an error if identifier is empty or limit < 1.
	// Returns an empty non-nil slice (not nil) if no entries exist for the identifier.
	Recall(ctx context.Context, identifier string, query string, limit int) ([]TypedEntry[T], error)
}

// TypedEntry wraps a user-defined value with its similarity score.
type TypedEntry[T any] struct {
	Value T
	Score float64
}

// TypedScoredResult is the internal result type from TypedMemoryStore.Search.
type TypedScoredResult[T any] struct {
	Value T
	Score float64
}

// typedDataKey is the reserved metadata key used to store the JSON-serialized
// user struct. The underscore prefix follows the convention established by
// internal metadata keys to avoid collisions with user-defined metadata.
const typedDataKey = "_typed_data"

// encodeTypedValue marshals a value of type T into a metadata map
// with the JSON stored under the _typed_data key.
func encodeTypedValue[T any](value T) (map[string]string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("typed memory codec: marshal: %w", err)
	}
	return map[string]string{
		typedDataKey: string(data),
	}, nil
}

// decodeTypedValue unmarshals a value of type T from a metadata map
// by reading the _typed_data key.
func decodeTypedValue[T any](metadata map[string]string) (T, error) {
	var zero T
	raw, ok := metadata[typedDataKey]
	if !ok {
		return zero, errors.New("typed memory codec: missing _typed_data key in metadata")
	}
	var value T
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return zero, fmt.Errorf("typed memory codec: unmarshal: %w", err)
	}
	return value, nil
}

// TypedMemoryStore wraps a MemoryStore with typed serialization.
// It handles encoding user structs into Document metadata on Add and
// decoding them back on Search.
type TypedMemoryStore[T any] struct {
	store MemoryStore
}

// NewTypedMemoryStore creates a TypedMemoryStore wrapping the given MemoryStore.
func NewTypedMemoryStore[T any](store MemoryStore) *TypedMemoryStore[T] {
	return &TypedMemoryStore[T]{store: store}
}

// NewTypedScopedStore creates a TypedMemoryStore wrapping the given MemoryStore.
//
// Deprecated: Use NewTypedMemoryStore instead.
func NewTypedScopedStore[T any](store MemoryStore) *TypedMemoryStore[T] {
	return NewTypedMemoryStore[T](store)
}

// Add serializes values via the codec, sets Document.Content from contentFunc,
// and delegates to MemoryStore.Add.
func (s *TypedMemoryStore[T]) Add(ctx context.Context, identifier string, values []T, contentFunc func(T) string, embeddings [][]float64) error {
	docs := make([]agent.Document, len(values))
	for i, v := range values {
		meta, err := encodeTypedValue(v)
		if err != nil {
			return fmt.Errorf("typed memory store: encode: %w", err)
		}
		docs[i] = agent.Document{
			Content:  contentFunc(v),
			Metadata: meta,
		}
	}
	return s.store.Add(ctx, identifier, docs, embeddings)
}

// Search delegates to MemoryStore.Search and deserializes each result back
// into type T.
func (s *TypedMemoryStore[T]) Search(ctx context.Context, identifier string, queryEmbedding []float64, topK int) ([]TypedScoredResult[T], error) {
	results, err := s.store.Search(ctx, identifier, queryEmbedding, topK)
	if err != nil {
		return nil, err
	}

	typed := make([]TypedScoredResult[T], 0, len(results))
	for _, sd := range results {
		value, err := decodeTypedValue[T](sd.Document.Metadata)
		if err != nil {
			return nil, fmt.Errorf("typed memory store: decode: %w", err)
		}
		typed = append(typed, TypedScoredResult[T]{
			Value: value,
			Score: sd.Score,
		})
	}
	return typed, nil
}

// Compile-time assertion that TypedAdapter implements TypedMemory.
var _ TypedMemory[struct{}] = (*TypedAdapter[struct{}])(nil)

// TypedAdapter implements TypedMemory[T] by composing a TypedMemoryStore,
// Embedder, and content extraction function.
// Documented in docs/memory.md — update when changing behavior.
type TypedAdapter[T any] struct {
	store       *TypedMemoryStore[T]
	embedder    agent.Embedder
	contentFunc func(T) string
}

// NewTypedAdapter creates a TypedAdapter.
func NewTypedAdapter[T any](store *TypedMemoryStore[T], embedder agent.Embedder, contentFunc func(T) string) *TypedAdapter[T] {
	return &TypedAdapter[T]{
		store:       store,
		embedder:    embedder,
		contentFunc: contentFunc,
	}
}

// Remember implements TypedMemory[T].Remember. It validates inputs, extracts
// content via contentFunc, embeds the text, and stores the value.
func (a *TypedAdapter[T]) Remember(ctx context.Context, identifier string, value T) error {
	if identifier == "" {
		return errors.New("typed memory: identifier must not be empty")
	}

	content := a.contentFunc(value)
	if content == "" {
		return errors.New("typed memory: content must not be empty")
	}

	embedding, err := a.embedder.Embed(ctx, content)
	if err != nil {
		return fmt.Errorf("typed memory: embed: %w", err)
	}

	if err := a.store.Add(ctx, identifier, []T{value}, a.contentFunc, [][]float64{embedding}); err != nil {
		return fmt.Errorf("typed memory: store add: %w", err)
	}

	return nil
}

// Recall implements TypedMemory[T].Recall. It validates inputs, embeds the
// query, searches the store, and converts results to TypedEntry values.
// Returns an empty non-nil slice when no results are found.
func (a *TypedAdapter[T]) Recall(ctx context.Context, identifier string, query string, limit int) ([]TypedEntry[T], error) {
	if identifier == "" {
		return nil, errors.New("typed memory: identifier must not be empty")
	}
	if limit < 1 {
		return nil, errors.New("typed memory: limit must be at least 1")
	}

	embedding, err := a.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("typed memory: embed query: %w", err)
	}

	results, err := a.store.Search(ctx, identifier, embedding, limit)
	if err != nil {
		return nil, fmt.Errorf("typed memory: store search: %w", err)
	}

	entries := make([]TypedEntry[T], 0, len(results))
	for _, r := range results {
		entries = append(entries, TypedEntry[T]{
			Value: r.Value,
			Score: r.Score,
		})
	}

	return entries, nil
}

// NewTypedInMemory creates an in-memory TypedAdapter — the typed equivalent
// of NewInMemory. Creates InMemoryStore → TypedMemoryStore → TypedAdapter.
func NewTypedInMemory[T any](embedder agent.Embedder, contentFunc func(T) string) *TypedAdapter[T] {
	memStore := NewInMemoryStore()
	typedStore := NewTypedMemoryStore[T](memStore)
	return NewTypedAdapter(typedStore, embedder, contentFunc)
}
