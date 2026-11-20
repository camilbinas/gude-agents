package agent

import "context"

// Hooks observes the agent lifecycle with plain functions. Set only the fields
// you need; a misspelled field is a compile error. Each function gets a Start
// and an End record. The context a hook returns at Start is passed to that
// same hook at End (other hooks' contexts are not), and also threads into the
// hooks registered after it and into the work being observed. A nil return
// keeps the incoming context. A returned context that does not derive from the
// incoming one still inherits its cancellation and deadline.
//
// A panic inside a hook is recovered and ignored: it never fails the
// invocation and never prevents other hooks' End functions from running.
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
