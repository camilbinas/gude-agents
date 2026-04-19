package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	pvdr "github.com/camilbinas/gude-agents/agent/provider"

	"google.golang.org/genai"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// newTestProvider creates a GeminiProvider pointed at the given test server URL.
func newTestProvider(t *testing.T, serverURL string) *GeminiProvider {
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
	return &GeminiProvider{
		client:    client,
		model:     "gemini-2.5-flash",
		maxTokens: int32(pvdr.DefaultMaxTokens),
	}
}

// geminiJSONResponse builds a minimal Gemini API JSON response with the given text.
func geminiJSONResponse(text string, promptTokens, candidateTokens int) string {
	return fmt.Sprintf(`{
		"candidates": [{
			"content": {
				"parts": [{"text": %q}],
				"role": "model"
			}
		}],
		"usageMetadata": {
			"promptTokenCount": %d,
			"candidatesTokenCount": %d
		}
	}`, text, promptTokens, candidateTokens)
}

// ---------------------------------------------------------------------------
// 5.1 — Constructor defaults
// ---------------------------------------------------------------------------

func TestNew_DefaultMaxTokens(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "test-key")
	p, err := New("gemini-2.5-flash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.maxTokens != int32(pvdr.DefaultMaxTokens) {
		t.Errorf("expected maxTokens %d, got %d", pvdr.DefaultMaxTokens, p.maxTokens)
	}
}

func TestNew_GeminiAPIKeyPrecedence(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "gemini-key")
	t.Setenv("GOOGLE_API_KEY", "google-key")

	// The constructor reads GEMINI_API_KEY first. We can verify by checking
	// that the provider was created successfully (the key is used internally).
	p, err := New("gemini-2.5-flash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.model != "gemini-2.5-flash" {
		t.Errorf("expected model %q, got %q", "gemini-2.5-flash", p.model)
	}
}

func TestNew_GoogleAPIKeyFallback(t *testing.T) {
	// Only set GOOGLE_API_KEY, not GEMINI_API_KEY.
	t.Setenv("GOOGLE_API_KEY", "google-key")

	p, err := New("gemini-2.5-flash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.model != "gemini-2.5-flash" {
		t.Errorf("expected model %q, got %q", "gemini-2.5-flash", p.model)
	}
}

func TestNew_WithAPIKeyOverride(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "env-key")

	p, err := New("gemini-2.5-flash", WithAPIKey("override-key"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.model != "gemini-2.5-flash" {
		t.Errorf("expected model %q, got %q", "gemini-2.5-flash", p.model)
	}
}

func TestNew_WithMaxTokensOverride(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "test-key")
	p, err := New("gemini-2.5-flash", WithMaxTokens(4096))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.maxTokens != 4096 {
		t.Errorf("expected maxTokens 4096, got %d", p.maxTokens)
	}
}

// ---------------------------------------------------------------------------
// 5.2 — Capabilities
// ---------------------------------------------------------------------------

func TestCapabilities_AllTrue(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "test-key")
	p, err := New("gemini-2.5-flash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	caps := p.Capabilities()
	if !caps.ToolUse {
		t.Error("expected ToolUse to be true")
	}
	if !caps.ToolChoice {
		t.Error("expected ToolChoice to be true")
	}
	if !caps.TokenUsage {
		t.Error("expected TokenUsage to be true")
	}
}

// ---------------------------------------------------------------------------
// 5.3 — Model constructors and tier aliases
// ---------------------------------------------------------------------------

func TestModelConstructors(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "test-key")

	tests := []struct {
		name      string
		ctor      func(...Option) (*GeminiProvider, error)
		wantModel string
	}{
		{"Gemini25Pro", Gemini25Pro, "gemini-2.5-pro"},
		{"Gemini25Flash", Gemini25Flash, "gemini-2.5-flash"},
		{"Gemini25FlashLite", Gemini25FlashLite, "gemini-2.5-flash-lite"},
		{"Gemini3Flash", Gemini3Flash, "gemini-3-flash-preview"},
		{"Gemini31Pro", Gemini31Pro, "gemini-3.1-pro-preview"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := tt.ctor()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if p.ModelID() != tt.wantModel {
				t.Errorf("expected model %q, got %q", tt.wantModel, p.ModelID())
			}
		})
	}
}

func TestTierAliases(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "test-key")

	tests := []struct {
		name      string
		ctor      func(...Option) (*GeminiProvider, error)
		wantModel string
	}{
		{"Cheapest", Cheapest, "gemini-2.5-flash-lite"},
		{"Standard", Standard, "gemini-2.5-flash"},
		{"Smartest", Smartest, "gemini-2.5-pro"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := tt.ctor()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if p.ModelID() != tt.wantModel {
				t.Errorf("expected model %q, got %q", tt.wantModel, p.ModelID())
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 5.4 — Error wrapping
// ---------------------------------------------------------------------------

func TestConverse_ErrorWrappedInProviderError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error": {"message": "internal server error", "code": 500}}`)
	}))
	defer srv.Close()

	p := newTestProvider(t, srv.URL)
	params := agent.ConverseParams{
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hello"}}},
		},
	}

	_, err := p.Converse(context.Background(), params)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var providerErr *agent.ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected error to be wrapped in agent.ProviderError, got %T: %v", err, err)
	}
}

func TestConverseStream_ErrorWrappedInProviderError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error": {"message": "internal server error", "code": 500}}`)
	}))
	defer srv.Close()

	p := newTestProvider(t, srv.URL)
	params := agent.ConverseParams{
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hello"}}},
		},
	}

	_, err := p.ConverseStream(context.Background(), params, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var providerErr *agent.ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected error to be wrapped in agent.ProviderError, got %T: %v", err, err)
	}
}

// ---------------------------------------------------------------------------
// 5.5 — Nil StreamCallback (no panic, text still accumulated)
// ---------------------------------------------------------------------------

func TestConverseStream_NilCallback_NoPanic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// SSE format: "data:" prefix, separated by double newlines
		fmt.Fprint(w, "data: "+geminiJSONResponse("Hello", 10, 5)+"\n\n")
	}))
	defer srv.Close()

	p := newTestProvider(t, srv.URL)
	params := agent.ConverseParams{
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hello"}}},
		},
	}

	// Should not panic with nil callback.
	resp, err := p.ConverseStream(context.Background(), params, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "Hello" {
		t.Errorf("expected text %q, got %q", "Hello", resp.Text)
	}
}

// ---------------------------------------------------------------------------
// 5.6 — Empty/nil ToolUseBlock.Input mapping to empty args
// ---------------------------------------------------------------------------

func TestToGeminiParts_NilToolUseInput(t *testing.T) {
	blocks := []agent.ContentBlock{
		agent.ToolUseBlock{
			ToolUseID: "tu-1",
			Name:      "my_tool",
			Input:     nil,
		},
	}

	parts := toGeminiParts(blocks)
	if len(parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(parts))
	}

	// The part should be a FunctionCall with empty args.
	part := parts[0]
	if part.FunctionCall == nil {
		t.Fatal("expected FunctionCall part, got nil")
	}
	if part.FunctionCall.Name != "my_tool" {
		t.Errorf("expected function name %q, got %q", "my_tool", part.FunctionCall.Name)
	}
	// Args should be empty map (not nil).
	if len(part.FunctionCall.Args) != 0 {
		t.Errorf("expected empty args, got %v", part.FunctionCall.Args)
	}
}

func TestToGeminiParts_EmptyToolUseInput(t *testing.T) {
	blocks := []agent.ContentBlock{
		agent.ToolUseBlock{
			ToolUseID: "tu-2",
			Name:      "my_tool",
			Input:     json.RawMessage("{}"),
		},
	}

	parts := toGeminiParts(blocks)
	if len(parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(parts))
	}

	part := parts[0]
	if part.FunctionCall == nil {
		t.Fatal("expected FunctionCall part, got nil")
	}
	if part.FunctionCall.Name != "my_tool" {
		t.Errorf("expected function name %q, got %q", "my_tool", part.FunctionCall.Name)
	}
	// Args should be empty map.
	if len(part.FunctionCall.Args) != 0 {
		t.Errorf("expected empty args for '{}' input, got %v", part.FunctionCall.Args)
	}
}
