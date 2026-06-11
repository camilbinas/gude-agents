package anthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/tool"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// newTestEstimator creates an Estimator backed by a test server.
func newTestEstimator(serverURL string) *Estimator {
	client := anthropicsdk.NewClient(
		option.WithAPIKey("test-key"),
		option.WithBaseURL(serverURL),
	)
	return &Estimator{
		client: client,
		model:  "claude-3-5-haiku-20241022",
	}
}

func TestEstimator_ReturnsTokenCount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify it's hitting the count_tokens endpoint.
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}

		resp := map[string]any{
			"input_tokens": 25,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	est := newTestEstimator(srv.URL)
	params := agent.ConverseParams{
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "Hello, world!"}}},
		},
		System: "You are helpful.",
	}

	count, err := est.EstimateTokens(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 25 {
		t.Errorf("expected 25 tokens, got %d", count)
	}
}

func TestEstimator_WithToolConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)

		// Verify tools are included in the request.
		tools, ok := body["tools"]
		if !ok {
			t.Error("expected tools in request body")
		}
		toolSlice, ok := tools.([]any)
		if !ok || len(toolSlice) == 0 {
			t.Error("expected non-empty tools array")
		}

		resp := map[string]any{
			"input_tokens": 50,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	est := newTestEstimator(srv.URL)
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
					"required": []string{"location"},
				},
			},
		},
	}

	count, err := est.EstimateTokens(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 50 {
		t.Errorf("expected 50 tokens, got %d", count)
	}
}

func TestEstimator_APIError_PropagatesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{
			"type":    "error",
			"error":   map[string]any{"type": "invalid_request_error", "message": "bad request"},
			"message": "bad request",
		})
	}))
	defer srv.Close()

	est := newTestEstimator(srv.URL)
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
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{
			"type":    "error",
			"error":   map[string]any{"type": "api_error", "message": "internal error"},
			"message": "internal error",
		})
	}))
	defer srv.Close()

	est := newTestEstimator(srv.URL)
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

func TestEstimator_WithSystemPrompt(t *testing.T) {
	var capturedBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedBody)

		resp := map[string]any{
			"input_tokens": 15,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	est := newTestEstimator(srv.URL)
	params := agent.ConverseParams{
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "Hi"}}},
		},
		System: "Be brief.",
	}

	count, err := est.EstimateTokens(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 15 {
		t.Errorf("expected 15 tokens, got %d", count)
	}

	// Verify system prompt was included in the request.
	if capturedBody["system"] == nil {
		t.Error("expected system field in request body")
	}
}
