package agent

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// TestNewSimple_RunsThroughCanonicalPipeline verifies NewSimple tools use the
// normal pipeline: role policy, approval interrupts, and handler execution.
func TestNewSimple_RunsThroughCanonicalPipeline(t *testing.T) {
	newAgent := func(t *testing.T, calls *atomic.Int32, opts ...tool.Option) *Agent {
		t.Helper()
		health := tool.NewSimple("health", "Check service health", func(context.Context) (string, error) {
			calls.Add(1)
			return "healthy", nil
		}, opts...)
		p := newScriptedProvider(
			&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "tc-1", Name: "health", Input: json.RawMessage(`{}`)}}},
			&ModelResponse{Text: "done"},
		)
		a, err := New(p, "sys", WithTools(health), WithRoleEnforcement())
		if err != nil {
			t.Fatal(err)
		}
		return a
	}

	t.Run("executes", func(t *testing.T) {
		var calls atomic.Int32
		res, err := newAgent(t, &calls).Invoke(Background(), "check")
		if err != nil || res.Text != "done" || calls.Load() != 1 {
			t.Fatalf("text=%q err=%v calls=%d", res.Text, err, calls.Load())
		}
	})

	t.Run("approval interrupts before handler", func(t *testing.T) {
		var calls atomic.Int32
		res, err := newAgent(t, &calls, tool.RequiresApproval()).Invoke(Background(), "check")
		if err != nil {
			t.Fatal(err)
		}
		if res.StopReason != StopInterrupt || res.Interrupt == nil || res.Interrupt.Type != InterruptApproval {
			t.Fatalf("expected approval interrupt, got %q", res.StopReason)
		}
		if calls.Load() != 0 {
			t.Fatal("handler ran before approval")
		}
	})

	t.Run("role policy denies", func(t *testing.T) {
		var calls atomic.Int32
		ctx := Background().WithPrincipal(Principal{ID: "u", Roles: []string{"guest"}})
		if _, err := newAgent(t, &calls, tool.AllowRoles("admin")).Invoke(ctx, "check"); err != nil {
			t.Fatal(err)
		}
		if calls.Load() != 0 {
			t.Fatal("handler ran for a denied role")
		}
	})
}
