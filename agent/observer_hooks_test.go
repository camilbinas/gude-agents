package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/camilbinas/gude-agents/agent/tool"
)

type hooksCtxKey struct{}

func hooksTestAgent(t *testing.T, opts ...Option) *Runtime {
	t.Helper()
	echo := tool.NewRaw("echo", "echo", map[string]any{"type": "object"},
		func(context.Context, json.RawMessage) (string, error) { return "ok", nil })
	provider := newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "t1", Name: "echo", Input: json.RawMessage(`{}`)}}},
		&ModelResponse{Text: "done"},
	)
	a, err := New(provider, "sys", append([]Option{WithTools(echo)}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestWithHooks_CallsOnlySetFields(t *testing.T) {
	var phases []Phase
	var models int
	a := hooksTestAgent(t, WithHooks(Hooks{
		Tool: func(ctx context.Context, r ToolCallRecord) context.Context {
			phases = append(phases, r.Phase)
			return ctx
		},
		Model: func(ctx context.Context, r ModelCallRecord) context.Context {
			models++
			return ctx
		},
	}))
	if _, err := a.Invoke(Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if len(phases) != 2 || phases[0] != Start || phases[1] != End {
		t.Fatalf("tool phases = %v, want [start end]", phases)
	}
	if models != 4 {
		t.Fatalf("model records = %d, want 4 (2 calls x start/end)", models)
	}
}

func TestWithHooks_StartContextReachesEnd(t *testing.T) {
	var got any
	a := hooksTestAgent(t, WithHooks(Hooks{
		Tool: func(ctx context.Context, r ToolCallRecord) context.Context {
			if r.Phase == Start {
				return context.WithValue(ctx, hooksCtxKey{}, "span")
			}
			got = ctx.Value(hooksCtxKey{})
			return ctx
		},
	}))
	if _, err := a.Invoke(Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if got != "span" {
		t.Fatalf("end context value = %v, want span", got)
	}
}

type orderObserver struct{ log *[]string }

func (o orderObserver) ObserveTool(ctx context.Context, r ToolCallRecord) context.Context {
	*o.log = append(*o.log, "observer-"+string(r.Phase))
	return ctx
}

func TestWithHooks_MixesWithObserversInOrder(t *testing.T) {
	var log []string
	a := hooksTestAgent(t,
		WithHooks(Hooks{Tool: func(ctx context.Context, r ToolCallRecord) context.Context {
			log = append(log, "hooks-"+string(r.Phase))
			return ctx
		}}),
		WithObserver(orderObserver{&log}),
	)
	if _, err := a.Invoke(Background(), "go"); err != nil {
		t.Fatal(err)
	}
	want := []string{"hooks-start", "observer-start", "observer-end", "hooks-end"}
	if len(log) != len(want) {
		t.Fatalf("order = %v, want %v", log, want)
	}
	for i := range want {
		if log[i] != want[i] {
			t.Fatalf("order = %v, want %v", log, want)
		}
	}
}

func TestWithHooks_ToolLogOnlyWhenSet(t *testing.T) {
	if (&hooks{observers: []Observer{hooksObserver{Hooks{Tool: func(ctx context.Context, _ ToolCallRecord) context.Context { return ctx }}}}}).hasToolLogObserver() {
		t.Fatal("Hooks without ToolLog must not enable tool logging")
	}
	if !(&hooks{observers: []Observer{hooksObserver{Hooks{ToolLog: func(ctx context.Context, _ ToolLogRecord) context.Context { return ctx }}}}}).hasToolLogObserver() {
		t.Fatal("Hooks with ToolLog must enable tool logging")
	}
}

func TestWithHooks_EmptyIsHarmless(t *testing.T) {
	a := hooksTestAgent(t, WithHooks(Hooks{}))
	if _, err := a.Invoke(Background(), "go"); err != nil {
		t.Fatal(err)
	}
}
