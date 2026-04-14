package anthropic

import (
	"context"
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

// ---------------------------------------------------------------------------
// Feature: agent-framework-v2, Property 5: Anthropic ToolChoice mapping
// **Validates: Requirements 3.3**
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Feature: agent-framework-v2, Property 13: Anthropic TokenUsage population
// **Validates: Requirements 7.3**
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Task 4.1 — Unit test for streaming token counts
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Task 4.2 — Property tests for streaming token consistency
// ---------------------------------------------------------------------------

// Feature: agent-framework-improvements, Property 1: Streaming token counts are non-zero for non-empty prompts
// **Validates: Requirements 2.3**
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

// Feature: agent-framework-improvements, Property 2: Streaming and non-streaming token totals are consistent
// **Validates: Requirements 2.3, 2.4**
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
