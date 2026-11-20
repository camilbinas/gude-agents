package openai

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/camilbinas/gude-agents/agent"

	openaisdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

func sseChunk(delta string) string {
	return fmt.Sprintf("data: {\"id\":\"c\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":%s}]}\n\n", delta)
}

func streamOnce(t *testing.T, p *OpenAIProvider) *agent.ModelResponse {
	t.Helper()
	resp, err := p.Stream(context.Background(), agent.ModelRequest{
		Messages: []agent.Message{{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hi"}}}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestStream_ParallelToolCallsAtSameIndexStaySeparate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseChunk(`{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"f","arguments":"{\"x\":"}}]}`))
		fmt.Fprint(w, sseChunk(`{"tool_calls":[{"index":0,"function":{"arguments":"1}"}}]}`))
		fmt.Fprint(w, sseChunk(`{"tool_calls":[{"index":0,"id":"call_b","type":"function","function":{"name":"g","arguments":"{\"y\":2}"}}]}`))
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	p, _ := New("m", WithAPIKey("k"), WithBaseURL(srv.URL))
	resp := streamOnce(t, p)
	if len(resp.ToolCalls) != 2 {
		t.Fatalf("ToolCalls = %#v", resp.ToolCalls)
	}
	a, b := resp.ToolCalls[0], resp.ToolCalls[1]
	if a.ToolUseID != "call_a" || a.Name != "f" || string(a.Input) != `{"x":1}` {
		t.Errorf("a = %#v (%s)", a, a.Input)
	}
	if b.ToolUseID != "call_b" || b.Name != "g" || string(b.Input) != `{"y":2}` {
		t.Errorf("b = %#v (%s)", b, b.Input)
	}
}

func TestStream_MissingToolCallIDIsGenerated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseChunk(`{"tool_calls":[{"index":0,"type":"function","function":{"name":"f","arguments":"{}"}}]}`))
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	p, _ := New("m", WithAPIKey("k"), WithBaseURL(srv.URL))
	resp := streamOnce(t, p)
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].ToolUseID == "" {
		t.Fatalf("ToolCalls = %#v", resp.ToolCalls)
	}
}

func TestCustomBaseURLNeverSendsEnvAPIKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-real-secret")
	var auth atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	ctors := map[string]func() (*OpenAIProvider, error){
		"New+WithBaseURL": func() (*OpenAIProvider, error) { return New("m", WithBaseURL(srv.URL)) },
		"NewCompatible":   func() (*OpenAIProvider, error) { return NewCompatible("m", WithBaseURL(srv.URL)) },
	}
	for name, mk := range ctors {
		t.Run(name, func(t *testing.T) {
			p, err := mk()
			if err != nil {
				t.Fatal(err)
			}
			streamOnce(t, p)
			got, _ := auth.Load().(string)
			if got == "" || got == "Bearer sk-real-secret" {
				t.Fatalf("Authorization = %q", got)
			}
		})
	}
	// An explicit key is still honoured.
	p, _ := NewCompatible("m", WithBaseURL(srv.URL), WithAPIKey("explicit"))
	streamOnce(t, p)
	if got := auth.Load().(string); got != "Bearer explicit" {
		t.Errorf("Authorization = %q", got)
	}
}

type closeTracker struct {
	io.ReadCloser
	closed *atomic.Bool
}

func (c closeTracker) Close() error { c.closed.Store(true); return c.ReadCloser.Close() }

type trackingTransport struct{ closed atomic.Bool }

func (tt *trackingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := http.DefaultTransport.RoundTrip(r)
	if err == nil {
		resp.Body = closeTracker{resp.Body, &tt.closed}
	}
	return resp, err
}

func TestStream_ClosesResponseBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseChunk(`{"content":"x"}`))
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	tt := &trackingTransport{}
	p, _ := New("m", WithAPIKey("k"), WithBaseURL(srv.URL))
	client := openaisdk.NewClient(option.WithAPIKey("k"), option.WithBaseURL(srv.URL), option.WithHTTPClient(&http.Client{Transport: tt}))
	p.client = &client
	streamOnce(t, p)
	if !tt.closed.Load() {
		t.Fatal("response body was not closed")
	}
}

func TestToOpenAIMessages_IgnoresThinkingBlocks(t *testing.T) {
	msgs, err := toOpenAIMessages([]agent.Message{{Role: agent.RoleAssistant, Content: []agent.ContentBlock{
		agent.ThinkingBlock{Text: "t", Signature: "s"}, agent.TextBlock{Text: "answer"},
	}}}, "")
	if err != nil || len(msgs) != 1 {
		t.Fatalf("msgs=%d err=%v", len(msgs), err)
	}
}
