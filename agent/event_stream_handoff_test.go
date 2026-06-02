package agent

import (
	"encoding/json"
	"testing"

	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// TestInvokeEventStream_EmitsHandoffRequestedEvent verifies that when the agent
// calls the handoff tool, InvokeEventStream emits an EventHandoffRequested event
// with the reason and question populated, followed by the terminal EventInvokeEnd.
func TestInvokeEventStream_EmitsHandoffRequestedEvent(t *testing.T) {
	provider := newScriptedProvider(
		&ProviderResponse{
			ToolCalls: []tool.Call{{
				ToolUseID: "h1",
				Name:      "escalate",
				Input:     json.RawMessage(`{"reason":"high value","question":"Approve refund?"}`),
			}},
		},
	)

	a, err := New(provider, prompt.Text("helpful"), []tool.Tool{NewHandoffTool("escalate", "")})
	if err != nil {
		t.Fatal(err)
	}

	ch := a.InvokeEventStream(Background(), "refund please")

	var handoffEv *AgentEvent
	var invokeEndEv *AgentEvent

	for ev := range ch {
		ev := ev
		switch ev.Type {
		case EventHandoffRequested:
			handoffEv = &ev
		case EventInvokeEnd:
			invokeEndEv = &ev
		}
	}

	if handoffEv == nil {
		t.Fatal("expected EventHandoffRequested, got none")
	}
	if handoffEv.HandoffReason != "high value" {
		t.Errorf("HandoffReason = %q, want %q", handoffEv.HandoffReason, "high value")
	}
	if handoffEv.HandoffQuestion != "Approve refund?" {
		t.Errorf("HandoffQuestion = %q, want %q", handoffEv.HandoffQuestion, "Approve refund?")
	}

	if invokeEndEv == nil {
		t.Fatal("expected EventInvokeEnd")
	}
	// Err should carry ErrHandoffRequested so non-event consumers can still detect it.
	if invokeEndEv.Err == nil {
		t.Error("expected EventInvokeEnd.Err to be non-nil (ErrHandoffRequested)")
	}
}

// TestInvokeEventStream_NoHandoffEvent verifies that EventHandoffRequested is NOT
// emitted on a normal (non-handoff) invocation.
func TestInvokeEventStream_NoHandoffEvent(t *testing.T) {
	provider := newScriptedProvider(
		&ProviderResponse{Text: "Hello!"},
	)

	a, err := New(provider, prompt.Text("helpful"), nil)
	if err != nil {
		t.Fatal(err)
	}

	ch := a.InvokeEventStream(Background(), "hi")

	for ev := range ch {
		if ev.Type == EventHandoffRequested {
			t.Error("unexpected EventHandoffRequested on normal invocation")
		}
	}
}
