package agent

import "context"

// Hooks observes the agent lifecycle with plain functions. Set only the fields
// you need; a misspelled field is a compile error. Each function gets a Start
// and an End record; the context it returns at Start is passed to End.
type Hooks struct {
	Invoke       func(context.Context, InvokeRecord) context.Context
	Iteration    func(context.Context, IterationRecord) context.Context
	Model        func(context.Context, ModelCallRecord) context.Context
	Tool         func(context.Context, ToolCallRecord) context.Context
	Guardrail    func(context.Context, GuardrailRecord) context.Context
	Conversation func(context.Context, ConversationRecord) context.Context
	Retrieval    func(context.Context, RetrievalRecord) context.Context
	Attachment   func(context.Context, AttachmentRecord) context.Context
	Limit        func(context.Context, LimitRecord) context.Context
	ToolLog      func(context.Context, ToolLogRecord) context.Context
	Interrupt    func(context.Context, InterruptRecord) context.Context
}

// WithHooks registers h. It is repeatable and mixes with WithObserver; all run
// in registration order at start and reverse order at end.
func WithHooks(h Hooks) Option {
	return func(a *Runtime) error {
		a.observers = append(a.observers, hooksObserver{h})
		return nil
	}
}

// hooksObserver adapts Hooks to the observer capability interfaces.
type hooksObserver struct{ h Hooks }

func hookCall[R any](fn func(context.Context, R) context.Context, ctx context.Context, r R) context.Context {
	if fn == nil {
		return ctx
	}
	return fn(ctx, r)
}

func (o hooksObserver) ObserveInvoke(ctx context.Context, r InvokeRecord) context.Context {
	return hookCall(o.h.Invoke, ctx, r)
}

func (o hooksObserver) ObserveIteration(ctx context.Context, r IterationRecord) context.Context {
	return hookCall(o.h.Iteration, ctx, r)
}

func (o hooksObserver) ObserveModel(ctx context.Context, r ModelCallRecord) context.Context {
	return hookCall(o.h.Model, ctx, r)
}

func (o hooksObserver) ObserveTool(ctx context.Context, r ToolCallRecord) context.Context {
	return hookCall(o.h.Tool, ctx, r)
}

func (o hooksObserver) ObserveGuardrail(ctx context.Context, r GuardrailRecord) context.Context {
	return hookCall(o.h.Guardrail, ctx, r)
}

func (o hooksObserver) ObserveConversation(ctx context.Context, r ConversationRecord) context.Context {
	return hookCall(o.h.Conversation, ctx, r)
}

func (o hooksObserver) ObserveRetrieval(ctx context.Context, r RetrievalRecord) context.Context {
	return hookCall(o.h.Retrieval, ctx, r)
}

func (o hooksObserver) ObserveAttachment(ctx context.Context, r AttachmentRecord) context.Context {
	return hookCall(o.h.Attachment, ctx, r)
}

func (o hooksObserver) ObserveLimit(ctx context.Context, r LimitRecord) context.Context {
	return hookCall(o.h.Limit, ctx, r)
}

func (o hooksObserver) ObserveToolLog(ctx context.Context, r ToolLogRecord) context.Context {
	return hookCall(o.h.ToolLog, ctx, r)
}

func (o hooksObserver) ObserveInterrupt(ctx context.Context, r InterruptRecord) context.Context {
	return hookCall(o.h.Interrupt, ctx, r)
}
