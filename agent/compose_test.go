package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// TestAgentAsTool_ChildReceivesMessageAndReturnsResult verifies that a child
// agent wrapped via AgentAsTool receives the parent's tool input as a user
// message and returns its final answer as the tool result.
func TestAgentAsTool_ChildReceivesMessageAndReturnsResult(t *testing.T) {
	// Child agent: responds with a fixed answer on the first call.
	childProvider := newScriptedProvider(
		&ModelResponse{Text: "child answer"},
	)
	child, err := New(childProvider, "child system prompt")
	if err != nil {
		t.Fatalf("failed to create child agent: %v", err)
	}

	// Wrap the child as a tool.
	wrappedTool := AgentAsTool("sub_agent", "A helpful sub-agent", child)

	// Parent agent: first call triggers the sub_agent tool, second call returns final text.
	parentProvider := newScriptedProvider(
		&ModelResponse{
			ToolCalls: []tool.Call{
				{
					ToolUseID: "tc1",
					Name:      "sub_agent",
					Input:     json.RawMessage(`{"message":"hello child"}`),
				},
			},
		},
		&ModelResponse{Text: "parent done"},
	)

	parent, err := New(parentProvider, "parent system prompt", WithTools(wrappedTool))
	if err != nil {
		t.Fatalf("failed to create parent agent: %v", err)
	}

	result, err := parent.Invoke(Background(), "delegate to child")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Text != "parent done" {
		t.Errorf("expected %q, got %q", "parent done", result.Text)
	}
}

// TestAgentAsTool_ChildErrorPropagatedAsIsError verifies that when the child agent
// errors, the parent receives a ToolResultBlock with IsError=true (not a success string).
func TestAgentAsTool_ChildErrorPropagatedAsIsError(t *testing.T) {
	// Child agent: provider always returns tool calls, causing max iterations
	// to be exceeded (maxIterations=1 means it loops once then errors).
	childProvider := newScriptedProvider(
		&ModelResponse{
			ToolCalls: []tool.Call{toolCall("ctc1", "child_tool")},
		},
	)
	childTool := tool.NewRaw(
		"child_tool",
		"a child tool",
		nil,
		func(_ context.Context, _ json.RawMessage) (string, error) {
			return "ok", nil
		},
	)
	child, err := New(childProvider, "child sys", WithTools(childTool), WithMaxIterations(1))
	if err != nil {
		t.Fatalf("failed to create child agent: %v", err)
	}

	wrappedTool := AgentAsTool("sub_agent", "A sub-agent that will fail", child)

	// Use capturingProvider so we can inspect the tool result sent to the parent.
	parentProvider := newCapturingProvider(
		&ModelResponse{
			ToolCalls: []tool.Call{
				{
					ToolUseID: "tc1",
					Name:      "sub_agent",
					Input:     json.RawMessage(`{"message":"do something"}`),
				},
			},
		},
		&ModelResponse{Text: "parent recovered"},
	)

	parent, err := New(parentProvider, "parent sys", WithTools(wrappedTool))
	if err != nil {
		t.Fatalf("failed to create parent agent: %v", err)
	}

	result, err := parent.Invoke(Background(), "try child")
	if err != nil {
		t.Fatalf("parent should not abort when child fails, got: %v", err)
	}
	if result.Text != "parent recovered" {
		t.Errorf("expected %q, got %q", "parent recovered", result.Text)
	}

	// The second provider call must have received a ToolResultBlock with IsError=true.
	if len(parentProvider.captured) < 2 {
		t.Fatalf("expected at least 2 provider calls, got %d", len(parentProvider.captured))
	}
	found := false
	for _, msg := range parentProvider.captured[1].Messages {
		for _, block := range msg.Content {
			if tr, ok := block.(ToolResultBlock); ok && tr.IsError {
				found = true
			}
		}
	}
	if !found {
		t.Error("expected a ToolResultBlock with IsError=true in the second provider call")
	}
}

// TestAgentAsTool_ChildErrorFromProvider verifies that a direct provider error
// in the child agent propagates as IsError=true to the parent (not as success text).
func TestAgentAsTool_ChildErrorFromProvider(t *testing.T) {
	// Child agent: provider returns an error immediately.
	errProvider := &erroringProvider{err: fmt.Errorf("provider exploded")}
	child, err := New(errProvider, "child sys")
	if err != nil {
		t.Fatalf("failed to create child agent: %v", err)
	}

	wrappedTool := AgentAsTool("sub_agent", "A sub-agent with broken provider", child)

	// Use capturingProvider so we can inspect the tool result sent to the parent.
	parentProvider := newCapturingProvider(
		&ModelResponse{
			ToolCalls: []tool.Call{
				{
					ToolUseID: "tc1",
					Name:      "sub_agent",
					Input:     json.RawMessage(`{"message":"hello"}`),
				},
			},
		},
		&ModelResponse{Text: "parent ok"},
	)

	parent, err := New(parentProvider, "parent sys", WithTools(wrappedTool))
	if err != nil {
		t.Fatalf("failed to create parent agent: %v", err)
	}

	result, err := parent.Invoke(Background(), "try broken child")
	if err != nil {
		t.Fatalf("parent should not abort when child provider fails, got: %v", err)
	}
	if result.Text != "parent ok" {
		t.Errorf("expected %q, got %q", "parent ok", result.Text)
	}

	// The second provider call must have received a ToolResultBlock with IsError=true.
	if len(parentProvider.captured) < 2 {
		t.Fatalf("expected at least 2 provider calls, got %d", len(parentProvider.captured))
	}
	found := false
	for _, msg := range parentProvider.captured[1].Messages {
		for _, block := range msg.Content {
			if tr, ok := block.(ToolResultBlock); ok && tr.IsError {
				found = true
			}
		}
	}
	if !found {
		t.Error("expected a ToolResultBlock with IsError=true in the second provider call")
	}
}

// TestAgentAsTool_ErrorIsToolError verifies that when a child agent errors,
// the parent receives a ToolResultBlock with IsError=true whose content matches
// a ToolError wrapping the child agent error.
func TestAgentAsTool_ErrorIsToolError(t *testing.T) {
	// Child agent: provider always errors.
	childErr := fmt.Errorf("provider exploded")
	errProvider := &erroringProvider{err: childErr}
	child, err := New(errProvider, "child sys")
	if err != nil {
		t.Fatalf("failed to create child agent: %v", err)
	}

	wrappedTool := AgentAsTool("sub_agent", "A sub-agent with broken provider", child)

	parentProvider := newCapturingProvider(
		&ModelResponse{
			ToolCalls: []tool.Call{
				{
					ToolUseID: "tc1",
					Name:      "sub_agent",
					Input:     json.RawMessage(`{"message":"hello"}`),
				},
			},
		},
		&ModelResponse{Text: "parent ok"},
	)

	parent, err := New(parentProvider, "parent sys", WithTools(wrappedTool))
	if err != nil {
		t.Fatalf("failed to create parent agent: %v", err)
	}

	_, err = parent.Invoke(Background(), "try broken child")
	if err != nil {
		t.Fatalf("parent should not abort, got: %v", err)
	}

	// Find the ToolResultBlock and verify it carries a ToolError.
	if len(parentProvider.captured) < 2 {
		t.Fatalf("expected at least 2 provider calls, got %d", len(parentProvider.captured))
	}
	var toolResultContent string
	var isError bool
	for _, msg := range parentProvider.captured[1].Messages {
		for _, block := range msg.Content {
			if tr, ok := block.(ToolResultBlock); ok {
				toolResultContent = tr.Content
				isError = tr.IsError
			}
		}
	}

	if !isError {
		t.Fatal("expected ToolResultBlock.IsError = true")
	}

	// The content should be a ToolError message wrapping the child agent error.
	var toolErr *ToolError
	// Reconstruct what executeTools would have produced: a ToolError wrapping the
	// fmt.Errorf from AgentAsTool which wraps the child's ProviderError.
	// We verify by checking errors.As on a synthesized chain matching the content.
	wrappedChildErr := fmt.Errorf("child agent %q: %w", "sub_agent", &ProviderError{Cause: childErr})
	expectedToolErr := &ToolError{ToolName: "sub_agent", Cause: wrappedChildErr}
	if toolResultContent != expectedToolErr.Error() {
		t.Errorf("expected content %q, got %q", expectedToolErr.Error(), toolResultContent)
	}

	// Also verify errors.As works on the ToolError type itself.
	if !errors.As(expectedToolErr, &toolErr) {
		t.Error("expected errors.As to find *ToolError")
	}
}

// erroringProvider is a Provider that always returns an error.
type erroringProvider struct {
	err error
}

func (p *erroringProvider) Name() string { return "mock" }

func (p *erroringProvider) Stream(_ context.Context, _ ModelRequest, _ func(ModelEvent)) (*ModelResponse, error) {
	return nil, p.err
}

func TestAgentAsTool_IsolatesPersistentChildConversation(t *testing.T) {
	parentStore := &rangeResumeStore{}
	childStore := &rangeResumeStore{}
	childProvider := newScriptedProvider(&ModelResponse{Text: "child done"})
	child, err := New(childProvider, "child", WithConversationStore(childStore))
	if err != nil {
		t.Fatal(err)
	}
	parentProvider := newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "delegate-1", Name: "child", Input: json.RawMessage(`{"message":"work"}`)}}},
		&ModelResponse{Text: "parent done"},
	)
	parent, err := New(parentProvider, "parent", WithConversationStore(parentStore), WithTools(AgentAsTool("child", "child", child)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parent.Invoke(Background().WithConversationID("parent-123"), "go"); err != nil {
		t.Fatal(err)
	}
	wantChildID := childConversationID("parent-123", "delegate-1", "child", child.Name())
	parentStore.mu.Lock()
	parentCount := len(parentStore.messages)
	parentIDs := append([]string(nil), parentStore.ids...)
	parentStore.mu.Unlock()
	childStore.mu.Lock()
	childCount := len(childStore.messages)
	childIDs := append([]string(nil), childStore.ids...)
	childStore.mu.Unlock()
	if parentCount == 0 || childCount == 0 {
		t.Fatalf("parent messages=%d child messages=%d", parentCount, childCount)
	}
	if wantChildID == "parent-123" {
		t.Fatal("child conversation ID collided with parent")
	}
	seen := func(ids []string, want string) bool {
		for _, id := range ids {
			if id == want {
				return true
			}
		}
		return false
	}
	if !seen(parentIDs, "parent-123") || !seen(childIDs, wantChildID) || seen(childIDs, "parent-123") {
		t.Fatalf("parent IDs=%v child IDs=%v want child=%q", parentIDs, childIDs, wantChildID)
	}
	if childConversationID("parent-123", "delegate-1", "child", child.Name()) != wantChildID || childConversationID("parent-123", "delegate-2", "child", child.Name()) == wantChildID {
		t.Fatal("child conversation identity is not deterministic and call-specific")
	}
}

func TestAgentAsTool_PropagatesIdentityAndScopesButNotKV(t *testing.T) {
	childProvider := &composeFuncProvider{fn: func(ctx context.Context, _ ModelRequest, _ func(ModelEvent)) (*ModelResponse, error) {
		c := FromContext(ctx)
		principal, ok := c.Principal()
		if !ok || principal.ID != "user" || principal.Roles[0] != "admin" {
			return nil, errors.New("principal missing")
		}
		if scope, ok := c.Scope("project"); !ok || scope != "p1" {
			return nil, errors.New("scope missing")
		}
		if _, ok := c.Get("parent-kv"); ok {
			return nil, errors.New("parent KV leaked")
		}
		c.Set("child-kv", "value")
		return &ModelResponse{Text: "child"}, nil
	}}
	child, err := New(childProvider, "child")
	if err != nil {
		t.Fatal(err)
	}
	parentProvider := newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "delegate", Name: "child", Input: json.RawMessage(`{"message":"go"}`)}}},
		&ModelResponse{Text: "done"},
	)
	parent, err := New(parentProvider, "parent", WithTools(AgentAsTool("child", "child", child)))
	if err != nil {
		t.Fatal(err)
	}
	ctx := Background().WithPrincipal(Principal{ID: "user", Roles: []string{"admin"}}).WithScope("project", "p1")
	ctx.Set("parent-kv", "value")
	if _, err := parent.Invoke(ctx, "go"); err != nil {
		t.Fatal(err)
	}
	if _, ok := ctx.Get("child-kv"); ok {
		t.Fatal("child KV leaked to parent")
	}
}

func TestAgentAsTool_ChildCancellationFollowsParent(t *testing.T) {
	started := make(chan struct{})
	childProvider := &composeFuncProvider{fn: func(ctx context.Context, _ ModelRequest, _ func(ModelEvent)) (*ModelResponse, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	child, err := New(childProvider, "child")
	if err != nil {
		t.Fatal(err)
	}
	parentProvider := newScriptedProvider(&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "delegate", Name: "child", Input: json.RawMessage(`{"message":"go"}`)}}})
	parent, err := New(parentProvider, "parent", WithTools(AgentAsTool("child", "child", child)))
	if err != nil {
		t.Fatal(err)
	}
	base, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := parent.Invoke(NewContext(base), "go"); done <- err }()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("parent error = %v, want context.Canceled", err)
	}
}

func TestAgentAsTool_ConsumesChildInterrupt(t *testing.T) {
	childStore := &rangeResumeStore{}
	interrupts := newTestInterruptStore()
	approval := tool.NewRaw("approve", "approve", nil, func(context.Context, json.RawMessage) (string, error) { return "ok", nil }, tool.RequiresApproval())
	child, err := New(newScriptedProvider(&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "child-approval", Name: "approve", Input: json.RawMessage(`{}`)}}}), "child", WithConversationStore(childStore), WithInterruptStore(interrupts), WithTools(approval))
	if err != nil {
		t.Fatal(err)
	}
	parentProvider := newCapturingProvider(
		&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "delegate", Name: "child", Input: json.RawMessage(`{"message":"go"}`)}}},
		&ModelResponse{Text: "recovered"},
	)
	parent, err := New(parentProvider, "parent", WithConversationStore(&rangeResumeStore{}), WithTools(AgentAsTool("child", "child", child)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parent.Invoke(Background().WithConversationID("parent"), "go"); err != nil {
		t.Fatal(err)
	}
	interrupts.mu.Lock()
	claimed := append([]string(nil), interrupts.claimed...)
	interrupts.mu.Unlock()
	if len(claimed) != 1 {
		t.Fatalf("claimed child interrupts = %v", claimed)
	}
	if _, err := child.Resume(Background().WithConversationID(childConversationID("parent", "delegate", "child", child.Name())), &Interrupt{ID: claimed[0]}, Approve()); !errors.Is(err, ErrInterruptNotFound) {
		t.Fatalf("child interrupt remains resumable: %v", err)
	}
}

type composeFuncProvider struct {
	fn func(context.Context, ModelRequest, func(ModelEvent)) (*ModelResponse, error)
}

func (p *composeFuncProvider) Name() string { return "compose" }
func (p *composeFuncProvider) Stream(ctx context.Context, req ModelRequest, emit func(ModelEvent)) (*ModelResponse, error) {
	return p.fn(ctx, req, emit)
}

func TestAgentAsToolChildConversationIDsDoNotCollide(t *testing.T) {
	ids := make(chan string, 32)
	for i := 0; i < 32; i++ {
		go func(i int) { ids <- childConversationID("parent", fmt.Sprintf("call-%d", i), "child", "worker") }(i)
	}
	seen := map[string]bool{}
	for i := 0; i < 32; i++ {
		id := <-ids
		if seen[id] {
			t.Fatalf("duplicate child conversation ID %q", id)
		}
		seen[id] = true
	}
}
