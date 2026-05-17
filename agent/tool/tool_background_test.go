package tool

import (
	"context"
	"encoding/json"
	"testing"
)

// --- NewBackground[T] constructor tests ---

type bgInput struct {
	Query string `json:"query" required:"true" description:"Search query"`
	Limit int    `json:"limit" description:"Max results"`
}

func TestNewBackground_PopulatesSpec(t *testing.T) {
	tl := NewBackground("search", "Run a search", "searching…", func(_ context.Context, in bgInput) (string, error) {
		return "done", nil
	})

	if tl.Spec.Name != "search" {
		t.Errorf("expected Name=%q, got %q", "search", tl.Spec.Name)
	}
	if tl.Spec.Description != "Run a search" {
		t.Errorf("expected Description=%q, got %q", "Run a search", tl.Spec.Description)
	}
	if tl.Spec.InputSchema == nil {
		t.Fatal("expected non-nil InputSchema")
	}

	// Verify schema matches the struct type T.
	props, ok := tl.Spec.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected InputSchema to have 'properties'")
	}
	if _, ok := props["query"]; !ok {
		t.Error("expected 'query' property in schema")
	}
	if _, ok := props["limit"]; !ok {
		t.Error("expected 'limit' property in schema")
	}

	// Verify schema matches GenerateSchema[bgInput]() output.
	expected := GenerateSchema[bgInput]()
	expectedProps := expected["properties"].(map[string]any)
	for key := range expectedProps {
		if _, ok := props[key]; !ok {
			t.Errorf("expected property %q in InputSchema", key)
		}
	}
}

func TestNewBackground_IsBackground(t *testing.T) {
	tl := NewBackground("bg", "desc", "ack!", func(_ context.Context, in bgInput) (string, error) {
		return "", nil
	})

	if !tl.IsBackground() {
		t.Error("expected IsBackground() == true")
	}
}

func TestNewBackground_Ack(t *testing.T) {
	const ack = "working on it…"
	tl := NewBackground("bg", "desc", ack, func(_ context.Context, in bgInput) (string, error) {
		return "", nil
	})

	if tl.Ack() != ack {
		t.Errorf("expected Ack()=%q, got %q", ack, tl.Ack())
	}
}

func TestNewBackground_HandlerRoundTrips(t *testing.T) {
	var received bgInput
	tl := NewBackground("bg", "desc", "ack", func(_ context.Context, in bgInput) (string, error) {
		received = in
		return "result:" + in.Query, nil
	})

	input := bgInput{Query: "hello", Limit: 42}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	result, err := tl.Handler(context.Background(), json.RawMessage(raw))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	if received.Query != "hello" {
		t.Errorf("expected Query=%q, got %q", "hello", received.Query)
	}
	if received.Limit != 42 {
		t.Errorf("expected Limit=%d, got %d", 42, received.Limit)
	}
	if result != "result:hello" {
		t.Errorf("expected result=%q, got %q", "result:hello", result)
	}
}

func TestNewBackground_HandlerInvalidJSON(t *testing.T) {
	tl := NewBackground("bg", "desc", "ack", func(_ context.Context, in bgInput) (string, error) {
		return "should not reach", nil
	})

	_, err := tl.Handler(context.Background(), json.RawMessage(`{invalid`))
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestNewBackground_RichHandlerIsNil(t *testing.T) {
	tl := NewBackground("bg", "desc", "ack", func(_ context.Context, in bgInput) (string, error) {
		return "", nil
	})

	if tl.RichHandler != nil {
		t.Error("expected RichHandler to be nil for Background_Tool")
	}
}

// --- NewBackgroundRaw constructor tests ---

func TestNewBackgroundRaw_StoresSchemaVerbatim(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{"type": "string"},
		},
		"required": []string{"url"},
	}

	tl := NewBackgroundRaw("fetch", "Fetch URL", "fetching…", schema, func(_ context.Context, input json.RawMessage) (string, error) {
		return "", nil
	})

	if tl.Spec.Name != "fetch" {
		t.Errorf("expected Name=%q, got %q", "fetch", tl.Spec.Name)
	}
	if tl.Spec.Description != "Fetch URL" {
		t.Errorf("expected Description=%q, got %q", "Fetch URL", tl.Spec.Description)
	}

	// Schema should be the exact same map reference.
	props, ok := tl.Spec.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected InputSchema to have 'properties'")
	}
	if _, ok := props["url"]; !ok {
		t.Error("expected 'url' property in schema")
	}
	req, ok := tl.Spec.InputSchema["required"].([]string)
	if !ok {
		t.Fatal("expected InputSchema to have 'required'")
	}
	if len(req) != 1 || req[0] != "url" {
		t.Errorf("expected required=[url], got %v", req)
	}
}

func TestNewBackgroundRaw_NilSchemaDefaults(t *testing.T) {
	tl := NewBackgroundRaw("noop", "No-op", "ok", nil, func(_ context.Context, input json.RawMessage) (string, error) {
		return "", nil
	})

	if tl.Spec.InputSchema == nil {
		t.Fatal("expected non-nil InputSchema when nil schema passed")
	}
	if tl.Spec.InputSchema["type"] != "object" {
		t.Errorf("expected default schema type=object, got %v", tl.Spec.InputSchema["type"])
	}
}

func TestNewBackgroundRaw_IsBackground(t *testing.T) {
	tl := NewBackgroundRaw("bg", "desc", "ack", nil, func(_ context.Context, input json.RawMessage) (string, error) {
		return "", nil
	})

	if !tl.IsBackground() {
		t.Error("expected IsBackground() == true")
	}
}

func TestNewBackgroundRaw_Ack(t *testing.T) {
	const ack = "processing…"
	tl := NewBackgroundRaw("bg", "desc", ack, nil, func(_ context.Context, input json.RawMessage) (string, error) {
		return "", nil
	})

	if tl.Ack() != ack {
		t.Errorf("expected Ack()=%q, got %q", ack, tl.Ack())
	}
}

func TestNewBackgroundRaw_HandlerForwardsRawMessage(t *testing.T) {
	var received json.RawMessage
	tl := NewBackgroundRaw("bg", "desc", "ack", nil, func(_ context.Context, input json.RawMessage) (string, error) {
		received = input
		return "got:" + string(input), nil
	})

	payload := json.RawMessage(`{"key":"value"}`)
	result, err := tl.Handler(context.Background(), payload)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	if string(received) != `{"key":"value"}` {
		t.Errorf("expected received=%q, got %q", `{"key":"value"}`, string(received))
	}
	if result != `got:{"key":"value"}` {
		t.Errorf("expected result=%q, got %q", `got:{"key":"value"}`, result)
	}
}

func TestNewBackgroundRaw_RichHandlerIsNil(t *testing.T) {
	tl := NewBackgroundRaw("bg", "desc", "ack", nil, func(_ context.Context, input json.RawMessage) (string, error) {
		return "", nil
	})

	if tl.RichHandler != nil {
		t.Error("expected RichHandler to be nil for Background_Tool")
	}
}

// --- Non-background tools should report IsBackground() == false ---

func TestNonBackgroundTool_IsBackgroundFalse(t *testing.T) {
	tl := New("sync", "A sync tool", func(_ context.Context, in bgInput) (string, error) {
		return "", nil
	})

	if tl.IsBackground() {
		t.Error("expected IsBackground() == false for tool.New")
	}
	if tl.Ack() != "" {
		t.Errorf("expected Ack()=%q for non-background tool, got %q", "", tl.Ack())
	}
}
