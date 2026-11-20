package agent

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/camilbinas/gude-agents/agent/tool"
	"sync"
	"testing"
)

type isoKey struct{}

func TestHooks_EndReceivesOwnStartContext(t *testing.T) {
	var got []string
	mk := func(name string) Hooks {
		return Hooks{Model: func(ctx context.Context, r ModelCallRecord) context.Context {
			v, _ := ctx.Value(isoKey{}).(string)
			if r.Phase == End {
				got = append(got, name+":"+v)
				return ctx
			}
			return context.WithValue(ctx, isoKey{}, name)
		}}
	}
	h := hooks{hooks: []Hooks{mk("a"), mk("b")}}
	_, f := h.onModelStart(Background(), ModelCallRecord{})
	f.finish(nil, TokenUsage{}, 0, "")
	if len(got) != 2 || got[0] != "b:b" || got[1] != "a:a" {
		t.Fatalf("End contexts = %v, want [b:b a:a]", got)
	}
}

func TestHooks_PanicDoesNotSkipEnd(t *testing.T) {
	ended := false
	h := hooks{hooks: []Hooks{
		{Invoke: func(ctx context.Context, r InvokeRecord) context.Context {
			if r.Phase == End {
				ended = true
			}
			return ctx
		}},
		{Invoke: func(ctx context.Context, r InvokeRecord) context.Context { panic("boom") }},
	}}
	_, f := h.onInvokeStart(Background(), InvokeRecord{})
	f.finish(Result{}, nil)
	if !ended {
		t.Fatal("End hook of the first hook did not run after a later hook panicked")
	}
}

func TestHooks_UnrelatedContextKeepsCancellation(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	h := hooks{hooks: []Hooks{{Invoke: func(ctx context.Context, r InvokeRecord) context.Context {
		return context.WithValue(context.Background(), isoKey{}, 1)
	}}}}
	c, _ := h.onInvokeStart(NewContext(parent), InvokeRecord{})
	if c.Err() != nil {
		t.Fatal("context cancelled too early")
	}
	cancel()
	<-c.Done()
	if !errors.Is(c.Err(), context.Canceled) {
		t.Fatalf("Err = %v, want Canceled", c.Err())
	}
}

func TestToolPanicBecomesToolErrorAndEndHookRuns(t *testing.T) {
	for _, mode := range []ToolExecutionMode{ToolExecutionSequential, ToolExecutionParallel} {
		var toolEnds, invokeEnds int
		var mu sync.Mutex
		boom := tool.NewRaw("boom", "boom", nil, func(context.Context, json.RawMessage) (string, error) {
			panic("kaboom")
		})
		ok := tool.NewRaw("ok", "ok", nil, func(context.Context, json.RawMessage) (string, error) { return "fine", nil })
		provider := &approvalBatchProvider{responses: []*ModelResponse{
			{ToolCalls: batchOf("boom", "ok")}, {Text: "done"},
		}}
		a, err := New(provider, "sys", WithTools(boom, ok), WithToolExecution(mode), WithHooks(Hooks{
			Tool: func(ctx context.Context, r ToolCallRecord) context.Context {
				if r.Phase == End {
					mu.Lock()
					toolEnds++
					mu.Unlock()
				}
				return ctx
			},
			Invoke: func(ctx context.Context, r InvokeRecord) context.Context {
				if r.Phase == End {
					invokeEnds++
				}
				return ctx
			},
		}))
		if err != nil {
			t.Fatal(err)
		}
		var events []Event
		for ev, err := range a.Stream(Background(), "go") {
			if err != nil {
				t.Fatalf("%v: stream error %v", mode, err)
			}
			events = append(events, ev)
		}
		if toolEnds != 2 || invokeEnds != 1 {
			t.Fatalf("%v: toolEnds=%d invokeEnds=%d", mode, toolEnds, invokeEnds)
		}
		found := false
		for _, ev := range events {
			if ev.Type == EventToolEnd && ev.Tool.Name == "boom" && ev.Tool.Error != nil && ev.Tool.Error.Code == ErrorCodeTool {
				found = true
			}
		}
		if !found {
			t.Fatalf("%v: no tool_end with ErrorCodeTool for the panicking tool", mode)
		}
	}
}
