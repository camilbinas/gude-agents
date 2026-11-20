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

// preserveContext threads a hook's returned context. A nil result keeps the
// current context. A hook that returns a context unrelated to current (one
// that does not share its cancellation) would otherwise drop the invocation's
// cancellation and deadline, so cancellation of current is propagated into
// the returned context.
func preserveContext(current, next context.Context) context.Context {
	if next == nil {
		return current
	}
	cd := current.Done()
	if cd == nil || next.Done() == cd {
		return next
	}
	merged, cancel := context.WithCancelCause(next)
	context.AfterFunc(current, func() { cancel(context.Cause(current)) })
	return merged
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

// safeHook calls fn and converts a panic into a nil context, so one faulty
// hook can neither crash the invocation nor skip the other hooks' End calls.
func safeHook[R any](fn hookFn[R], ctx context.Context, r R) (out context.Context) {
	defer func() {
		if recover() != nil {
			out = nil
		}
	}()
	return fn(ctx, r)
}

// callStart runs fns in registration order, threading the context through. It
// returns the merged context and, per hook, the context that hook returned
// (or received, when it returned nil or panicked); callEnd hands each hook its
// own context at End.
func callStart[R any](ctx context.Context, fns []hookFn[R], r R) (context.Context, []context.Context) {
	ctxs := make([]context.Context, len(fns))
	for i, fn := range fns {
		ctx = preserveContext(ctx, safeHook(fn, ctx, r))
		ctxs[i] = ctx
	}
	return ctx, ctxs
}

// callEnd runs fns in reverse registration order. Hook i receives ctxs[i], the
// context it returned at Start; when ctxs is nil (End-only records) the
// context is threaded through instead.
func callEnd[R any](ctx context.Context, fns []hookFn[R], ctxs []context.Context, r R) context.Context {
	for i := len(fns) - 1; i >= 0; i-- {
		in := ctx
		if i < len(ctxs) {
			in = ctxs[i]
		}
		if next := safeHook(fns[i], in, r); ctxs == nil {
			ctx = preserveContext(ctx, next)
		}
	}
	return ctx
}

type invokeFinisher struct {
	ctx    context.Context
	fns    []hookFn[InvokeRecord]
	ctxs   []context.Context
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
	return callEnd(f.ctx, f.fns, f.ctxs, r)
}

func (h *hooks) onInvokeStart(c *Context, record InvokeRecord) (*Context, *invokeFinisher) {
	record.Phase = Start
	record.Timestamp = time.Now()
	fns := pick(h.hooks, func(k Hooks) hookFn[InvokeRecord] { return k.Invoke })
	ctx, ctxs := callStart(context.Context(c), fns, record)
	return c.withContext(ctx), &invokeFinisher{ctx: ctx, fns: fns, ctxs: ctxs, record: record}
}

type iterationFinisher struct {
	ctx    context.Context
	fns    []hookFn[IterationRecord]
	ctxs   []context.Context
	record IterationRecord
}

func (f *iterationFinisher) finish(toolCount int, isFinal bool, err error) context.Context {
	now := time.Now()
	r := f.record
	r.Phase, r.ToolCount, r.IsFinal, r.Err = End, toolCount, isFinal, err
	r.Timestamp, r.Duration = now, now.Sub(f.record.Timestamp)
	return callEnd(f.ctx, f.fns, f.ctxs, r)
}

func (h *hooks) onIterationStart(c *Context, iteration int) (*Context, *iterationFinisher) {
	record := IterationRecord{Phase: Start, Iteration: iteration, Timestamp: time.Now()}
	fns := pick(h.hooks, func(k Hooks) hookFn[IterationRecord] { return k.Iteration })
	ctx, ctxs := callStart(context.Context(c), fns, record)
	return c.withContext(ctx), &iterationFinisher{ctx: ctx, fns: fns, ctxs: ctxs, record: record}
}

type modelFinisher struct {
	ctx    context.Context
	fns    []hookFn[ModelCallRecord]
	ctxs   []context.Context
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
	return callEnd(f.ctx, f.fns, f.ctxs, r)
}

func (h *hooks) onModelStart(c *Context, record ModelCallRecord) (*Context, *modelFinisher) {
	record.Phase, record.Timestamp = Start, time.Now()
	fns := pick(h.hooks, func(k Hooks) hookFn[ModelCallRecord] { return k.Model })
	ctx, ctxs := callStart(context.Context(c), fns, record)
	return c.withContext(ctx), &modelFinisher{ctx: ctx, fns: fns, ctxs: ctxs, record: record}
}

type toolFinisher struct {
	ctx    context.Context
	fns    []hookFn[ToolCallRecord]
	ctxs   []context.Context
	record ToolCallRecord
}

func (f *toolFinisher) finish(err error, output string, resultIsError, allowed bool, denialReason string) context.Context {
	now := time.Now()
	r := f.record
	r.Phase, r.Err, r.Output = End, err, output
	r.ResultIsError, r.Allowed, r.DenialReason = resultIsError, allowed, denialReason
	r.Timestamp, r.Duration = now, now.Sub(f.record.Timestamp)
	return callEnd(f.ctx, f.fns, f.ctxs, r)
}

func (h *hooks) onToolStart(c *Context, record ToolCallRecord) (*Context, *toolFinisher) {
	record.Phase, record.Timestamp = Start, time.Now()
	fns := pick(h.hooks, func(k Hooks) hookFn[ToolCallRecord] { return k.Tool })
	merged, ctxs := callStart(context.Context(c), fns, record)
	c = c.withContext(merged)
	if h.hasToolLogHook() {
		c = c.withContext(withToolLogger(c.Context, &hookToolLogger{
			hooks: h,
			ctx:   c.Context,
			base: ToolLogRecord{CallID: record.CallID, Name: record.Name,
				Principal: record.Principal, ConversationID: record.ConversationID},
		}))
	}
	return c, &toolFinisher{ctx: c.Context, fns: fns, ctxs: ctxs, record: record}
}

type guardrailFinisher struct {
	ctx    context.Context
	fns    []hookFn[GuardrailRecord]
	ctxs   []context.Context
	record GuardrailRecord
}

func (f *guardrailFinisher) finish(err error, output string) context.Context {
	now := time.Now()
	r := f.record
	r.Phase, r.Err, r.Output, r.Blocked = End, err, output, err != nil
	r.Timestamp, r.Duration = now, now.Sub(f.record.Timestamp)
	return callEnd(f.ctx, f.fns, f.ctxs, r)
}

func (h *hooks) onGuardrailStart(c *Context, direction, input string) (*Context, *guardrailFinisher) {
	record := GuardrailRecord{Phase: Start, Direction: direction, Input: input, Timestamp: time.Now()}
	fns := pick(h.hooks, func(k Hooks) hookFn[GuardrailRecord] { return k.Guardrail })
	ctx, ctxs := callStart(context.Context(c), fns, record)
	return c.withContext(ctx), &guardrailFinisher{ctx: ctx, fns: fns, ctxs: ctxs, record: record}
}

type conversationFinisher struct {
	ctx    context.Context
	fns    []hookFn[ConversationRecord]
	ctxs   []context.Context
	record ConversationRecord
}

func (f *conversationFinisher) finish(err error, messageCount int, revision uint64) context.Context {
	now := time.Now()
	r := f.record
	r.Phase, r.Err, r.MessageCount, r.Revision = End, err, messageCount, revision
	r.Timestamp, r.Duration = now, now.Sub(f.record.Timestamp)
	return callEnd(f.ctx, f.fns, f.ctxs, r)
}

func (h *hooks) onConversationStart(c *Context, record ConversationRecord) (*Context, *conversationFinisher) {
	record.Phase, record.Timestamp = Start, time.Now()
	fns := pick(h.hooks, func(k Hooks) hookFn[ConversationRecord] { return k.Conversation })
	ctx, ctxs := callStart(context.Context(c), fns, record)
	return c.withContext(ctx), &conversationFinisher{ctx: ctx, fns: fns, ctxs: ctxs, record: record}
}

type retrievalFinisher struct {
	ctx    context.Context
	fns    []hookFn[RetrievalRecord]
	ctxs   []context.Context
	record RetrievalRecord
}

func (f *retrievalFinisher) finish(err error, documentCount int) context.Context {
	now := time.Now()
	r := f.record
	r.Phase, r.Err, r.DocumentCount = End, err, documentCount
	r.Timestamp, r.Duration = now, now.Sub(f.record.Timestamp)
	return callEnd(f.ctx, f.fns, f.ctxs, r)
}

func (h *hooks) onRetrievalStart(c *Context, query string) (*Context, *retrievalFinisher) {
	record := RetrievalRecord{Phase: Start, Query: query, Timestamp: time.Now()}
	fns := pick(h.hooks, func(k Hooks) hookFn[RetrievalRecord] { return k.Retrieval })
	ctx, ctxs := callStart(context.Context(c), fns, record)
	return c.withContext(ctx), &retrievalFinisher{ctx: ctx, fns: fns, ctxs: ctxs, record: record}
}

func (h *hooks) onAttachment(c *Context, imageCount, documentCount int) context.Context {
	record := AttachmentRecord{Phase: End, ImageCount: imageCount, DocumentCount: documentCount, Timestamp: time.Now()}
	fns := pick(h.hooks, func(k Hooks) hookFn[AttachmentRecord] { return k.Attachment })
	return callEnd(context.Context(c), fns, nil, record)
}

func (h *hooks) onLimit(c *Context, name string, limit int, err error) context.Context {
	record := LimitRecord{Phase: End, Name: name, Limit: limit, Err: err, Timestamp: time.Now()}
	fns := pick(h.hooks, func(k Hooks) hookFn[LimitRecord] { return k.Limit })
	return callEnd(context.Context(c), fns, nil, record)
}

func (h *hooks) onToolLog(ctx context.Context, record ToolLogRecord) context.Context {
	record.Phase, record.Timestamp = End, time.Now()
	fns := pick(h.hooks, func(k Hooks) hookFn[ToolLogRecord] { return k.ToolLog })
	return callEnd(ctx, fns, nil, record)
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
	return callEnd(context.Context(c), fns, nil, record)
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
