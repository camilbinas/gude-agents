package agent

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// deleteOrderTool is a potentially destructive tool used in approval tests.
func deleteOrderTool() tool.Tool {
	return tool.NewRaw(
		"delete_order",
		"Permanently deletes an order",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"order_id": map[string]any{"type": "string"},
			},
			"required": []string{"order_id"},
		},
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

// TestRequiresApproval_PausesLoop verifies that when the LLM calls a tool
// marked with RequiresApproval, the loop returns ErrToolApprovalRequired and
// the ApprovalRequest is available on the Context.
func TestRequiresApproval_PausesLoop(t *testing.T) {
	provider := newScriptedProvider(
		&ProviderResponse{
			ToolCalls: []tool.Call{{
				ToolUseID: "tc-1",
				Name:      "delete_order",
				Input:     json.RawMessage(`{"order_id":"ORD-99"}`),
			}},
		},
	)

	a, err := New(provider, prompt.Text("helpful"), []tool.Tool{deleteOrderTool()})
	if err != nil {
		t.Fatal(err)
	}

	c := Background()
	err = a.InvokeStream(c, "delete order 99", nil)
	if !errors.Is(err, ErrToolApprovalRequired) {
		t.Fatalf("expected ErrToolApprovalRequired, got %v", err)
	}

	ar, ok := GetApprovalRequest(c)
	if !ok {
		t.Fatal("expected ApprovalRequest on Context")
	}
	if ar.ToolName != "delete_order" {
		t.Errorf("ToolName = %q, want %q", ar.ToolName, "delete_order")
	}
	if ar.ToolUseID != "tc-1" {
		t.Errorf("ToolUseID = %q, want %q", ar.ToolUseID, "tc-1")
	}
	var input struct {
		OrderID string `json:"order_id"`
	}
	json.Unmarshal(ar.ToolInput, &input)
	if input.OrderID != "ORD-99" {
		t.Errorf("ToolInput.order_id = %q, want %q", input.OrderID, "ORD-99")
	}
	if len(ar.Messages) == 0 {
		t.Error("expected non-empty message snapshot in ApprovalRequest")
	}
}

// TestResumeWithApproval_Allow verifies that approving runs the tool handler
// and the agent loop continues to a final response.
func TestResumeWithApproval_Allow(t *testing.T) {
	provider := newScriptedProvider(
		// First call: LLM requests the tool.
		&ProviderResponse{
			ToolCalls: []tool.Call{{
				ToolUseID: "tc-1",
				Name:      "delete_order",
				Input:     json.RawMessage(`{"order_id":"ORD-99"}`),
			}},
		},
		// Second call (after approval + tool execution): final answer.
		&ProviderResponse{Text: "Order ORD-99 has been deleted."},
	)

	a, err := New(provider, prompt.Text("helpful"), []tool.Tool{deleteOrderTool()})
	if err != nil {
		t.Fatal(err)
	}

	c := Background()
	err = a.InvokeStream(c, "delete order 99", nil)
	if !errors.Is(err, ErrToolApprovalRequired) {
		t.Fatalf("expected ErrToolApprovalRequired, got %v", err)
	}

	ar, _ := GetApprovalRequest(c)
	result, err := a.ResumeWithApprovalInvoke(c, ar, tool.Allow())
	if err != nil {
		t.Fatalf("ResumeWithApprovalInvoke failed: %v", err)
	}
	if result != "Order ORD-99 has been deleted." {
		t.Errorf("result = %q, want %q", result, "Order ORD-99 has been deleted.")
	}
}

// TestResumeWithApproval_Deny verifies that denying injects a denial result
// and the agent loop continues without running the handler.
func TestResumeWithApproval_Deny(t *testing.T) {
	handlerCalled := false
	dt := tool.NewRaw(
		"delete_order",
		"Permanently deletes an order",
		map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (string, error) {
			handlerCalled = true
			return `{"deleted":true}`, nil
		},
		tool.RequiresApproval(),
	)

	provider := newScriptedProvider(
		&ProviderResponse{
			ToolCalls: []tool.Call{{
				ToolUseID: "tc-1",
				Name:      "delete_order",
				Input:     json.RawMessage(`{"order_id":"ORD-99"}`),
			}},
		},
		// After denial, LLM gets a denial result and produces a final text.
		&ProviderResponse{Text: "I couldn't delete the order — access was denied."},
	)

	a, err := New(provider, prompt.Text("helpful"), []tool.Tool{dt})
	if err != nil {
		t.Fatal(err)
	}

	c := Background()
	err = a.InvokeStream(c, "delete order 99", nil)
	if !errors.Is(err, ErrToolApprovalRequired) {
		t.Fatalf("expected ErrToolApprovalRequired, got %v", err)
	}

	ar, _ := GetApprovalRequest(c)
	result, err := a.ResumeWithApprovalInvoke(c, ar, tool.Deny("access denied by admin"))
	if err != nil {
		t.Fatalf("ResumeWithApprovalInvoke denied failed: %v", err)
	}
	if handlerCalled {
		t.Error("handler should not have been called on denial")
	}
	if result != "I couldn't delete the order — access was denied." {
		t.Errorf("result = %q", result)
	}
}

// TestRequiresApproval_NormalToolUnaffected verifies that tools without
// RequiresApproval still execute normally.
func TestRequiresApproval_NormalToolUnaffected(t *testing.T) {
	normalTool := tool.NewRaw(
		"get_info",
		"Gets some info",
		map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (string, error) {
			return `{"info":"ok"}`, nil
		},
		// No RequiresApproval()
	)

	provider := newScriptedProvider(
		&ProviderResponse{
			ToolCalls: []tool.Call{{
				ToolUseID: "tc-1",
				Name:      "get_info",
				Input:     json.RawMessage(`{}`),
			}},
		},
		&ProviderResponse{Text: "Here is the info."},
	)

	a, err := New(provider, prompt.Text("helpful"), []tool.Tool{normalTool})
	if err != nil {
		t.Fatal(err)
	}

	c := Background()
	result, err := a.Invoke(c, "get info")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Here is the info." {
		t.Errorf("result = %q", result)
	}
}

// TestInvokeEventStream_EmitsToolApprovalRequired verifies that
// EventToolApprovalRequired is emitted with the correct tool name and input.
func TestInvokeEventStream_EmitsToolApprovalRequired(t *testing.T) {
	provider := newScriptedProvider(
		&ProviderResponse{
			ToolCalls: []tool.Call{{
				ToolUseID: "tc-1",
				Name:      "delete_order",
				Input:     json.RawMessage(`{"order_id":"ORD-42"}`),
			}},
		},
	)

	a, err := New(provider, prompt.Text("helpful"), []tool.Tool{deleteOrderTool()})
	if err != nil {
		t.Fatal(err)
	}

	ch := a.InvokeEventStream(Background(), "delete order 42")

	var approvalEv *AgentEvent
	for ev := range ch {
		ev := ev
		if ev.Type == EventToolApprovalRequired {
			approvalEv = &ev
		}
	}

	if approvalEv == nil {
		t.Fatal("expected EventToolApprovalRequired, got none")
	}
	if approvalEv.ApprovalToolName != "delete_order" {
		t.Errorf("ApprovalToolName = %q, want %q", approvalEv.ApprovalToolName, "delete_order")
	}
	var input struct {
		OrderID string `json:"order_id"`
	}
	json.Unmarshal(approvalEv.ApprovalToolInput, &input)
	if input.OrderID != "ORD-42" {
		t.Errorf("ApprovalToolInput.order_id = %q, want ORD-42", input.OrderID)
	}
}

// TestGetApprovalRequest_NilContext verifies nil-safety.
func TestGetApprovalRequest_NilContext(t *testing.T) {
	ar, ok := GetApprovalRequest(nil)
	if ok || ar != nil {
		t.Error("expected nil, false for nil Context")
	}
}

// TestApprovalRequest_SnapshotNoDuplicateToolUseID verifies that ar.Messages
// does NOT contain a tool result for the pending ToolUseID. If it did,
// ResumeWithApproval would append a second result with the same ID, causing
// a ValidationException from Bedrock ("duplicate Ids").
func TestApprovalRequest_SnapshotNoDuplicateToolUseID(t *testing.T) {
	provider := newScriptedProvider(
		&ProviderResponse{
			ToolCalls: []tool.Call{{
				ToolUseID: "tc-dup",
				Name:      "delete_order",
				Input:     json.RawMessage(`{"order_id":"ORD-1"}`),
			}},
		},
	)

	a, err := New(provider, prompt.Text("helpful"), []tool.Tool{deleteOrderTool()})
	if err != nil {
		t.Fatal(err)
	}

	c := Background()
	err = a.InvokeStream(c, "delete order 1", nil)
	if !errors.Is(err, ErrToolApprovalRequired) {
		t.Fatalf("expected ErrToolApprovalRequired, got %v", err)
	}

	ar, _ := GetApprovalRequest(c)

	// Count how many tool results in the snapshot carry the pending ToolUseID.
	count := 0
	for _, msg := range ar.Messages {
		for _, block := range msg.Content {
			if tr, ok := block.(ToolResultBlock); ok && tr.ToolUseID == ar.ToolUseID {
				count++
			}
		}
	}

	if count != 0 {
		t.Errorf("snapshot contains %d tool result(s) for ToolUseID %q — would cause duplicate ID error on resume", count, ar.ToolUseID)
	}
}

func TestResumeWithApproval_RerunsExecutionChecks(t *testing.T) {
	t.Run("schema", func(t *testing.T) {
		handlerCalled := false
		strictTool := tool.NewRaw("strict", "requires an id", map[string]any{
			"type":       "object",
			"required":   []string{"id"},
			"properties": map[string]any{"id": map[string]any{"type": "string"}},
		}, func(_ context.Context, _ json.RawMessage) (string, error) {
			handlerCalled = true
			return "ok", nil
		}, tool.RequiresApproval())
		provider := newScriptedProvider(
			&ProviderResponse{ToolCalls: []tool.Call{{ToolUseID: "schema-1", Name: "strict", Input: json.RawMessage(`{"id":"original"}`)}}},
			&ProviderResponse{Text: "schema rejected"},
		)
		a, err := New(provider, prompt.Text("helpful"), []tool.Tool{strictTool})
		if err != nil {
			t.Fatal(err)
		}
		c := Background()
		if err := a.InvokeStream(c, "run", nil); !errors.Is(err, ErrToolApprovalRequired) {
			t.Fatalf("expected approval request, got %v", err)
		}
		ar, _ := GetApprovalRequest(c)
		ar.ToolInput = json.RawMessage(`{}`)
		if _, err := a.ResumeWithApprovalInvoke(c, ar, tool.Allow()); err != nil {
			t.Fatalf("ResumeWithApprovalInvoke: %v", err)
		}
		if handlerCalled {
			t.Fatal("handler ran despite invalid approved input")
		}
	})

	t.Run("role", func(t *testing.T) {
		handlerCalled := false
		restricted := tool.NewRaw("restricted", "admin only", map[string]any{"type": "object"}, func(_ context.Context, _ json.RawMessage) (string, error) {
			handlerCalled = true
			return "secret", nil
		}, tool.RequiresApproval(), tool.AllowRoles("admin"))
		provider := newScriptedProvider(
			&ProviderResponse{ToolCalls: []tool.Call{{ToolUseID: "role-1", Name: "restricted", Input: json.RawMessage(`{}`)}}},
			&ProviderResponse{Text: "role rejected"},
		)
		a, err := New(provider, prompt.Text("helpful"), []tool.Tool{restricted})
		if err != nil {
			t.Fatal(err)
		}
		c := Background().WithPrincipal(Principal{ID: "admin", Roles: []string{"admin"}})
		if err := a.InvokeStream(c, "run", nil); !errors.Is(err, ErrToolApprovalRequired) {
			t.Fatalf("expected approval request, got %v", err)
		}
		ar, _ := GetApprovalRequest(c)
		c.Set(principalKey{}, Principal{ID: "guest", Roles: []string{"guest"}})
		if _, err := a.ResumeWithApprovalInvoke(c, ar, tool.Allow()); err != nil {
			t.Fatalf("ResumeWithApprovalInvoke: %v", err)
		}
		if handlerCalled {
			t.Fatal("handler ran after approved caller lost the required role")
		}
	})

	t.Run("guard and middleware", func(t *testing.T) {
		var calls []string
		guarded := tool.NewRaw("guarded", "guarded tool", map[string]any{"type": "object"}, func(_ context.Context, _ json.RawMessage) (string, error) {
			calls = append(calls, "handler")
			return "ok", nil
		}, tool.RequiresApproval())
		guarded.Guard = func(_ context.Context, _ json.RawMessage) (tool.Decision, error) {
			calls = append(calls, "guard")
			return tool.Allow(), nil
		}
		middleware := func(next ToolHandlerFunc) ToolHandlerFunc {
			return func(c *Context, name string, input json.RawMessage) (string, error) {
				calls = append(calls, "before middleware")
				out, err := next(c, name, input)
				calls = append(calls, "after middleware")
				return out, err
			}
		}
		provider := newScriptedProvider(
			&ProviderResponse{ToolCalls: []tool.Call{{ToolUseID: "guard-1", Name: "guarded", Input: json.RawMessage(`{}`)}}},
			&ProviderResponse{Text: "done"},
		)
		a, err := New(provider, prompt.Text("helpful"), []tool.Tool{guarded}, WithMiddleware(middleware))
		if err != nil {
			t.Fatal(err)
		}
		c := Background()
		if err := a.InvokeStream(c, "run", nil); !errors.Is(err, ErrToolApprovalRequired) {
			t.Fatalf("expected approval request, got %v", err)
		}
		ar, _ := GetApprovalRequest(c)
		if _, err := a.ResumeWithApprovalInvoke(c, ar, tool.Allow()); err != nil {
			t.Fatalf("ResumeWithApprovalInvoke: %v", err)
		}
		want := []string{"before middleware", "guard", "handler", "after middleware"}
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

func TestResumeWithApproval_RichHandlerOnlyTool(t *testing.T) {
	richCalled := false
	richTool := tool.NewRichRaw("screenshot", "captures a screenshot", map[string]any{"type": "object"}, func(_ context.Context, _ json.RawMessage) (*tool.Output, error) {
		richCalled = true
		return &tool.Output{Text: "screenshot captured", Images: []tool.Image{{Base64: "aW1hZ2U=", MIMEType: "image/png"}}}, nil
	}, tool.RequiresApproval())
	provider := newScriptedProvider(
		&ProviderResponse{ToolCalls: []tool.Call{{ToolUseID: "rich-1", Name: "screenshot", Input: json.RawMessage(`{}`)}}},
		&ProviderResponse{Text: "I reviewed the screenshot."},
	)
	a, err := New(provider, prompt.Text("helpful"), []tool.Tool{richTool})
	if err != nil {
		t.Fatal(err)
	}
	c := Background()
	if err := a.InvokeStream(c, "take a screenshot", nil); !errors.Is(err, ErrToolApprovalRequired) {
		t.Fatalf("expected approval request, got %v", err)
	}
	ar, _ := GetApprovalRequest(c)
	result, err := a.ResumeWithApprovalInvoke(c, ar, tool.Allow())
	if err != nil {
		t.Fatalf("ResumeWithApprovalInvoke: %v", err)
	}
	if !richCalled {
		t.Fatal("rich handler was not called")
	}
	if result != "I reviewed the screenshot." {
		t.Errorf("result = %q", result)
	}
}

type approvalBatchProvider struct {
	mu        sync.Mutex
	responses []*ProviderResponse
	params    []ConverseParams
}

func (p *approvalBatchProvider) Name() string { return "approval-batch" }

func (p *approvalBatchProvider) Converse(_ context.Context, params ConverseParams) (*ProviderResponse, error) {
	return p.next(params)
}

func (p *approvalBatchProvider) ConverseStream(_ context.Context, params ConverseParams, cb StreamCallback) (*ProviderResponse, error) {
	response, err := p.next(params)
	if err != nil {
		return nil, err
	}
	if response.Text != "" && cb != nil {
		cb(response.Text)
	}
	return response, nil
}

func (p *approvalBatchProvider) next(params ConverseParams) (*ProviderResponse, error) {
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

func TestResumeWithApprovals_TwoCallsOrderedSequentialAndParallel(t *testing.T) {
	for _, parallel := range []bool{false, true} {
		t.Run(map[bool]string{false: "sequential", true: "parallel"}[parallel], func(t *testing.T) {
			provider := &approvalBatchProvider{responses: []*ProviderResponse{
				{ToolCalls: []tool.Call{
					{ToolUseID: "allow-1", Name: "allowed", Input: json.RawMessage(`{"value":"first"}`)},
					{ToolUseID: "deny-2", Name: "denied", Input: json.RawMessage(`{"value":"second"}`)},
				}},
				{Text: "batch complete"},
			}}
			var mu sync.Mutex
			handlers := make([]string, 0, 2)
			allowed := tool.NewRaw("allowed", "allowed", map[string]any{"type": "object"}, func(_ context.Context, _ json.RawMessage) (string, error) {
				mu.Lock()
				defer mu.Unlock()
				handlers = append(handlers, "allowed")
				return "allowed result", nil
			}, tool.RequiresApproval())
			denied := tool.NewRaw("denied", "denied", map[string]any{"type": "object"}, func(_ context.Context, _ json.RawMessage) (string, error) {
				mu.Lock()
				defer mu.Unlock()
				handlers = append(handlers, "denied")
				return "denied result", nil
			}, tool.RequiresApproval())
			opts := []Option{}
			if parallel {
				opts = append(opts, WithParallelToolExecution())
			}
			a, err := New(provider, prompt.Text("helpful"), []tool.Tool{allowed, denied}, opts...)
			if err != nil {
				t.Fatal(err)
			}

			c := Background()
			if err := a.InvokeStream(c, "run both", nil); !errors.Is(err, ErrToolApprovalRequired) {
				t.Fatalf("InvokeStream error = %v, want ErrToolApprovalRequired", err)
			}
			ar, ok := GetApprovalRequest(c)
			if !ok {
				t.Fatal("expected approval request on caller context")
			}
			if ar.ToolName != "allowed" || ar.ToolUseID != "allow-1" {
				t.Fatalf("legacy mirrors = %q/%q, want allowed/allow-1", ar.ToolName, ar.ToolUseID)
			}
			if len(ar.Calls) != 2 || ar.Calls[0].ToolUseID != "allow-1" || ar.Calls[1].ToolUseID != "deny-2" {
				t.Fatalf("approval calls = %#v, want provider order", ar.Calls)
			}
			for _, message := range ar.Messages {
				for _, block := range message.Content {
					if result, ok := block.(ToolResultBlock); ok && result.Content == approvalSentinel {
						t.Fatal("approval sentinel leaked into snapshot")
					}
				}
			}

			result, err := a.ResumeWithApprovalsInvoke(c, ar, map[string]tool.Decision{
				"allow-1": tool.Allow(),
				"deny-2":  tool.Deny("human denied"),
			})
			if err != nil {
				t.Fatalf("ResumeWithApprovalsInvoke: %v", err)
			}
			if result != "batch complete" {
				t.Fatalf("result = %q, want batch complete", result)
			}
			mu.Lock()
			gotHandlers := append([]string(nil), handlers...)
			mu.Unlock()
			if len(gotHandlers) != 1 || gotHandlers[0] != "allowed" {
				t.Fatalf("handlers = %v, want only allowed", gotHandlers)
			}

			provider.mu.Lock()
			if len(provider.params) != 2 {
				provider.mu.Unlock()
				t.Fatalf("provider calls = %d, want 2", len(provider.params))
			}
			messages := provider.params[1].Messages
			provider.mu.Unlock()
			last := messages[len(messages)-1]
			if last.Role != RoleUser || len(last.Content) != 2 {
				t.Fatalf("resumed result message = %#v, want one two-result user message", last)
			}
			first, firstOK := last.Content[0].(ToolResultBlock)
			second, secondOK := last.Content[1].(ToolResultBlock)
			if !firstOK || !secondOK || first.ToolUseID != "allow-1" || first.Content != "allowed result" || second.ToolUseID != "deny-2" || !second.IsError {
				t.Fatalf("ordered results = %#v, want allowed then denied", last.Content)
			}
		})
	}
}

func TestResumeWithApprovals_InvalidDecisionMapRunsNoHandlers(t *testing.T) {
	called := 0
	makeTool := func(name string) tool.Tool {
		return tool.NewRaw(name, name, map[string]any{"type": "object"}, func(_ context.Context, _ json.RawMessage) (string, error) {
			called++
			return "unexpected", nil
		}, tool.RequiresApproval())
	}
	a, err := New(newScriptedProvider(), prompt.Text("helpful"), []tool.Tool{makeTool("first"), makeTool("second")})
	if err != nil {
		t.Fatal(err)
	}
	ar := &ApprovalRequest{Calls: []ApprovalCall{
		{ToolName: "first", ToolUseID: "first-id", ToolInput: json.RawMessage(`{}`)},
		{ToolName: "second", ToolUseID: "second-id", ToolInput: json.RawMessage(`{}`)},
	}}
	for name, decisions := range map[string]map[string]tool.Decision{
		"missing": {"first-id": tool.Allow()},
		"extra":   {"first-id": tool.Allow(), "second-id": tool.Allow(), "extra": tool.Allow()},
	} {
		t.Run(name, func(t *testing.T) {
			if err := a.ResumeWithApprovals(Background(), ar, decisions, nil); err == nil {
				t.Fatal("expected validation error")
			}
			if called != 0 {
				t.Fatalf("handler count = %d, want 0", called)
			}
		})
	}
}

func TestInvokeEventStream_EmitsOrderedApprovalCalls(t *testing.T) {
	provider := &approvalBatchProvider{responses: []*ProviderResponse{{ToolCalls: []tool.Call{
		{ToolUseID: "first", Name: "first", Input: json.RawMessage(`{}`)},
		{ToolUseID: "second", Name: "second", Input: json.RawMessage(`{}`)},
	}}}}
	first := tool.NewRaw("first", "first", map[string]any{"type": "object"}, dummyHandler, tool.RequiresApproval())
	second := tool.NewRaw("second", "second", map[string]any{"type": "object"}, dummyHandler, tool.RequiresApproval())
	a, err := New(provider, prompt.Text("helpful"), []tool.Tool{first, second}, WithParallelToolExecution())
	if err != nil {
		t.Fatal(err)
	}
	c := Background()
	var approvalEvent *AgentEvent
	for event := range a.InvokeEventStream(c, "run") {
		if event.Type == EventToolApprovalRequired {
			event := event
			approvalEvent = &event
		}
	}
	if approvalEvent == nil || len(approvalEvent.ApprovalCalls) != 2 {
		t.Fatalf("approval event = %#v, want two calls", approvalEvent)
	}
	if approvalEvent.ApprovalCalls[0].ToolUseID != "first" || approvalEvent.ApprovalCalls[1].ToolUseID != "second" {
		t.Fatalf("event calls = %#v, want provider order", approvalEvent.ApprovalCalls)
	}
	if approvalEvent.ApprovalToolName != "first" {
		// ApprovalToolName remains the singleton-compatible mirror; ApprovalCalls
		// carries the ToolUseIDs for batch consumers.
		t.Fatalf("legacy event approval name = %q, want first", approvalEvent.ApprovalToolName)
	}
	ar, ok := GetApprovalRequest(c)
	if !ok || len(ar.Calls) != 2 || ar.Calls[0].ToolUseID != "first" || ar.Calls[1].ToolUseID != "second" {
		t.Fatalf("caller approval request = %#v, want ordered calls", ar)
	}
}
