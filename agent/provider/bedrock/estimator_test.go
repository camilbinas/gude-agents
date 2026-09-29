package bedrock

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// testEstimatorClient creates a Bedrock client pointed at a local test server.
func testEstimatorClient(t *testing.T, endpoint string) *bedrockruntime.Client {
	t.Helper()
	for _, key := range []string{
		"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN",
		"AWS_REGION", "AWS_DEFAULT_REGION", "AWS_PROFILE",
		"AWS_BEARER_TOKEN_BEDROCK",
	} {
		t.Setenv(key, "")
	}
	return bedrockruntime.New(bedrockruntime.Options{
		Region:       "us-east-1",
		Credentials:  credentials.NewStaticCredentialsProvider("AKID", "SECRET", "SESSION"),
		BaseEndpoint: aws.String(endpoint),
	})
}

func TestEstimator_ReturnsTokenCount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"inputTokens": 42,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := testEstimatorClient(t, srv.URL)
	est := &Estimator{
		client: client,
		model:  "anthropic.claude-3-5-haiku-20241022-v1:0",
	}

	params := agent.ModelRequest{
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "Hello"}}},
		},
		System: "You are a helpful assistant.",
	}

	count, err := est.EstimateTokens(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 42 {
		t.Errorf("expected 42 tokens, got %d", count)
	}
}

func TestEstimator_WithSystemPrompt(t *testing.T) {
	var capturedBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedBody)
		resp := map[string]any{
			"inputTokens": 10,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := testEstimatorClient(t, srv.URL)
	est := &Estimator{
		client: client,
		model:  "anthropic.claude-3-5-haiku-20241022-v1:0",
	}

	params := agent.ModelRequest{
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "Hi"}}},
		},
		System: "Be concise.",
		Tools: []tool.Spec{{
			Name:        "lookup",
			Description: "Look up a value",
			InputSchema: map[string]any{"type": "object"},
		}},
	}

	count, err := est.EstimateTokens(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 10 {
		t.Errorf("expected 10 tokens, got %d", count)
	}
	body, err := json.Marshal(capturedBody)
	if err != nil {
		t.Fatalf("marshal captured request: %v", err)
	}
	for _, want := range []string{"Be concise.", `"toolConfig"`, "lookup"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("request body %s does not contain %q", body, want)
		}
	}
}

func TestEstimator_APIError_PropagatesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"message":"validation error"}`))
	}))
	defer srv.Close()

	client := testEstimatorClient(t, srv.URL)
	est := &Estimator{
		client: client,
		model:  "anthropic.claude-3-5-haiku-20241022-v1:0",
	}

	params := agent.ModelRequest{
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
		w.Write([]byte(`{"message":"internal server error"}`))
	}))
	defer srv.Close()

	client := testEstimatorClient(t, srv.URL)
	est := &Estimator{
		client: client,
		model:  "anthropic.claude-3-5-haiku-20241022-v1:0",
	}

	params := agent.ModelRequest{
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

func TestEstimator_EmptyMessages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"inputTokens": 0,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := testEstimatorClient(t, srv.URL)
	est := &Estimator{
		client: client,
		model:  "anthropic.claude-3-5-haiku-20241022-v1:0",
	}

	params := agent.ModelRequest{
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: ""}}},
		},
	}

	count, err := est.EstimateTokens(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 tokens, got %d", count)
	}
}
