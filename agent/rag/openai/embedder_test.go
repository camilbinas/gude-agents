package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAIEmbedder_EmptyTextError(t *testing.T) {
	e, err := NewEmbedder(WithEmbedderAPIKey("test-key"))
	if err != nil {
		t.Fatalf("unexpected constructor error: %v", err)
	}
	_, err = e.Embed(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty text, got nil")
	}
	want := "openai embedder: text must not be empty"
	if err.Error() != want {
		t.Errorf("expected error %q, got %q", want, err.Error())
	}
}

func TestOpenAIEmbedder_DefaultModel(t *testing.T) {
	e, err := NewEmbedder(WithEmbedderAPIKey("test-key"))
	if err != nil {
		t.Fatalf("unexpected constructor error: %v", err)
	}
	if e.model != "text-embedding-3-small" {
		t.Errorf("expected default model %q, got %q", "text-embedding-3-small", e.model)
	}
}

func TestOpenAIEmbedder_ConstructorOptions(t *testing.T) {
	e, err := NewEmbedder(
		WithEmbedderModel("text-embedding-3-large"),
		WithEmbedderAPIKey("sk-test-key"),
		WithEmbedderBaseURL("https://custom.api.example.com"),
	)
	if err != nil {
		t.Fatalf("unexpected constructor error: %v", err)
	}
	if e.model != "text-embedding-3-large" {
		t.Errorf("expected model %q, got %q", "text-embedding-3-large", e.model)
	}
	if e.client == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestOpenAIEmbedder_ErrorPrefixWrapping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		resp := map[string]any{
			"error": map[string]any{
				"message": "simulated API error",
				"type":    "invalid_request_error",
				"code":    "invalid_api_key",
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	e, err := NewEmbedder(
		WithEmbedderAPIKey("sk-test-key"),
		WithEmbedderBaseURL(srv.URL),
	)
	if err != nil {
		t.Fatalf("unexpected constructor error: %v", err)
	}

	_, err = e.Embed(context.Background(), "hello world")
	if err == nil {
		t.Fatal("expected error from mock server, got nil")
	}
	if !strings.HasPrefix(err.Error(), "openai embedder: ") {
		t.Errorf("expected error to start with %q, got %q", "openai embedder: ", err.Error())
	}
}
