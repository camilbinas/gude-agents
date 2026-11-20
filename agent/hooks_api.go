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

// WithHooks registers h. It is repeatable; all registered hooks run in
// registration order at start and reverse order at end.
func WithHooks(h Hooks) Option {
	return func(a *Runtime) error {
		a.hooks = append(a.hooks, h)
		return nil
	}
}
