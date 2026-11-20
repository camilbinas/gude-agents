package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// Signed thinking must lead the assistant tool-use turn on the next request,
// otherwise Anthropic and Bedrock reject the tool loop.
func TestToolLoop_ReplaysThinkingFirst(t *testing.T) {
	signed := ThinkingBlock{Text: "plan", Signature: "sig"}
	unsigned := ThinkingBlock{Text: "no signature"}
	rp := &recordingProvider{responses: []*ModelResponse{
		{
			Text:      "let me check",
			Thinking:  []ThinkingBlock{signed, unsigned},
			ToolCalls: []tool.Call{toolCall("tc1", "echo")},
		},
		{Text: "done"},
	}}
	echo := tool.NewRaw("echo", "echoes", nil, func(context.Context, json.RawMessage) (string, error) {
		return "echoed", nil
	})

	a, err := New(rp, "sys", WithTools(echo))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Invoke(Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if len(rp.calls) != 2 {
		t.Fatalf("expected 2 provider calls, got %d", len(rp.calls))
	}

	var assistant *Message
	msgs := rp.calls[1].Messages
	for i := range msgs {
		if msgs[i].Role == RoleAssistant {
			assistant = &msgs[i]
		}
	}
	if assistant == nil {
		t.Fatal("no assistant message in second request")
	}
	if len(assistant.Content) != 3 {
		t.Fatalf("expected thinking, text, tool use; got %d blocks: %#v", len(assistant.Content), assistant.Content)
	}
	if got, ok := assistant.Content[0].(ThinkingBlock); !ok || got != signed {
		t.Errorf("block 0 = %#v, want signed thinking", assistant.Content[0])
	}
	if _, ok := assistant.Content[1].(TextBlock); !ok {
		t.Errorf("block 1 = %#v, want text", assistant.Content[1])
	}
	if _, ok := assistant.Content[2].(ToolUseBlock); !ok {
		t.Errorf("block 2 = %#v, want tool use", assistant.Content[2])
	}
}
