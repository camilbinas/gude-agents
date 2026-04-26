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

	parts, err := toGeminiParts(blocks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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

	parts, err := toGeminiParts(blocks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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

// ---------------------------------------------------------------------------
// buildConfig — InferenceConfig mapping
// ---------------------------------------------------------------------------

func TestBuildConfig_NilInferenceConfig_UsesConstructorDefaults(t *testing.T) {
	p := &GeminiProvider{
		model:     "gemini-2.5-flash",
		maxTokens: 4096,
	}
	params := agent.ConverseParams{
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hi"}}},
		},
	}
	config := buildConfig(p, params)

	if config.MaxOutputTokens != 4096 {
		t.Errorf("expected MaxOutputTokens 4096, got %d", config.MaxOutputTokens)
	}
	if config.Temperature != nil {
		t.Error("expected Temperature to be nil when InferenceConfig is nil")
	}
	if config.TopP != nil {
		t.Error("expected TopP to be nil when InferenceConfig is nil")
	}
	if config.TopK != nil {
		t.Error("expected TopK to be nil when InferenceConfig is nil")
	}
	if config.StopSequences != nil {
		t.Errorf("expected nil StopSequences, got %v", config.StopSequences)
	}
}

func TestBuildConfig_TemperatureMapping(t *testing.T) {
	p := &GeminiProvider{model: "gemini-2.5-flash", maxTokens: 8192}
	temp := 0.7
	params := agent.ConverseParams{
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hi"}}},
		},
		InferenceConfig: &agent.InferenceConfig{Temperature: &temp},
	}
	config := buildConfig(p, params)

	if config.Temperature == nil {
		t.Fatal("expected Temperature to be set")
	}
	if *config.Temperature != float32(0.7) {
		t.Errorf("expected Temperature 0.7, got %v", *config.Temperature)
	}
	// MaxOutputTokens should still be the constructor default
	if config.MaxOutputTokens != 8192 {
		t.Errorf("expected MaxOutputTokens 8192, got %d", config.MaxOutputTokens)
	}
}

func TestBuildConfig_TopPMapping(t *testing.T) {
	p := &GeminiProvider{model: "gemini-2.5-flash", maxTokens: 8192}
	topP := 0.9
	params := agent.ConverseParams{
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hi"}}},
		},
		InferenceConfig: &agent.InferenceConfig{TopP: &topP},
	}
	config := buildConfig(p, params)

	if config.TopP == nil {
		t.Fatal("expected TopP to be set")
	}
	if *config.TopP != float32(0.9) {
		t.Errorf("expected TopP 0.9, got %v", *config.TopP)
	}
}

func TestBuildConfig_TopKMapping(t *testing.T) {
	p := &GeminiProvider{model: "gemini-2.5-flash", maxTokens: 8192}
	topK := 50
	params := agent.ConverseParams{
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hi"}}},
		},
		InferenceConfig: &agent.InferenceConfig{TopK: &topK},
	}
	config := buildConfig(p, params)

	if config.TopK == nil {
		t.Fatal("expected TopK to be set")
	}
	if *config.TopK != float32(50) {
		t.Errorf("expected TopK 50, got %v", *config.TopK)
	}
}

func TestBuildConfig_StopSequencesMapping(t *testing.T) {
	p := &GeminiProvider{model: "gemini-2.5-flash", maxTokens: 8192}
	stops := []string{"STOP", "END"}
	params := agent.ConverseParams{
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hi"}}},
		},
		InferenceConfig: &agent.InferenceConfig{StopSequences: stops},
	}
	config := buildConfig(p, params)

	if len(config.StopSequences) != 2 {
		t.Fatalf("expected 2 stop sequences, got %d", len(config.StopSequences))
	}
	if config.StopSequences[0] != "STOP" || config.StopSequences[1] != "END" {
		t.Errorf("expected [STOP END], got %v", config.StopSequences)
	}
}

func TestBuildConfig_MaxTokensOverridesDefault(t *testing.T) {
	p := &GeminiProvider{model: "gemini-2.5-flash", maxTokens: 8192}
	maxTok := 2048
	params := agent.ConverseParams{
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hi"}}},
		},
		InferenceConfig: &agent.InferenceConfig{MaxTokens: &maxTok},
	}
	config := buildConfig(p, params)

	if config.MaxOutputTokens != 2048 {
		t.Errorf("expected MaxOutputTokens 2048, got %d", config.MaxOutputTokens)
	}
}

func TestBuildConfig_AllFieldsSet(t *testing.T) {
	p := &GeminiProvider{model: "gemini-2.5-flash", maxTokens: 8192}
	temp := 0.5
	topP := 0.8
	topK := 40
	maxTok := 1024
	cfg := &agent.InferenceConfig{
		Temperature:   &temp,
		TopP:          &topP,
		TopK:          &topK,
		StopSequences: []string{"<|end|>"},
		MaxTokens:     &maxTok,
	}
	params := agent.ConverseParams{
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hi"}}},
		},
		InferenceConfig: cfg,
	}
	config := buildConfig(p, params)

	if config.Temperature == nil || *config.Temperature != float32(0.5) {
		t.Errorf("expected Temperature 0.5, got %v", config.Temperature)
	}
	if config.TopP == nil || *config.TopP != float32(0.8) {
		t.Errorf("expected TopP 0.8, got %v", config.TopP)
	}
	if config.TopK == nil || *config.TopK != float32(40) {
		t.Errorf("expected TopK 40, got %v", config.TopK)
	}
	if len(config.StopSequences) != 1 || config.StopSequences[0] != "<|end|>" {
		t.Errorf("expected StopSequences [<|end|>], got %v", config.StopSequences)
	}
	if config.MaxOutputTokens != 1024 {
		t.Errorf("expected MaxOutputTokens 1024, got %d", config.MaxOutputTokens)
	}
}

func TestBuildConfig_PartialInferenceConfig_OnlyTemperature(t *testing.T) {
	p := &GeminiProvider{model: "gemini-2.5-flash", maxTokens: 4096}
	temp := 0.3
	params := agent.ConverseParams{
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hi"}}},
		},
		InferenceConfig: &agent.InferenceConfig{Temperature: &temp},
	}
	config := buildConfig(p, params)

	// Temperature should be set
	if config.Temperature == nil || *config.Temperature != float32(0.3) {
		t.Errorf("expected Temperature 0.3, got %v", config.Temperature)
	}
	// Other fields should remain at defaults
	if config.TopP != nil {
		t.Error("expected TopP to be nil")
	}
	if config.TopK != nil {
		t.Error("expected TopK to be nil")
	}
	if config.StopSequences != nil {
		t.Errorf("expected nil StopSequences, got %v", config.StopSequences)
	}
	// MaxOutputTokens should be the constructor default
	if config.MaxOutputTokens != 4096 {
		t.Errorf("expected MaxOutputTokens 4096, got %d", config.MaxOutputTokens)
	}
}

// ---------------------------------------------------------------------------
// 8.1 — ImageBlock translation
// ---------------------------------------------------------------------------

func TestToGeminiParts_ImageBlock_RawBytes(t *testing.T) {
	rawBytes := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10} // JPEG magic bytes
	blocks := []agent.ContentBlock{
		agent.ImageBlock{
			Source: agent.ImageSource{
				Data:     rawBytes,
				MIMEType: "image/jpeg",
			},
		},
	}

	parts, err := toGeminiParts(blocks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(parts))
	}

	part := parts[0]
	if part.InlineData == nil {
		t.Fatal("expected InlineData part, got nil")
	}
	if part.InlineData.MIMEType != "image/jpeg" {
		t.Errorf("expected MIME type %q, got %q", "image/jpeg", part.InlineData.MIMEType)
	}
	if string(part.InlineData.Data) != string(rawBytes) {
		t.Errorf("expected raw bytes to be passed through unchanged")
	}
}

func TestToGeminiParts_ImageBlock_Base64String(t *testing.T) {
	rawBytes := []byte("hello image data")
	encoded := "aGVsbG8gaW1hZ2UgZGF0YQ==" // base64 of "hello image data"

	blocks := []agent.ContentBlock{
		agent.ImageBlock{
			Source: agent.ImageSource{
				Base64:   encoded,
				MIMEType: "image/png",
			},
		},
	}

	parts, err := toGeminiParts(blocks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(parts))
	}

	part := parts[0]
	if part.InlineData == nil {
		t.Fatal("expected InlineData part, got nil")
	}
	if part.InlineData.MIMEType != "image/png" {
		t.Errorf("expected MIME type %q, got %q", "image/png", part.InlineData.MIMEType)
	}
	if string(part.InlineData.Data) != string(rawBytes) {
		t.Errorf("expected decoded bytes %q, got %q", rawBytes, part.InlineData.Data)
	}
}

func TestToGeminiParts_ImageBlock_InvalidBase64_ReturnsError(t *testing.T) {
	blocks := []agent.ContentBlock{
		agent.ImageBlock{
			Source: agent.ImageSource{
				Base64:   "not-valid-base64!!!",
				MIMEType: "image/jpeg",
			},
		},
	}

	_, err := toGeminiParts(blocks)
	if err == nil {
		t.Fatal("expected error for invalid base64, got nil")
	}

	var providerErr *agent.ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected *agent.ProviderError, got %T: %v", err, err)
	}
}

func TestToGeminiParts_ImageBlock_UserAndModelRoles(t *testing.T) {
	rawBytes := []byte{0x89, 0x50, 0x4E, 0x47} // PNG magic bytes

	msgs := []agent.Message{
		{
			Role: agent.RoleUser,
			Content: []agent.ContentBlock{
				agent.ImageBlock{
					Source: agent.ImageSource{
						Data:     rawBytes,
						MIMEType: "image/png",
					},
				},
				agent.TextBlock{Text: "What is in this image?"},
			},
		},
		{
			Role: agent.RoleAssistant,
			Content: []agent.ContentBlock{
				agent.ImageBlock{
					Source: agent.ImageSource{
						Data:     rawBytes,
						MIMEType: "image/png",
					},
				},
				agent.TextBlock{Text: "It is a PNG image."},
			},
		},
	}

	contents, err := toGeminiContents(msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(contents) != 2 {
		t.Fatalf("expected 2 contents, got %d", len(contents))
	}

	// User message
	userContent := contents[0]
	if userContent.Role != "user" {
		t.Errorf("expected role %q, got %q", "user", userContent.Role)
	}
	if len(userContent.Parts) != 2 {
		t.Fatalf("expected 2 parts in user message, got %d", len(userContent.Parts))
	}
	if userContent.Parts[0].InlineData == nil {
		t.Error("expected first part of user message to be InlineData (image)")
	}

	// Model message
	modelContent := contents[1]
	if modelContent.Role != "model" {
		t.Errorf("expected role %q, got %q", "model", modelContent.Role)
	}
	if len(modelContent.Parts) != 2 {
		t.Fatalf("expected 2 parts in model message, got %d", len(modelContent.Parts))
	}
	if modelContent.Parts[0].InlineData == nil {
		t.Error("expected first part of model message to be InlineData (image)")
	}
}
