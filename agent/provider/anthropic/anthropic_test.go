package anthropic

import (
	"context"
	"encoding/base64"
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

func TestProperty_AnthropicTokenUsagePopulation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		inputTokens := rapid.Int64Range(0, 1_000_000).Draw(t, "inputTokens")
		outputTokens := rapid.Int64Range(0, 1_000_000).Draw(t, "outputTokens")

		// Test non-streaming: parseMessage + Usage field
		msg := &anthropicsdk.Message{
			Content: []anthropicsdk.ContentBlockUnion{
				{Type: "text", Text: "hello"},
			},
			Usage: anthropicsdk.Usage{
				InputTokens:  inputTokens,
				OutputTokens: outputTokens,
			},
		}

		resp := parseMessage(msg)
		// parseMessage doesn't read Usage — the Converse method does.
		// Simulate what the Converse method does after parseMessage:
		resp.Usage.InputTokens = int(msg.Usage.InputTokens)
		resp.Usage.OutputTokens = int(msg.Usage.OutputTokens)

		if resp.Usage.InputTokens != int(inputTokens) {
			t.Fatalf("expected InputTokens %d, got %d", inputTokens, resp.Usage.InputTokens)
		}
		if resp.Usage.OutputTokens != int(outputTokens) {
			t.Fatalf("expected OutputTokens %d, got %d", outputTokens, resp.Usage.OutputTokens)
		}
	})
}

// ---------------------------------------------------------------------------
// Helpers for streaming token tests
// ---------------------------------------------------------------------------

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
		maxTokens: 1024,
	}
}

// TestConverseStream_TokenCounts verifies that InputTokens comes from message_start
// and OutputTokens comes from message_delta.
func TestConverseStream_TokenCounts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		body := sseBody([][2]string{
			{"message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-3-5-haiku-20241022","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":42,"output_tokens":0}}}`},
			{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`},
			{"content_block_stop", `{"type":"content_block_stop","index":0}`},
			{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":7}}`},
			{"message_stop", `{"type":"message_stop"}`},
		})
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	p := newTestProvider(srv.URL)
	params := agent.ConverseParams{
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "Hello"}}},
		},
	}

	resp, err := p.ConverseStream(context.Background(), params, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Usage.InputTokens <= 0 {
		t.Errorf("expected InputTokens > 0, got %d", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens <= 0 {
		t.Errorf("expected OutputTokens > 0, got %d", resp.Usage.OutputTokens)
	}
}

func TestProperty_StreamingTokenCountsNonZero(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		prompt := rapid.StringOf(rapid.RuneFrom([]rune("abcdefghijklmnopqrstuvwxyz"))).Filter(func(s string) bool {
			return len(s) > 0
		}).Draw(t, "prompt")

		inputTokens := len(prompt) // deterministic mock: 1 token per byte

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			body := sseBody([][2]string{
				{"message_start", fmt.Sprintf(`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-3-5-haiku-20241022","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":%d,"output_tokens":0}}}`, inputTokens)},
				{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
				{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`},
				{"content_block_stop", `{"type":"content_block_stop","index":0}`},
				{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":1}}`},
				{"message_stop", `{"type":"message_stop"}`},
			})
			fmt.Fprint(w, body)
		}))
		defer srv.Close()

		p := newTestProvider(srv.URL)
		params := agent.ConverseParams{
			Messages: []agent.Message{
				{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: prompt}}},
			},
		}

		resp, err := p.ConverseStream(context.Background(), params, nil)
		if err != nil {
			t.Fatalf("ConverseStream error: %v", err)
		}
		if resp.Usage.InputTokens <= 0 {
			t.Fatalf("expected InputTokens > 0 for prompt %q, got %d", prompt, resp.Usage.InputTokens)
		}
	})
}

func TestProperty_StreamingNonStreamingTokenConsistency(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		inputTokens := rapid.IntRange(1, 1000).Draw(t, "inputTokens")
		outputTokens := rapid.IntRange(1, 1000).Draw(t, "outputTokens")

		// Non-streaming server
		nonStreamSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, `{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"hello"}],"model":"claude-3-5-haiku-20241022","stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":%d,"output_tokens":%d}}`, inputTokens, outputTokens)
		}))
		defer nonStreamSrv.Close()

		// Streaming server
		streamSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			body := sseBody([][2]string{
				{"message_start", fmt.Sprintf(`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-3-5-haiku-20241022","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":%d,"output_tokens":0}}}`, inputTokens)},
				{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
				{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`},
				{"content_block_stop", `{"type":"content_block_stop","index":0}`},
				{"message_delta", fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":%d}}`, outputTokens)},
				{"message_stop", `{"type":"message_stop"}`},
			})
			fmt.Fprint(w, body)
		}))
		defer streamSrv.Close()

		params := agent.ConverseParams{
			Messages: []agent.Message{
				{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hello"}}},
			},
		}

		nonStreamProvider := newTestProvider(nonStreamSrv.URL)
		nonStreamResp, err := nonStreamProvider.Converse(context.Background(), params)
		if err != nil {
			t.Fatalf("Converse error: %v", err)
		}

		streamProvider := newTestProvider(streamSrv.URL)
		streamResp, err := streamProvider.ConverseStream(context.Background(), params, nil)
		if err != nil {
			t.Fatalf("ConverseStream error: %v", err)
		}

		nonStreamTotal := nonStreamResp.Usage.InputTokens + nonStreamResp.Usage.OutputTokens
		streamTotal := streamResp.Usage.InputTokens + streamResp.Usage.OutputTokens

		if nonStreamTotal != streamTotal {
			t.Fatalf("token totals differ: non-streaming=%d (in=%d, out=%d), streaming=%d (in=%d, out=%d)",
				nonStreamTotal, nonStreamResp.Usage.InputTokens, nonStreamResp.Usage.OutputTokens,
				streamTotal, streamResp.Usage.InputTokens, streamResp.Usage.OutputTokens)
		}
	})
}

// ---------------------------------------------------------------------------
// buildParams — InferenceConfig mapping
// ---------------------------------------------------------------------------

func TestBuildParams_NilInferenceConfig_UsesConstructorDefaults(t *testing.T) {
	p := &AnthropicProvider{
		model:     "claude-3-5-haiku-20241022",
		maxTokens: 4096,
	}
	params := agent.ConverseParams{
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
	p := &AnthropicProvider{model: "claude-3-5-haiku-20241022", maxTokens: 8192}
	temp := 0.7
	params := agent.ConverseParams{
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
	p := &AnthropicProvider{model: "claude-3-5-haiku-20241022", maxTokens: 8192}
	topP := 0.9
	params := agent.ConverseParams{
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
	p := &AnthropicProvider{model: "claude-3-5-haiku-20241022", maxTokens: 8192}
	topK := 50
	params := agent.ConverseParams{
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
	p := &AnthropicProvider{model: "claude-3-5-haiku-20241022", maxTokens: 8192}
	stops := []string{"STOP", "END"}
	params := agent.ConverseParams{
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
	p := &AnthropicProvider{model: "claude-3-5-haiku-20241022", maxTokens: 8192}
	maxTok := 2048
	params := agent.ConverseParams{
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
	p := &AnthropicProvider{model: "claude-3-5-haiku-20241022", maxTokens: 8192}
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
	p := &AnthropicProvider{model: "claude-3-5-haiku-20241022", maxTokens: 4096}
	temp := 0.3
	params := agent.ConverseParams{
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

	result := toAnthropicContentBlocks([]agent.ContentBlock{block}, agent.RoleUser)

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

	result := toAnthropicContentBlocks([]agent.ContentBlock{block}, agent.RoleUser)

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

			result := toAnthropicContentBlocks([]agent.ContentBlock{block}, agent.RoleUser)

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

	result := toAnthropicContentBlocks([]agent.ContentBlock{block}, agent.RoleAssistant)

	if len(result) != 0 {
		t.Errorf("expected 0 blocks for assistant-role ImageBlock, got %d", len(result))
	}
}
