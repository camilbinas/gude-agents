package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/camilbinas/gude-agents/agent/tool"
)

func TestMiddleware_StandardContextAndToolCall(t *testing.T) {
	type key string
	ctx := context.WithValue(context.Background(), key("trace"), "abc")
	var got ToolCall
	mw := func(next ToolHandlerFunc) ToolHandlerFunc {
		return func(ctx context.Context, call ToolCall) (ToolResult, error) {
			if ctx.Value(key("trace")) != "abc" {
				t.Fatal("standard context value missing")
			}
			got = call
			return next(ctx, call)
		}
	}
	base := ToolHandlerFunc(func(_ context.Context, call ToolCall) (ToolResult, error) {
		return ToolResult{Text: call.Name}, nil
	})
	result, err := ChainMiddleware(base, mw)(ctx, ToolCall{ID: "call-42", Name: "echo", Input: json.RawMessage(`{"x":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "echo" || got.ID != "call-42" || got.Name != "echo" || string(got.Input) != `{"x":1}` {
		t.Fatalf("result=%#v call=%#v", result, got)
	}
}

func TestMiddleware_ChainExecutionOrder(t *testing.T) {
	var order []string
	wrap := func(name string) Middleware {
		return func(next ToolHandlerFunc) ToolHandlerFunc {
			return func(ctx context.Context, call ToolCall) (ToolResult, error) {
				order = append(order, "before-"+name)
				out, err := next(ctx, call)
				order = append(order, "after-"+name)
				return out, err
			}
		}
	}
	base := func(context.Context, ToolCall) (ToolResult, error) {
		order = append(order, "handler")
		return ToolResult{Text: "ok"}, nil
	}
	_, err := ChainMiddleware(base, wrap("A"), wrap("B"))(context.Background(), ToolCall{Name: "t"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"before-A", "before-B", "handler", "after-B", "after-A"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order=%v want=%v", order, want)
	}
}

func TestMiddleware_ShortCircuitToolResult(t *testing.T) {
	called := false
	mw := func(ToolHandlerFunc) ToolHandlerFunc {
		return func(context.Context, ToolCall) (ToolResult, error) {
			return ToolResult{Text: "blocked", IsError: true}, nil
		}
	}
	base := func(context.Context, ToolCall) (ToolResult, error) {
		called = true
		return ToolResult{}, nil
	}
	got, err := ChainMiddleware(base, mw)(context.Background(), ToolCall{})
	if err != nil || called || got.Text != "blocked" || !got.IsError {
		t.Fatalf("got=%#v called=%v err=%v", got, called, err)
	}
}

func TestMiddleware_IntegrationWithAgentIncludesProviderCallID(t *testing.T) {
	sp := newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "provider-id", Name: "greet", Input: json.RawMessage(`{}`)}}},
		&ModelResponse{Text: "done"},
	)
	greet := tool.NewRaw("greet", "says hello", nil, func(context.Context, json.RawMessage) (string, error) { return "hello", nil })
	var got ToolCall
	mw := func(next ToolHandlerFunc) ToolHandlerFunc {
		return func(ctx context.Context, call ToolCall) (ToolResult, error) {
			got = call
			if FromContext(ctx) == nil {
				t.Fatal("middleware did not receive invocation Context")
			}
			return next(ctx, call)
		}
	}
	a, err := New(sp, "sys", WithTools(greet), WithMiddleware(mw))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Invoke(Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	if got.ID != "provider-id" || got.Name != "greet" {
		t.Fatalf("call=%#v", got)
	}
}
