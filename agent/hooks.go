package agent

import (
	"context"
	"time"
)

// hooks is the invocation-local lifecycle dispatcher. It is immutable after
// construction and therefore safe for concurrent tool calls.
type hooks struct {
	hooks []Hooks
}

type hookFn[R any] = func(context.Context, R) context.Context

func preserveContext(current, next context.Context) context.Context {
	if next == nil {
		return current
	}
	return next
}

// pick returns the non-nil functions selected from hs, in registration order.
func pick[R any](hs []Hooks, sel func(Hooks) hookFn[R]) []hookFn[R] {
	var fns []hookFn[R]
	for _, h := range hs {
		if fn := sel(h); fn != nil {
			fns = append(fns, fn)
		}
	}
	return fns
}

// callStart runs fns in registration order, threading the context through.
func callStart[R any](ctx context.Context, fns []hookFn[R], r R) context.Context {
	for _, fn := range fns {
		ctx = preserveContext(ctx, fn(ctx, r))
	}
	return ctx
}

// callEnd runs fns in reverse registration order, threading the context through.
func callEnd[R any](ctx context.Context, fns []hookFn[R], r R) context.Context {
	for i := len(fns) - 1; i >= 0; i-- {
		ctx = preserveContext(ctx, fns[i](ctx, r))
	}
	return ctx
}

type invokeFinisher struct {
	ctx    context.Context
	fns    []hookFn[InvokeRecord]
	record InvokeRecord
}

func (f *invokeFinisher) finish(res Result, err error) context.Context {
	now := time.Now()
	r := f.record
	r.Phase = End
	r.Response = res.Text
	r.Usage = res.Usage
	r.StopReason = res.StopReason
	r.Interrupt = res.Interrupt
	r.Err = err
	r.Timestamp = now
	r.Duration = now.Sub(f.record.Timestamp)
	return callEnd(f.ctx, f.fns, r)
}

func (h *hooks) onInvokeStart(c *Context, record InvokeRecord) (*Context, *invokeFinisher) {
	record.Phase = Start
	record.Timestamp = time.Now()
	fns := pick(h.hooks, func(k Hooks) hookFn[InvokeRecord] { return k.Invoke })
	ctx := callStart(context.Context(c), fns, record)
	return c.withContext(ctx), &invokeFinisher{ctx: ctx, fns: fns, record: record}
}

type iterationFinisher struct {
	ctx    context.Context
	fns    []hookFn[IterationRecord]
	record IterationRecord
}

func (f *iterationFinisher) finish(toolCount int, isFinal bool, err error) context.Context {
	now := time.Now()
	r := f.record
	r.Phase, r.ToolCount, r.IsFinal, r.Err = End, toolCount, isFinal, err
	r.Timestamp, r.Duration = now, now.Sub(f.record.Timestamp)
	return callEnd(f.ctx, f.fns, r)
}

func (h *hooks) onIterationStart(c *Context, iteration int) (*Context, *iterationFinisher) {
	record := IterationRecord{Phase: Start, Iteration: iteration, Timestamp: time.Now()}
	fns := pick(h.hooks, func(k Hooks) hookFn[IterationRecord] { return k.Iteration })
	ctx := callStart(context.Context(c), fns, record)
	return c.withContext(ctx), &iterationFinisher{ctx: ctx, fns: fns, record: record}
}

type modelFinisher struct {
	ctx    context.Context
	fns    []hookFn[ModelCallRecord]
	record ModelCallRecord
}

func (f *modelFinisher) finish(err error, usage TokenUsage, toolCallCount int, responseText string) context.Context {
	return f.finishWith(err, usage, toolCallCount, responseText, deriveStopReason(toolCallCount, err))
}

// finishResponse records a successful model call, preferring the provider's
// reported stop reason over the derived one.
func (f *modelFinisher) finishResponse(resp *ModelResponse, responseText string) context.Context {
	return f.finishWith(nil, resp.Usage, len(resp.ToolCalls), responseText, modelStopReason(resp, nil))
}

func (f *modelFinisher) finishWith(err error, usage TokenUsage, toolCallCount int, responseText, stopReason string) context.Context {
	now := time.Now()
	r := f.record
	r.Phase, r.Err, r.Usage = End, err, usage
	r.ToolCallCount, r.ResponseText = toolCallCount, responseText
	r.StopReason = stopReason
	r.Timestamp, r.Duration = now, now.Sub(f.record.Timestamp)
	return callEnd(f.ctx, f.fns, r)
}

func (h *hooks) onModelStart(c *Context, record ModelCallRecord) (*Context, *modelFinisher) {
	record.Phase, record.Timestamp = Start, time.Now()
	fns := pick(h.hooks, func(k Hooks) hookFn[ModelCallRecord] { return k.Model })
	ctx := callStart(context.Context(c), fns, record)
	return c.withContext(ctx), &modelFinisher{ctx: ctx, fns: fns, record: record}
}

type toolFinisher struct {
	ctx    context.Context
	fns    []hookFn[ToolCallRecord]
	record ToolCallRecord
}

func (f *toolFinisher) finish(err error, output string, resultIsError, allowed bool, denialReason string) context.Context {
	now := time.Now()
	r := f.record
	r.Phase, r.Err, r.Output = End, err, output
	r.ResultIsError, r.Allowed, r.DenialReason = resultIsError, allowed, denialReason
	r.Timestamp, r.Duration = now, now.Sub(f.record.Timestamp)
	return callEnd(f.ctx, f.fns, r)
}

func (h *hooks) onToolStart(c *Context, record ToolCallRecord) (*Context, *toolFinisher) {
	record.Phase, record.Timestamp = Start, time.Now()
	fns := pick(h.hooks, func(k Hooks) hookFn[ToolCallRecord] { return k.Tool })
	c = c.withContext(callStart(context.Context(c), fns, record))
	if h.hasToolLogHook() {
		c = c.withContext(withToolLogger(c.Context, &hookToolLogger{
			hooks: h,
			ctx:   c.Context,
			base: ToolLogRecord{CallID: record.CallID, Name: record.Name,
				Principal: record.Principal, ConversationID: record.ConversationID},
		}))
	}
	return c, &toolFinisher{ctx: c.Context, fns: fns, record: record}
}

type guardrailFinisher struct {
	ctx    context.Context
	fns    []hookFn[GuardrailRecord]
	record GuardrailRecord
}

func (f *guardrailFinisher) finish(err error, output string) context.Context {
	now := time.Now()
	r := f.record
	r.Phase, r.Err, r.Output, r.Blocked = End, err, output, err != nil
	r.Timestamp, r.Duration = now, now.Sub(f.record.Timestamp)
	return callEnd(f.ctx, f.fns, r)
}

func (h *hooks) onGuardrailStart(c *Context, direction, input string) (*Context, *guardrailFinisher) {
	record := GuardrailRecord{Phase: Start, Direction: direction, Input: input, Timestamp: time.Now()}
	fns := pick(h.hooks, func(k Hooks) hookFn[GuardrailRecord] { return k.Guardrail })
	ctx := callStart(context.Context(c), fns, record)
	return c.withContext(ctx), &guardrailFinisher{ctx: ctx, fns: fns, record: record}
}

type conversationFinisher struct {
	ctx    context.Context
	fns    []hookFn[ConversationRecord]
	record ConversationRecord
}

func (f *conversationFinisher) finish(err error, messageCount int, revision uint64) context.Context {
	now := time.Now()
	r := f.record
	r.Phase, r.Err, r.MessageCount, r.Revision = End, err, messageCount, revision
	r.Timestamp, r.Duration = now, now.Sub(f.record.Timestamp)
	return callEnd(f.ctx, f.fns, r)
}

func (h *hooks) onConversationStart(c *Context, record ConversationRecord) (*Context, *conversationFinisher) {
	record.Phase, record.Timestamp = Start, time.Now()
	fns := pick(h.hooks, func(k Hooks) hookFn[ConversationRecord] { return k.Conversation })
	ctx := callStart(context.Context(c), fns, record)
	return c.withContext(ctx), &conversationFinisher{ctx: ctx, fns: fns, record: record}
}

type retrievalFinisher struct {
	ctx    context.Context
	fns    []hookFn[RetrievalRecord]
	record RetrievalRecord
}

func (f *retrievalFinisher) finish(err error, documentCount int) context.Context {
	now := time.Now()
	r := f.record
	r.Phase, r.Err, r.DocumentCount = End, err, documentCount
	r.Timestamp, r.Duration = now, now.Sub(f.record.Timestamp)
	return callEnd(f.ctx, f.fns, r)
}

func (h *hooks) onRetrievalStart(c *Context, query string) (*Context, *retrievalFinisher) {
	record := RetrievalRecord{Phase: Start, Query: query, Timestamp: time.Now()}
	fns := pick(h.hooks, func(k Hooks) hookFn[RetrievalRecord] { return k.Retrieval })
	ctx := callStart(context.Context(c), fns, record)
	return c.withContext(ctx), &retrievalFinisher{ctx: ctx, fns: fns, record: record}
}

func (h *hooks) onAttachment(c *Context, imageCount, documentCount int) context.Context {
	record := AttachmentRecord{Phase: End, ImageCount: imageCount, DocumentCount: documentCount, Timestamp: time.Now()}
	fns := pick(h.hooks, func(k Hooks) hookFn[AttachmentRecord] { return k.Attachment })
	return callEnd(context.Context(c), fns, record)
}

func (h *hooks) onLimit(c *Context, name string, limit int, err error) context.Context {
	record := LimitRecord{Phase: End, Name: name, Limit: limit, Err: err, Timestamp: time.Now()}
	fns := pick(h.hooks, func(k Hooks) hookFn[LimitRecord] { return k.Limit })
	return callEnd(context.Context(c), fns, record)
}

func (h *hooks) onToolLog(ctx context.Context, record ToolLogRecord) context.Context {
	record.Phase, record.Timestamp = End, time.Now()
	fns := pick(h.hooks, func(k Hooks) hookFn[ToolLogRecord] { return k.ToolLog })
	return callEnd(ctx, fns, record)
}

func (h *hooks) onInterrupt(c *Context, in *Interrupt) context.Context {
	principal, _ := c.Principal()
	record := InterruptRecord{
		Phase: End, InterruptID: in.ExecutionID, Type: in.Type, Principal: principal,
		ConversationID: in.ConversationID, Timestamp: time.Now(),
	}
	if in.Approval != nil {
		record.ApprovalCalls = append([]ApprovalCall(nil), in.Approval.Calls...)
	}
	if in.Input != nil {
		record.Reason, record.Question = in.Input.Reason, in.Input.Question
	}
	fns := pick(h.hooks, func(k Hooks) hookFn[InterruptRecord] { return k.Interrupt })
	return callEnd(context.Context(c), fns, record)
}

func (h *hooks) hasToolLogHook() bool {
	for _, k := range h.hooks {
		if k.ToolLog != nil {
			return true
		}
	}
	return false
}

// Model stop reasons reported in EventModelEnd lifecycle events and
// ModelCallRecord.StopReason. When the provider reports a stop reason, its
// normalized ModelStopReason value (for example "max_tokens") is used instead.
const (
	StopReasonEndTurn = "end_turn"
	StopReasonToolUse = "tool_use"
	StopReasonError   = "error"
)

// modelStopReason returns the provider-reported stop reason when present and
// otherwise derives one from the tool call count and error.
func modelStopReason(resp *ModelResponse, err error) string {
	if err == nil && resp != nil && resp.StopReason != ModelStopUnknown {
		return string(resp.StopReason)
	}
	toolCalls := 0
	if resp != nil {
		toolCalls = len(resp.ToolCalls)
	}
	return deriveStopReason(toolCalls, err)
}

func deriveStopReason(toolCallCount int, err error) string {
	if err != nil {
		return StopReasonError
	}
	if toolCallCount > 0 {
		return StopReasonToolUse
	}
	return StopReasonEndTurn
}
