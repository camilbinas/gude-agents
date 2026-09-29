package tool

import (
	"context"
	"encoding/json"
	"testing"
)

type bgInput struct {
	Query string `json:"query" required:"true" description:"Search query"`
	Limit int    `json:"limit" description:"Max results"`
}

func TestNewBackground(t *testing.T) {
	var received bgInput
	tl := NewBackground("search", "Run a search", "searching…", func(_ context.Context, in bgInput) (string, error) {
		received = in
		return "result:" + in.Query, nil
	})
	if !tl.IsBackground() {
		t.Fatalf("IsBackground() = %v, want true", tl.IsBackground())
	}
	if tl.Ack() != "searching…" {
		t.Fatalf("Ack() = %q", tl.Ack())
	}
	if tl.Spec.InputSchema["type"] != "object" {
		t.Fatalf("schema = %#v", tl.Spec.InputSchema)
	}
	got, err := tl.Handler(context.Background(), json.RawMessage(`{"query":"hello","limit":42}`))
	if err != nil {
		t.Fatal(err)
	}
	if got != "result:hello" || received.Limit != 42 {
		t.Fatalf("got %q, input %#v", got, received)
	}
}

func TestNewBackgroundWithRawMessageSchema(t *testing.T) {
	schema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"url": map[string]any{"type": "string"}},
		"required":   []string{"url"},
	}
	var received json.RawMessage
	tl := NewBackground("fetch", "Fetch URL", "fetching…", func(_ context.Context, input json.RawMessage) (string, error) {
		received = append(json.RawMessage(nil), input...)
		return "got:" + string(input), nil
	}, WithSchema(schema))
	payload := json.RawMessage(`{"url":"https://example.com"}`)
	got, err := tl.Handler(context.Background(), payload)
	if err != nil {
		t.Fatal(err)
	}
	if string(received) != string(payload) || got != "got:"+string(payload) {
		t.Fatalf("received=%s got=%q", received, got)
	}
	if tl.Spec.InputSchema["required"].([]string)[0] != "url" {
		t.Fatalf("schema not preserved: %#v", tl.Spec.InputSchema)
	}
}

func TestNewBackgroundInvalidJSON(t *testing.T) {
	tl := NewBackground("bg", "desc", "ack", func(_ context.Context, in bgInput) (string, error) { return in.Query, nil })
	if _, err := tl.Handler(context.Background(), json.RawMessage(`{invalid`)); err == nil {
		t.Fatal("expected invalid JSON error")
	}
}

func TestNonBackgroundToolMarkers(t *testing.T) {
	tl := New("sync", "A sync tool", func(_ context.Context, in bgInput) (string, error) { return in.Query, nil })
	if tl.IsBackground() || tl.Ack() != "" {
		t.Fatalf("unexpected async markers")
	}
}
