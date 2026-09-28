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

// ApprovalCall identifies one tool call awaiting human approval. Calls are
// always kept in provider response order so resumed tool results are stable.
type ApprovalCall struct {
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
	ToolUseID string          `json:"tool_use_id"`
}

// ApprovalRequest captures pending tool calls that need human approval.
type ApprovalRequest struct {
	// Calls contains every pending approval in provider response order.
	Calls []ApprovalCall

	// ToolName, ToolInput, and ToolUseID are legacy mirrors of the first call.
	// They remain for single-call callers and ResumeWithApproval compatibility.
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

// approvalCallsFromResults returns the approval-required calls in provider
// response order. Sentinel results are internal-only and never reach history.
func approvalCallsFromResults(results []ToolResultBlock, calls []tool.Call) []ApprovalCall {
	pending := make([]ApprovalCall, 0)
	for i, result := range results {
		if result.Content != approvalSentinel || i >= len(calls) {
			continue
		}
		call := calls[i]
		pending = append(pending, ApprovalCall{
			ToolName:  call.Name,
			ToolInput: call.Input,
			ToolUseID: call.ToolUseID,
		})
	}
	return pending
}

func copyApprovalCalls(calls []ApprovalCall) []ApprovalCall {
	out := make([]ApprovalCall, len(calls))
	for i, call := range calls {
		out[i] = call
		if call.ToolInput != nil {
			out[i].ToolInput = append(json.RawMessage(nil), call.ToolInput...)
		}
	}
	return out
}

func setLegacyApprovalFields(ar *ApprovalRequest) {
	if len(ar.Calls) == 0 {
		return
	}
	first := ar.Calls[0]
	ar.ToolName = first.ToolName
	ar.ToolInput = first.ToolInput
	ar.ToolUseID = first.ToolUseID
}

// ResumeWithApproval continues a single-call approval request. It deliberately
// uses the legacy scalar fields even when Calls is populated, so existing
// callers retain their original behavior. Batch requests must use
// ResumeWithApprovals.
func (a *Agent) ResumeWithApproval(c *Context, ar *ApprovalRequest, decision tool.Decision, cb StreamCallback) error {
	if ar == nil {
		return fmt.Errorf("approval resume: nil request")
	}
	if len(ar.Calls) > 1 {
		return fmt.Errorf("approval resume: request contains %d calls; use ResumeWithApprovals", len(ar.Calls))
	}
	if ar.ToolName == "" || ar.ToolUseID == "" {
		return fmt.Errorf("approval resume: empty legacy approval call")
	}
	return a.resumeWithApprovalCalls(c, ar, []ApprovalCall{{
		ToolName: ar.ToolName, ToolInput: ar.ToolInput, ToolUseID: ar.ToolUseID,
	}}, map[string]tool.Decision{ar.ToolUseID: decision}, cb)
}

// ResumeWithApprovals continues an invocation after every pending call has an
// explicit human decision. Decisions must exactly match Calls by ToolUseID.
func (a *Agent) ResumeWithApprovals(c *Context, ar *ApprovalRequest, decisions map[string]tool.Decision, cb StreamCallback) error {
	if ar == nil {
		return fmt.Errorf("approval resume: nil request")
	}
	if len(ar.Calls) == 0 {
		return fmt.Errorf("approval resume: empty approval calls")
	}
	if len(decisions) == 0 {
		return fmt.Errorf("approval resume: empty decisions")
	}
	seen := make(map[string]struct{}, len(ar.Calls))
	for _, call := range ar.Calls {
		if call.ToolUseID == "" {
			return fmt.Errorf("approval resume: empty tool use ID")
		}
		if _, ok := seen[call.ToolUseID]; ok {
			return fmt.Errorf("approval resume: duplicate tool use ID %q", call.ToolUseID)
		}
		seen[call.ToolUseID] = struct{}{}
		if _, ok := decisions[call.ToolUseID]; !ok {
			return fmt.Errorf("approval resume: missing decision for tool use ID %q", call.ToolUseID)
		}
	}
	for id := range decisions {
		if _, ok := seen[id]; !ok {
			return fmt.Errorf("approval resume: unexpected decision for tool use ID %q", id)
		}
	}
	return a.resumeWithApprovalCalls(c, ar, ar.Calls, decisions, cb)
}

func (a *Agent) resumeWithApprovalCalls(c *Context, ar *ApprovalRequest, calls []ApprovalCall, decisions map[string]tool.Decision, cb StreamCallback) error {
	if c == nil {
		return fmt.Errorf("approval resume: nil context")
	}
	messages := append([]Message(nil), ar.Messages...)

	convID := ar.ConversationID
	if convID == "" {
		convID = resolveConversationID(c, a.conversationID)
	}

	// Resolve all allowed tools before executing any handler, so a malformed
	// request cannot partially execute a batch.
	resolvedTools := make(map[string]tool.Tool, len(calls))
	for _, call := range calls {
		if !decisions[call.ToolUseID].Allow {
			continue
		}
		a.toolsMu.RLock()
		t, ok := a.tools[call.ToolName]
		a.toolsMu.RUnlock()
		if !ok {
			return fmt.Errorf("approval resume: tool %q not found", call.ToolName)
		}
		resolvedTools[call.ToolUseID] = t
	}

	results := make([]ContentBlock, 0, len(calls))
	for _, call := range calls {
		decision := decisions[call.ToolUseID]
		if decision.Allow {
			results = append(results, a.executeApprovedTool(c, resolvedTools[call.ToolUseID], &ApprovalRequest{
				ToolName: call.ToolName, ToolInput: call.ToolInput, ToolUseID: call.ToolUseID,
			}, convID))
			continue
		}
		if a.auditHook != nil {
			p, _ := GetTyped[Principal](c, principalKey{})
			a.auditHook.OnToolCall(AuditRecord{
				Event:          AuditEventToolCall,
				Principal:      p,
				ToolName:       call.ToolName,
				ToolInput:      inputForAudit(call.ToolInput, a.auditCaptureContent),
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
		results = append(results, ToolResultBlock{
			ToolUseID: call.ToolUseID,
			Content:   denialResultJSON(call.ToolName, reason),
			IsError:   true,
		})
	}
	messages = append(messages, Message{Role: RoleUser, Content: results})

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

// ResumeWithApprovalsInvoke is a convenience wrapper over ResumeWithApprovals
// that collects streamed chunks into a single string.
func (a *Agent) ResumeWithApprovalsInvoke(c *Context, ar *ApprovalRequest, decisions map[string]tool.Decision) (string, error) {
	var result string
	err := a.ResumeWithApprovals(c, ar, decisions, func(chunk string) {
		result += chunk
	})
	return result, err
}

func approvalStoreKey(convID string) string {
	return "approval:" + convID
}
