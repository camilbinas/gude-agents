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

func TestWithHooks_MultipleRunInOrder(t *testing.T) {
	var log []string
	rec := func(name string) Hooks {
		return Hooks{Tool: func(ctx context.Context, r ToolCallRecord) context.Context {
			log = append(log, name+"-"+string(r.Phase))
			return ctx
		}}
	}
	a := hooksTestAgent(t, WithHooks(rec("a")), WithHooks(rec("b")))
	if _, err := a.Invoke(Background(), "go"); err != nil {
		t.Fatal(err)
	}
	want := []string{"a-start", "b-start", "b-end", "a-end"}
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
	if (&hooks{hooks: []Hooks{{Tool: func(ctx context.Context, _ ToolCallRecord) context.Context { return ctx }}}}).hasToolLogHook() {
		t.Fatal("Hooks without ToolLog must not enable tool logging")
	}
	if !(&hooks{hooks: []Hooks{{ToolLog: func(ctx context.Context, _ ToolLogRecord) context.Context { return ctx }}}}).hasToolLogHook() {
		t.Fatal("Hooks with ToolLog must enable tool logging")
	}
}

func TestWithHooks_EmptyIsHarmless(t *testing.T) {
	a := hooksTestAgent(t, WithHooks(Hooks{}))
	if _, err := a.Invoke(Background(), "go"); err != nil {
		t.Fatal(err)
	}
}
