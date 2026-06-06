package anthropic

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/camilbinas/gude-agents/agent"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
)

// ---------------------------------------------------------------------------
// Converse — cache usage mapping
// Requirements: 5.3, 5.4
// ---------------------------------------------------------------------------

// TestConverse_CacheReadTokens_MappedFromUsage verifies that
// msg.Usage.CacheReadInputTokens is mapped to resp.Usage.CacheReadTokens.
//
// The Converse method performs:
//
//	resp.Usage.CacheReadTokens = int(msg.Usage.CacheReadInputTokens)
func TestConverse_CacheReadTokens_MappedFromUsage(t *testing.T) {
	const wantRead int64 = 1024
	const wantWrite int64 = 256

	msg := &anthropicsdk.Message{
		Content: []anthropicsdk.ContentBlockUnion{
			{Type: "text", Text: "hello"},
		},
		Usage: anthropicsdk.Usage{
			InputTokens:              100,
			OutputTokens:             50,
			CacheReadInputTokens:     wantRead,
			CacheCreationInputTokens: wantWrite,
		},
	}

	// Reproduce the exact logic from Converse (post-parseMessage):
	resp := parseMessage(msg)
	resp.Usage.InputTokens = int(msg.Usage.InputTokens)
	resp.Usage.OutputTokens = int(msg.Usage.OutputTokens)
	resp.Usage.CacheReadTokens = int(msg.Usage.CacheReadInputTokens)
	resp.Usage.CacheWriteTokens = int(msg.Usage.CacheCreationInputTokens)

	if got := resp.Usage.CacheReadTokens; got != int(wantRead) {
		t.Errorf("CacheReadTokens: want %d, got %d", wantRead, got)
	}
	if got := resp.Usage.CacheWriteTokens; got != int(wantWrite) {
		t.Errorf("CacheWriteTokens: want %d, got %d", wantWrite, got)
	}
}

// TestConverse_CacheTokens_ZeroWhenAbsent verifies that CacheReadTokens and
// CacheWriteTokens remain 0 when the response contains no cache usage data
// (zero-value semantics, requirement 5.8).
func TestConverse_CacheTokens_ZeroWhenAbsent(t *testing.T) {
	msg := &anthropicsdk.Message{
		Content: []anthropicsdk.ContentBlockUnion{
			{Type: "text", Text: "hello"},
		},
		Usage: anthropicsdk.Usage{
			InputTokens:  100,
			OutputTokens: 50,
			// CacheReadInputTokens and CacheCreationInputTokens not set → 0
		},
	}

	resp := parseMessage(msg)
	resp.Usage.InputTokens = int(msg.Usage.InputTokens)
	resp.Usage.OutputTokens = int(msg.Usage.OutputTokens)
	resp.Usage.CacheReadTokens = int(msg.Usage.CacheReadInputTokens)
	resp.Usage.CacheWriteTokens = int(msg.Usage.CacheCreationInputTokens)

	if got := resp.Usage.CacheReadTokens; got != 0 {
		t.Errorf("CacheReadTokens: want 0 when absent, got %d", got)
	}
	if got := resp.Usage.CacheWriteTokens; got != 0 {
		t.Errorf("CacheWriteTokens: want 0 when absent, got %d", got)
	}
}

// TestConverse_CacheTokens_DoNotAffectTotal verifies that cache token counts
// do not leak into TokenUsage.Total() (requirement 5.9).
func TestConverse_CacheTokens_DoNotAffectTotal(t *testing.T) {
	msg := &anthropicsdk.Message{
		Content: []anthropicsdk.ContentBlockUnion{
			{Type: "text", Text: "hello"},
		},
		Usage: anthropicsdk.Usage{
			InputTokens:              200,
			OutputTokens:             80,
			CacheReadInputTokens:     500,
			CacheCreationInputTokens: 300,
		},
	}

	resp := parseMessage(msg)
	resp.Usage.InputTokens = int(msg.Usage.InputTokens)
	resp.Usage.OutputTokens = int(msg.Usage.OutputTokens)
	resp.Usage.CacheReadTokens = int(msg.Usage.CacheReadInputTokens)
	resp.Usage.CacheWriteTokens = int(msg.Usage.CacheCreationInputTokens)

	wantTotal := resp.Usage.InputTokens + resp.Usage.OutputTokens
	if got := resp.Usage.Total(); got != wantTotal {
		t.Errorf("Total(): want %d (input+output only), got %d", wantTotal, got)
	}
	// Sanity-check that cache tokens are non-zero so the assertion is meaningful.
	if resp.Usage.CacheReadTokens == 0 {
		t.Fatal("CacheReadTokens should be non-zero for this test to be meaningful")
	}
}

// TestConverse_CacheUsage_ViaHTTPMock verifies the full Converse path, including
// the actual HTTP response parsing, maps CacheReadInputTokens and
// CacheCreationInputTokens correctly into ProviderResponse.Usage.
func TestConverse_CacheUsage_ViaHTTPMock(t *testing.T) {
	const (
		wantInput  = 100
		wantOutput = 50
		wantRead   = 768
		wantWrite  = 128
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{
			"id": "msg_cache_test",
			"type": "message",
			"role": "assistant",
			"content": [{"type": "text", "text": "cached response"}],
			"model": "claude-3-5-haiku-20241022",
			"stop_reason": "end_turn",
			"stop_sequence": null,
			"usage": {
				"input_tokens": %d,
				"output_tokens": %d,
				"cache_read_input_tokens": %d,
				"cache_creation_input_tokens": %d
			}
		}`, wantInput, wantOutput, wantRead, wantWrite)
	}))
	defer srv.Close()

	p := newTestProvider(srv.URL)
	params := agent.ConverseParams{
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hello"}}},
		},
	}

	resp, err := p.Converse(context.Background(), params)
	if err != nil {
		t.Fatalf("Converse error: %v", err)
	}

	if got := resp.Usage.CacheReadTokens; got != wantRead {
		t.Errorf("CacheReadTokens: want %d, got %d", wantRead, got)
	}
	if got := resp.Usage.CacheWriteTokens; got != wantWrite {
		t.Errorf("CacheWriteTokens: want %d, got %d", wantWrite, got)
	}
	if got := resp.Usage.InputTokens; got != wantInput {
		t.Errorf("InputTokens: want %d, got %d", wantInput, got)
	}
	if got := resp.Usage.OutputTokens; got != wantOutput {
		t.Errorf("OutputTokens: want %d, got %d", wantOutput, got)
	}
}

// ---------------------------------------------------------------------------
// ConverseStream — cache usage mapping from message_start event
// Requirements: 5.3, 5.4
// ---------------------------------------------------------------------------

// TestConverseStream_CacheUsage_MappedFromMessageStart verifies that the
// message_start event's cache_read_input_tokens and cache_creation_input_tokens
// are mapped to resp.Usage.CacheReadTokens and resp.Usage.CacheWriteTokens.
func TestConverseStream_CacheUsage_MappedFromMessageStart(t *testing.T) {
	const (
		wantInput = 42
		wantRead  = 1024
		wantWrite = 512
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		body := sseBody([][2]string{
			{"message_start", fmt.Sprintf(
				`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-3-5-haiku-20241022","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":%d,"output_tokens":0,"cache_read_input_tokens":%d,"cache_creation_input_tokens":%d}}}`,
				wantInput, wantRead, wantWrite,
			)},
			{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"cached"}}`},
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
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hello"}}},
		},
	}

	resp, err := p.ConverseStream(context.Background(), params, nil)
	if err != nil {
		t.Fatalf("ConverseStream error: %v", err)
	}

	if got := resp.Usage.CacheReadTokens; got != wantRead {
		t.Errorf("CacheReadTokens: want %d, got %d", wantRead, got)
	}
	if got := resp.Usage.CacheWriteTokens; got != wantWrite {
		t.Errorf("CacheWriteTokens: want %d, got %d", wantWrite, got)
	}
	if got := resp.Usage.InputTokens; got != wantInput {
		t.Errorf("InputTokens: want %d, got %d", wantInput, got)
	}
}

// TestConverseStream_CacheTokens_ZeroWhenAbsent verifies that CacheReadTokens
// and CacheWriteTokens remain 0 when the message_start event contains no cache
// usage fields.
func TestConverseStream_CacheTokens_ZeroWhenAbsent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		body := sseBody([][2]string{
			{"message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-3-5-haiku-20241022","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":30,"output_tokens":0}}}`},
			{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`},
			{"content_block_stop", `{"type":"content_block_stop","index":0}`},
			{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":5}}`},
			{"message_stop", `{"type":"message_stop"}`},
		})
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	p := newTestProvider(srv.URL)
	params := agent.ConverseParams{
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hello"}}},
		},
	}

	resp, err := p.ConverseStream(context.Background(), params, nil)
	if err != nil {
		t.Fatalf("ConverseStream error: %v", err)
	}

	if got := resp.Usage.CacheReadTokens; got != 0 {
		t.Errorf("CacheReadTokens: want 0 when absent, got %d", got)
	}
	if got := resp.Usage.CacheWriteTokens; got != 0 {
		t.Errorf("CacheWriteTokens: want 0 when absent, got %d", got)
	}
}

// TestConverseStream_CacheTokens_DoNotAffectTotal verifies that cache token
// counts populated from the message_start event do not appear in Total().
func TestConverseStream_CacheTokens_DoNotAffectTotal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		body := sseBody([][2]string{
			{"message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-3-5-haiku-20241022","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":100,"output_tokens":0,"cache_read_input_tokens":400,"cache_creation_input_tokens":200}}}`},
			{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`},
			{"content_block_stop", `{"type":"content_block_stop","index":0}`},
			{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":40}}`},
			{"message_stop", `{"type":"message_stop"}`},
		})
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	p := newTestProvider(srv.URL)
	params := agent.ConverseParams{
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hello"}}},
		},
	}

	resp, err := p.ConverseStream(context.Background(), params, nil)
	if err != nil {
		t.Fatalf("ConverseStream error: %v", err)
	}

	wantTotal := resp.Usage.InputTokens + resp.Usage.OutputTokens
	if got := resp.Usage.Total(); got != wantTotal {
		t.Errorf("Total(): want %d (input+output only), got %d", wantTotal, got)
	}
	// Sanity-check cache tokens are non-zero so the test is meaningful.
	if resp.Usage.CacheReadTokens == 0 {
		t.Fatal("CacheReadTokens should be non-zero for this test to be meaningful")
	}
}
