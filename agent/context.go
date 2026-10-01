package agent

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"sync"
)

// Context carries stdlib context semantics plus the configuration of one agent
// invocation. It embeds context.Context, so *Context satisfies
// context.Context and can be passed to tools, middleware and providers.
//
// A Context has three layers:
//
//   - invocation config (conversation ID, identity, scopes, principal,
//     attachments, inference overrides, instructions, observability hooks).
//     Configure it with the WithX methods before invoking the agent. WithX
//     methods mutate the receiver and return the same pointer for chaining.
//   - a user key/value store (Set / Get / GetTyped). It is invocation-scoped,
//     safe for concurrent use and shared by the invocation and all of its tool
//     calls, so a tool can Set a value that a later ToolFilter or tool reads.
//   - framework runtime state (usage, event emitter, per-tool-call scratch
//     state). It is internal and never stored in the user key/value store.
//
// Each tool call receives a child Context that shares the config values, the
// user key/value store, cancellation, tracing and the event emitter, but has
// isolated per-call runtime state (call ID, widget accumulator, guard state).
type Context struct {
	context.Context

	cfg  invocationConfig
	kv   *kvStore
	rt   *invocationRuntime // nil outside a running invocation
	call *toolCallRuntime   // nil outside a tool call
}

// invocationConfig is the invocation-scoped configuration. It is copied by
// value into derived contexts; scopes is treated as copy-on-write.
type invocationConfig struct {
	conversationID  string
	images          []ImageBlock
	documents       []DocumentBlock
	inferenceConfig *InferenceConfig
	identity        string
	scopes          map[string]string
	principal       *Principal
	instructions    string
	detailedEvents  bool
	observers       []Observer
	observersSet    bool
}

// kvStore is the user key/value store shared by an invocation and its tool calls.
type kvStore struct {
	mu   sync.RWMutex
	data map[any]any
}

// invocationRuntime holds framework-internal state for one running invocation.
type invocationRuntime struct {
	mu    sync.Mutex
	usage TokenUsage
	sink  *eventSink // nil = events are discarded
}

func (r *invocationRuntime) addUsage(u TokenUsage) TokenUsage {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.usage.InputTokens += u.InputTokens
	r.usage.OutputTokens += u.OutputTokens
	r.usage.CacheReadTokens += u.CacheReadTokens
	r.usage.CacheWriteTokens += u.CacheWriteTokens
	return r.usage
}

func (r *invocationRuntime) totalUsage() TokenUsage {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.usage
}

// toolCallRuntime holds isolated scratch state for a single tool call.
type toolCallRuntime struct {
	id    string
	name  string
	async bool // true when the call runs on a worker goroutine (parallel tools)

	mu         sync.Mutex
	widgets    []WidgetBlock
	humanInput *InputInterrupt
}

func (r *toolCallRuntime) appendWidget(w WidgetBlock) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.widgets = append(r.widgets, w)
}

func (r *toolCallRuntime) drainWidgets() []WidgetBlock {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.widgets
	r.widgets = nil
	return out
}

func (r *toolCallRuntime) setHumanInput(in *InputInterrupt) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.humanInput = in
}

func (r *toolCallRuntime) takeHumanInput() *InputInterrupt {
	r.mu.Lock()
	defer r.mu.Unlock()
	in := r.humanInput
	r.humanInput = nil
	return in
}

// contextKey lets FromContext find a *Context through derived stdlib contexts.
type contextKey struct{}

// NewContext creates a new *Context wrapping the given parent.
// It panics if parent is nil.
func NewContext(parent context.Context) *Context {
	if parent == nil {
		panic("agent: cannot create Context from nil parent")
	}
	return &Context{
		Context: parent,
		kv:      &kvStore{data: make(map[any]any)},
	}
}

// Background creates a new *Context wrapping context.Background().
func Background() *Context {
	return NewContext(context.Background())
}

// Value implements context.Context. It additionally resolves the *Context
// itself so FromContext works through contexts derived with the stdlib
// helpers (context.WithValue, context.WithTimeout, ...).
func (c *Context) Value(key any) any {
	if _, ok := key.(contextKey); ok {
		return c
	}
	return c.Context.Value(key)
}

// ---------------------------------------------------------------------------
// User key/value store
// ---------------------------------------------------------------------------

// Set stores a value in the invocation-scoped key/value store. The store is
// shared by the invocation and all of its tool calls. Safe for concurrent use.
func (c *Context) Set(key, value any) {
	c.kv.mu.Lock()
	defer c.kv.mu.Unlock()
	c.kv.data[key] = value
}

// Get retrieves a value from the invocation-scoped key/value store.
// Safe for concurrent use.
func (c *Context) Get(key any) (any, bool) {
	c.kv.mu.RLock()
	defer c.kv.mu.RUnlock()
	v, ok := c.kv.data[key]
	return v, ok
}

// GetTyped retrieves a typed value from the invocation-scoped key/value store.
// Returns the zero value and false if the key doesn't exist or the value is
// not assignable to T.
func GetTyped[T any](c *Context, key any) (T, bool) {
	v, ok := c.Get(key)
	if !ok {
		var zero T
		return zero, false
	}
	t, ok := v.(T)
	return t, ok
}

// ---------------------------------------------------------------------------
// Invocation config
// ---------------------------------------------------------------------------

// ConversationID returns the conversation ID for the invocation.
func (c *Context) ConversationID() string { return c.cfg.conversationID }

// WithConversationID sets the conversation ID. When the Agent has a
// ConversationStore, every invocation requires a non-empty ID (see
// ErrConversationIDRequired). Without a store the ID has no persistence effect.
func (c *Context) WithConversationID(id string) *Context {
	c.cfg.conversationID = id
	return c
}

// Images returns the images attached to the invocation.
func (c *Context) Images() []ImageBlock { return c.cfg.images }

// WithImages attaches images to the user message of the invocation.
func (c *Context) WithImages(imgs []ImageBlock) *Context {
	c.cfg.images = imgs
	return c
}

// Documents returns the documents attached to the invocation.
func (c *Context) Documents() []DocumentBlock { return c.cfg.documents }

// WithDocuments attaches documents to the user message of the invocation.
func (c *Context) WithDocuments(docs []DocumentBlock) *Context {
	c.cfg.documents = docs
	return c
}

// InferenceConfig returns the per-invocation inference config override.
func (c *Context) InferenceConfig() *InferenceConfig { return c.cfg.inferenceConfig }

// WithInferenceConfig sets the per-invocation inference config override.
func (c *Context) WithInferenceConfig(cfg *InferenceConfig) *Context {
	c.cfg.inferenceConfig = cfg
	return c
}

// Identity returns the identity used to scope per-user state such as memory.
func (c *Context) Identity() string { return c.cfg.identity }

// WithIdentity sets the identity used to scope per-user state such as memory.
func (c *Context) WithIdentity(id string) *Context {
	c.cfg.identity = id
	return c
}

// WithScope sets a named scope value. Use named scopes when an agent needs
// several independent scopes (e.g. user preferences keyed by user ID and
// project notes keyed by project ID). Scopes never fall back to Identity.
func (c *Context) WithScope(key, value string) *Context {
	scopes := make(map[string]string, len(c.cfg.scopes)+1)
	maps.Copy(scopes, c.cfg.scopes)
	scopes[key] = value
	c.cfg.scopes = scopes // copy-on-write: derived contexts keep their snapshot
	return c
}

// Scope returns the value of a named scope and whether it was set.
func (c *Context) Scope(key string) (string, bool) {
	v, ok := c.cfg.scopes[key]
	return v, ok
}

// Instructions returns the per-invocation instructions override, or "".
func (c *Context) Instructions() string { return c.cfg.instructions }

// WithInstructions replaces the agent's configured instructions (system
// prompt) for this invocation only. Pass "" to use the agent's instructions.
func (c *Context) WithInstructions(s string) *Context {
	c.cfg.instructions = s
	return c
}

// WithDetailedEvents enables detailed lifecycle events (iteration, model and
// max-iterations events) in Stream for this invocation.
func (c *Context) WithDetailedEvents() *Context {
	c.cfg.detailedEvents = true
	return c
}

// WithObservers replaces the Agent's observers for this invocation. Passing no
// observers disables observation for the invocation.
func (c *Context) WithObservers(observers ...Observer) *Context {
	c.cfg.observers = append([]Observer(nil), observers...)
	c.cfg.observersSet = true
	return c
}

// Clone returns a new *Context with a copy of the invocation config, the same
// parent context.Context, an empty independent key/value store and no
// runtime state. Use it to fork independent sub-invocations.
func (c *Context) Clone() *Context {
	return &Context{
		Context: c.Context,
		cfg:     c.cfg,
		kv:      &kvStore{data: make(map[any]any)},
	}
}

// ---------------------------------------------------------------------------
// Internal derivation
// ---------------------------------------------------------------------------

// withContext returns a shallow copy of c with a different embedded
// context.Context. Config values, key/value store and runtime are shared.
func (c *Context) withContext(ctx context.Context) *Context {
	cp := *c
	cp.Context = ctx
	return &cp
}

// forInvocation derives the internal Context for one invocation: same config
// values and user key/value store, fresh runtime, no tool-call state.
func (c *Context) forInvocation(ctx context.Context, rt *invocationRuntime) *Context {
	return &Context{Context: ctx, cfg: c.cfg, kv: c.kv, rt: rt}
}

// forToolCall derives the child Context for one tool call.
func (c *Context) forToolCall(call *toolCallRuntime) *Context {
	cp := *c
	cp.call = call
	return &cp
}

// emit delivers an event for this context's invocation, if a stream consumer
// is attached. Tool calls running on worker goroutines enqueue the event so
// that it is yielded from the consumer's goroutine.
func (c *Context) emit(ev Event) bool {
	if c.rt == nil || c.rt.sink == nil {
		return true
	}
	if c.call != nil && c.call.async {
		c.rt.sink.enqueue(ev)
		return true
	}
	return c.rt.sink.emit(ev)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// FromContext extracts the *Context from a context.Context, including stdlib
// contexts derived from a *Context. Returns nil if none is found.
func FromContext(ctx context.Context) *Context {
	if ctx == nil {
		return nil
	}
	if c, ok := ctx.(*Context); ok {
		return c
	}
	c, _ := ctx.Value(contextKey{}).(*Context)
	return c
}

// IdentityFrom returns the invocation identity carried by ctx, or "".
func IdentityFrom(ctx context.Context) string {
	if c := FromContext(ctx); c != nil {
		return c.Identity()
	}
	return ""
}

// ScopeFrom returns the named scope value carried by ctx and whether it was
// set. It never falls back to the identity.
func ScopeFrom(ctx context.Context, key string) (string, bool) {
	if c := FromContext(ctx); c != nil {
		return c.Scope(key)
	}
	return "", false
}

// ErrNoToolCall is returned by EmitWidget when called outside a tool call.
var ErrNoToolCall = errors.New("agent: not inside a tool call")

// EmitWidget emits a WidgetBlock from inside a tool handler or middleware.
// The widget is attached to the current tool call (persisted after its
// ToolUseBlock) and delivered as an EventWidget carrying the call ID.
// Returns an error if the block is invalid or ctx is not a tool-call context.
func EmitWidget(ctx context.Context, w WidgetBlock) error {
	if err := w.Validate(); err != nil {
		return err
	}
	c := FromContext(ctx)
	if c == nil || c.call == nil {
		return ErrNoToolCall
	}
	c.call.appendWidget(w)
	c.emit(Event{Type: EventWidget, Widget: &WidgetEvent{
		CallID:  c.call.id,
		Type:    w.Type,
		Payload: cloneRaw(w.Payload),
	}})
	return nil
}

// EmitEvent emits a user-defined event onto the invocation's Stream. It is a
// no-op when ctx carries no running invocation. name should be a short,
// dot-namespaced tag (e.g. "rag.retrieved"); payload is JSON-encoded.
// Safe for concurrent use.
func EmitEvent(ctx context.Context, name string, payload any) error {
	c := FromContext(ctx)
	if c == nil || c.rt == nil {
		return nil
	}
	var raw json.RawMessage
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		raw = b
	}
	c.emit(Event{Type: EventCustom, Custom: &CustomEvent{Name: name, Payload: raw}})
	return nil
}

func cloneRaw(b json.RawMessage) json.RawMessage {
	if b == nil {
		return nil
	}
	return append(json.RawMessage(nil), b...)
}

// tokenUsageKey is the context key for cumulative token usage.
type tokenUsageKey struct{}

// WithTokenUsage attaches cumulative TokenUsage to the context. The agent loop
// sets this before calling Conversation.Save so that conversation strategies
// (e.g. token-aware summarization) can use provider-reported token counts.
func WithTokenUsage(ctx context.Context, usage TokenUsage) context.Context {
	return context.WithValue(ctx, tokenUsageKey{}, &usage)
}

// GetTokenUsage retrieves the cumulative TokenUsage from the context.
// Returns zero value and false if none is attached.
func GetTokenUsage(ctx context.Context) (TokenUsage, bool) {
	u, ok := ctx.Value(tokenUsageKey{}).(*TokenUsage)
	if !ok || u == nil {
		return TokenUsage{}, false
	}
	return *u, true
}
