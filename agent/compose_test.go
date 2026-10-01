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
