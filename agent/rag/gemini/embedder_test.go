package gemini

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/genai"
)

// newTestEmbedder creates an Embedder pointed at a local test server.
func newTestEmbedder(t *testing.T, serverURL string) *Embedder {
	t.Helper()
	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:  "test-key",
		Backend: genai.BackendGeminiAPI,
		HTTPOptions: genai.HTTPOptions{
			BaseURL: serverURL,
		},
	})
	if err != nil {
		t.Fatalf("failed to create genai client: %v", err)
	}
	return &Embedder{client: client, model: "gemini-embedding-001"}
}

// embedResponse builds a minimal Gemini embedding API JSON response.
func embedResponse(values []float32) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = fmt.Sprintf("%f", v)
	}
	return fmt.Sprintf(`{"embeddings":[{"values":[%s]}]}`, strings.Join(parts, ","))
}

func TestEmbed_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, embedResponse([]float32{0.1, 0.2, 0.3}))
	}))
	defer srv.Close()

	e := newTestEmbedder(t, srv.URL)
	vec, err := e.Embed(context.Background(), "hello world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vec) != 3 {
		t.Fatalf("expected 3 dimensions, got %d", len(vec))
	}
	// Verify float32 → float64 conversion.
	if vec[0] < 0.09 || vec[0] > 0.11 {
		t.Errorf("expected vec[0] ≈ 0.1, got %f", vec[0])
	}
}

func TestEmbed_EmptyTextError(t *testing.T) {
	e := &Embedder{model: "gemini-embedding-001"}
	_, err := e.Embed(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty text, got nil")
	}
	if !strings.Contains(err.Error(), "text must not be empty") {
		t.Errorf("expected 'text must not be empty' error, got: %v", err)
	}
}

func TestEmbed_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":{"message":"internal error","code":500}}`)
	}))
	defer srv.Close()

	e := newTestEmbedder(t, srv.URL)
	_, err := e.Embed(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error from mock server, got nil")
	}
	if !strings.HasPrefix(err.Error(), "gemini embedder: ") {
		t.Errorf("expected error to start with %q, got %q", "gemini embedder: ", err.Error())
	}
}

func TestEmbed_EmptyEmbeddingsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"embeddings":[]}`)
	}))
	defer srv.Close()

	e := newTestEmbedder(t, srv.URL)
	_, err := e.Embed(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error for empty embeddings, got nil")
	}
	if !strings.Contains(err.Error(), "no embedding returned") {
		t.Errorf("expected 'no embedding returned' error, got: %v", err)
	}
}

func TestNewEmbedder_DefaultModel(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "test-key")
	e, err := NewEmbedder()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.model != "gemini-embedding-001" {
		t.Errorf("expected default model %q, got %q", "gemini-embedding-001", e.model)
	}
}

func TestNewEmbedder_WithModelOption(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "test-key")
	e, err := NewEmbedder(WithModel("gemini-embedding-002"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.model != "gemini-embedding-002" {
		t.Errorf("expected model %q, got %q", "gemini-embedding-002", e.model)
	}
}

func TestGeminiEmbedding001_Model(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "test-key")
	e, err := GeminiEmbedding001()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.model != "gemini-embedding-001" {
		t.Errorf("expected model %q, got %q", "gemini-embedding-001", e.model)
	}
}

func TestGeminiEmbedding002_Model(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "test-key")
	e, err := GeminiEmbedding002()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.model != "gemini-embedding-002" {
		t.Errorf("expected model %q, got %q", "gemini-embedding-002", e.model)
	}
}

func TestNewEmbedder_APIKeyPrecedence(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "gemini-key")
	t.Setenv("GOOGLE_API_KEY", "google-key")
	e, err := NewEmbedder()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e == nil {
		t.Fatal("expected non-nil embedder")
	}
}

func TestNewEmbedder_GoogleAPIKeyFallback(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "google-key")
	e, err := NewEmbedder()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e == nil {
		t.Fatal("expected non-nil embedder")
	}
}
