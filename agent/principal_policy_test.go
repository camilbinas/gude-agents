package agent

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/camilbinas/gude-agents/agent/tool"
)

func principalPolicyTool(name string, calls *atomic.Int32, opts ...tool.Option) tool.Tool {
	return tool.NewRaw(name, name, nil, func(context.Context, json.RawMessage) (string, error) {
		calls.Add(1)
		return "executed", nil
	}, opts...)
}

func TestRoleFilterRequiresPrincipalForDeclaredPolicy(t *testing.T) {
	filter := RoleFilter()
	var calls atomic.Int32
	unrestricted := principalPolicyTool("open", &calls)
	cases := []struct {
		name string
		tool tool.Tool
	}{
		{name: "allow roles", tool: principalPolicyTool("roles", &calls, tool.AllowRoles("admin"))},
		{name: "deny roles", tool: principalPolicyTool("deny-roles", &calls, tool.DenyRoles("guest"))},
		{name: "allow attributes", tool: principalPolicyTool("attrs", &calls, tool.AllowWhen(func(attrs map[string]string) bool { return attrs["tenant"] == "acme" }))},
		{name: "deny attributes", tool: principalPolicyTool("deny-attrs", &calls, tool.DenyWhen(func(attrs map[string]string) bool { return attrs["suspended"] == "true" }))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if filter(Background(), tc.tool) {
				t.Fatal("declared policy must not be visible without a principal")
			}
		})
	}
	if !filter(Background(), unrestricted) {
		t.Fatal("unrestricted tool must remain visible without a principal")
	}
}

func TestProtectedToolsDenyMissingPrincipalBeforeHandler(t *testing.T) {
	cases := []struct {
		name string
		opts []tool.Option
	}{
		{name: "role policy", opts: []tool.Option{tool.AllowRoles("admin")}},
		{name: "attribute policy", opts: []tool.Option{tool.AllowWhen(func(attrs map[string]string) bool { return attrs["tenant"] == "acme" })}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			protected := principalPolicyTool("protected", &calls, tc.opts...)
			provider := newScriptedProvider(
				&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "call-1", Name: "protected", Input: json.RawMessage(`{}`)}}},
				&ModelResponse{Text: "denied"},
			)
			a, err := New(provider, "test", WithTools(protected))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := a.Invoke(Background(), "run"); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 0 {
				t.Fatalf("handler calls = %d, want 0", calls.Load())
			}
		})
	}
}

func TestApprovalResumeDeniesProtectedToolWithoutPrincipal(t *testing.T) {
	var calls atomic.Int32
	protected := principalPolicyTool("protected", &calls,
		tool.AllowWhen(func(attrs map[string]string) bool { return attrs["tenant"] == "acme" }),
		tool.RequiresApproval(),
	)
	provider := newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "call-1", Name: "protected", Input: json.RawMessage(`{}`)}}},
		&ModelResponse{Text: "denied"},
	)
	a, err := New(provider, "test", WithTools(protected))
	if err != nil {
		t.Fatal(err)
	}
	initial := Background().WithPrincipal(Principal{ID: "u1", Attrs: map[string]string{"tenant": "acme"}})
	paused, err := a.Invoke(initial, "run")
	if err != nil || paused.Interrupt == nil || paused.Interrupt.Type != InterruptApproval {
		t.Fatalf("Invoke = %+v, %v; want approval interrupt", paused, err)
	}
	if _, err := a.Resume(Background(), paused.Interrupt, Approve()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatalf("handler calls = %d, want 0 after principal-less resume", calls.Load())
	}
}

func TestRecoveryDeniesProtectedReplayWithoutPrincipal(t *testing.T) {
	conversations, executions := newTestMemoryStore(), newTestExecutionStore()
	seedInFlightToolExecution(t, conversations, executions, true)
	var calls atomic.Int32
	protected := principalPolicyTool("charge", &calls,
		tool.AllowRoles("admin"),
		tool.WithReplaySafe(),
	)
	a, err := New(newScriptedProvider(), "test", WithTools(protected), WithConversationStore(conversations), WithExecutionStore(executions))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.RecoverExecution(Background(), "execution-1"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatalf("handler calls = %d, want 0 after principal-less recovery", calls.Load())
	}
}
