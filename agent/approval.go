package agent

import (
	"encoding/json"
	"fmt"
	"time"

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
		toolResult = a.executeApprovedTool(c, t, ar, convID)
	} else {
		if a.auditHook != nil {
			p, _ := GetTyped[Principal](c, principalKey{})
			a.auditHook.OnToolCall(AuditRecord{
				Event:          AuditEventToolCall,
				Principal:      p,
				ToolName:       ar.ToolName,
				ToolInput:      inputForAudit(ar.ToolInput, a.auditCaptureContent),
				Allowed:        false,
				DenialReason:   DenialReasonToolApprovalDenied,
				ConversationID: convID,
				Timestamp:      time.Now(),
			})
		}
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
	usage, _, err := a.runLoop(c, convID, messages, 0, a.instructionsFor(c), mergedInferenceCfg, cb, &h, nil, a.cachingEnabled)

	c.setUsage(usage)

	if err == nil && a.handoffStore != nil && convID != "" {
		if err := a.handoffStore.DeleteHandoff(c, approvalStoreKey(convID)); err != nil {
			return fmt.Errorf("delete approval handoff: %w", err)
		}
	}

	return err
}

// executeApprovedTool reruns the tool execution checks after human approval,
// intentionally skipping only the approval gate that has already been satisfied.
func (a *Agent) executeApprovedTool(c *Context, t tool.Tool, ar *ApprovalRequest, convID string) ToolResultBlock {
	result := ToolResultBlock{ToolUseID: ar.ToolUseID}

	if p, ok := GetTyped[Principal](c, principalKey{}); ok && !t.AllowedWithAttrs(p.Roles, p.Attrs) {
		reason := "caller does not have the required role"
		result.Content = denialResultJSON(ar.ToolName, reason)
		result.IsError = true
		return result
	}

	if err := ValidateToolInput(t.Spec.InputSchema, ar.ToolInput); err != nil {
		toolErr := &ToolError{ToolName: ar.ToolName, Cause: err}
		result.Content = toolErr.Error()
		result.IsError = true
		return result
	}

	// Match normal execution by giving the approved call fresh per-call state.
	toolC := c
	toolC.Set(widgetAccumulatorKey{}, &widgetAccumulator{})
	toolC.Set(guardDenialKey{}, nil)

	var richOutput *tool.Output
	handler := ChainMiddleware(
		func(c *Context, toolName string, input json.RawMessage) (string, error) {
			if t.Guard != nil {
				decision, err := t.Guard(c, input)
				if err != nil {
					reason := err.Error()
					denial := guardDenialState{Tool: toolName, Reason: reason, Result: denialResultJSON(toolName, reason)}
					c.Set(guardDenialKey{}, denial)
					return denial.Result, nil
				}
				if !decision.Allow {
					denial := guardDenialState{Tool: toolName, Reason: decision.Reason, Result: denialResultJSON(toolName, decision.Reason)}
					c.Set(guardDenialKey{}, denial)
					return denial.Result, nil
				}
			}
			if t.RichHandler != nil {
				var err error
				richOutput, err = t.RichHandler(c, input)
				if err != nil {
					return "", err
				}
				if richOutput == nil {
					return "", fmt.Errorf("rich tool %q returned nil output", toolName)
				}
				return richOutput.Text, nil
			}
			if t.Handler == nil {
				return "", fmt.Errorf("tool %q has no handler", toolName)
			}
			return t.Handler(c, input)
		},
		append([]Middleware{}, a.middlewares...)...,
	)

	out, err := handler(toolC, ar.ToolName, ar.ToolInput)
	if denial, ok := GetTyped[guardDenialState](toolC, guardDenialKey{}); ok {
		result.Content = denial.Result
		result.IsError = true
		return result
	}
	if err != nil {
		toolErr := &ToolError{ToolName: ar.ToolName, Cause: err}
		result.Content = toolErr.Error()
		result.IsError = true
		return result
	}

	result.Content = out
	if richOutput != nil {
		for _, image := range richOutput.Images {
			result.Images = append(result.Images, ImageBlock{Source: ImageSource{
				Data: image.Data, Base64: image.Base64, URL: image.URL, MIMEType: image.MIMEType,
			}})
		}
	}
	return result
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
