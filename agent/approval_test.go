package agent

import (
	"context"
	"encoding/json"
	"errors"
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
		schema,
		func(_ context.Context, input json.RawMessage) (string, error) {
			var p struct {
				OrderID string `json:"order_id"`
			}
			json.Unmarshal(input, &p)
			return `{"deleted":true,"order_id":"` + p.OrderID + `"}`, nil
		},
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
	if in.ExecutionID == "" {
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
	dt := tool.NewRaw(
		"delete_order",
		"Permanently deletes an order",
		nil,
		func(_ context.Context, _ json.RawMessage) (string, error) {
			handlerCalled = true
			return `{"deleted":true}`, nil
		},
		tool.RequiresApproval(),
	)
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
	normalTool := tool.NewRaw("get_info", "Gets some info", nil, func(_ context.Context, _ json.RawMessage) (string, error) { return `{"info":"ok"}`, nil })
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
			allowed := tool.NewRaw("allowed", "allowed", nil, record("allowed", "allowed result"), tool.RequiresApproval())
			free := tool.NewRaw("free", "free", nil, record("free", "free result"))
			denied := tool.NewRaw("denied", "denied", nil, record("denied", "denied result"), tool.RequiresApproval())
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
			mu.Lock()
			prePause := append([]string(nil), handlers...)
			mu.Unlock()
			if len(prePause) != 0 {
				t.Fatalf("handlers ran before approval: %v", prePause)
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
			if len(got) != 2 || got[0] != "allowed" || got[1] != "free" {
				if !parallel {
					t.Fatalf("handlers = %v, want [allowed free]", got)
				}
				if len(got) != 2 || (got[0] != "allowed" && got[1] != "allowed") || (got[0] != "free" && got[1] != "free") {
					t.Fatalf("parallel handlers = %v, want allowed and free", got)
				}
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
				return tool.NewRaw(
					name,
					name,
					nil,
					func(context.Context, json.RawMessage) (string, error) {
						handlerCalls.Add(1)
						return "ran", nil
					},
					tool.RequiresApproval(),
				)
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
		return tool.NewRaw(
			name,
			name,
			nil,
			func(_ context.Context, _ json.RawMessage) (string, error) {
				called++
				return "unexpected", nil
			},
			tool.RequiresApproval(),
		)
	}
	provider := &approvalBatchProvider{}
	a, err := New(provider, "helpful", WithTools(makeTool("first"), makeTool("second")))
	if err != nil {
		t.Fatal(err)
	}
	in := &Interrupt{ExecutionID: "exec-i-1", ExecutionVersion: 1, Type: InterruptApproval, Approval: &ApprovalInterrupt{Calls: []ApprovalCall{
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
	human := &Interrupt{ExecutionID: "exec-i-2", ExecutionVersion: 1, Type: InterruptHumanInput, Input: &InputInterrupt{Question: "?"}}
	if _, err := a.Resume(Background(), human, Approve()); err == nil {
		t.Fatal("Approve on a human_input interrupt must fail")
	}
}

func TestResume_InvalidResponseDoesNotConsumeInterrupt(t *testing.T) {
	var handlerCalls atomic.Int32
	approved := tool.NewRaw(
		"approved",
		"approved",
		nil,
		func(context.Context, json.RawMessage) (string, error) {
			handlerCalls.Add(1)
			return "ok", nil
		},
		tool.RequiresApproval(),
	)
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
		approved := tool.NewRaw(
			"approved",
			"approved",
			nil,
			func(context.Context, json.RawMessage) (string, error) {
				handlerCalls.Add(1)
				return "ok", nil
			},
			tool.RequiresApproval(),
		)
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
		if _, err := a.Resume(Background(), in, Approve()); !errors.Is(err, ErrExecutionConflict) {
			t.Fatalf("replay err = %v, want ErrExecutionConflict", err)
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
				if !errors.Is(err, ErrExecutionConflict) {
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

func TestResume_CanonicalMiddlewarePipelineSupportsEveryToolKind(t *testing.T) {
	objectSchema := tool.WithSchema(map[string]any{"type": "object"})
	tests := []struct {
		name  string
		build func(chan struct{}) tool.Tool
	}{
		{
			name: "plain",
			build: func(done chan struct{}) tool.Tool {
				return tool.NewRaw(
					"work",
					"plain work",
					nil,
					func(context.Context, json.RawMessage) (string, error) {
						close(done)
						return "plain result", nil
					},
					objectSchema,
					tool.RequiresApproval(),
				)
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

func TestResume_PostClaimFailureRemainsConsumed(t *testing.T) {
	var handlerCalls atomic.Int32
	approved := tool.NewRaw(
		"approved",
		"approved",
		nil,
		func(context.Context, json.RawMessage) (string, error) {
			handlerCalls.Add(1)
			return "ok", nil
		},
		tool.RequiresApproval(),
	)
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
	if _, err := a.Resume(Background(), in, Approve()); !errors.Is(err, ErrExecutionConflict) {
		t.Fatalf("retry error = %v, want ErrExecutionConflict", err)
	}
	if handlerCalls.Load() != 1 {
		t.Fatalf("handler replayed after post-claim failure: calls = %d", handlerCalls.Load())
	}
}

func TestResume_ApprovedToolErrorIsNotReplayable(t *testing.T) {
	var handlerCalls atomic.Int32
	toolErr := errors.New("tool failed after side effect boundary")
	approved := tool.NewRaw(
		"approved",
		"approved",
		nil,
		func(context.Context, json.RawMessage) (string, error) {
			handlerCalls.Add(1)
			return "", toolErr
		},
		tool.RequiresApproval(),
	)
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
	if _, err := a.Resume(Background(), in, Approve()); !errors.Is(err, ErrExecutionConflict) {
		t.Fatalf("retry error = %v, want ErrExecutionConflict", err)
	}
	if handlerCalls.Load() != 1 {
		t.Fatalf("failed approved tool replayed: calls = %d", handlerCalls.Load())
	}
}
