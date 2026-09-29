package anthropic

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
)

func TestStream_CacheUsageMappedFromMessageStart(t *testing.T) {
	const (
		wantInput  = 42
		wantOutput = 7
		wantRead   = 1024
		wantWrite  = 512
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseBody([][2]string{
			{"message_start", fmt.Sprintf(
				`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-3-5-haiku-20241022","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":%d,"output_tokens":0,"cache_read_input_tokens":%d,"cache_creation_input_tokens":%d}}}`,
				wantInput, wantRead, wantWrite,
			)},
			{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"cached"}}`},
			{"content_block_stop", `{"type":"content_block_stop","index":0}`},
			{"message_delta", fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":%d}}`, wantOutput)},
			{"message_stop", `{"type":"message_stop"}`},
		}))
	}))
	defer srv.Close()

	resp, err := newTestProvider(srv.URL).Stream(context.Background(), agent.ModelRequest{
		Messages: []agent.Message{{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hello"}}}},
	}, nil)
	if err != nil {
		t.Fatalf("Stream error: %v", err)
	}

	want := agent.TokenUsage{
		InputTokens:      wantInput,
		OutputTokens:     wantOutput,
		CacheReadTokens:  wantRead,
		CacheWriteTokens: wantWrite,
	}
	if resp.Usage != want {
		t.Errorf("Usage = %#v, want %#v", resp.Usage, want)
	}
}

func TestStream_CacheTokensZeroWhenAbsent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseBody([][2]string{
			{"message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-3-5-haiku-20241022","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":30,"output_tokens":0}}}`},
			{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`},
			{"content_block_stop", `{"type":"content_block_stop","index":0}`},
			{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":5}}`},
			{"message_stop", `{"type":"message_stop"}`},
		}))
	}))
	defer srv.Close()

	resp, err := newTestProvider(srv.URL).Stream(context.Background(), agent.ModelRequest{
		Messages: []agent.Message{{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hello"}}}},
	}, nil)
	if err != nil {
		t.Fatalf("Stream error: %v", err)
	}
	if resp.Usage.CacheReadTokens != 0 || resp.Usage.CacheWriteTokens != 0 {
		t.Errorf("cache usage = (%d, %d), want zero", resp.Usage.CacheReadTokens, resp.Usage.CacheWriteTokens)
	}
}

func TestStream_CacheTokensDoNotAffectTotal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseBody([][2]string{
			{"message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-3-5-haiku-20241022","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":100,"output_tokens":0,"cache_read_input_tokens":400,"cache_creation_input_tokens":200}}}`},
			{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":40}}`},
			{"message_stop", `{"type":"message_stop"}`},
		}))
	}))
	defer srv.Close()

	resp, err := newTestProvider(srv.URL).Stream(context.Background(), agent.ModelRequest{
		Messages: []agent.Message{{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hello"}}}},
	}, nil)
	if err != nil {
		t.Fatalf("Stream error: %v", err)
	}
	if got, want := resp.Usage.Total(), resp.Usage.InputTokens+resp.Usage.OutputTokens; got != want {
		t.Errorf("Total() = %d, want %d", got, want)
	}
	if resp.Usage.CacheReadTokens == 0 || resp.Usage.CacheWriteTokens == 0 {
		t.Fatalf("cache usage = %#v, want non-zero fields", resp.Usage)
	}
}
