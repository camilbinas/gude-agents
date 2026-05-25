package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// TestInvokeEventStream_EmitCustomEvent verifies that a tool handler can
// emit a user-defined event via Context.EmitEvent and that the event is
// delivered onto the InvokeEventStream channel as EventCustom.
func TestInvokeEventStream_EmitCustomEvent(t *testing.T) {
	type EmitInput struct {
		What string `json:"what"`
	}

	emitTool := tool.New("emit", "emit a custom event",
		func(ctx context.Context, in EmitInput) (string, error) {
			c := FromContext(ctx)
			if c == nil {
				t.Fatal("expected *Context inside tool handler")
			}
			c.EmitEvent("test.emitted", map[string]any{"what": in.What, "n": 42})
			return "ok", nil
		},
	)

	provider := newScriptedProvider(
		&ProviderResponse{
			ToolCalls: []tool.Call{{ToolUseID: "1", Name: "emit", Input: json.RawMessage(`{"what":"hello"}`)}},
		},
		&ProviderResponse{Text: "done"},
	)

	a, err := New(provider, prompt.Text("sys"), []tool.Tool{emitTool})
	if err != nil {
		t.Fatal(err)
	}

	events := a.InvokeEventStream(Background(), "go")

	var got AgentEvent
	for ev := range events {
		if ev.Type == EventCustom {
			got = ev
		}
	}

	if got.Type != EventCustom {
		t.Fatal("expected EventCustom on the stream, got none")
	}
	if got.CustomName != "test.emitted" {
		t.Errorf("CustomName=%q, want test.emitted", got.CustomName)
	}
	var payload struct {
		What string `json:"what"`
		N    int    `json:"n"`
	}
	if err := json.Unmarshal(got.CustomPayload, &payload); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	if payload.What != "hello" || payload.N != 42 {
		t.Errorf("payload=%+v, want {hello 42}", payload)
	}
}

// TestEmitEvent_NoStream is a no-op — no panic, no error — when the agent
// is invoked via the plain Invoke path with no event hook attached.
func TestEmitEvent_NoStream(t *testing.T) {
	emitTool := tool.New("emit", "emit a custom event",
		func(ctx context.Context, _ struct{}) (string, error) {
			c := FromContext(ctx)
			if c == nil {
				t.Fatal("expected *Context inside tool handler")
			}
			c.EmitEvent("test.emitted", "anything")
			return "ok", nil
		},
	)

	provider := newScriptedProvider(
		&ProviderResponse{ToolCalls: []tool.Call{toolCall("1", "emit")}},
		&ProviderResponse{Text: "done"},
	)
	a, err := New(provider, prompt.Text("sys"), []tool.Tool{emitTool})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Invoke(Background(), "go"); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
}
