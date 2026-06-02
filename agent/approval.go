package agent

import (
	"encoding/json"
	"fmt"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// approvalKey is the Context key for storing the pending ApprovalRequest.
type approvalKey struct{}

const approvalSentinel = "__approval_required__"

// ApprovalRequest captures the pending tool call that needs human approval.
type ApprovalRequest struct {
	ToolName       string
	ToolInput      json.RawMessage
	ToolUseID      string
	ConversationID string
	// Messages is the full conversation snapshot at the point of the pause.
	Messages []Message
	// NodeName is set by the graph layer to scope the approval to the correct node.
	NodeName string
}

// GetApprovalRequest extracts the ApprovalRequest from a *Context.
// Returns nil, false if no approval was requested.
func GetApprovalRequest(c *Context) (*ApprovalRequest, bool) {
	if c == nil {
		return nil, false
	}
	v, ok := c.Get(approvalKey{})
	if !ok {
		return nil, false
	}
	ar, ok := v.(*ApprovalRequest)
	return ar, ok
}

func isApprovalResult(results []ToolResultBlock) bool {
	for _, r := range results {
		if r.Content == approvalSentinel {
			return true
		}
	}
	return false
}

// ResumeWithApproval continues an invocation after a human approves or denies a
// pending tool call. On allow, the tool handler runs before re-entering the loop.
// On deny, a structured denial result is injected and the loop continues without
// running the handler.
func (a *Agent) ResumeWithApproval(c *Context, ar *ApprovalRequest, decision tool.Decision, cb StreamCallback) error {
	messages := make([]Message, len(ar.Messages))
	copy(messages, ar.Messages)

	convID := ar.ConversationID
	if convID == "" {
		convID = resolveConversationID(c, a.conversationID)
	}

	var toolResult ToolResultBlock

	if decision.Allow {
		a.toolsMu.RLock()
		t, ok := a.tools[ar.ToolName]
		a.toolsMu.RUnlock()
		if !ok {
			return fmt.Errorf("approval resume: tool %q not found", ar.ToolName)
		}
		allMiddleware := append([]Middleware{}, a.middlewares...)
		handler := ChainMiddleware(
			func(c *Context, toolName string, input json.RawMessage) (string, error) {
				return t.Handler(c, input)
			},
			allMiddleware...,
		)
		out, err := handler(c, ar.ToolName, ar.ToolInput)
		if err != nil {
			toolErr := &ToolError{ToolName: ar.ToolName, Cause: err}
			toolResult = ToolResultBlock{ToolUseID: ar.ToolUseID, Content: toolErr.Error(), IsError: true}
		} else {
			toolResult = ToolResultBlock{ToolUseID: ar.ToolUseID, Content: out}
		}
	} else {
		reason := decision.Reason
		if reason == "" {
			reason = "request denied by human reviewer"
		}
		toolResult = ToolResultBlock{
			ToolUseID: ar.ToolUseID,
			Content:   denialResultJSON(ar.ToolName, reason),
			IsError:   true,
		}
	}

	messages = append(messages, Message{
		Role:    RoleUser,
		Content: []ContentBlock{toolResult},
	})

	if a.backgroundRegistry != nil && a.conversation != nil && convID != "" {
		m := a.backgroundRegistry.lockFor(convID)
		m.Lock()
		defer m.Unlock()
	}

	mergedInferenceCfg := mergeInferenceConfig(a.inferenceConfig, c.InferenceConfig())
	if err := validateInferenceConfig(mergedInferenceCfg); err != nil {
		return fmt.Errorf("inference config: %w", err)
	}

	h := a.hooks(c)
	usage, _, err := a.runLoop(c, convID, messages, 0, a.instructionsFor(c), mergedInferenceCfg, cb, &h, nil)

	c.setUsage(usage)

	if err == nil && a.handoffStore != nil && convID != "" {
		_ = a.handoffStore.DeleteHandoff(c, approvalStoreKey(convID))
	}

	return err
}

// ResumeWithApprovalInvoke is a convenience wrapper over ResumeWithApproval
// that collects streamed chunks into a single string.
func (a *Agent) ResumeWithApprovalInvoke(c *Context, ar *ApprovalRequest, decision tool.Decision) (string, error) {
	var result string
	err := a.ResumeWithApproval(c, ar, decision, func(chunk string) {
		result += chunk
	})
	return result, err
}

func approvalStoreKey(convID string) string {
	return "approval:" + convID
}
