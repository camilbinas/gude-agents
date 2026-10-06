package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// toolOutcomeKind classifies exactly one durable result for a tool call.
// Only toolOutcomeDefinitive can become a canonical ToolResult block.
type toolOutcomeKind uint8

const (
	toolOutcomeDefinitive toolOutcomeKind = iota
	toolOutcomePendingApproval
	toolOutcomeDeferred
	toolOutcomeUnknown
	toolOutcomeCoordinationFailure
)

// toolOutcome is the result of running one tool call through the pipeline.
type toolOutcome struct {
	kind       toolOutcomeKind
	result     ToolResultBlock
	widgets    []WidgetBlock
	humanInput *InputInterrupt // set when the call requested human input
	err        error           // terminal recovery/coordinator error; never model-visible
}

// executeBatch runs a batch of tool calls through the canonical pipeline, in
// parallel unless disabled via WithSequentialTools. decisions, when non-nil,
// carries the human approval decision for each call of a resumed approval
// interrupt.
func (r *run) executeBatch(c *Context, calls []tool.Call, available map[string]tool.Tool, decisions []*tool.Decision) []toolOutcome {
	outcomes := make([]toolOutcome, len(calls))
	exec := func(i int, async bool) {
		var d *tool.Decision
		if decisions != nil {
			d = decisions[i]
		}
		t, ok := available[calls[i].Name]
		outcomes[i] = r.executeCall(c, calls[i], t, ok, d, async)
	}
	run := func(indices []int) {
		if r.a.parallelTools && len(indices) > 1 {
			var sink *eventSink
			if c.rt != nil {
				sink = c.rt.sink
			}
			runParallel(sink, len(indices), func(i int) { exec(indices[i], true) })
			return
		}
		for _, i := range indices {
			if err := c.Err(); err != nil {
				outcomes[i] = toolOutcome{result: ToolResultBlock{
					ToolUseID: calls[i].ToolUseID, Content: "not executed: " + err.Error(), IsError: true,
				}}
				continue
			}
			exec(i, false)
		}
	}

	// Initial approval batches preflight approval-configured calls through the
	// normal authorization/schema/guard pipeline before any sibling handler is
	// allowed to run. Resume supplies decisions and executes the full batch.
	if decisions == nil {
		var approvals, remaining []int
		for i, call := range calls {
			if t, ok := available[call.Name]; ok && t.NeedsApproval() {
				approvals = append(approvals, i)
			} else {
				remaining = append(remaining, i)
			}
		}
		if len(approvals) > 0 {
			run(approvals)
			for _, i := range approvals {
				if outcomes[i].kind == toolOutcomePendingApproval {
					for _, sibling := range remaining {
						outcomes[sibling] = toolOutcome{kind: toolOutcomeDeferred}
					}
					return outcomes
				}
			}
			run(remaining)
			return outcomes
		}
	}

	all := make([]int, len(calls))
	for i := range calls {
		all[i] = i
	}
	run(all)
	return outcomes
}

// executeCall is the single tool pipeline used by normal and resumed
// execution: lookup, RBAC/ABAC, schema validation, guard, approval gate
// (skipped when already approved), middleware, handler selection
// (plain / rich / background), widgets, audit, hooks and result conversion.
//
// decision is nil for normal execution. For a resumed approval it carries the
// human decision: Allow satisfies the approval gate, Deny short-circuits.
func (r *run) executeCall(parent *Context, tc tool.Call, t tool.Tool, found bool, decision *tool.Decision, async bool) toolOutcome {
	a := r.a
	callRT := &toolCallRuntime{id: tc.ToolUseID, name: tc.Name, async: async}
	if durable, ok := r.toolExecution(tc.ToolUseID); ok {
		callRT.idempotencyKey = durable.IdempotencyKey
		callRT.recoveryReplay = r.recovering && durable.Status == ToolExecutionInFlight
	}
	callC := parent.forToolCall(callRT)
	out := toolOutcome{kind: toolOutcomeDefinitive, result: ToolResultBlock{ToolUseID: tc.ToolUseID}}

	started := false
	emitStart := func() {
		started = true
		callC.emit(Event{Type: EventToolStart, Tool: &ToolEvent{CallID: tc.ToolUseID, Name: tc.Name, Input: cloneRaw(tc.Input)}})
	}
	emitEnd := func(output string, info *ErrorInfo, d time.Duration) {
		if !started {
			emitStart()
		}
		callC.emit(Event{Type: EventToolEnd, Tool: &ToolEvent{CallID: tc.ToolUseID, Name: tc.Name, Output: output, Error: info, Duration: d}})
	}
	fail := func(content, code string, d time.Duration) toolOutcome {
		out.result.Content = content
		out.result.IsError = true
		emitEnd("", &ErrorInfo{Code: code, Message: content}, d)
		return out
	}

	principal, hasPrincipal := parent.Principal()
	toolC, tf := r.h.onToolStart(callC, ToolCallRecord{
		CallID: tc.ToolUseID, Name: tc.Name, Input: cloneRaw(tc.Input),
		Principal: principal, ConversationID: r.convID, Allowed: true,
		IdempotencyKey: callRT.idempotencyKey, ReplaySafe: found && t.ReplaySafe(), RecoveryReplay: callRT.recoveryReplay,
	})
	begin := time.Now()

	// 1. Lookup.
	if !found {
		toolErr := fmt.Errorf("unknown tool: %s", tc.Name)
		tf.finish(toolErr, "", true, true, "")
		return fail(toolErr.Error(), ErrorCodeUnknownTool, time.Since(begin))
	}

	// Human denial of a resumed approval: no handler, no guard.
	if decision != nil && !decision.Allow {
		reason := decision.Reason
		if reason == "" {
			reason = "request denied by human reviewer"
		}
		denyErr := fmt.Errorf("%w: tool=%q reason=%q", ErrToolCallDenied, tc.Name, reason)
		tf.finish(denyErr, "", true, false, DenialReasonToolApprovalDenied)
		return fail(denialResultJSON(tc.Name, reason), ErrorCodeToolDenied, time.Since(begin))
	}

	// 2. RBAC / ABAC — defense in depth against calls that bypassed filterTools.
	if hasPrincipal && !t.AllowedWithAttrs(principal.Roles, principal.Attrs) {
		denialReason := DenialReasonAttrCondition
		if !t.RolesAllowed(principal.Roles) {
			denialReason = DenialReasonRolePolicy
		}
		reason := "caller does not have the required role"
		denyErr := fmt.Errorf("%w: tool=%q reason=%q", ErrToolCallDenied, tc.Name, reason)
		dur := time.Since(begin)
		tf.finish(denyErr, "", true, false, denialReason)
		return fail(denialResultJSON(tc.Name, reason), ErrorCodeToolDenied, dur)
	}

	// 3. Schema validation.
	if err := ValidateToolInput(t.Spec.InputSchema, tc.Input); err != nil {
		dur := time.Since(begin)
		tf.finish(err, "", true, true, "")
		return fail((&ToolError{ToolName: tc.Name, Cause: err}).Error(), ErrorCodeInvalidInput, dur)
	}

	// 4. Guard.
	if t.Guard != nil {
		d, err := t.Guard(toolC, tc.Input)
		if err != nil {
			d = tool.Deny(err.Error())
		}
		if !d.Allow {
			denyErr := fmt.Errorf("%w: tool=%q reason=%q", ErrToolCallDenied, tc.Name, d.Reason)
			dur := time.Since(begin)
			tf.finish(denyErr, "", true, false, DenialReasonGuard)
			return fail(denialResultJSON(tc.Name, d.Reason), ErrorCodeToolDenied, dur)
		}
	}

	// 5. Approval gate.
	if t.NeedsApproval() && decision == nil {
		tf.finish(nil, "approval required", false, true, "")
		out.kind = toolOutcomePendingApproval
		return out
	}

	// 6. Persist Ready -> InFlight before the handler sees a side effect.
	key, recoveryReplay, claimErr := r.beginToolExecution(tc.ToolUseID, tc.Name)
	if claimErr != nil {
		tf.finish(claimErr, "", true, false, "")
		out.err = claimErr
		if errors.Is(claimErr, ErrToolExecutionUncertain) {
			out.kind = toolOutcomeUnknown
			emitEnd("", &ErrorInfo{Code: ErrorCodeOutcomeUnknown, Message: claimErr.Error()}, time.Since(begin))
		} else {
			// A lost Ready→InFlight claim is a coordination failure, not an
			// execution result. The caller must retain the active batch for a
			// winner/recovery to resolve; never acknowledge it to the model.
			out.kind = toolOutcomeCoordinationFailure
			emitEnd("", &ErrorInfo{Code: ErrorCodeOutcomeUnknown, Message: claimErr.Error()}, time.Since(begin))
		}
		return out
	}
	callRT.idempotencyKey, callRT.recoveryReplay = key, recoveryReplay

	// 7. Middleware + handler selection.
	// The event payload intentionally does not expose the idempotency key;
	// observers receive it through ToolCallRecord instead.
	emitStart()
	inner := func(hc context.Context, call ToolCall) (ToolResult, error) {
		execC := FromContext(hc)
		if execC == nil {
			execC = toolC
		}
		switch {
		case t.IsBackground():
			text, err := r.dispatchBackground(execC, t, tc, call.Input)
			return ToolResult{Text: text}, err
		case t.RichHandler != nil:
			o, err := t.RichHandler(hc, call.Input)
			if err != nil {
				return ToolResult{}, err
			}
			if o == nil {
				return ToolResult{}, fmt.Errorf("rich tool %q returned nil output", call.Name)
			}
			result := ToolResult{Text: o.Text}
			for _, img := range o.Images {
				result.Images = append(result.Images, ImageBlock{Source: ImageSource{
					Data: img.Data, Base64: img.Base64, URL: img.URL, MIMEType: img.MIMEType,
				}})
			}
			return result, nil
		case t.Handler != nil:
			text, err := t.Handler(hc, call.Input)
			return ToolResult{Text: text}, err
		default:
			return ToolResult{}, fmt.Errorf("tool %q has no handler", call.Name)
		}
	}
	handler := ChainMiddleware(inner, a.middlewares...)
	handlerContext := toolC.withContext(tool.WithExecutionMetadata(toolC.Context, key, recoveryReplay))
	result, err := handler(handlerContext, ToolCall{ID: tc.ToolUseID, Name: tc.Name, Input: cloneRaw(tc.Input)})
	dur := time.Since(begin)

	// 7. Widgets.
	out.widgets = callRT.drainWidgets()

	if err != nil {
		if tool.IsOutcomeUnknown(err) {
			uncertain := &ToolExecutionUncertainError{
				ExecutionID: r.c.ExecutionID(), CallID: tc.ToolUseID, ToolName: tc.Name, IdempotencyKey: key,
			}
			tf.finish(uncertain, "", true, true, "")
			out.kind = toolOutcomeUnknown
			out.err = uncertain
			emitEnd("", &ErrorInfo{Code: ErrorCodeOutcomeUnknown, Message: err.Error()}, dur)
			return out
		}
		tf.finish(err, "", true, true, "")
		out.result.Content = (&ToolError{ToolName: tc.Name, Cause: err}).Error()
		out.result.IsError = true
		emitEnd("", &ErrorInfo{Code: ErrorCodeTool, Message: out.result.Content}, dur)
		return out
	}

	// 8. Observer completion and result conversion.
	tf.finish(nil, result.Text, result.IsError, true, "")
	out.result.Content = result.Text
	out.result.Images = append(out.result.Images, result.Images...)
	out.result.IsError = result.IsError
	out.humanInput = callRT.takeHumanInput()
	var info *ErrorInfo
	if result.IsError {
		info = &ErrorInfo{Code: ErrorCodeTool, Message: result.Text}
	}
	emitEnd(result.Text, info, dur)
	return out
}

// dispatchBackground hands a Background_Tool call to the background registry
// and returns its ack string synchronously.
func (r *run) dispatchBackground(c *Context, t tool.Tool, tc tool.Call, input json.RawMessage) (string, error) {
	a := r.a
	if a.conversation == nil || a.backgroundRegistry == nil {
		return "", fmt.Errorf("background tool requires a conversation store")
	}
	if r.convID == "" {
		return "", fmt.Errorf("background tool requires a conversation id")
	}
	cfg := c.cfg
	cfg.images, cfg.documents = nil, nil
	if cfg.principal != nil {
		// Deep-copy the principal before crossing into the detached
		// background goroutine: cfg.principal currently aliases the
		// invocation's principal, and the background handler may run
		// concurrently with the rest of this invocation.
		cloned := clonePrincipal(*cfg.principal)
		cfg.principal = &cloned
	}
	if err := a.backgroundRegistry.dispatch(backgroundDispatch{
		toolName:       tc.Name,
		toolUseID:      tc.ToolUseID,
		conversationID: r.convID,
		cfg:            cfg,
		rawInput:       input,
		handler:        t.Handler,
		ack:            t.Ack(),
		dispatchedAt:   time.Now(),
	}); err != nil {
		return "", err
	}
	return t.Ack(), nil
}
