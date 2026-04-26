package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// ---------------------------------------------------------------------------
// 8.3 – Middleware unit tests
// ---------------------------------------------------------------------------

func TestMiddleware_SingleWrapping(t *testing.T) {
	// A single middleware that records the tool name and delegates to next.
	var called string
	mw := func(next ToolHandlerFunc) ToolHandlerFunc {
		return func(ctx context.Context, toolName string, input json.RawMessage) (string, error) {
			called = toolName
			return next(ctx, toolName, input)
		}
	}

	base := func(ctx context.Context, toolName string, input json.RawMessage) (string, error) {
		return "base-result", nil
	}

	handler := ChainMiddleware(base, mw)
	result, err := handler(context.Background(), "my-tool", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called != "my-tool" {
		t.Errorf("expected middleware to see tool name %q, got %q", "my-tool", called)
	}
	if result != "base-result" {
		t.Errorf("expected %q, got %q", "base-result", result)
	}
}

func TestMiddleware_ChainExecutionOrder(t *testing.T) {
	// Two middlewares: A (outermost) and B (inner).
	// Expected order: before-A, before-B, handler, after-B, after-A.
	var order []string

	mwA := func(next ToolHandlerFunc) ToolHandlerFunc {
		return func(ctx context.Context, toolName string, input json.RawMessage) (string, error) {
			order = append(order, "before-A")
			result, err := next(ctx, toolName, input)
			order = append(order, "after-A")
			return result, err
		}
	}

	mwB := func(next ToolHandlerFunc) ToolHandlerFunc {
		return func(ctx context.Context, toolName string, input json.RawMessage) (string, error) {
			order = append(order, "before-B")
			result, err := next(ctx, toolName, input)
			order = append(order, "after-B")
			return result, err
		}
	}

	base := func(ctx context.Context, toolName string, input json.RawMessage) (string, error) {
		order = append(order, "handler")
		return "ok", nil
	}

	handler := ChainMiddleware(base, mwA, mwB)
	_, err := handler(context.Background(), "t", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []string{"before-A", "before-B", "handler", "after-B", "after-A"}
	if len(order) != len(expected) {
		t.Fatalf("expected %d entries, got %d: %v", len(expected), len(order), order)
	}
	for i, want := range expected {
		if order[i] != want {
			t.Errorf("order[%d]: expected %q, got %q", i, want, order[i])
		}
	}
}

func TestMiddleware_ShortCircuit(t *testing.T) {
	// Middleware returns a result without calling next.
	// The underlying handler must never be called.
	handlerCalled := false

	mw := func(next ToolHandlerFunc) ToolHandlerFunc {
		return func(ctx context.Context, toolName string, input json.RawMessage) (string, error) {
			return "short-circuited", nil // does NOT call next
		}
	}

	base := func(ctx context.Context, toolName string, input json.RawMessage) (string, error) {
		handlerCalled = true
		return "base", nil
	}

	handler := ChainMiddleware(base, mw)
	result, err := handler(context.Background(), "t", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if handlerCalled {
		t.Error("expected underlying handler NOT to be called, but it was")
	}
	if result != "short-circuited" {
		t.Errorf("expected %q, got %q", "short-circuited", result)
	}
}

func TestMiddleware_NoMiddlewares(t *testing.T) {
	// chainMiddleware with no middlewares should just return the base handler.
	base := func(ctx context.Context, toolName string, input json.RawMessage) (string, error) {
		return "direct", nil
	}

	handler := ChainMiddleware(base)
	result, err := handler(context.Background(), "t", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "direct" {
		t.Errorf("expected %q, got %q", "direct", result)
	}
}

// ---------------------------------------------------------------------------
// Integration: middleware invoked via Agent with WithMiddleware option
// ---------------------------------------------------------------------------

func TestMiddleware_IntegrationWithAgent(t *testing.T) {
	// Set up a scripted provider: first response triggers a tool call,
	// second response is the final answer.
	sp := newScriptedProvider(
		&ProviderResponse{ToolCalls: []tool.Call{toolCall("tc1", "greet")}},
		&ProviderResponse{Text: "done"},
	)

	greetTool := tool.NewRaw("greet", "says hello", map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (string, error) {
			return "hello", nil
		})

	// Middleware records every tool invocation.
	var invocations []string
	logMW := func(next ToolHandlerFunc) ToolHandlerFunc {
		return func(ctx context.Context, toolName string, input json.RawMessage) (string, error) {
			invocations = append(invocations, toolName)
			return next(ctx, toolName, input)
		}
	}

	a, err := New(sp, prompt.Text("sys"), []tool.Tool{greetTool}, WithMiddleware(logMW))
	if err != nil {
		t.Fatal(err)
	}

	result, _, err := a.Invoke(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "done" {
		t.Errorf("expected %q, got %q", "done", result)
	}
	if len(invocations) != 1 || invocations[0] != "greet" {
		t.Errorf("expected middleware to record [greet], got %v", invocations)
	}
}
