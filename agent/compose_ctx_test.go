package agent

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/camilbinas/gude-agents/agent/tool"
)

type mwKey struct{}

func TestAgentAsTool_ChildSeesMiddlewareContextAndOwnHooks(t *testing.T) {
	var childSawMW atomic.Bool
	var childOwnHook, parentPerInvocationInvokes atomic.Int32
	child, err := New(newScriptedProvider(&ModelResponse{Text: "child"}), "child", WithHooks(Hooks{
		Invoke: func(ctx context.Context, r InvokeRecord) context.Context {
			if r.Phase == Start {
				childOwnHook.Add(1)
				if ctx.Value(mwKey{}) == "mw" {
					childSawMW.Store(true)
				}
			}
			return ctx
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	mw := func(next ToolHandlerFunc) ToolHandlerFunc {
		return func(ctx context.Context, call ToolCall) (ToolResult, error) {
			return next(context.WithValue(ctx, mwKey{}, "mw"), call)
		}
	}
	parent, err := New(newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "t1", Name: "sub", Input: json.RawMessage(`{"message":"hi"}`)}}},
		&ModelResponse{Text: "done"},
	), "parent", WithTools(AgentAsTool("sub", "sub", child)), WithMiddleware(mw))
	if err != nil {
		t.Fatal(err)
	}
	c := Background().WithHooks(Hooks{Invoke: func(ctx context.Context, r InvokeRecord) context.Context {
		if r.Phase == Start {
			parentPerInvocationInvokes.Add(1)
		}
		return ctx
	}})
	if _, err := parent.Invoke(c, "go"); err != nil {
		t.Fatal(err)
	}
	if !childSawMW.Load() {
		t.Error("child invocation did not inherit the middleware-provided context")
	}
	if childOwnHook.Load() != 1 {
		t.Errorf("child's own hook ran %d times, want 1", childOwnHook.Load())
	}
	if parentPerInvocationInvokes.Load() != 1 {
		t.Errorf("parent per-invocation hook ran %d times, want 1 (not for the child)", parentPerInvocationInvokes.Load())
	}
}
