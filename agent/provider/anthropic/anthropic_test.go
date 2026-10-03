package anthropic

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/tool"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"pgregory.net/rapid"
)

func TestProperty_AnthropicToolChoiceMapping(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		mode := rapid.SampledFrom([]tool.ChoiceMode{
			tool.ChoiceAuto,
			tool.ChoiceAny,
			tool.ChoiceTool,
		}).Draw(t, "mode")

		tc := &tool.Choice{Mode: mode}
		if mode == tool.ChoiceTool {
			tc.Name = rapid.StringMatching(`[a-zA-Z_][a-zA-Z0-9_]{0,63}`).Draw(t, "toolName")
		}

		result := toAnthropicToolChoice(tc)

		switch mode {
		case tool.ChoiceAuto:
			if result.OfAuto == nil {
				t.Fatal("expected OfAuto to be set for ToolChoiceAuto")
			}
			if result.OfAny != nil || result.OfTool != nil {
				t.Fatal("expected only OfAuto to be set")
			}
		case tool.ChoiceAny:
			if result.OfAny == nil {
				t.Fatal("expected OfAny to be set for ToolChoiceAny")
			}
			if result.OfAuto != nil || result.OfTool != nil {
				t.Fatal("expected only OfAny to be set")
			}
		case tool.ChoiceTool:
			if result.OfTool == nil {
				t.Fatal("expected OfTool to be set for ToolChoiceTool")
			}
			if result.OfAuto != nil || result.OfAny != nil {
				t.Fatal("expected only OfTool to be set")
			}
			if result.OfTool.Name != tc.Name {
				t.Fatalf("expected tool name %q, got %q", tc.Name, result.OfTool.Name)
			}
		}
	})
}

// ptr returns a pointer to the given int64 value. Used in tests to set *int64 fields inline.
func ptr(v int64) *int64 { return &v }

// sseBody builds a minimal SSE response body with the given events.
// Each entry is (eventType, jsonData).
func sseBody(events [][2]string) string {
	body := ""
	for _, ev := range events {
		body += fmt.Sprintf("event: %s\ndata: %s\n\n", ev[0], ev[1])
	}
	return body
}

// newTestProvider creates an AnthropicProvider pointed at the given test server URL.
func newTestProvider(serverURL string) *AnthropicProvider {
	client := anthropicsdk.NewClient(
		option.WithAPIKey("test-key"),
		option.WithBaseURL(serverURL),
	)
	return &AnthropicProvider{
		client:    client,
		model:     "claude-3-5-haiku-20241022",
		maxTokens: ptr(1024),
	}
}

func TestStream_EmitsEventsAndReturnsFullResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseBody([][2]string{
			{"message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-3-5-haiku-20241022","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":42,"output_tokens":0,"cache_read_input_tokens":8,"cache_creation_input_tokens":3}}}`},
			{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Consider "}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"tools"}}`},
			{"content_block_stop", `{"type":"content_block_stop","index":0}`},
			{"content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Hello, "}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"world"}}`},
			{"content_block_stop", `{"type":"content_block_stop","index":1}`},
			{"content_block_start", `{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{}}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"city\":\""}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"Paris\"}"}}`},
			{"content_block_stop", `{"type":"content_block_stop","index":2}`},
			{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":17}}`},
			{"message_stop", `{"type":"message_stop"}`},
		}))
	}))
	defer srv.Close()

	var events []agent.ModelEvent
	resp, err := newTestProvider(srv.URL).Stream(context.Background(), agent.ModelRequest{
		Messages: []agent.Message{{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "Hello"}}}},
	}, func(event agent.ModelEvent) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatalf("Stream error: %v", err)
	}

	wantEvents := []agent.ModelEvent{
		{Type: agent.ModelEventThinking, Text: "Consider "},
		{Type: agent.ModelEventThinking, Text: "tools"},
		{Type: agent.ModelEventText, Text: "Hello, "},
		{Type: agent.ModelEventText, Text: "world"},
	}
	if len(events) != len(wantEvents) {
		t.Fatalf("events = %#v, want %#v", events, wantEvents)
	}
	for i := range wantEvents {
		if events[i] != wantEvents[i] {
			t.Errorf("event %d = %#v, want %#v", i, events[i], wantEvents[i])
		}
	}
	if resp.Text != "Hello, world" {
		t.Errorf("Text = %q, want %q", resp.Text, "Hello, world")
	}
	if got := resp.Metadata["thinking"]; got != "Consider tools" {
		t.Errorf("Metadata[thinking] = %#v, want %q", got, "Consider tools")
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("ToolCalls = %#v, want one call", resp.ToolCalls)
	}
	call := resp.ToolCalls[0]
	if call.ToolUseID != "toolu_1" || call.Name != "get_weather" || string(call.Input) != `{"city":"Paris"}` {
		t.Errorf("ToolCalls[0] = %#v", call)
	}
	wantUsage := (agent.TokenUsage{InputTokens: 42, OutputTokens: 17, CacheReadTokens: 8, CacheWriteTokens: 3})
	if resp.Usage != wantUsage {
		t.Errorf("Usage = %#v, want %#v", resp.Usage, wantUsage)
	}
}

func TestStream_NilEmitStillReturnsResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseBody([][2]string{
			{"message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-3-5-haiku-20241022","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":2,"output_tokens":0}}}`},
			{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`},
			{"content_block_stop", `{"type":"content_block_stop","index":0}`},
			{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":1}}`},
			{"message_stop", `{"type":"message_stop"}`},
		}))
	}))
	defer srv.Close()

	resp, err := newTestProvider(srv.URL).Stream(context.Background(), agent.ModelRequest{
		Messages: []agent.Message{{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hi"}}}},
	}, nil)
	if err != nil {
		t.Fatalf("Stream error: %v", err)
	}
	if resp.Text != "ok" || resp.Usage.InputTokens != 2 || resp.Usage.OutputTokens != 1 {
		t.Errorf("response = %#v", resp)
	}
}

func TestStream_WrapsProviderError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"type":"error","error":{"type":"api_error","message":"failed"}}`, http.StatusInternalServerError)
	}))
	defer srv.Close()

	resp, err := newTestProvider(srv.URL).Stream(context.Background(), agent.ModelRequest{
		Messages: []agent.Message{{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hi"}}}},
	}, nil)
	if resp != nil {
		t.Errorf("response = %#v, want nil", resp)
	}
	var providerErr *agent.ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("error = %v, want *agent.ProviderError", err)
	}
}

func TestBuildParams_ToolsMapping(t *testing.T) {
	p := &AnthropicProvider{model: "claude-3-5-haiku-20241022"}
	result := p.buildParams(agent.ModelRequest{Tools: []tool.Spec{{
		Name:        "get_weather",
		Description: "Get weather",
		InputSchema: map[string]any{
			"properties": map[string]any{"city": map[string]any{"type": "string"}},
			"required":   []string{"city"},
		},
	}}})
	if len(result.Tools) != 1 || result.Tools[0].OfTool == nil {
		t.Fatalf("Tools = %#v, want one tool", result.Tools)
	}
	if result.Tools[0].OfTool.Name != "get_weather" {
		t.Errorf("tool name = %q, want get_weather", result.Tools[0].OfTool.Name)
	}
}

// ---------------------------------------------------------------------------
// buildParams — InferenceConfig mapping
// ---------------------------------------------------------------------------

func TestBuildParams_NilInferenceConfig_UsesConstructorDefaults(t *testing.T) {
	p := &AnthropicProvider{
		model:     "claude-3-5-haiku-20241022",
		maxTokens: ptr(4096),
	}
	params := agent.ModelRequest{
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hi"}}},
		},
	}
	result := p.buildParams(params)

	if result.MaxTokens != 4096 {
		t.Errorf("expected MaxTokens 4096, got %d", result.MaxTokens)
	}
	if result.Temperature.Valid() {
		t.Error("expected Temperature to not be set when InferenceConfig is nil")
	}
	if result.TopP.Valid() {
		t.Error("expected TopP to not be set when InferenceConfig is nil")
	}
	if result.TopK.Valid() {
		t.Error("expected TopK to not be set when InferenceConfig is nil")
	}
	if result.StopSequences != nil {
		t.Errorf("expected nil StopSequences, got %v", result.StopSequences)
	}
}

func TestBuildParams_TemperatureMapping(t *testing.T) {
	p := &AnthropicProvider{model: "claude-3-5-haiku-20241022", maxTokens: ptr(8192)}
	temp := 0.7
	params := agent.ModelRequest{
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hi"}}},
		},
		InferenceConfig: &agent.InferenceConfig{Temperature: &temp},
	}
	result := p.buildParams(params)

	if !result.Temperature.Valid() {
		t.Fatal("expected Temperature to be set")
	}
	if result.Temperature.Value != 0.7 {
		t.Errorf("expected Temperature 0.7, got %v", result.Temperature.Value)
	}
	// MaxTokens should still be the constructor default
	if result.MaxTokens != 8192 {
		t.Errorf("expected MaxTokens 8192, got %d", result.MaxTokens)
	}
}

func TestBuildParams_TopPMapping(t *testing.T) {
	p := &AnthropicProvider{model: "claude-3-5-haiku-20241022", maxTokens: ptr(8192)}
	topP := 0.9
	params := agent.ModelRequest{
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hi"}}},
		},
		InferenceConfig: &agent.InferenceConfig{TopP: &topP},
	}
	result := p.buildParams(params)

	if !result.TopP.Valid() {
		t.Fatal("expected TopP to be set")
	}
	if result.TopP.Value != 0.9 {
		t.Errorf("expected TopP 0.9, got %v", result.TopP.Value)
	}
}

func TestBuildParams_TopKMapping(t *testing.T) {
	p := &AnthropicProvider{model: "claude-3-5-haiku-20241022", maxTokens: ptr(8192)}
	topK := 50
	params := agent.ModelRequest{
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hi"}}},
		},
		InferenceConfig: &agent.InferenceConfig{TopK: &topK},
	}
	result := p.buildParams(params)

	if !result.TopK.Valid() {
		t.Fatal("expected TopK to be set")
	}
	if result.TopK.Value != 50 {
		t.Errorf("expected TopK 50, got %v", result.TopK.Value)
	}
}

func TestBuildParams_StopSequencesMapping(t *testing.T) {
	p := &AnthropicProvider{model: "claude-3-5-haiku-20241022", maxTokens: ptr(8192)}
	stops := []string{"STOP", "END"}
	params := agent.ModelRequest{
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hi"}}},
		},
		InferenceConfig: &agent.InferenceConfig{StopSequences: stops},
	}
	result := p.buildParams(params)

	if len(result.StopSequences) != 2 {
		t.Fatalf("expected 2 stop sequences, got %d", len(result.StopSequences))
	}
	if result.StopSequences[0] != "STOP" || result.StopSequences[1] != "END" {
		t.Errorf("expected [STOP END], got %v", result.StopSequences)
	}
}

func TestBuildParams_MaxTokensOverridesDefault(t *testing.T) {
	p := &AnthropicProvider{model: "claude-3-5-haiku-20241022", maxTokens: ptr(8192)}
	maxTok := 2048
	params := agent.ModelRequest{
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hi"}}},
		},
		InferenceConfig: &agent.InferenceConfig{MaxTokens: &maxTok},
	}
	result := p.buildParams(params)

	if result.MaxTokens != 2048 {
		t.Errorf("expected MaxTokens 2048, got %d", result.MaxTokens)
	}
}

func TestBuildParams_AllFieldsSet(t *testing.T) {
	p := &AnthropicProvider{model: "claude-3-5-haiku-20241022", maxTokens: ptr(8192)}
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
	params := agent.ModelRequest{
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hi"}}},
		},
		InferenceConfig: cfg,
	}
	result := p.buildParams(params)

	if !result.Temperature.Valid() || result.Temperature.Value != 0.5 {
		t.Errorf("expected Temperature 0.5, got %v", result.Temperature.Value)
	}
	if !result.TopP.Valid() || result.TopP.Value != 0.8 {
		t.Errorf("expected TopP 0.8, got %v", result.TopP.Value)
	}
	if !result.TopK.Valid() || result.TopK.Value != 40 {
		t.Errorf("expected TopK 40, got %v", result.TopK.Value)
	}
	if len(result.StopSequences) != 1 || result.StopSequences[0] != "<|end|>" {
		t.Errorf("expected StopSequences [<|end|>], got %v", result.StopSequences)
	}
	if result.MaxTokens != 1024 {
		t.Errorf("expected MaxTokens 1024, got %d", result.MaxTokens)
	}
}

func TestBuildParams_PartialInferenceConfig_OnlyTemperature(t *testing.T) {
	p := &AnthropicProvider{model: "claude-3-5-haiku-20241022", maxTokens: ptr(4096)}
	temp := 0.3
	params := agent.ModelRequest{
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hi"}}},
		},
		InferenceConfig: &agent.InferenceConfig{Temperature: &temp},
	}
	result := p.buildParams(params)

	// Temperature should be set
	if !result.Temperature.Valid() || result.Temperature.Value != 0.3 {
		t.Errorf("expected Temperature 0.3, got %v", result.Temperature.Value)
	}
	// Other fields should remain at defaults
	if result.TopP.Valid() {
		t.Error("expected TopP to not be set")
	}
	if result.TopK.Valid() {
		t.Error("expected TopK to not be set")
	}
	if result.StopSequences != nil {
		t.Errorf("expected nil StopSequences, got %v", result.StopSequences)
	}
	// MaxTokens should be the constructor default
	if result.MaxTokens != 4096 {
		t.Errorf("expected MaxTokens 4096, got %d", result.MaxTokens)
	}
}

// TestToAnthropicContentBlocks_ImageBlock_RawBytes verifies that raw bytes are
// base64-encoded and the resulting block uses the base64 source type.
func TestToAnthropicContentBlocks_ImageBlock_RawBytes(t *testing.T) {
	rawBytes := []byte{0xFF, 0xD8, 0xFF, 0xE0} // JPEG magic bytes
	block := agent.ImageBlock{
		Source: agent.ImageSource{
			Data:     rawBytes,
			MIMEType: "image/jpeg",
		},
	}

	result := toAnthropicContentBlocks([]agent.ContentBlock{block}, agent.RoleUser, false)

	if len(result) != 1 {
		t.Fatalf("expected 1 block, got %d", len(result))
	}
	img := result[0].OfImage
	if img == nil {
		t.Fatal("expected OfImage to be set")
	}
	if img.Source.OfBase64 == nil {
		t.Fatal("expected OfBase64 source to be set")
	}

	expectedEncoded := base64.StdEncoding.EncodeToString(rawBytes)
	if img.Source.OfBase64.Data != expectedEncoded {
		t.Errorf("expected encoded data %q, got %q", expectedEncoded, img.Source.OfBase64.Data)
	}
}

// TestToAnthropicContentBlocks_ImageBlock_PreEncodedBase64 verifies that a
// pre-encoded base64 string is used directly without re-encoding.
func TestToAnthropicContentBlocks_ImageBlock_PreEncodedBase64(t *testing.T) {
	rawBytes := []byte{0x89, 0x50, 0x4E, 0x47} // PNG magic bytes
	preEncoded := base64.StdEncoding.EncodeToString(rawBytes)

	block := agent.ImageBlock{
		Source: agent.ImageSource{
			Base64:   preEncoded,
			MIMEType: "image/png",
		},
	}

	result := toAnthropicContentBlocks([]agent.ContentBlock{block}, agent.RoleUser, false)

	if len(result) != 1 {
		t.Fatalf("expected 1 block, got %d", len(result))
	}
	img := result[0].OfImage
	if img == nil {
		t.Fatal("expected OfImage to be set")
	}
	if img.Source.OfBase64 == nil {
		t.Fatal("expected OfBase64 source to be set")
	}
	// Must be the original pre-encoded string, not double-encoded.
	if img.Source.OfBase64.Data != preEncoded {
		t.Errorf("expected pre-encoded data %q, got %q", preEncoded, img.Source.OfBase64.Data)
	}
}

// TestToAnthropicContentBlocks_ImageBlock_MIMETypeMapping verifies that the
// MIMEType field is mapped directly to the media_type in the SDK struct.
func TestToAnthropicContentBlocks_ImageBlock_MIMETypeMapping(t *testing.T) {
	mimeTypes := []string{"image/jpeg", "image/png", "image/gif", "image/webp"}

	for _, mime := range mimeTypes {
		t.Run(mime, func(t *testing.T) {
			block := agent.ImageBlock{
				Source: agent.ImageSource{
					Data:     []byte{0x01, 0x02},
					MIMEType: mime,
				},
			}

			result := toAnthropicContentBlocks([]agent.ContentBlock{block}, agent.RoleUser, false)

			if len(result) != 1 {
				t.Fatalf("expected 1 block, got %d", len(result))
			}
			img := result[0].OfImage
			if img == nil {
				t.Fatal("expected OfImage to be set")
			}
			if img.Source.OfBase64 == nil {
				t.Fatal("expected OfBase64 source to be set")
			}
			if string(img.Source.OfBase64.MediaType) != mime {
				t.Errorf("expected media_type %q, got %q", mime, img.Source.OfBase64.MediaType)
			}
		})
	}
}

// TestToAnthropicContentBlocks_ImageBlock_AssistantRoleSkipped verifies that
// an ImageBlock in an assistant-role message is skipped without panic or output.
func TestToAnthropicContentBlocks_ImageBlock_AssistantRoleSkipped(t *testing.T) {
	block := agent.ImageBlock{
		Source: agent.ImageSource{
			Data:     []byte{0x01, 0x02, 0x03},
			MIMEType: "image/png",
		},
	}

	result := toAnthropicContentBlocks([]agent.ContentBlock{block}, agent.RoleAssistant, false)

	if len(result) != 0 {
		t.Errorf("expected 0 blocks for assistant-role ImageBlock, got %d", len(result))
	}
}

func TestBuildAnthropicDocParam_FileID(t *testing.T) {
	doc, ok := buildAnthropicDocParam(agent.DocumentBlock{Source: agent.DocumentSource{
		FileID: "file_abc123",
	}})
	if !ok {
		t.Fatal("expected FileID document conversion to succeed")
	}
	if doc.Source.OfFile == nil {
		t.Fatalf("expected FileID document source, got %#v", doc.Source)
	}
	if doc.Source.OfFile.FileID != "file_abc123" {
		t.Errorf("FileID = %q, want %q", doc.Source.OfFile.FileID, "file_abc123")
	}
}

func TestClaudeHaiku45MaxTokensDefault(t *testing.T) {
	tests := []struct {
		name    string
		factory func(...Option) (*AnthropicProvider, error)
		want    int64
	}{
		{
			name:    "Haiku 4.5 constructor",
			factory: ClaudeHaiku4_5,
			want:    64_000,
		},
		{
			name:    "Cheapest constructor",
			factory: Cheapest,
			want:    64_000,
		},
		{
			name: "generic constructor retains generic default",
			factory: func(opts ...Option) (*AnthropicProvider, error) {
				return New("claude-haiku-4-5", opts...)
			},
			want: 128000,
		},
		{
			name:    "other model constructor retains generic default",
			factory: ClaudeSonnet5,
			want:    128000,
		},
	}

	params := agent.ModelRequest{
		Messages: []agent.Message{{
			Role:    agent.RoleUser,
			Content: []agent.ContentBlock{agent.TextBlock{Text: "hi"}},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := tt.factory()
			if err != nil {
				t.Fatalf("new provider: %v", err)
			}
			if got := p.buildParams(params).MaxTokens; got != tt.want {
				t.Errorf("MaxTokens = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestClaudeHaiku45MaxTokenOverrides(t *testing.T) {
	p, err := ClaudeHaiku4_5(WithMaxTokens(2048))
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	params := agent.ModelRequest{
		Messages: []agent.Message{{
			Role:    agent.RoleUser,
			Content: []agent.ContentBlock{agent.TextBlock{Text: "hi"}},
		}},
	}
	if got := p.buildParams(params).MaxTokens; got != 2048 {
		t.Errorf("provider option MaxTokens = %d, want 2048", got)
	}

	perCallLimit := 1024
	params.InferenceConfig = &agent.InferenceConfig{MaxTokens: &perCallLimit}
	if got := p.buildParams(params).MaxTokens; got != int64(perCallLimit) {
		t.Errorf("per-call MaxTokens = %d, want %d", got, perCallLimit)
	}
}

func TestBuildParamsUsesAdvisoryMaxTokensCapabilities(t *testing.T) {
	requestLimit := 2048
	overrideLimit := 8192
	tests := []struct {
		name       string
		maxTokens  *int64
		budget     int64
		requestMax *int
		want       int64
	}{
		{
			name: "implicit default uses capability ceiling",
			want: 4096,
		},
		{
			name:      "explicit provider limit wins below ceiling",
			maxTokens: ptr(2048),
			want:      2048,
		},
		{
			name:      "explicit provider limit includes thinking headroom",
			maxTokens: ptr(2048),
			budget:    1024,
			want:      3072,
		},
		{
			name:   "implicit ceiling contains thinking headroom",
			budget: 1024,
			want:   4096,
		},
		{
			name:       "per-call limit includes thinking headroom",
			budget:     1024,
			requestMax: &requestLimit,
			want:       3072,
		},
		{
			name:      "explicit provider limit is not reduced by advisory ceiling",
			maxTokens: ptr(8192),
			want:      8192,
		},
		{
			name:       "per-call limit is not reduced by advisory ceiling",
			requestMax: &overrideLimit,
			want:       8192,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &AnthropicProvider{
				model:          "test-model",
				maxTokens:      tt.maxTokens,
				thinkingBudget: tt.budget,
				capabilities:   agent.ModelCapabilities{MaxOutputTokens: 4096},
			}
			params := agent.ModelRequest{}
			if tt.requestMax != nil {
				params.InferenceConfig = &agent.InferenceConfig{MaxTokens: tt.requestMax}
			}
			got := p.buildParams(params)
			if got.MaxTokens != tt.want {
				t.Fatalf("MaxTokens = %d, want %d", got.MaxTokens, tt.want)
			}
			if tt.budget > 0 && got.Thinking.OfEnabled == nil {
				t.Fatal("thinking was not enabled")
			}
		})
	}
}

func TestClaude55ToolChoiceCapabilities(t *testing.T) {
	for _, factory := range []struct {
		name string
		new  func(...Option) (*AnthropicProvider, error)
	}{
		{name: "Sonnet 5.5", new: ClaudeSonnet5_5},
		{name: "Opus 5.5", new: ClaudeOpus5_5},
	} {
		t.Run(factory.name, func(t *testing.T) {
			p, err := factory.new()
			if err != nil {
				t.Fatal(err)
			}
			caps := p.Capabilities()
			if caps.ToolUse != agent.Supported {
				t.Fatalf("ToolUse = %v, want Supported", caps.ToolUse)
			}
			if caps.ToolChoice.Auto != agent.Supported || caps.ToolChoice.Required != agent.Unsupported || caps.ToolChoice.Specific != agent.Unsupported {
				t.Fatalf("ToolChoice = %#v, want Auto supported and Required/Specific unsupported", caps.ToolChoice)
			}
		})
	}
}

func TestClaude55CallerCapabilityOverrideWins(t *testing.T) {
	p, err := ClaudeSonnet5_5(WithToolChoiceSpecific(agent.Supported))
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Capabilities().ToolChoice.Specific; got != agent.Supported {
		t.Fatalf("Specific = %v, want caller override Supported", got)
	}
}
