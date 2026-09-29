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
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"input_tokens": 25})
	}))
	defer srv.Close()

	est := newTestEstimator(srv.URL)
	request := agent.ModelRequest{
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "Hello, world!"}}},
		},
		System: "You are helpful.",
	}

	count, err := est.EstimateTokens(context.Background(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 25 {
		t.Errorf("expected 25 tokens, got %d", count)
	}
}

func TestEstimator_WithTools(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}

		tools, ok := body["tools"]
		if !ok {
			t.Error("expected tools in request body")
		}
		toolSlice, ok := tools.([]any)
		if !ok || len(toolSlice) == 0 {
			t.Error("expected non-empty tools array")
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"input_tokens": 50})
	}))
	defer srv.Close()

	est := newTestEstimator(srv.URL)
	request := agent.ModelRequest{
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "Use a tool"}}},
		},
		Tools: []tool.Spec{
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

	count, err := est.EstimateTokens(context.Background(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 50 {
		t.Errorf("expected 50 tokens, got %d", count)
	}
}

func TestEstimator_APIError_PropagatesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{
			"type":    "error",
			"error":   map[string]any{"type": "invalid_request_error", "message": "bad request"},
			"message": "bad request",
		})
	}))
	defer srv.Close()

	count, err := newTestEstimator(srv.URL).EstimateTokens(context.Background(), agent.ModelRequest{
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "Hello"}}},
		},
	})
	if err == nil {
		t.Fatal("expected error from API failure, got nil")
	}
	if count != 0 {
		t.Errorf("expected 0 on error, got %d", count)
	}
}

func TestEstimator_ServerError_PropagatesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{
			"type":    "error",
			"error":   map[string]any{"type": "api_error", "message": "internal error"},
			"message": "internal error",
		})
	}))
	defer srv.Close()

	count, err := newTestEstimator(srv.URL).EstimateTokens(context.Background(), agent.ModelRequest{
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "Hello"}}},
		},
	})
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
		if err := json.NewDecoder(r.Body).Decode(&capturedBody); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"input_tokens": 15})
	}))
	defer srv.Close()

	count, err := newTestEstimator(srv.URL).EstimateTokens(context.Background(), agent.ModelRequest{
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "Hi"}}},
		},
		System: "Be brief.",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 15 {
		t.Errorf("expected 15 tokens, got %d", count)
	}
	if capturedBody["system"] == nil {
		t.Error("expected system field in request body")
	}
}
