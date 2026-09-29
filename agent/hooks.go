package agent

import (
	"context"
	"time"
)

// hooks is the invocation-local lifecycle dispatcher. It is immutable after
// construction and therefore safe for concurrent tool calls.
type hooks struct {
	observers []Observer
}

func preserveContext(current, next context.Context) context.Context {
	if next == nil {
		return current
	}
	return next
}

type invokeFinisher struct {
	ctx       context.Context
	observers []InvokeObserver
	record    InvokeRecord
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
	ctx := f.ctx
	for i := len(f.observers) - 1; i >= 0; i-- {
		ctx = preserveContext(ctx, f.observers[i].ObserveInvoke(ctx, r))
	}
	return ctx
}

func (h *hooks) onInvokeStart(c *Context, record InvokeRecord) (*Context, *invokeFinisher) {
	record.Phase = Start
	record.Timestamp = time.Now()
	ctx := context.Context(c)
	var observers []InvokeObserver
	for _, candidate := range h.observers {
		if observer, ok := candidate.(InvokeObserver); ok {
			observers = append(observers, observer)
			ctx = preserveContext(ctx, observer.ObserveInvoke(ctx, record))
		}
	}
	return c.withContext(ctx), &invokeFinisher{ctx: ctx, observers: observers, record: record}
}

type iterationFinisher struct {
	ctx       context.Context
	observers []IterationObserver
	record    IterationRecord
}

func (f *iterationFinisher) finish(toolCount int, isFinal bool, err error) context.Context {
	now := time.Now()
	r := f.record
	r.Phase, r.ToolCount, r.IsFinal, r.Err = End, toolCount, isFinal, err
	r.Timestamp, r.Duration = now, now.Sub(f.record.Timestamp)
	ctx := f.ctx
	for i := len(f.observers) - 1; i >= 0; i-- {
		ctx = preserveContext(ctx, f.observers[i].ObserveIteration(ctx, r))
	}
	return ctx
}

func (h *hooks) onIterationStart(c *Context, iteration int) (*Context, *iterationFinisher) {
	record := IterationRecord{Phase: Start, Iteration: iteration, Timestamp: time.Now()}
	ctx := context.Context(c)
	var observers []IterationObserver
	for _, candidate := range h.observers {
		if observer, ok := candidate.(IterationObserver); ok {
			observers = append(observers, observer)
			ctx = preserveContext(ctx, observer.ObserveIteration(ctx, record))
		}
	}
	return c.withContext(ctx), &iterationFinisher{ctx: ctx, observers: observers, record: record}
}

type modelFinisher struct {
	ctx       context.Context
	observers []ModelObserver
	record    ModelCallRecord
}

func (f *modelFinisher) finish(err error, usage TokenUsage, toolCallCount int, responseText string) context.Context {
	now := time.Now()
	r := f.record
	r.Phase, r.Err, r.Usage = End, err, usage
	r.ToolCallCount, r.ResponseText = toolCallCount, responseText
	r.StopReason = deriveStopReason(toolCallCount, err)
	r.Timestamp, r.Duration = now, now.Sub(f.record.Timestamp)
	ctx := f.ctx
	for i := len(f.observers) - 1; i >= 0; i-- {
		ctx = preserveContext(ctx, f.observers[i].ObserveModel(ctx, r))
	}
	return ctx
}

func (h *hooks) onModelStart(c *Context, record ModelCallRecord) (*Context, *modelFinisher) {
	record.Phase, record.Timestamp = Start, time.Now()
	ctx := context.Context(c)
	var observers []ModelObserver
	for _, candidate := range h.observers {
		if observer, ok := candidate.(ModelObserver); ok {
			observers = append(observers, observer)
			ctx = preserveContext(ctx, observer.ObserveModel(ctx, record))
		}
	}
	return c.withContext(ctx), &modelFinisher{ctx: ctx, observers: observers, record: record}
}

type toolFinisher struct {
	ctx       context.Context
	observers []ToolObserver
	record    ToolCallRecord
}

func (f *toolFinisher) finish(err error, output string, resultIsError, allowed bool, denialReason string) context.Context {
	now := time.Now()
	r := f.record
	r.Phase, r.Err, r.Output = End, err, output
	r.ResultIsError, r.Allowed, r.DenialReason = resultIsError, allowed, denialReason
	r.Timestamp, r.Duration = now, now.Sub(f.record.Timestamp)
	ctx := f.ctx
	for i := len(f.observers) - 1; i >= 0; i-- {
		ctx = preserveContext(ctx, f.observers[i].ObserveTool(ctx, r))
	}
	return ctx
}

func (h *hooks) onToolStart(c *Context, record ToolCallRecord) (*Context, *toolFinisher) {
	record.Phase, record.Timestamp = Start, time.Now()
	ctx := context.Context(c)
	var observers []ToolObserver
	for _, candidate := range h.observers {
		if observer, ok := candidate.(ToolObserver); ok {
			observers = append(observers, observer)
			ctx = preserveContext(ctx, observer.ObserveTool(ctx, record))
		}
	}
	c = c.withContext(ctx)
	if h.hasToolLogObserver() {
		c = c.withContext(withToolLogger(c.Context, &observerToolLogger{
			hooks: h,
			ctx:   c.Context,
			base: ToolLogRecord{CallID: record.CallID, Name: record.Name,
				Principal: record.Principal, ConversationID: record.ConversationID},
		}))
	}
	return c, &toolFinisher{ctx: c.Context, observers: observers, record: record}
}

type guardrailFinisher struct {
	ctx       context.Context
	observers []GuardrailObserver
	record    GuardrailRecord
}

func (f *guardrailFinisher) finish(err error, output string) context.Context {
	now := time.Now()
	r := f.record
	r.Phase, r.Err, r.Output, r.Blocked = End, err, output, err != nil
	r.Timestamp, r.Duration = now, now.Sub(f.record.Timestamp)
	ctx := f.ctx
	for i := len(f.observers) - 1; i >= 0; i-- {
		ctx = preserveContext(ctx, f.observers[i].ObserveGuardrail(ctx, r))
	}
	return ctx
}

func (h *hooks) onGuardrailStart(c *Context, direction, input string) (*Context, *guardrailFinisher) {
	record := GuardrailRecord{Phase: Start, Direction: direction, Input: input, Timestamp: time.Now()}
	ctx := context.Context(c)
	var observers []GuardrailObserver
	for _, candidate := range h.observers {
		if observer, ok := candidate.(GuardrailObserver); ok {
			observers = append(observers, observer)
			ctx = preserveContext(ctx, observer.ObserveGuardrail(ctx, record))
		}
	}
	return c.withContext(ctx), &guardrailFinisher{ctx: ctx, observers: observers, record: record}
}

type conversationFinisher struct {
	ctx       context.Context
	observers []ConversationObserver
	record    ConversationRecord
}

func (f *conversationFinisher) finish(err error, messageCount int, revision uint64) context.Context {
	now := time.Now()
	r := f.record
	r.Phase, r.Err, r.MessageCount, r.Revision = End, err, messageCount, revision
	r.Timestamp, r.Duration = now, now.Sub(f.record.Timestamp)
	ctx := f.ctx
	for i := len(f.observers) - 1; i >= 0; i-- {
		ctx = preserveContext(ctx, f.observers[i].ObserveConversation(ctx, r))
	}
	return ctx
}

func (h *hooks) onConversationStart(c *Context, record ConversationRecord) (*Context, *conversationFinisher) {
	record.Phase, record.Timestamp = Start, time.Now()
	ctx := context.Context(c)
	var observers []ConversationObserver
	for _, candidate := range h.observers {
		if observer, ok := candidate.(ConversationObserver); ok {
			observers = append(observers, observer)
			ctx = preserveContext(ctx, observer.ObserveConversation(ctx, record))
		}
	}
	return c.withContext(ctx), &conversationFinisher{ctx: ctx, observers: observers, record: record}
}

type retrievalFinisher struct {
	ctx       context.Context
	observers []RetrievalObserver
	record    RetrievalRecord
}

func (f *retrievalFinisher) finish(err error, documentCount int) context.Context {
	now := time.Now()
	r := f.record
	r.Phase, r.Err, r.DocumentCount = End, err, documentCount
	r.Timestamp, r.Duration = now, now.Sub(f.record.Timestamp)
	ctx := f.ctx
	for i := len(f.observers) - 1; i >= 0; i-- {
		ctx = preserveContext(ctx, f.observers[i].ObserveRetrieval(ctx, r))
	}
	return ctx
}

func (h *hooks) onRetrievalStart(c *Context, query string) (*Context, *retrievalFinisher) {
	record := RetrievalRecord{Phase: Start, Query: query, Timestamp: time.Now()}
	ctx := context.Context(c)
	var observers []RetrievalObserver
	for _, candidate := range h.observers {
		if observer, ok := candidate.(RetrievalObserver); ok {
			observers = append(observers, observer)
			ctx = preserveContext(ctx, observer.ObserveRetrieval(ctx, record))
		}
	}
	return c.withContext(ctx), &retrievalFinisher{ctx: ctx, observers: observers, record: record}
}

func (h *hooks) onAttachment(c *Context, imageCount, documentCount int) context.Context {
	record := AttachmentRecord{Phase: End, ImageCount: imageCount, DocumentCount: documentCount, Timestamp: time.Now()}
	ctx := context.Context(c)
	for i := len(h.observers) - 1; i >= 0; i-- {
		if observer, ok := h.observers[i].(AttachmentObserver); ok {
			ctx = preserveContext(ctx, observer.ObserveAttachment(ctx, record))
		}
	}
	return ctx
}

func (h *hooks) onLimit(c *Context, name string, limit int, err error) context.Context {
	record := LimitRecord{Phase: End, Name: name, Limit: limit, Err: err, Timestamp: time.Now()}
	ctx := context.Context(c)
	for i := len(h.observers) - 1; i >= 0; i-- {
		if observer, ok := h.observers[i].(LimitObserver); ok {
			ctx = preserveContext(ctx, observer.ObserveLimit(ctx, record))
		}
	}
	return ctx
}

func (h *hooks) onToolLog(ctx context.Context, record ToolLogRecord) context.Context {
	record.Phase, record.Timestamp = End, time.Now()
	for i := len(h.observers) - 1; i >= 0; i-- {
		if observer, ok := h.observers[i].(ToolLogObserver); ok {
			ctx = preserveContext(ctx, observer.ObserveToolLog(ctx, record))
		}
	}
	return ctx
}

func (h *hooks) onInterrupt(c *Context, in *Interrupt) context.Context {
	principal, _ := c.Principal()
	record := InterruptRecord{
		Phase: End, InterruptID: in.ID, Type: in.Type, Principal: principal,
		ConversationID: in.ConversationID, Timestamp: time.Now(),
	}
	if in.Approval != nil {
		record.ApprovalCalls = append([]ApprovalCall(nil), in.Approval.Calls...)
	}
	if in.Input != nil {
		record.Reason, record.Question = in.Input.Reason, in.Input.Question
	}
	ctx := context.Context(c)
	for i := len(h.observers) - 1; i >= 0; i-- {
		if observer, ok := h.observers[i].(InterruptObserver); ok {
			ctx = preserveContext(ctx, observer.ObserveInterrupt(ctx, record))
		}
	}
	return ctx
}

func (h *hooks) hasToolLogObserver() bool {
	for _, candidate := range h.observers {
		if _, ok := candidate.(ToolLogObserver); ok {
			return true
		}
	}
	return false
}

// Model stop reasons reported in EventModelEnd lifecycle events.
const (
	StopReasonEndTurn = "end_turn"
	StopReasonToolUse = "tool_use"
	StopReasonError   = "error"
)

func deriveStopReason(toolCallCount int, err error) string {
	if err != nil {
		return StopReasonError
	}
	if toolCallCount > 0 {
		return StopReasonToolUse
	}
	return StopReasonEndTurn
}
