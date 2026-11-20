package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// runTools runs one batch of tool calls and returns the tool results the
// provider saw in its final request.
func runTools(t *testing.T, tools []tool.Tool, calls []string, opts ...Option) []ToolResultBlock {
	t.Helper()
	provider := &approvalBatchProvider{responses: []*ModelResponse{{ToolCalls: batchOf(calls...)}, {Text: "done"}}}
	a, err := New(provider, "sys", append([]Option{WithTools(tools...)}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Invoke(Background(), "go"); err != nil {
		t.Fatal(err)
	}
	last := provider.params[len(provider.params)-1].Messages
	var out []ToolResultBlock
	for _, b := range last[len(last)-1].Content {
		out = append(out, b.(ToolResultBlock))
	}
	return out
}

func blockingTool(name string) tool.Tool {
	return tool.NewRaw(name, name, nil, func(ctx context.Context, _ json.RawMessage) (string, error) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(300 * time.Millisecond):
			return "finished", nil
		}
	})
}

func TestWithToolTimeoutAndPerToolOverride(t *testing.T) {
	results := runTools(t, []tool.Tool{blockingTool("slow"), blockingTool("exempt")}, []string{"slow", "exempt"},
		WithToolTimeout(30*time.Millisecond), WithToolTimeoutFor("exempt", 5*time.Second))
	if !results[0].IsError || !strings.Contains(results[0].Content, "deadline exceeded") {
		t.Fatalf("slow result = %+v, want deadline error", results[0])
	}
	if results[1].IsError || results[1].Content != "finished" {
		t.Fatalf("exempt result = %+v", results[1])
	}
}

func TestWithMaxToolResultBytesTruncatesWithMarker(t *testing.T) {
	big := tool.NewRaw("big", "big", nil, func(context.Context, json.RawMessage) (string, error) {
		return "héllo wörld, this is long", nil
	})
	results := runTools(t, []tool.Tool{big}, []string{"big"}, WithMaxToolResultBytes(2))
	// Byte 2 falls inside the 2-byte 'é', so the cut backs up to 1 byte.
	if !strings.HasPrefix(results[0].Content, "h\n[truncated: ") || !strings.HasSuffix(results[0].Content, " bytes omitted]") {
		t.Fatalf("content = %q", results[0].Content)
	}
	results = runTools(t, []tool.Tool{big}, []string{"big"})
	if results[0].Content != "héllo wörld, this is long" {
		t.Fatalf("default must not truncate: %q", results[0].Content)
	}
}

func TestToolLimitOptionValidation(t *testing.T) {
	for _, opt := range []Option{WithToolTimeout(-1), WithToolTimeoutFor("x", -1), WithToolTimeoutFor("", time.Second), WithMaxToolResultBytes(-1)} {
		if _, err := New(mockProvider{}, "s", opt); err == nil {
			t.Fatal("want validation error")
		}
	}
}
