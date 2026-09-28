package agent

import (
	"context"
	"encoding/json"
	"errors"
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
