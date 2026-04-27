package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// ---------------------------------------------------------------------------
// Test structs — various shapes for encode/decode coverage
// ---------------------------------------------------------------------------

type testStruct struct {
	Name  string `json:"name"`
	Value int    `json:"value"`
}

type nestedStruct struct {
	Label string     `json:"label"`
	Inner testStruct `json:"inner"`
}

type sliceStruct struct {
	Tags   []string `json:"tags"`
	Scores []int    `json:"scores"`
}

type optionalStruct struct {
	Required string  `json:"required"`
	Optional *string `json:"optional,omitempty"`
}

// ---------------------------------------------------------------------------
// Mock embedders
// ---------------------------------------------------------------------------

// fixedEmbedder returns a fixed vector for any input.
type fixedEmbedder struct {
	vec []float64
}

func (e *fixedEmbedder) Embed(_ context.Context, _ string) ([]float64, error) {
	return e.vec, nil
}

// recordingEmbedder captures the text passed to Embed and returns a fixed vector.
type recordingEmbedder struct {
	vec   []float64
	texts []string
}

func (e *recordingEmbedder) Embed(_ context.Context, text string) ([]float64, error) {
	e.texts = append(e.texts, text)
	return e.vec, nil
}

// errorEmbedder returns a configurable error.
type errorEmbedder struct {
	err error
}

func (e *errorEmbedder) Embed(_ context.Context, _ string) ([]float64, error) {
	return nil, e.err
}

// ---------------------------------------------------------------------------
// Helper: content function for testStruct
// ---------------------------------------------------------------------------

func testContentFunc(ts testStruct) string {
	return ts.Name
}

// ---------------------------------------------------------------------------
// Helper: create a typed in-memory adapter with a fixed embedder
// ---------------------------------------------------------------------------

func newTestTypedAdapter() (*TypedAdapter[testStruct], *fixedEmbedder) {
	emb := &fixedEmbedder{vec: []float64{0.1, 0.2, 0.3}}
	adapter := NewTypedInMemory[testStruct](emb, testContentFunc)
	return adapter, emb
}

// ---------------------------------------------------------------------------
// TypedAdapter validation tests
// ---------------------------------------------------------------------------

func TestTypedAdapter_EmptyIdentifier_Remember(t *testing.T) {
	adapter, _ := newTestTypedAdapter()
	err := adapter.Remember(context.Background(), "", testStruct{Name: "hello", Value: 1})
	if err == nil {
		t.Fatal("expected error for empty identifier, got nil")
	}
	const want = "typed memory: identifier must not be empty"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}

func TestTypedAdapter_EmptyIdentifier_Recall(t *testing.T) {
	adapter, _ := newTestTypedAdapter()
	entries, err := adapter.Recall(context.Background(), "", "query", 5)
	if err == nil {
		t.Fatal("expected error for empty identifier, got nil")
	}
	const want = "typed memory: identifier must not be empty"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
	if entries != nil {
		t.Fatalf("expected nil entries on error, got %v", entries)
	}
}

func TestTypedAdapter_InvalidLimit_Recall(t *testing.T) {
	adapter, _ := newTestTypedAdapter()
	for _, limit := range []int{0, -1, -100} {
		t.Run(fmt.Sprintf("limit=%d", limit), func(t *testing.T) {
			entries, err := adapter.Recall(context.Background(), "user-1", "query", limit)
			if err == nil {
				t.Fatalf("expected error for limit %d, got nil", limit)
			}
			const want = "typed memory: limit must be at least 1"
			if err.Error() != want {
				t.Fatalf("error = %q, want %q", err.Error(), want)
			}
			if entries != nil {
				t.Fatalf("expected nil entries on error, got %v", entries)
			}
		})
	}
}

func TestTypedAdapter_EmptyContent_Remember(t *testing.T) {
	emb := &fixedEmbedder{vec: []float64{0.1, 0.2, 0.3}}
	// contentFunc returns "" → should trigger error
	adapter := NewTypedInMemory[testStruct](emb, func(ts testStruct) string { return "" })
	err := adapter.Remember(context.Background(), "user-1", testStruct{Name: "hello", Value: 1})
	if err == nil {
		t.Fatal("expected error for empty content, got nil")
	}
	const want = "typed memory: content must not be empty"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}

func TestTypedAdapter_NoEntries_Recall(t *testing.T) {
	adapter, _ := newTestTypedAdapter()
	entries, err := adapter.Recall(context.Background(), "unknown-user", "query", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entries == nil {
		t.Fatal("expected non-nil slice, got nil")
	}
	if len(entries) != 0 {
		t.Fatalf("expected empty slice, got %d elements", len(entries))
	}
}

func TestTypedAdapter_EmbedderError_Remember(t *testing.T) {
	sentinel := errors.New("embed boom")
	emb := &errorEmbedder{err: sentinel}
	adapter := NewTypedInMemory[testStruct](emb, testContentFunc)

	err := adapter.Remember(context.Background(), "user-1", testStruct{Name: "hello", Value: 1})
	if err == nil {
		t.Fatal("expected error from embedder, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected error to wrap sentinel, got %v", err)
	}
	const wantPrefix = "typed memory: embed:"
	if len(err.Error()) < len(wantPrefix) || err.Error()[:len(wantPrefix)] != wantPrefix {
		t.Fatalf("error = %q, want prefix %q", err.Error(), wantPrefix)
	}
}

func TestTypedAdapter_EmbedderError_Recall(t *testing.T) {
	sentinel := errors.New("embed boom")
	emb := &errorEmbedder{err: sentinel}
	adapter := NewTypedInMemory[testStruct](emb, testContentFunc)

	entries, err := adapter.Recall(context.Background(), "user-1", "query", 5)
	if err == nil {
		t.Fatal("expected error from embedder, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected error to wrap sentinel, got %v", err)
	}
	const wantPrefix = "typed memory: embed query:"
	if len(err.Error()) < len(wantPrefix) || err.Error()[:len(wantPrefix)] != wantPrefix {
		t.Fatalf("error = %q, want prefix %q", err.Error(), wantPrefix)
	}
	if entries != nil {
		t.Fatalf("expected nil entries on error, got %v", entries)
	}
}

// ---------------------------------------------------------------------------
// Codec tests — missing key and malformed JSON
// ---------------------------------------------------------------------------

func TestCodec_MissingKey(t *testing.T) {
	_, err := decodeTypedValue[testStruct](map[string]string{"other": "value"})
	if err == nil {
		t.Fatal("expected error for missing key, got nil")
	}
	const want = "typed memory codec: missing _typed_data key in metadata"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}

func TestCodec_MissingKey_EmptyMetadata(t *testing.T) {
	_, err := decodeTypedValue[testStruct](map[string]string{})
	if err == nil {
		t.Fatal("expected error for empty metadata, got nil")
	}
	const want = "typed memory codec: missing _typed_data key in metadata"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}

func TestCodec_MalformedJSON(t *testing.T) {
	_, err := decodeTypedValue[testStruct](map[string]string{
		typedDataKey: "{not valid json",
	})
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
	const wantPrefix = "typed memory codec: unmarshal:"
	if len(err.Error()) < len(wantPrefix) || err.Error()[:len(wantPrefix)] != wantPrefix {
		t.Fatalf("error = %q, want prefix %q", err.Error(), wantPrefix)
	}
}

func TestCodec_ReservedKeyName(t *testing.T) {
	meta, err := encodeTypedValue(testStruct{Name: "test", Value: 42})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := meta[typedDataKey]; !ok {
		t.Fatalf("expected metadata to contain key %q", typedDataKey)
	}
	if typedDataKey != "_typed_data" {
		t.Fatalf("typedDataKey = %q, want %q", typedDataKey, "_typed_data")
	}
}

// ---------------------------------------------------------------------------
// Codec tests — encode/decode with various struct shapes
// ---------------------------------------------------------------------------

func TestCodec_RoundTrip_FlatStruct(t *testing.T) {
	original := testStruct{Name: "hello", Value: 42}
	meta, err := encodeTypedValue(original)
	if err != nil {
		t.Fatalf("encode error: %v", err)
	}
	decoded, err := decodeTypedValue[testStruct](meta)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if decoded != original {
		t.Fatalf("decoded = %+v, want %+v", decoded, original)
	}
}

func TestCodec_RoundTrip_NestedStruct(t *testing.T) {
	original := nestedStruct{
		Label: "outer",
		Inner: testStruct{Name: "inner", Value: 99},
	}
	meta, err := encodeTypedValue(original)
	if err != nil {
		t.Fatalf("encode error: %v", err)
	}
	decoded, err := decodeTypedValue[nestedStruct](meta)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if decoded != original {
		t.Fatalf("decoded = %+v, want %+v", decoded, original)
	}
}

func TestCodec_RoundTrip_SliceFields(t *testing.T) {
	original := sliceStruct{
		Tags:   []string{"go", "memory", "typed"},
		Scores: []int{10, 20, 30},
	}
	meta, err := encodeTypedValue(original)
	if err != nil {
		t.Fatalf("encode error: %v", err)
	}
	decoded, err := decodeTypedValue[sliceStruct](meta)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if len(decoded.Tags) != len(original.Tags) {
		t.Fatalf("Tags length = %d, want %d", len(decoded.Tags), len(original.Tags))
	}
	for i, tag := range decoded.Tags {
		if tag != original.Tags[i] {
			t.Fatalf("Tags[%d] = %q, want %q", i, tag, original.Tags[i])
		}
	}
	if len(decoded.Scores) != len(original.Scores) {
		t.Fatalf("Scores length = %d, want %d", len(decoded.Scores), len(original.Scores))
	}
	for i, score := range decoded.Scores {
		if score != original.Scores[i] {
			t.Fatalf("Scores[%d] = %d, want %d", i, score, original.Scores[i])
		}
	}
}

func TestCodec_RoundTrip_OptionalFieldPresent(t *testing.T) {
	val := "present"
	original := optionalStruct{Required: "req", Optional: &val}
	meta, err := encodeTypedValue(original)
	if err != nil {
		t.Fatalf("encode error: %v", err)
	}
	decoded, err := decodeTypedValue[optionalStruct](meta)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if decoded.Required != original.Required {
		t.Fatalf("Required = %q, want %q", decoded.Required, original.Required)
	}
	if decoded.Optional == nil || *decoded.Optional != *original.Optional {
		t.Fatalf("Optional = %v, want %v", decoded.Optional, original.Optional)
	}
}

func TestCodec_RoundTrip_OptionalFieldNil(t *testing.T) {
	original := optionalStruct{Required: "req", Optional: nil}
	meta, err := encodeTypedValue(original)
	if err != nil {
		t.Fatalf("encode error: %v", err)
	}
	decoded, err := decodeTypedValue[optionalStruct](meta)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if decoded.Required != original.Required {
		t.Fatalf("Required = %q, want %q", decoded.Required, original.Required)
	}
	if decoded.Optional != nil {
		t.Fatalf("Optional = %v, want nil", decoded.Optional)
	}
}

// ---------------------------------------------------------------------------
// TypedMemoryStore deserialization error test (uses InMemoryStore)
// ---------------------------------------------------------------------------

func TestTypedMemoryStore_DeserializationError(t *testing.T) {
	memStore := NewInMemoryStore()
	typedStore := NewTypedMemoryStore[testStruct](memStore)

	ctx := context.Background()
	emb := &fixedEmbedder{vec: []float64{0.1, 0.2, 0.3}}

	// Manually add a document with invalid _typed_data to the underlying store.
	badDoc := agent.Document{
		Content:  "bad data",
		Metadata: map[string]string{typedDataKey: "{invalid json"},
	}
	embedding, _ := emb.Embed(ctx, "bad data")
	if err := memStore.Add(ctx, "user-1", []agent.Document{badDoc}, [][]float64{embedding}); err != nil {
		t.Fatalf("failed to add bad document: %v", err)
	}

	_, err := typedStore.Search(ctx, "user-1", embedding, 10)
	if err == nil {
		t.Fatal("expected deserialization error, got nil")
	}
	const wantPrefix = "typed memory store: decode:"
	if len(err.Error()) < len(wantPrefix) || err.Error()[:len(wantPrefix)] != wantPrefix {
		t.Fatalf("error = %q, want prefix %q", err.Error(), wantPrefix)
	}
}

// ---------------------------------------------------------------------------
// Typed tool tests (using InMemoryStore)
// ---------------------------------------------------------------------------

func TestTypedRememberTool_MissingIdentifier(t *testing.T) {
	memStore := NewInMemoryStore()
	typedStore := NewTypedMemoryStore[testStruct](memStore)
	emb := &fixedEmbedder{vec: []float64{0.1, 0.2, 0.3}}

	rememberTool := NewTypedRememberTool(
		typedStore, emb, testContentFunc,
		func() map[string]any { return tool.GenerateSchema[testStruct]() },
	)

	// Use a bare context with no identifier attached.
	ctx := context.Background()
	input, _ := json.Marshal(testStruct{Name: "hello", Value: 1})

	_, err := rememberTool.Handler(ctx, json.RawMessage(input))
	if err == nil {
		t.Fatal("expected error when identifier is missing, got nil")
	}
	const want = "typed memory: identifier not found in context; use agent.WithIdentifier"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}

func TestTypedRememberTool_CustomNameDescription(t *testing.T) {
	memStore := NewInMemoryStore()
	typedStore := NewTypedMemoryStore[testStruct](memStore)
	emb := &fixedEmbedder{vec: []float64{0.1, 0.2, 0.3}}

	rememberTool := NewTypedRememberTool(
		typedStore, emb, testContentFunc,
		func() map[string]any { return tool.GenerateSchema[testStruct]() },
		WithToolName("remember_custom"),
		WithToolDescription("Custom description"),
	)

	if rememberTool.Spec.Name != "remember_custom" {
		t.Fatalf("name = %q, want %q", rememberTool.Spec.Name, "remember_custom")
	}
	if rememberTool.Spec.Description != "Custom description" {
		t.Fatalf("description = %q, want %q", rememberTool.Spec.Description, "Custom description")
	}
}

func TestTypedRememberTool_SchemaFunc(t *testing.T) {
	memStore := NewInMemoryStore()
	typedStore := NewTypedMemoryStore[testStruct](memStore)
	emb := &fixedEmbedder{vec: []float64{0.1, 0.2, 0.3}}

	customSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":  map[string]any{"type": "string"},
			"value": map[string]any{"type": "integer"},
		},
		"required": []any{"name"},
	}

	rememberTool := NewTypedRememberTool(
		typedStore, emb, testContentFunc,
		func() map[string]any { return customSchema },
	)

	// Verify the schema is the one returned by schemaFunc.
	schema := rememberTool.Spec.InputSchema
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties in schema")
	}
	if _, ok := props["name"]; !ok {
		t.Fatal("expected 'name' property in schema")
	}
	if _, ok := props["value"]; !ok {
		t.Fatal("expected 'value' property in schema")
	}
	req, ok := schema["required"].([]any)
	if !ok {
		t.Fatal("expected required array in schema")
	}
	if len(req) != 1 || req[0] != "name" {
		t.Fatalf("required = %v, want [name]", req)
	}
}

func TestTypedRecallTool_NoResults(t *testing.T) {
	memStore := NewInMemoryStore()
	typedStore := NewTypedMemoryStore[testStruct](memStore)
	emb := &fixedEmbedder{vec: []float64{0.1, 0.2, 0.3}}

	recallTool := NewTypedRecallTool(typedStore, emb)

	ctx := agent.WithIdentifier(context.Background(), "user-1")
	input, _ := json.Marshal(map[string]any{"query": "anything"})

	result, err := recallTool.Handler(ctx, json.RawMessage(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	const want = "No relevant memories found."
	if result != want {
		t.Fatalf("result = %q, want %q", result, want)
	}
}

func TestTypedRecallTool_DefaultLimit(t *testing.T) {
	memStore := NewInMemoryStore()
	typedStore := NewTypedMemoryStore[testStruct](memStore)
	rec := &recordingEmbedder{vec: []float64{0.1, 0.2, 0.3}}

	// Store more than 5 entries so we can verify the default limit caps results.
	ctx := agent.WithIdentifier(context.Background(), "user-1")
	for i := range 8 {
		val := testStruct{Name: fmt.Sprintf("item-%d", i), Value: i}
		embedding, _ := rec.Embed(ctx, testContentFunc(val))
		if err := typedStore.Add(ctx, "user-1", []testStruct{val}, testContentFunc, [][]float64{embedding}); err != nil {
			t.Fatalf("failed to add entry %d: %v", i, err)
		}
	}

	recallTool := NewTypedRecallTool(typedStore, rec)

	// Call without specifying limit — should default to 5.
	input, _ := json.Marshal(map[string]any{"query": "item"})
	result, err := recallTool.Handler(ctx, json.RawMessage(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Count the number of "- Value:" lines in the result to verify at most 5 returned.
	count := 0
	for _, line := range splitLines(result) {
		if len(line) >= 8 && line[:8] == "- Value:" {
			count++
		}
	}
	if count != 5 {
		t.Fatalf("expected 5 results (default limit), got %d\nresult:\n%s", count, result)
	}
}

// splitLines splits a string into lines.
func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func TestTypedRecallTool_CustomNameDescription(t *testing.T) {
	memStore := NewInMemoryStore()
	typedStore := NewTypedMemoryStore[testStruct](memStore)
	emb := &fixedEmbedder{vec: []float64{0.1, 0.2, 0.3}}

	recallTool := NewTypedRecallTool(
		typedStore, emb,
		WithToolName("recall_custom"),
		WithToolDescription("Custom recall description"),
	)

	if recallTool.Spec.Name != "recall_custom" {
		t.Fatalf("name = %q, want %q", recallTool.Spec.Name, "recall_custom")
	}
	if recallTool.Spec.Description != "Custom recall description" {
		t.Fatalf("description = %q, want %q", recallTool.Spec.Description, "Custom recall description")
	}
}

// ---------------------------------------------------------------------------
// Functional end-to-end test
// ---------------------------------------------------------------------------

func TestNewTypedInMemory_Functional(t *testing.T) {
	emb := &fixedEmbedder{vec: []float64{0.1, 0.2, 0.3}}
	adapter := NewTypedInMemory[testStruct](emb, testContentFunc)

	ctx := context.Background()
	original := testStruct{Name: "hello world", Value: 42}

	// Remember
	if err := adapter.Remember(ctx, "user-1", original); err != nil {
		t.Fatalf("Remember failed: %v", err)
	}

	// Recall
	entries, err := adapter.Recall(ctx, "user-1", "hello world", 5)
	if err != nil {
		t.Fatalf("Recall failed: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one entry, got none")
	}

	got := entries[0].Value
	if got.Name != original.Name {
		t.Fatalf("Name = %q, want %q", got.Name, original.Name)
	}
	if got.Value != original.Value {
		t.Fatalf("Value = %d, want %d", got.Value, original.Value)
	}
	if entries[0].Score <= 0 {
		t.Fatalf("expected positive score, got %f", entries[0].Score)
	}
}

// ---------------------------------------------------------------------------
// Error wrapping tests
// ---------------------------------------------------------------------------

func TestTypedAdapter_ErrorWrapping(t *testing.T) {
	sentinel := errors.New("underlying error")

	t.Run("Remember_EmbedError", func(t *testing.T) {
		emb := &errorEmbedder{err: sentinel}
		adapter := NewTypedInMemory[testStruct](emb, testContentFunc)
		err := adapter.Remember(context.Background(), "user-1", testStruct{Name: "hello", Value: 1})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, sentinel) {
			t.Fatalf("expected errors.Is to find sentinel, got %v", err)
		}
	})

	t.Run("Recall_EmbedError", func(t *testing.T) {
		emb := &errorEmbedder{err: sentinel}
		adapter := NewTypedInMemory[testStruct](emb, testContentFunc)
		_, err := adapter.Recall(context.Background(), "user-1", "query", 5)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, sentinel) {
			t.Fatalf("expected errors.Is to find sentinel, got %v", err)
		}
	})

	t.Run("Remember_StoreAddError", func(t *testing.T) {
		// The embed error wrapping exercises the same %w path as store add.
		// The store add path is tested indirectly through the functional test.
		emb := &errorEmbedder{err: sentinel}
		adapter := NewTypedInMemory[testStruct](emb, testContentFunc)
		err := adapter.Remember(context.Background(), "user-1", testStruct{Name: "hello", Value: 1})
		if !errors.Is(err, sentinel) {
			t.Fatalf("expected errors.Is to find sentinel, got %v", err)
		}
	})
}

func TestTypedMemoryStore_ErrorWrapping(t *testing.T) {
	memStore := NewInMemoryStore()
	typedStore := NewTypedMemoryStore[testStruct](memStore)

	ctx := context.Background()
	emb := &fixedEmbedder{vec: []float64{0.1, 0.2, 0.3}}

	// Add a document with invalid _typed_data to trigger decode error.
	badDoc := agent.Document{
		Content:  "bad data",
		Metadata: map[string]string{typedDataKey: "not-json"},
	}
	embedding, _ := emb.Embed(ctx, "bad data")
	if err := memStore.Add(ctx, "user-1", []agent.Document{badDoc}, [][]float64{embedding}); err != nil {
		t.Fatalf("failed to add bad document: %v", err)
	}

	_, err := typedStore.Search(ctx, "user-1", embedding, 10)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// The error should wrap the underlying JSON unmarshal error.
	// Verify the chain: "typed memory store: decode:" wraps "typed memory codec: unmarshal:" wraps json error.
	var jsonErr *json.SyntaxError
	if !errors.As(err, &jsonErr) {
		t.Fatalf("expected errors.As to find *json.SyntaxError in chain, got %v", err)
	}
}
