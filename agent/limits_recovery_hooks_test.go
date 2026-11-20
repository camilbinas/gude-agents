package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/camilbinas/gude-agents/agent/tool"
)

func TestRecoverExecutionWrapsToolHooksInInvoke(t *testing.T) {
	conversations, executions := newTestMemoryStore(), newTestExecutionStore()
	seedInFlightToolExecution(t, conversations, executions, true)
	var order []string
	charge := tool.NewRaw("charge", "charge", nil, func(context.Context, json.RawMessage) (string, error) {
		return "charged", nil
	}, tool.WithReplaySafe())
	a, err := New(newScriptedProvider(), "x", WithTools(charge),
		WithConversationStore(conversations), WithExecutionStore(executions),
		WithHooks(Hooks{
			Invoke: func(ctx context.Context, r InvokeRecord) context.Context {
				order = append(order, "invoke:"+string(r.Phase))
				return ctx
			},
			Tool: func(ctx context.Context, r ToolCallRecord) context.Context {
				order = append(order, "tool:"+string(r.Phase))
				return ctx
			},
		}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.RecoverExecution(Background(), "execution-1"); err != nil {
		t.Fatal(err)
	}
	want := []string{"invoke:start", "tool:start", "tool:end", "invoke:end"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

type rejectingLimiter struct{}

func (rejectingLimiter) Reserve(context.Context, RateLimitRequest) (RateLimitLease, error) {
	return nil, ErrRateLimitExceeded
}
func (rejectingLimiter) Commit(context.Context, RateLimitLease, TokenUsage) error { return nil }
func (rejectingLimiter) Release(context.Context, RateLimitLease) error            { return nil }

func TestLimitHookFiresForTokenBudgetAndRateLimit(t *testing.T) {
	var names []string
	hook := WithHooks(Hooks{Limit: func(ctx context.Context, r LimitRecord) context.Context {
		names = append(names, r.Name)
		return ctx
	}})

	a, err := New(newScriptedProvider(&ModelResponse{Text: "x", Usage: TokenUsage{InputTokens: 50, OutputTokens: 50}}), "s", WithTokenBudget(10), hook)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Invoke(Background(), "hi"); !errors.Is(err, ErrTokenBudgetExceeded) {
		t.Fatalf("err = %v", err)
	}
	b, err := New(newScriptedProvider(&ModelResponse{Text: "x"}), "s", WithRateLimiter(rejectingLimiter{}), hook)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Invoke(Background(), "hi"); !errors.Is(err, ErrRateLimitExceeded) {
		t.Fatalf("err = %v", err)
	}
	if len(names) != 2 || names[0] != LimitTokenBudget || names[1] != LimitRateLimit {
		t.Fatalf("limit hooks = %v", names)
	}
}
