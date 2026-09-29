package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/camilbinas/gude-agents/agent/tool"
)

func humanInputCall(id string) tool.Call {
	return tool.Call{
		ToolUseID: id,
		Name:      "request_human_input",
		Input:     json.RawMessage(`{"reason":"need info","question":"What is the order ID?"}`),
	}
}

func TestHumanInputTool_InvokeReturnsInterrupt(t *testing.T) {
	provider := newScriptedProvider(&ModelResponse{ToolCalls: []tool.Call{humanInputCall("h1")}})
	a, err := New(provider, "You are helpful.", WithTools(NewHumanInputTool("request_human_input", "")))
	if err != nil {
		t.Fatal(err)
	}
	res, err := a.Invoke(Background(), "Process refund #123")
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}
	if res.StopReason != StopInterrupt || res.Interrupt == nil {
		t.Fatalf("result = %+v, want interrupt", res)
	}
	in := res.Interrupt
	if in.Type != InterruptHumanInput || in.Input == nil || in.Approval != nil {
		t.Fatalf("interrupt = %+v, want human_input", in)
	}
	if in.Input.Reason != "need info" || in.Input.Question != "What is the order ID?" {
		t.Errorf("input = %+v", in.Input)
	}
	if len(in.Messages) == 0 {
		t.Fatal("expected a message snapshot")
	}
	last := in.Messages[len(in.Messages)-1]
	tr, ok := last.Content[0].(ToolResultBlock)
	if !ok || tr.ToolUseID != "h1" || tr.Content != humanInputPausedResult {
		t.Fatalf("last snapshot message = %#v, want paused tool result", last)
	}
}

func TestResume_RespondContinuesAfterHumanInput(t *testing.T) {
	provider := &approvalBatchProvider{responses: []*ModelResponse{
		{ToolCalls: []tool.Call{humanInputCall("h1")}},
		{Text: "Refund processed for order 456."},
	}}
	a, err := New(provider, "You are helpful.", WithTools(NewHumanInputTool("request_human_input", "")),
		WithInputGuardrail(func(_ *Context, s string) (string, error) { return s + "!", nil }))
	if err != nil {
		t.Fatal(err)
	}
	in := mustInterrupt(t, a, Background(), "Process a refund")

	res, err := a.Resume(Background(), in, Respond("Order 456"))
	if err != nil {
		t.Fatalf("Resume failed: %v", err)
	}
	if res.Text != "Refund processed for order 456." || res.StopReason != StopEndTurn {
		t.Errorf("result = %+v", res)
	}
	msgs := provider.params[1].Messages
	last := msgs[len(msgs)-1]
	if tb, ok := last.Content[len(last.Content)-1].(TextBlock); !ok || tb.Text != "Order 456!" {
		t.Fatalf("resumed user message = %#v, want guardrail-processed answer", last)
	}
}

// TestHumanInput_PreservesConversationContext verifies that earlier tool
// results are part of the snapshot.
func TestHumanInput_PreservesConversationContext(t *testing.T) {
	lookup := newTestRaw("lookup", "looks up", map[string]any{"type": "object"},
		func(context.Context, json.RawMessage) (string, error) { return "order 42 found", nil })
	provider := newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "t1", Name: "lookup", Input: json.RawMessage(`{}`)}}},
		&ModelResponse{ToolCalls: []tool.Call{humanInputCall("h1")}},
	)
	a, err := New(provider, "You are helpful.", WithTools(lookup, NewHumanInputTool("request_human_input", "")))
	if err != nil {
		t.Fatal(err)
	}
	in := mustInterrupt(t, a, Background(), "Find order")
	found := false
	for _, m := range in.Messages {
		for _, b := range m.Content {
			if tr, ok := b.(ToolResultBlock); ok && tr.Content == "order 42 found" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("earlier tool result missing from snapshot")
	}
}

// TestHumanInput_TakesPrecedenceOverPendingApproval verifies that approval
// calls in the same batch as a human-input call are never executed.
func TestHumanInput_TakesPrecedenceOverPendingApproval(t *testing.T) {
	called := false
	danger := newTestRaw("danger", "danger", map[string]any{"type": "object"},
		func(context.Context, json.RawMessage) (string, error) { called = true; return "boom", nil },
		tool.RequiresApproval())
	provider := newScriptedProvider(&ModelResponse{ToolCalls: []tool.Call{
		{ToolUseID: "d1", Name: "danger", Input: json.RawMessage(`{}`)},
		humanInputCall("h1"),
	}})
	a, err := New(provider, "x", WithTools(danger, NewHumanInputTool("request_human_input", "")))
	if err != nil {
		t.Fatal(err)
	}
	in := mustInterrupt(t, a, Background(), "go")
	if in.Type != InterruptHumanInput {
		t.Fatalf("type = %q, want human_input", in.Type)
	}
	if called {
		t.Fatal("approval-required tool must not run")
	}
	last := in.Messages[len(in.Messages)-1]
	if len(last.Content) != 2 {
		t.Fatalf("results = %#v, want both calls answered", last.Content)
	}
	if tr := last.Content[0].(ToolResultBlock); tr.ToolUseID != "d1" || !tr.IsError {
		t.Fatalf("pending approval result = %#v, want not-executed error", tr)
	}
}

func TestHumanInput_InterruptStoreLifecycle(t *testing.T) {
	store := newTestInterruptStore()
	provider := newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{humanInputCall("h1")}},
		&ModelResponse{Text: "thanks"},
	)
	a, err := New(provider, "x", WithTools(NewHumanInputTool("request_human_input", "")),
		WithInterruptStore(store))
	if err != nil {
		t.Fatal(err)
	}
	in := mustInterrupt(t, a, Background(), "go")
	if _, ok := store.items[in.ID]; !ok {
		t.Fatal("interrupt was not saved")
	}
	loaded, err := a.LoadInterrupt(context.Background(), in.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Resume(Background(), loaded, Respond("42")); err != nil {
		t.Fatal(err)
	}
	if len(store.claimed) != 1 || store.claimed[0] != in.ID {
		t.Fatalf("claimed = %v, want [%s]", store.claimed, in.ID)
	}
	if _, err := a.Resume(Background(), in, Respond("again")); !errors.Is(err, ErrInterruptNotFound) {
		t.Fatalf("human-input replay err = %v, want ErrInterruptNotFound", err)
	}
}

func TestHumanInputTool_OutsideAgentFails(t *testing.T) {
	ht := NewHumanInputTool("ask", "")
	if _, err := ht.Handler(context.Background(), json.RawMessage(`{"reason":"r","question":"q"}`)); err == nil {
		t.Fatal("expected error outside an agent tool call")
	}
	if _, err := ht.Handler(context.Background(), json.RawMessage(`not json`)); err == nil || errors.Is(err, context.Canceled) {
		t.Fatal("expected invalid input error")
	}
}
