package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// deleteOrderTool is a potentially destructive tool used in approval tests.
func deleteOrderTool() tool.Tool {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"order_id": map[string]any{"type": "string"},
		},
		"required": []string{"order_id"},
	}
	return tool.NewRaw(
		"delete_order",
		"Permanently deletes an order",
		func(_ context.Context, input json.RawMessage) (string, error) {
			var p struct {
				OrderID string `json:"order_id"`
			}
			json.Unmarshal(input, &p)
			return `{"deleted":true,"order_id":"` + p.OrderID + `"}`, nil
		},
		tool.WithSchema(schema),
		tool.RequiresApproval(),
	)
}

// mustInterrupt invokes the agent and asserts it paused with an interrupt.
func mustInterrupt(t *testing.T, a *Agent, c *Context, msg string) *Interrupt {
	t.Helper()
	res, err := a.Invoke(c, msg)
	if err != nil {
		t.Fatalf("Invoke: unexpected error %v", err)
	}
	if res.StopReason != StopInterrupt || res.Interrupt == nil {
		t.Fatalf("Invoke: StopReason=%q Interrupt=%v, want interrupt", res.StopReason, res.Interrupt)
	}
	return res.Interrupt
}

// TestRequiresApproval_InvokeReturnsInterrupt verifies that a call to a tool
// marked with RequiresApproval pauses the invocation and returns an approval
// interrupt with a nil error.
func TestRequiresApproval_InvokeReturnsInterrupt(t *testing.T) {
	provider := newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{{
			ToolUseID: "tc-1", Name: "delete_order", Input: json.RawMessage(`{"order_id":"ORD-99"}`),
		}}},
	)
	a, err := New(provider, "helpful", WithTools(deleteOrderTool()))
	if err != nil {
		t.Fatal(err)
	}

	in := mustInterrupt(t, a, Background(), "delete order 99")
	if in.Type != InterruptApproval || in.Approval == nil || in.Input != nil {
		t.Fatalf("interrupt = %+v, want approval", in)
	}
	if in.ID == "" {
		t.Error("interrupt ID must be set")
	}
	if len(in.Approval.Calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(in.Approval.Calls))
	}
	call := in.Approval.Calls[0]
	if call.Name != "delete_order" || call.CallID != "tc-1" {
		t.Errorf("call = %+v", call)
	}
	var input struct {
		OrderID string `json:"order_id"`
	}
	json.Unmarshal(call.Input, &input)
	if input.OrderID != "ORD-99" {
		t.Errorf("input.order_id = %q, want ORD-99", input.OrderID)
	}
	if len(in.Messages) == 0 {
		t.Error("expected non-empty message snapshot")
	}
	// The snapshot must not contain a result for the pending call (duplicate
	// IDs on resume are rejected by providers).
	for _, msg := range in.Messages {
		for _, block := range msg.Content {
			if tr, ok := block.(ToolResultBlock); ok && tr.ToolUseID == call.CallID {
				t.Fatalf("snapshot contains a result for pending call %q", call.CallID)
			}
		}
	}
}

// TestRequiresApproval_StreamEmitsSameInterrupt verifies that Stream emits
// EventInterrupt followed by EventEnd whose Result carries the same interrupt.
func TestRequiresApproval_StreamEmitsSameInterrupt(t *testing.T) {
	provider := newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{{
			ToolUseID: "tc-1", Name: "delete_order", Input: json.RawMessage(`{"order_id":"ORD-42"}`),
		}}},
	)
	a, err := New(provider, "helpful", WithTools(deleteOrderTool()))
	if err != nil {
		t.Fatal(err)
	}

	var evInterrupt *Interrupt
	var end *Result
	var types []EventType
	for ev, err := range a.Stream(Background(), "delete order 42") {
		if err != nil {
			t.Fatalf("stream error: %v", err)
		}
		types = append(types, ev.Type)
		switch ev.Type {
		case EventInterrupt:
			evInterrupt = ev.Interrupt
		case EventEnd:
			end = ev.Result
		}
	}
	if evInterrupt == nil || end == nil {
		t.Fatalf("missing interrupt or end event: %v", types)
	}
	if types[len(types)-2] != EventInterrupt || types[len(types)-1] != EventEnd {
		t.Fatalf("event order = %v, want ... interrupt, end", types)
	}
	if end.StopReason != StopInterrupt || end.Interrupt != evInterrupt {
		t.Fatalf("end result = %+v, want the same interrupt", end)
	}
	for _, typ := range types {
		if typ == EventToolStart || typ == EventToolEnd {
			t.Fatalf("pending approval must not emit tool events: %v", types)
		}
	}
}

// TestResume_ApproveRunsTool verifies that Approve runs the tool handler and
// the loop continues to a final response.
func TestResume_ApproveRunsTool(t *testing.T) {
	provider := newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{{
			ToolUseID: "tc-1", Name: "delete_order", Input: json.RawMessage(`{"order_id":"ORD-99"}`),
		}}},
		&ModelResponse{Text: "Order ORD-99 has been deleted."},
	)
	a, err := New(provider, "helpful", WithTools(deleteOrderTool()))
	if err != nil {
		t.Fatal(err)
	}
	in := mustInterrupt(t, a, Background(), "delete order 99")

	res, err := a.Resume(Background(), in, Approve())
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if res.Text != "Order ORD-99 has been deleted." || res.StopReason != StopEndTurn {
		t.Errorf("result = %+v", res)
	}
}

// TestResume_DenySkipsHandler verifies that Deny injects a denial result and
// the loop continues without running the handler.
func TestResume_DenySkipsHandler(t *testing.T) {
	handlerCalled := false
	dt := tool.NewRaw("delete_order", "Permanently deletes an order", func(_ context.Context, _ json.RawMessage) (string, error) {
		handlerCalled = true
		return `{"deleted":true}`, nil
	}, tool.RequiresApproval())
	provider := &approvalBatchProvider{responses: []*ModelResponse{
		{ToolCalls: []tool.Call{{ToolUseID: "tc-1", Name: "delete_order", Input: json.RawMessage(`{}`)}}},
		{Text: "I couldn't delete the order."},
	}}
	a, err := New(provider, "helpful", WithTools(dt))
	if err != nil {
		t.Fatal(err)
	}
	in := mustInterrupt(t, a, Background(), "delete order 99")

	var ends []ToolEvent
	var res *Result
	for ev, err := range a.ResumeStream(Background(), in, Deny("access denied by admin")) {
		if err != nil {
			t.Fatalf("ResumeStream: %v", err)
		}
		if ev.Type == EventToolEnd {
			ends = append(ends, *ev.Tool)
		}
		if ev.Type == EventEnd {
			res = ev.Result
		}
	}
	if handlerCalled {
		t.Error("handler should not have been called on denial")
	}
	if res == nil || res.Text != "I couldn't delete the order." {
		t.Errorf("result = %+v", res)
	}
	if len(ends) != 1 || ends[0].CallID != "tc-1" || ends[0].Error == nil || ends[0].Error.Code != ErrorCodeToolDenied {
		t.Errorf("tool_end events = %+v, want one denied event for tc-1", ends)
	}
	msgs := provider.params[1].Messages
	last := msgs[len(msgs)-1]
	tr, ok := last.Content[0].(ToolResultBlock)
	if !ok || !tr.IsError || tr.ToolUseID != "tc-1" {
		t.Fatalf("resumed tool result = %#v, want denial for tc-1", last.Content)
	}
}

// TestRequiresApproval_NormalToolUnaffected verifies that tools without
// RequiresApproval still execute normally.
func TestRequiresApproval_NormalToolUnaffected(t *testing.T) {
	normalTool := tool.NewRaw("get_info", "Gets some info", func(_ context.Context, _ json.RawMessage) (string, error) { return `{"info":"ok"}`, nil })
	provider := newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "tc-1", Name: "get_info", Input: json.RawMessage(`{}`)}}},
		&ModelResponse{Text: "Here is the info."},
	)
	a, err := New(provider, "helpful", WithTools(normalTool))
	if err != nil {
		t.Fatal(err)
	}
	result, err := a.Invoke(Background(), "get info")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Text != "Here is the info." || result.Interrupt != nil {
		t.Errorf("result = %+v", result)
	}
}

func TestResume_RerunsExecutionChecks(t *testing.T) {
	t.Run("schema", func(t *testing.T) {
		handlerCalled := false
		strictTool := tool.NewRaw("strict", "requires an id", func(_ context.Context, _ json.RawMessage) (string, error) {
			handlerCalled = true
			return "ok", nil
		}, tool.WithSchema(map[string]any{
			"type":       "object",
			"required":   []string{"id"},
			"properties": map[string]any{"id": map[string]any{"type": "string"}},
		}), tool.RequiresApproval())
		provider := newScriptedProvider(
			&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "schema-1", Name: "strict", Input: json.RawMessage(`{"id":"original"}`)}}},
			&ModelResponse{Text: "schema rejected"},
		)
		store := newTestInterruptStore()
		a, err := New(provider, "helpful", WithTools(strictTool), WithInterruptStore(store))
		if err != nil {
			t.Fatal(err)
		}
		in := mustInterrupt(t, a, Background(), "run")
		store.mu.Lock()
		store.items[in.ID].Approval.Calls[0].Input = json.RawMessage(`{}`)
		store.mu.Unlock()
		if _, err := a.Resume(Background(), in, Approve()); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		if handlerCalled {
			t.Fatal("handler ran despite invalid approved input")
		}
	})

	t.Run("role", func(t *testing.T) {
		handlerCalled := false
		restricted := tool.NewRaw("restricted", "admin only", func(_ context.Context, _ json.RawMessage) (string, error) {
			handlerCalled = true
			return "secret", nil
		}, tool.RequiresApproval(), tool.AllowRoles("admin"))
		provider := newScriptedProvider(
			&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "role-1", Name: "restricted", Input: json.RawMessage(`{}`)}}},
			&ModelResponse{Text: "role rejected"},
		)
		a, err := New(provider, "helpful", WithTools(restricted))
		if err != nil {
			t.Fatal(err)
		}
		in := mustInterrupt(t, a, Background().WithPrincipal(Principal{ID: "admin", Roles: []string{"admin"}}), "run")
		guest := Background().WithPrincipal(Principal{ID: "guest", Roles: []string{"guest"}})
		if _, err := a.Resume(guest, in, Approve()); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		if handlerCalled {
			t.Fatal("handler ran after approved caller lost the required role")
		}
	})

	t.Run("guard and middleware", func(t *testing.T) {
		var calls []string
		guarded := tool.NewRaw("guarded", "guarded tool", func(_ context.Context, _ json.RawMessage) (string, error) {
			calls = append(calls, "handler")
			return "ok", nil
		}, tool.RequiresApproval(), tool.WithGuard(func(_ context.Context, _ json.RawMessage) (tool.Decision, error) {
			calls = append(calls, "guard")
			return tool.Allow(), nil
		}))
		middleware := func(next ToolHandlerFunc) ToolHandlerFunc {
			return func(ctx context.Context, call ToolCall) (ToolResult, error) {
				calls = append(calls, "before middleware")
				out, err := next(ctx, call)
				calls = append(calls, "after middleware")
				return out, err
			}
		}
		provider := newScriptedProvider(
			&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "guard-1", Name: "guarded", Input: json.RawMessage(`{}`)}}},
			&ModelResponse{Text: "done"},
		)
		a, err := New(provider, "helpful", WithTools(guarded), WithMiddleware(middleware))
		if err != nil {
			t.Fatal(err)
		}
		in := mustInterrupt(t, a, Background(), "run")
		// The guard runs before the approval gate on the first pass.
		if len(calls) != 1 || calls[0] != "guard" {
			t.Fatalf("pre-approval calls = %v, want [guard]", calls)
		}
		calls = nil
		if _, err := a.Resume(Background(), in, Approve()); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		want := []string{"guard", "before middleware", "handler", "after middleware"}
		if len(calls) != len(want) {
			t.Fatalf("calls = %v, want %v", calls, want)
		}
		for i := range want {
			if calls[i] != want[i] {
				t.Fatalf("calls = %v, want %v", calls, want)
			}
		}
	})
}

func TestResume_RichHandlerOnlyTool(t *testing.T) {
	richCalled := false
	richTool := tool.NewRich[json.RawMessage]("screenshot", "captures a screenshot", func(_ context.Context, _ json.RawMessage) (*tool.Output, error) {
		richCalled = true
		return &tool.Output{Text: "screenshot captured", Images: []tool.Image{{Base64: "aW1hZ2U=", MIMEType: "image/png"}}}, nil
	}, tool.WithSchema(map[string]any{"type": "object"}), tool.RequiresApproval())
	provider := newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "rich-1", Name: "screenshot", Input: json.RawMessage(`{}`)}}},
		&ModelResponse{Text: "I reviewed the screenshot."},
	)
	a, err := New(provider, "helpful", WithTools(richTool))
	if err != nil {
		t.Fatal(err)
	}
	in := mustInterrupt(t, a, Background(), "take a screenshot")
	result, err := a.Resume(Background(), in, Approve())
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if !richCalled {
		t.Fatal("rich handler was not called")
	}
	if result.Text != "I reviewed the screenshot." {
		t.Errorf("result = %q", result.Text)
	}
}

type approvalBatchProvider struct {
	mu        sync.Mutex
	responses []*ModelResponse
	params    []ModelRequest
}

func (p *approvalBatchProvider) Name() string { return "approval-batch" }

func (p *approvalBatchProvider) Stream(_ context.Context, params ModelRequest, cb func(ModelEvent)) (*ModelResponse, error) {
	response, err := p.next(params)
	if err != nil {
		return nil, err
	}
	if response.Text != "" && cb != nil {
		cb(ModelEvent{Type: ModelEventText, Text: response.Text})
	}
	return response, nil
}

func (p *approvalBatchProvider) next(params ModelRequest) (*ModelResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.params = append(p.params, params)
	if len(p.responses) == 0 {
		return nil, errors.New("approval batch provider: no response")
	}
	response := p.responses[0]
	p.responses = p.responses[1:]
	return response, nil
}

// TestResume_DecideBatchOrderedSequentialAndParallel covers a two-call batch:
// a completed normal call plus two approval calls, resumed with Decide.
func TestResume_DecideBatchOrderedSequentialAndParallel(t *testing.T) {
	for _, parallel := range []bool{false, true} {
		t.Run(map[bool]string{false: "sequential", true: "parallel"}[parallel], func(t *testing.T) {
			provider := &approvalBatchProvider{responses: []*ModelResponse{
				{ToolCalls: []tool.Call{
					{ToolUseID: "allow-1", Name: "allowed", Input: json.RawMessage(`{"value":"first"}`)},
					{ToolUseID: "free-2", Name: "free", Input: json.RawMessage(`{}`)},
					{ToolUseID: "deny-3", Name: "denied", Input: json.RawMessage(`{"value":"second"}`)},
				}},
				{Text: "batch complete"},
			}}
			var mu sync.Mutex
			handlers := make([]string, 0, 3)
			record := func(name, out string) func(context.Context, json.RawMessage) (string, error) {
				return func(context.Context, json.RawMessage) (string, error) {
					mu.Lock()
					defer mu.Unlock()
					handlers = append(handlers, name)
					return out, nil
				}
			}
			allowed := tool.NewRaw("allowed", "allowed", record("allowed", "allowed result"), tool.RequiresApproval())
			free := tool.NewRaw("free", "free", record("free", "free result"))
			denied := tool.NewRaw("denied", "denied", record("denied", "denied result"), tool.RequiresApproval())
			opts := []Option{WithTools(allowed, free, denied)}
			if !parallel {
				opts = append(opts, WithSequentialTools())
			}
			a, err := New(provider, "helpful", opts...)
			if err != nil {
				t.Fatal(err)
			}

			in := mustInterrupt(t, a, Background(), "run all")
			calls := in.Approval.Calls
			if len(calls) != 2 || calls[0].CallID != "allow-1" || calls[1].CallID != "deny-3" {
				t.Fatalf("approval calls = %#v, want provider order", calls)
			}

			result, err := a.Resume(Background(), in, Decide(map[string]tool.Decision{
				"allow-1": tool.Allow(),
				"deny-3":  tool.Deny("human denied"),
			}))
			if err != nil {
				t.Fatalf("Resume: %v", err)
			}
			if result.Text != "batch complete" {
				t.Fatalf("result = %q, want batch complete", result.Text)
			}
			mu.Lock()
			got := append([]string(nil), handlers...)
			mu.Unlock()
			if len(got) != 2 || got[0] != "free" || got[1] != "allowed" {
				t.Fatalf("handlers = %v, want [free allowed]", got)
			}

			provider.mu.Lock()
			if len(provider.params) != 2 {
				provider.mu.Unlock()
				t.Fatalf("provider calls = %d, want 2", len(provider.params))
			}
			messages := provider.params[1].Messages
			provider.mu.Unlock()
			last := messages[len(messages)-1]
			if last.Role != RoleUser || len(last.Content) != 3 {
				t.Fatalf("resumed result message = %#v, want one three-result user message", last)
			}
			wantIDs := []string{"allow-1", "free-2", "deny-3"}
			for i, id := range wantIDs {
				tr, ok := last.Content[i].(ToolResultBlock)
				if !ok || tr.ToolUseID != id {
					t.Fatalf("result %d = %#v, want %s", i, last.Content[i], id)
				}
			}
			if tr := last.Content[2].(ToolResultBlock); !tr.IsError {
				t.Fatalf("denied result must be an error: %#v", tr)
			}
		})
	}
}

// TestResume_DenyAppliesToEveryCallInBatch verifies that Deny denies every
// pending call of a multi-call approval interrupt, runs no handler, and
// reports one denied tool_end per call (in provider order) before the loop
// continues.
func TestResume_DenyAppliesToEveryCallInBatch(t *testing.T) {
	for _, parallel := range []bool{false, true} {
		t.Run(map[bool]string{false: "sequential", true: "parallel"}[parallel], func(t *testing.T) {
			var handlerCalls atomic.Int32
			mk := func(name string) tool.Tool {
				return tool.NewRaw(name, name, func(context.Context, json.RawMessage) (string, error) {
					handlerCalls.Add(1)
					return "ran", nil
				}, tool.RequiresApproval())
			}
			provider := &approvalBatchProvider{responses: []*ModelResponse{
				{ToolCalls: []tool.Call{
					{ToolUseID: "d-1", Name: "one", Input: json.RawMessage(`{}`)},
					{ToolUseID: "d-2", Name: "two", Input: json.RawMessage(`{}`)},
				}},
				{Text: "nothing was done"},
			}}
			opts := []Option{WithTools(mk("one"), mk("two"))}
			if !parallel {
				opts = append(opts, WithSequentialTools())
			}
			a, err := New(provider, "helpful", opts...)
			if err != nil {
				t.Fatal(err)
			}
			in := mustInterrupt(t, a, Background(), "run both")
			if len(in.Approval.Calls) != 2 {
				t.Fatalf("approval calls = %#v, want 2", in.Approval.Calls)
			}

			var denied []string
			var res *Result
			for ev, err := range a.ResumeStream(Background(), in, Deny("not today")) {
				if err != nil {
					t.Fatalf("ResumeStream: %v", err)
				}
				switch ev.Type {
				case EventToolEnd:
					if ev.Tool.Error == nil || ev.Tool.Error.Code != ErrorCodeToolDenied {
						t.Fatalf("tool_end = %+v, want denied", ev.Tool)
					}
					denied = append(denied, ev.Tool.CallID)
				case EventEnd:
					res = ev.Result
				}
			}
			if handlerCalls.Load() != 0 {
				t.Fatalf("handlers ran %d times on Deny", handlerCalls.Load())
			}
			if len(denied) != 2 {
				t.Fatalf("denied tool_end events = %v, want 2", denied)
			}
			if res == nil || res.Text != "nothing was done" || res.StopReason != StopEndTurn {
				t.Fatalf("result = %+v", res)
			}
			provider.mu.Lock()
			msgs := provider.params[1].Messages
			provider.mu.Unlock()
			last := msgs[len(msgs)-1]
			if len(last.Content) != 2 {
				t.Fatalf("resumed results = %#v, want 2", last.Content)
			}
			for i, id := range []string{"d-1", "d-2"} {
				tr, ok := last.Content[i].(ToolResultBlock)
				if !ok || tr.ToolUseID != id || !tr.IsError || !strings.Contains(tr.Content, "not today") {
					t.Fatalf("result %d = %#v, want denial for %s", i, last.Content[i], id)
				}
			}
		})
	}
}

// TestResume_InvalidResponsesRunNoHandlers verifies that the response is
// validated before any handler runs.
func TestResume_InvalidResponsesRunNoHandlers(t *testing.T) {
	called := 0
	makeTool := func(name string) tool.Tool {
		return tool.NewRaw(name, name, func(_ context.Context, _ json.RawMessage) (string, error) {
			called++
			return "unexpected", nil
		}, tool.RequiresApproval())
	}
	provider := &approvalBatchProvider{}
	a, err := New(provider, "helpful", WithTools(makeTool("first"), makeTool("second")))
	if err != nil {
		t.Fatal(err)
	}
	in := &Interrupt{ID: "i-1", Type: InterruptApproval, Approval: &ApprovalInterrupt{Calls: []ApprovalCall{
		{Name: "first", CallID: "first-id", Input: json.RawMessage(`{}`)},
		{Name: "second", CallID: "second-id", Input: json.RawMessage(`{}`)},
	}}}
	for name, resp := range map[string]ResumeResponse{
		"missing": Decide(map[string]tool.Decision{"first-id": tool.Allow()}),
		"extra":   Decide(map[string]tool.Decision{"first-id": tool.Allow(), "second-id": tool.Allow(), "extra": tool.Allow()}),
		"respond": Respond("hello"),
		"empty":   {},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := a.Resume(Background(), in, resp); err == nil {
				t.Fatal("expected validation error")
			}
			if called != 0 {
				t.Fatalf("handler count = %d, want 0", called)
			}
		})
	}
	if len(provider.params) != 0 {
		t.Fatalf("provider called %d times, want 0", len(provider.params))
	}
	human := &Interrupt{ID: "i-2", Type: InterruptHumanInput, Input: &InputInterrupt{Question: "?"}}
	if _, err := a.Resume(Background(), human, Approve()); err == nil {
		t.Fatal("Approve on a human_input interrupt must fail")
	}
}

func TestResume_InvalidResponseDoesNotConsumeInterrupt(t *testing.T) {
	var handlerCalls atomic.Int32
	approved := tool.NewRaw("approved", "approved", func(context.Context, json.RawMessage) (string, error) {
		handlerCalls.Add(1)
		return "ok", nil
	}, tool.RequiresApproval())
	provider := newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "tc-1", Name: "approved", Input: json.RawMessage(`{}`)}}},
		&ModelResponse{Text: "done"},
	)
	a, err := New(provider, "helpful", WithTools(approved))
	if err != nil {
		t.Fatal(err)
	}
	in := mustInterrupt(t, a, Background(), "run")

	if _, err := a.Resume(Background(), in, Respond("wrong response kind")); err == nil {
		t.Fatal("invalid response succeeded")
	}
	if handlerCalls.Load() != 0 {
		t.Fatal("invalid response consumed or executed the interrupt")
	}
	if _, err := a.Resume(Background(), in, Approve()); err != nil {
		t.Fatalf("valid retry: %v", err)
	}
	if handlerCalls.Load() != 1 {
		t.Fatalf("handler calls = %d, want 1", handlerCalls.Load())
	}
}

func TestResume_StatelessApprovalReplayHasOneWinner(t *testing.T) {
	newAgent := func(t *testing.T) (*Agent, *Interrupt, *atomic.Int32, *scriptedProvider) {
		t.Helper()
		var handlerCalls atomic.Int32
		approved := tool.NewRaw("approved", "approved", func(context.Context, json.RawMessage) (string, error) {
			handlerCalls.Add(1)
			return "ok", nil
		}, tool.RequiresApproval())
		provider := newScriptedProvider(
			&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "tc-1", Name: "approved", Input: json.RawMessage(`{}`)}}},
			&ModelResponse{Text: "done"},
		)
		a, err := New(provider, "helpful", WithTools(approved))
		if err != nil {
			t.Fatal(err)
		}
		return a, mustInterrupt(t, a, Background(), "run"), &handlerCalls, provider
	}

	t.Run("sequential", func(t *testing.T) {
		a, in, handlerCalls, provider := newAgent(t)
		if _, err := a.Resume(Background(), in, Approve()); err != nil {
			t.Fatalf("first resume: %v", err)
		}
		if _, err := a.Resume(Background(), in, Approve()); !errors.Is(err, ErrInterruptNotFound) {
			t.Fatalf("replay err = %v, want ErrInterruptNotFound", err)
		}
		if handlerCalls.Load() != 1 {
			t.Fatalf("handler calls = %d, want 1", handlerCalls.Load())
		}
		provider.mu.Lock()
		defer provider.mu.Unlock()
		if provider.callIndex != 2 {
			t.Fatalf("provider calls = %d, want 2", provider.callIndex)
		}
	})

	t.Run("concurrent", func(t *testing.T) {
		a, in, handlerCalls, provider := newAgent(t)
		const contenders = 32
		var winners atomic.Int32
		errs := make(chan error, contenders)
		var wg sync.WaitGroup
		wg.Add(contenders)
		for range contenders {
			go func() {
				defer wg.Done()
				_, err := a.Resume(Background(), in, Approve())
				if err == nil {
					winners.Add(1)
					return
				}
				if !errors.Is(err, ErrInterruptNotFound) {
					errs <- err
				}
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Errorf("resume: %v", err)
		}
		if winners.Load() != 1 {
			t.Fatalf("winners = %d, want 1", winners.Load())
		}
		if handlerCalls.Load() != 1 {
			t.Fatalf("handler calls = %d, want 1", handlerCalls.Load())
		}
		provider.mu.Lock()
		defer provider.mu.Unlock()
		if provider.callIndex != 2 {
			t.Fatalf("provider calls = %d, want 2", provider.callIndex)
		}
	})
}

// ---------------------------------------------------------------------------
// InterruptStore
// ---------------------------------------------------------------------------

type testInterruptStore struct {
	mu        sync.Mutex
	items     map[string]*Interrupt
	saveErr   error
	saveErrAt int
	saveCalls int
	claimErr  error
	claimed   []string
}

func newTestInterruptStore() *testInterruptStore {
	return &testInterruptStore{items: map[string]*Interrupt{}}
}

func (s *testInterruptStore) Save(_ context.Context, in *Interrupt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saveCalls++
	if s.saveErr != nil && (s.saveErrAt == 0 || s.saveCalls == s.saveErrAt) {
		return s.saveErr
	}
	if _, exists := s.items[in.ID]; exists {
		return fmt.Errorf("interrupt %q already exists", in.ID)
	}
	s.items[in.ID] = cloneInterrupt(in)
	return nil
}

func (s *testInterruptStore) Load(_ context.Context, id string) (*Interrupt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	in, ok := s.items[id]
	if !ok {
		return nil, ErrInterruptNotFound
	}
	return cloneInterrupt(in), nil
}

func (s *testInterruptStore) Claim(_ context.Context, id string) (*Interrupt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.claimErr != nil {
		return nil, s.claimErr
	}
	in, ok := s.items[id]
	if !ok {
		return nil, ErrInterruptNotFound
	}
	delete(s.items, id)
	s.claimed = append(s.claimed, id)
	return cloneInterrupt(in), nil
}

func TestInterruptStore_SaveLoadResumeClaim(t *testing.T) {
	store := newTestInterruptStore()
	provider := newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "tc-1", Name: "delete_order", Input: json.RawMessage(`{"order_id":"A"}`)}}},
		&ModelResponse{Text: "deleted"},
	)
	a, err := New(provider, "helpful", WithTools(deleteOrderTool()),
		WithConversationStore(newMemConversation()), WithInterruptStore(store))
	if err != nil {
		t.Fatal(err)
	}
	in := mustInterrupt(t, a, Background().WithConversationID("conv-1"), "delete A")
	if in.ConversationID != "conv-1" {
		t.Fatalf("ConversationID = %q, want conv-1", in.ConversationID)
	}
	if in.Revision != 1 {
		t.Fatalf("Revision = %d, want committed revision 1", in.Revision)
	}

	loaded, err := a.LoadInterrupt(context.Background(), in.ID)
	if err != nil {
		t.Fatalf("LoadInterrupt: %v", err)
	}
	if loaded.ID != in.ID || loaded.Revision != in.Revision {
		t.Fatalf("loaded interrupt = %#v, want ID %q revision %d", loaded, in.ID, in.Revision)
	}
	res, err := a.Resume(Background(), loaded, Approve())
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if res.Text != "deleted" {
		t.Fatalf("result = %q", res.Text)
	}
	if _, err := a.LoadInterrupt(context.Background(), in.ID); !errors.Is(err, ErrInterruptNotFound) {
		t.Fatalf("after resume LoadInterrupt err = %v, want ErrInterruptNotFound", err)
	}
}

func TestInterruptStore_Errors(t *testing.T) {
	pending := &ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "tc-1", Name: "delete_order", Input: json.RawMessage(`{"order_id":"A"}`)}}}

	collectPause := func(t *testing.T, a *Agent, ctx *Context) (Result, *Interrupt, error) {
		t.Helper()
		var result Result
		var interrupt *Interrupt
		var gotErr error
		for ev, err := range a.Stream(ctx, "delete") {
			if ev.Type == EventInterrupt {
				interrupt = ev.Interrupt
			}
			if ev.Type == EventEnd && ev.Result != nil {
				result = *ev.Result
			}
			if err != nil {
				gotErr = err
			}
		}
		return result, interrupt, gotErr
	}

	t.Run("interrupt save error exposes committed pause", func(t *testing.T) {
		conv := &failingSaveConversation{}
		store := newTestInterruptStore()
		store.saveErr = errors.New("disk full")
		observer := &recordingInterruptObserver{}
		a, err := New(newScriptedProvider(pending), "helpful", WithTools(deleteOrderTool()),
			WithConversationStore(conv), WithInterruptStore(store), WithObserver(observer))
		if err != nil {
			t.Fatal(err)
		}
		res, err := a.Invoke(Background().WithConversationID("c"), "delete")
		if !errors.Is(err, store.saveErr) {
			t.Fatalf("err = %v, want save error", err)
		}
		if res.StopReason != StopInterrupt || res.Interrupt == nil {
			t.Fatalf("result interrupt = %+v", res)
		}
		if res.Interrupt.Revision != 1 {
			t.Fatalf("interrupt revision = %d, want 1", res.Interrupt.Revision)
		}
		if observer.count() != 1 {
			t.Fatalf("interrupt observations = %d, want 1", observer.count())
		}
	})

	t.Run("flush error exposes committed pause before interrupt save", func(t *testing.T) {
		conv := &failingSaveConversation{flushErr: errors.New("flush failed")}
		store := newTestInterruptStore()
		observer := &recordingInterruptObserver{}
		a, err := New(newScriptedProvider(pending), "helpful", WithTools(deleteOrderTool()),
			WithConversationStore(conv), WithSyncConversation(), WithInterruptStore(store), WithObserver(observer))
		if err != nil {
			t.Fatal(err)
		}
		res, eventInterrupt, err := collectPause(t, a, Background().WithConversationID("c"))
		if !errors.Is(err, conv.flushErr) {
			t.Fatalf("err = %v, want flush error", err)
		}
		if res.StopReason != StopInterrupt || res.Interrupt == nil || eventInterrupt != res.Interrupt {
			t.Fatalf("result/event interrupt = %+v / %+v", res, eventInterrupt)
		}
		if res.Interrupt.Revision != 1 || observer.count() != 1 {
			t.Fatalf("interrupt = %+v observations = %d", res.Interrupt, observer.count())
		}
		store.mu.Lock()
		defer store.mu.Unlock()
		if store.saveCalls != 1 {
			t.Fatalf("interrupt store saves = %d, want 1 after committed flush failure", store.saveCalls)
		}
	})

	t.Run("conversation save error suppresses pause", func(t *testing.T) {
		conv := &failingSaveConversation{saveErr: errors.New("save failed")}
		store := newTestInterruptStore()
		observer := &recordingInterruptObserver{}
		a, err := New(newScriptedProvider(pending), "helpful", WithTools(deleteOrderTool()),
			WithConversationStore(conv), WithInterruptStore(store), WithObserver(observer))
		if err != nil {
			t.Fatal(err)
		}
		res, eventInterrupt, err := collectPause(t, a, Background().WithConversationID("c"))
		if !errors.Is(err, conv.saveErr) {
			t.Fatalf("err = %v, want conversation save error", err)
		}
		if res.Interrupt != nil || eventInterrupt != nil || observer.count() != 0 {
			t.Fatalf("pause escaped failed commit: result=%+v event=%+v observations=%d", res, eventInterrupt, observer.count())
		}
		store.mu.Lock()
		defer store.mu.Unlock()
		if store.saveCalls != 0 {
			t.Fatalf("interrupt store saves = %d, want 0", store.saveCalls)
		}
	})

	t.Run("claim error prevents execution", func(t *testing.T) {
		store := newTestInterruptStore()
		provider := newScriptedProvider(pending, &ModelResponse{Text: "done"})
		a, err := New(provider, "helpful", WithTools(deleteOrderTool()), WithInterruptStore(store))
		if err != nil {
			t.Fatal(err)
		}
		in := mustInterrupt(t, a, Background(), "delete")
		store.claimErr = errors.New("claim failed")
		res, err := a.Resume(Background(), in, Approve())
		if !errors.Is(err, store.claimErr) {
			t.Fatalf("err = %v, want claim error", err)
		}
		if !reflect.DeepEqual(res, Result{}) {
			t.Fatalf("result = %+v, want zero result", res)
		}
		provider.mu.Lock()
		defer provider.mu.Unlock()
		if provider.callIndex != 1 {
			t.Fatalf("provider calls = %d, want 1", provider.callIndex)
		}
	})

	t.Run("no store", func(t *testing.T) {
		a, err := New(newScriptedProvider(), "helpful")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.LoadInterrupt(context.Background(), "x"); !errors.Is(err, ErrNoInterruptStore) {
			t.Fatalf("err = %v, want ErrNoInterruptStore", err)
		}
		if _, err := New(newScriptedProvider(), "h", WithInterruptStore(nil)); err == nil {
			t.Fatal("WithInterruptStore(nil) must fail")
		}
	})
}

type failingSaveConversation struct {
	mu       sync.Mutex
	loadErr  error
	loads    int
	snapshot ConversationSnapshot
	saveErr  error
	flushErr error
	saves    int
	flushes  int
}

func (f *failingSaveConversation) Load(context.Context, string) (ConversationSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loads++
	if f.loadErr != nil {
		return ConversationSnapshot{}, f.loadErr
	}
	return ConversationSnapshot{
		Messages: append([]Message(nil), f.snapshot.Messages...),
		Revision: f.snapshot.Revision,
	}, nil
}

func (f *failingSaveConversation) Save(_ context.Context, _ string, messages []Message, expectedRevision uint64) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.saveErr != nil {
		return f.snapshot.Revision, f.saveErr
	}
	if expectedRevision != f.snapshot.Revision {
		return f.snapshot.Revision, ErrConversationConflict
	}
	f.snapshot.Messages = append([]Message(nil), messages...)
	f.snapshot.Revision++
	f.saves++
	return f.snapshot.Revision, nil
}

func (f *failingSaveConversation) Flush(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.flushes++
	return f.flushErr
}

func (f *failingSaveConversation) List(context.Context) ([]string, error) { return nil, nil }
func (f *failingSaveConversation) Delete(context.Context, string) error   { return nil }

type recordingInterruptObserver struct {
	mu      sync.Mutex
	records []InterruptRecord
}

func (o *recordingInterruptObserver) ObserveInterrupt(ctx context.Context, record InterruptRecord) context.Context {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.records = append(o.records, record)
	return ctx
}

func (o *recordingInterruptObserver) count() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.records)
}

func TestResume_StatelessInterruptIgnoresResumeContextConversation(t *testing.T) {
	store := newMemConversation()
	seeded := []Message{{Role: RoleUser, Content: []ContentBlock{TextBlock{Text: "persisted other conversation"}}}}
	if _, err := store.Save(context.Background(), "other", seeded, 0); err != nil {
		t.Fatal(err)
	}
	provider := &approvalBatchProvider{responses: []*ModelResponse{
		{ToolCalls: []tool.Call{{ToolUseID: "tc-1", Name: "delete_order", Input: json.RawMessage(`{"order_id":"A"}`)}}},
		{Text: "done"},
	}}
	a, err := New(provider, "helpful", WithTools(deleteOrderTool()), WithConversationStore(store))
	if err != nil {
		t.Fatal(err)
	}
	in := mustInterrupt(t, a, Background(), "delete A")
	if in.ConversationID != "" || in.Revision != 0 {
		t.Fatalf("stateless interrupt = %+v", in)
	}

	res, err := a.Resume(Background().WithConversationID("other"), in, Approve())
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "done" {
		t.Fatalf("result.Text = %q, want done", res.Text)
	}
	provider.mu.Lock()
	resumedMessages := append([]Message(nil), provider.params[1].Messages...)
	provider.mu.Unlock()
	for _, msg := range resumedMessages {
		for _, block := range msg.Content {
			if text, ok := block.(TextBlock); ok && text.Text == "persisted other conversation" {
				t.Fatal("resume loaded the caller context conversation instead of using the interrupt snapshot")
			}
		}
	}
	snapshot, err := store.Load(context.Background(), "other")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Revision != 1 || !reflect.DeepEqual(snapshot.Messages, seeded) {
		t.Fatalf("stateless resume modified other conversation: %+v", snapshot)
	}
	store.mu.Lock()
	_, emptySaved := store.data[""]
	store.mu.Unlock()
	if emptySaved {
		t.Fatal("stateless pause or resume persisted an empty conversation ID")
	}
}

func TestResume_RejectsSameMessagesAtNewerRevision(t *testing.T) {
	var handlerCalls atomic.Int32
	approved := tool.NewRaw("approved", "approved", func(context.Context, json.RawMessage) (string, error) {
		handlerCalls.Add(1)
		return "ran", nil
	}, tool.RequiresApproval())
	provider := &approvalBatchProvider{responses: []*ModelResponse{
		{ToolCalls: []tool.Call{{ToolUseID: "tc-1", Name: "approved", Input: json.RawMessage(`{}`)}}},
		{Text: "should not run"},
	}}
	store := newMemConversation()
	a, err := New(provider, "helpful", WithTools(approved), WithConversationStore(store))
	if err != nil {
		t.Fatal(err)
	}
	ctx := Background().WithConversationID("conv")
	in := mustInterrupt(t, a, ctx, "run")
	if _, err := store.Save(context.Background(), "conv", in.Messages, in.Revision); err != nil {
		t.Fatal(err)
	}

	if _, err := a.Resume(ctx, in, Approve()); !errors.Is(err, ErrConversationConflict) {
		t.Fatalf("Resume error = %v, want ErrConversationConflict", err)
	}
	if _, err := a.Resume(ctx, in, Approve()); !errors.Is(err, ErrInterruptNotFound) {
		t.Fatalf("retry error = %v, want ErrInterruptNotFound", err)
	}
	if handlerCalls.Load() != 0 {
		t.Fatal("handler ran before revision validation")
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.params) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(provider.params))
	}
}

func TestResume_ChainedInterruptConsumesPredecessorBeforeReplacement(t *testing.T) {
	makeAgent := func(t *testing.T, store *testInterruptStore) (*Agent, *failingSaveConversation) {
		t.Helper()
		ask := tool.NewRaw("needs_human", "needs human", func(ctx context.Context, _ json.RawMessage) (string, error) {
			callCtx := FromContext(ctx)
			callCtx.call.setHumanInput(&InputInterrupt{Reason: "review", Question: "continue?"})
			return humanInputPausedResult, nil
		}, tool.RequiresApproval())
		provider := newScriptedProvider(&ModelResponse{ToolCalls: []tool.Call{{
			ToolUseID: "tc-1", Name: "needs_human", Input: json.RawMessage(`{}`),
		}}})
		conv := &failingSaveConversation{}
		a, err := New(provider, "helpful", WithTools(ask), WithConversationStore(conv), WithInterruptStore(store))
		if err != nil {
			t.Fatal(err)
		}
		return a, conv
	}

	t.Run("success", func(t *testing.T) {
		store := newTestInterruptStore()
		a, _ := makeAgent(t, store)
		old := mustInterrupt(t, a, Background().WithConversationID("conv"), "run")
		res, err := a.Resume(Background(), old, Approve())
		if err != nil {
			t.Fatal(err)
		}
		if res.StopReason != StopInterrupt || res.Interrupt == nil || res.Interrupt.Type != InterruptHumanInput {
			t.Fatalf("replacement result = %+v", res)
		}
		if res.Interrupt.ID == old.ID || res.Interrupt.Revision != 2 {
			t.Fatalf("replacement interrupt = %+v, predecessor = %+v", res.Interrupt, old)
		}
		store.mu.Lock()
		defer store.mu.Unlock()
		if _, ok := store.items[res.Interrupt.ID]; !ok {
			t.Fatal("replacement was not persisted")
		}
		if _, ok := store.items[old.ID]; ok {
			t.Fatal("predecessor remained after replacement persistence")
		}
		if len(store.claimed) != 1 || store.claimed[0] != old.ID {
			t.Fatalf("claimed = %v, want [%s]", store.claimed, old.ID)
		}
	})

	t.Run("replacement save failure does not resurrect predecessor", func(t *testing.T) {
		store := newTestInterruptStore()
		store.saveErr = errors.New("replacement save failed")
		store.saveErrAt = 2
		a, _ := makeAgent(t, store)
		old := mustInterrupt(t, a, Background().WithConversationID("conv"), "run")
		res, err := a.Resume(Background(), old, Approve())
		if !errors.Is(err, store.saveErr) {
			t.Fatalf("Resume error = %v, want replacement save error", err)
		}
		if res.StopReason != StopInterrupt || res.Interrupt == nil || res.Interrupt.Revision != 2 {
			t.Fatalf("replacement result = %+v", res)
		}
		store.mu.Lock()
		defer store.mu.Unlock()
		if _, ok := store.items[old.ID]; ok {
			t.Fatal("claimed predecessor was resurrected after replacement save failure")
		}
		if _, ok := store.items[res.Interrupt.ID]; ok {
			t.Fatal("failed replacement unexpectedly persisted")
		}
		if len(store.claimed) != 1 || store.claimed[0] != old.ID {
			t.Fatalf("claimed = %v, want [%s]", store.claimed, old.ID)
		}
	})
}

func TestResume_CanonicalMiddlewarePipelineSupportsEveryToolKind(t *testing.T) {
	objectSchema := tool.WithSchema(map[string]any{"type": "object"})
	tests := []struct {
		name  string
		build func(chan struct{}) tool.Tool
	}{
		{
			name: "plain",
			build: func(done chan struct{}) tool.Tool {
				return tool.NewRaw("work", "plain work", func(context.Context, json.RawMessage) (string, error) {
					close(done)
					return "plain result", nil
				}, objectSchema, tool.RequiresApproval())
			},
		},
		{
			name: "rich",
			build: func(done chan struct{}) tool.Tool {
				return tool.NewRich[json.RawMessage]("work", "rich work", func(context.Context, json.RawMessage) (*tool.Output, error) {
					close(done)
					return &tool.Output{Text: "rich result", Images: []tool.Image{{Base64: "aW1hZ2U=", MIMEType: "image/png"}}}, nil
				}, objectSchema, tool.RequiresApproval())
			},
		},
		{
			name: "background",
			build: func(done chan struct{}) tool.Tool {
				return tool.NewBackground[json.RawMessage]("work", "background work", "queued", func(context.Context, json.RawMessage) (string, error) {
					close(done)
					return "background result", nil
				}, objectSchema, tool.RequiresApproval())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			done := make(chan struct{})
			provider := newScriptedProvider(
				&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "provider-call-id", Name: "work", Input: json.RawMessage(`{}`)}}},
				&ModelResponse{Text: "resumed"},
				&ModelResponse{Text: "re-entry"},
			)
			var middlewareCalls atomic.Int32
			var middlewareCall ToolCall
			middleware := func(next ToolHandlerFunc) ToolHandlerFunc {
				return func(ctx context.Context, call ToolCall) (ToolResult, error) {
					middlewareCalls.Add(1)
					middlewareCall = call
					return next(ctx, call)
				}
			}
			a, err := New(provider, "sys",
				WithTools(tt.build(done)),
				WithConversationStore(newTestMemoryStore()),
				WithMiddleware(middleware),
			)
			if err != nil {
				t.Fatal(err)
			}
			ctx := Background().WithConversationID("conversation-" + tt.name)
			in := mustInterrupt(t, a, ctx, "run work")
			if middlewareCalls.Load() != 0 {
				t.Fatal("middleware ran before approval")
			}
			result, err := a.Resume(ctx, in, Approve())
			if err != nil {
				t.Fatal(err)
			}
			if result.Text != "resumed" {
				t.Fatalf("result.Text = %q", result.Text)
			}
			if err := a.Shutdown(context.Background()); err != nil {
				t.Fatal(err)
			}
			if middlewareCalls.Load() != 1 {
				t.Fatalf("middleware calls = %d, want 1", middlewareCalls.Load())
			}
			if middlewareCall.ID != "provider-call-id" || middlewareCall.Name != "work" || string(middlewareCall.Input) != `{}` {
				t.Fatalf("middleware call = %#v", middlewareCall)
			}
			select {
			case <-done:
			default:
				t.Fatal("tool handler did not run")
			}
		})
	}
}

func TestMemoryInterruptStoreRejectsConsumedIDReuse(t *testing.T) {
	store := newMemoryInterruptStore()
	in := &Interrupt{ID: "one-shot", Type: InterruptHumanInput, Input: &InputInterrupt{Question: "continue?"}}
	if err := store.Save(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(context.Background(), in.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), in); err == nil {
		t.Fatal("Save recreated a consumed interrupt ID")
	}
	if _, err := store.Load(context.Background(), in.ID); !errors.Is(err, ErrInterruptNotFound) {
		t.Fatalf("Load error = %v, want ErrInterruptNotFound", err)
	}
}

func TestResume_PostClaimFailureRemainsConsumed(t *testing.T) {
	var handlerCalls atomic.Int32
	approved := tool.NewRaw("approved", "approved", func(context.Context, json.RawMessage) (string, error) {
		handlerCalls.Add(1)
		return "ok", nil
	}, tool.RequiresApproval())
	provider := &approvalBatchProvider{responses: []*ModelResponse{{
		ToolCalls: []tool.Call{{ToolUseID: "tc-1", Name: "approved", Input: json.RawMessage(`{}`)}},
	}}}
	a, err := New(provider, "helpful", WithTools(approved))
	if err != nil {
		t.Fatal(err)
	}
	in := mustInterrupt(t, a, Background(), "run")

	if _, err := a.Resume(Background(), in, Approve()); err == nil {
		t.Fatal("Resume succeeded despite the provider failing after the claim")
	}
	if handlerCalls.Load() != 1 {
		t.Fatalf("handler calls = %d, want 1", handlerCalls.Load())
	}
	if _, err := a.Resume(Background(), in, Approve()); !errors.Is(err, ErrInterruptNotFound) {
		t.Fatalf("retry error = %v, want ErrInterruptNotFound", err)
	}
	if handlerCalls.Load() != 1 {
		t.Fatalf("handler replayed after post-claim failure: calls = %d", handlerCalls.Load())
	}
}

func TestResume_ApprovedToolErrorIsNotReplayable(t *testing.T) {
	var handlerCalls atomic.Int32
	toolErr := errors.New("tool failed after side effect boundary")
	approved := tool.NewRaw("approved", "approved", func(context.Context, json.RawMessage) (string, error) {
		handlerCalls.Add(1)
		return "", toolErr
	}, tool.RequiresApproval())
	provider := &approvalBatchProvider{responses: []*ModelResponse{{
		ToolCalls: []tool.Call{{ToolUseID: "tc-1", Name: "approved", Input: json.RawMessage(`{}`)}},
	}}}
	a, err := New(provider, "helpful", WithTools(approved))
	if err != nil {
		t.Fatal(err)
	}
	in := mustInterrupt(t, a, Background(), "run")

	// Tool errors are returned to the model. This provider intentionally has no
	// follow-up response, so the resumed invocation then fails.
	if _, err := a.Resume(Background(), in, Approve()); err == nil {
		t.Fatal("Resume succeeded despite the missing follow-up provider response")
	}
	if handlerCalls.Load() != 1 {
		t.Fatalf("handler calls = %d, want 1", handlerCalls.Load())
	}
	if _, err := a.Resume(Background(), in, Approve()); !errors.Is(err, ErrInterruptNotFound) {
		t.Fatalf("retry error = %v, want ErrInterruptNotFound", err)
	}
	if handlerCalls.Load() != 1 {
		t.Fatalf("failed approved tool replayed: calls = %d", handlerCalls.Load())
	}
}
