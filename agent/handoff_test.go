package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
)

func TestHandoffTool_ReturnsErrHandoffRequested(t *testing.T) {
	provider := newScriptedProvider(
		// LLM calls the handoff tool.
		&ProviderResponse{
			ToolCalls: []tool.Call{{
				ToolUseID: "h1",
				Name:      "request_human_input",
				Input:     json.RawMessage(`{"reason":"need approval","question":"Approve refund?"}`),
			}},
		},
	)

	a, err := New(provider, prompt.Text("You are helpful."), []tool.Tool{HandoffTool()})
	if err != nil {
		t.Fatal(err)
	}

	ic := NewInvocationContext()
	ctx := WithInvocationContext(context.Background(), ic)

	_, err = a.InvokeStream(ctx, "Process refund #123", nil)
	if !errors.Is(err, ErrHandoffRequested) {
		t.Fatalf("expected ErrHandoffRequested, got %v", err)
	}

	hr, ok := GetHandoffRequest(ic)
	if !ok {
		t.Fatal("expected HandoffRequest in InvocationContext")
	}
	if hr.Reason != "need approval" {
		t.Errorf("reason = %q, want %q", hr.Reason, "need approval")
	}
	if hr.Question != "Approve refund?" {
		t.Errorf("question = %q, want %q", hr.Question, "Approve refund?")
	}
	if len(hr.Messages) == 0 {
		t.Fatal("expected messages to be preserved in HandoffRequest")
	}
}

func TestResume_ContinuesAfterHandoff(t *testing.T) {
	callCount := 0
	provider := newScriptedProvider(
		// First invocation: LLM calls handoff tool.
		&ProviderResponse{
			ToolCalls: []tool.Call{{
				ToolUseID: "h1",
				Name:      "request_human_input",
				Input:     json.RawMessage(`{"reason":"need info","question":"What is the order ID?"}`),
			}},
		},
		// Second invocation (Resume): LLM produces final answer.
		&ProviderResponse{Text: "Refund processed for order 456."},
	)

	counterTool := tool.NewRaw("count", "counts calls", map[string]any{"type": "object"},
		func(ctx context.Context, input json.RawMessage) (string, error) {
			callCount++
			return "counted", nil
		},
	)

	a, err := New(provider, prompt.Text("You are helpful."), []tool.Tool{HandoffTool(), counterTool})
	if err != nil {
		t.Fatal(err)
	}

	ic := NewInvocationContext()
	ctx := WithInvocationContext(context.Background(), ic)

	_, err = a.InvokeStream(ctx, "Process a refund", nil)
	if !errors.Is(err, ErrHandoffRequested) {
		t.Fatalf("expected ErrHandoffRequested, got %v", err)
	}

	hr, _ := GetHandoffRequest(ic)

	// Resume with human input.
	result, _, err := a.ResumeInvoke(ctx, hr, "Order 456")
	if err != nil {
		t.Fatalf("Resume failed: %v", err)
	}
	if result != "Refund processed for order 456." {
		t.Errorf("result = %q, want %q", result, "Refund processed for order 456.")
	}
}

func TestHandoff_PreservesConversationContext(t *testing.T) {
	provider := newScriptedProvider(
		// LLM calls a regular tool first.
		&ProviderResponse{
			ToolCalls: []tool.Call{{
				ToolUseID: "t1",
				Name:      "lookup",
				Input:     json.RawMessage(`{}`),
			}},
		},
		// Then calls handoff.
		&ProviderResponse{
			ToolCalls: []tool.Call{{
				ToolUseID: "h1",
				Name:      "request_human_input",
				Input:     json.RawMessage(`{"reason":"confirm","question":"Is this correct?"}`),
			}},
		},
	)

	lookupTool := tool.NewRaw("lookup", "looks up data", map[string]any{"type": "object"},
		func(ctx context.Context, input json.RawMessage) (string, error) {
			return "found: item ABC", nil
		},
	)

	a, err := New(provider, prompt.Text("You are helpful."), []tool.Tool{HandoffTool(), lookupTool})
	if err != nil {
		t.Fatal(err)
	}

	ic := NewInvocationContext()
	ctx := WithInvocationContext(context.Background(), ic)

	_, err = a.InvokeStream(ctx, "Check item ABC", nil)
	if !errors.Is(err, ErrHandoffRequested) {
		t.Fatalf("expected ErrHandoffRequested, got %v", err)
	}

	hr, _ := GetHandoffRequest(ic)

	// Messages should include: user msg, assistant tool call, tool result, assistant handoff call.
	// That's at least 4 messages (the tool work before the handoff is preserved).
	if len(hr.Messages) < 3 {
		t.Errorf("expected at least 3 messages in handoff context, got %d", len(hr.Messages))
	}
}

func TestGetHandoffRequest_NilContext(t *testing.T) {
	hr, ok := GetHandoffRequest(nil)
	if ok || hr != nil {
		t.Error("expected nil, false for nil InvocationContext")
	}
}

func TestGetHandoffRequest_NoHandoff(t *testing.T) {
	ic := NewInvocationContext()
	hr, ok := GetHandoffRequest(ic)
	if ok || hr != nil {
		t.Error("expected nil, false when no handoff was requested")
	}
}
