package gemini

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/tool"

	"google.golang.org/genai"
)

// newTestEstimator creates a GeminiProvider with a test server, then wraps it
// in an Estimator. The test server URL is used as the GenAI API base URL.
func newTestEstimator(t *testing.T, serverURL string) *Estimator {
	t.Helper()
	ctx := context.Background()
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  "test-key",
		Backend: genai.BackendGeminiAPI,
		HTTPOptions: genai.HTTPOptions{
			BaseURL: serverURL,
		},
	})
	if err != nil {
		t.Fatalf("failed to create genai client: %v", err)
	}
	return &Estimator{
		client: client,
		model:  "gemini-2.5-flash",
	}
}

func TestEstimator_ReturnsTokenCount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify we're hitting the countTokens endpoint.
		if !strings.Contains(r.URL.Path, "countTokens") {
			t.Errorf("expected countTokens in path, got %s", r.URL.Path)
		}

		resp := map[string]any{
			"totalTokens": 30,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	est := newTestEstimator(t, srv.URL)
	params := agent.ConverseParams{
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "Hello, world!"}}},
		},
	}

	count, err := est.EstimateTokens(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 30 {
		t.Errorf("expected 30 tokens, got %d", count)
	}
}

func TestEstimator_WithSystemPrompt_GeminiAPIBackend_ReturnsError(t *testing.T) {
	// The Gemini API (mldev) backend does not support systemInstruction in
	// countTokens requests — the SDK rejects this client-side.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"totalTokens": 20,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	est := newTestEstimator(t, srv.URL)
	params := agent.ConverseParams{
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "Hi"}}},
		},
		System: "You are a helpful assistant.",
	}

	count, err := est.EstimateTokens(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for systemInstruction on Gemini API backend, got nil")
	}
	if count != 0 {
		t.Errorf("expected 0 on error, got %d", count)
	}
	if !strings.Contains(err.Error(), "systemInstruction") {
		t.Errorf("expected error about systemInstruction, got: %v", err)
	}
}

func TestEstimator_WithToolConfig_GeminiAPIBackend_ReturnsError(t *testing.T) {
	// The Gemini API (mldev) backend does not support tools in
	// countTokens requests — the SDK rejects this client-side.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"totalTokens": 55,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	est := newTestEstimator(t, srv.URL)
	params := agent.ConverseParams{
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "Use a tool"}}},
		},
		ToolConfig: []tool.Spec{
			{
				Name:        "get_weather",
				Description: "Gets the weather for a location",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"location": map[string]any{"type": "string"},
					},
				},
			},
		},
	}

	count, err := est.EstimateTokens(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for tools on Gemini API backend, got nil")
	}
	if count != 0 {
		t.Errorf("expected 0 on error, got %d", count)
	}
	if !strings.Contains(err.Error(), "tools") {
		t.Errorf("expected error about tools, got: %v", err)
	}
}

func TestEstimator_APIError_PropagatesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"code":    400,
				"message": "Invalid request",
				"status":  "INVALID_ARGUMENT",
			},
		})
	}))
	defer srv.Close()

	est := newTestEstimator(t, srv.URL)
	params := agent.ConverseParams{
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "Hello"}}},
		},
	}

	count, err := est.EstimateTokens(context.Background(), params)
	if err == nil {
		t.Fatal("expected error from API failure, got nil")
	}
	if count != 0 {
		t.Errorf("expected 0 on error, got %d", count)
	}
}

func TestEstimator_ServerError_PropagatesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"code":    500,
				"message": "Internal server error",
				"status":  "INTERNAL",
			},
		})
	}))
	defer srv.Close()

	est := newTestEstimator(t, srv.URL)
	params := agent.ConverseParams{
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "Hello"}}},
		},
	}

	count, err := est.EstimateTokens(context.Background(), params)
	if err == nil {
		t.Fatal("expected error from server failure, got nil")
	}
	if count != 0 {
		t.Errorf("expected 0 on error, got %d", count)
	}
}
