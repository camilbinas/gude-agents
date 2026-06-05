package agent

import (
	"context"
	"encoding/json"
	"sync"
)

// Context carries both stdlib context semantics and invocation-scoped state.
// It embeds context.Context so *Context satisfies the context.Context interface.
type Context struct {
	context.Context

	mu              sync.RWMutex
	data            map[any]any
	usage           TokenUsage
	conversationID  string
	images          []ImageBlock
	documents       []DocumentBlock
	inferenceConfig *InferenceConfig
	eventHook       EventHook
	identifier      string
	scopes          map[string]string
	tracingHook     TracingHook
	metricsHook     MetricsHook
	loggingHook     LoggingHook

	// systemPromptOverride, when non-empty, replaces the agent's configured
	// instructions for this invocation only. Set by callers that need
	// per-request prompt selection (e.g. AgentCore A/B testing where the
	// gateway routes each session to a different configuration bundle).
	systemPromptOverride string
}

// NewContext creates a new *Context wrapping the given parent.
// It panics if parent is nil.
func NewContext(parent context.Context) *Context {
	if parent == nil {
		panic("agent: cannot create Context from nil parent")
	}
	return &Context{
		Context: parent,
		data:    make(map[any]any),
	}
}

// Background creates a new *Context wrapping context.Background().
func Background() *Context {
	return NewContext(context.Background())
}

// Set stores a value in the invocation-scoped key-value store.
// Safe for concurrent use.
func (c *Context) Set(key, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[key] = value
}

// Get retrieves a value from the invocation-scoped key-value store.
// Safe for concurrent use.
func (c *Context) Get(key any) (any, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, ok := c.data[key]
	return v, ok
}

// Usage returns the cumulative token usage for the invocation.
func (c *Context) Usage() TokenUsage {
	return c.usage
}

// ConversationID returns the conversation ID for the invocation.
func (c *Context) ConversationID() string {
	return c.conversationID
}

// Images returns the attached images for the invocation.
func (c *Context) Images() []ImageBlock {
	return c.images
}

// Documents returns the attached documents for the invocation.
func (c *Context) Documents() []DocumentBlock {
	return c.documents
}

// InferenceConfig returns the per-invocation inference config override.
func (c *Context) InferenceConfig() *InferenceConfig {
	return c.inferenceConfig
}

// EventHook returns the per-invocation event hook.
func (c *Context) EventHook() EventHook {
	return c.eventHook
}

// Identifier returns the scoping identity for memory operations.
func (c *Context) Identifier() string {
	return c.identifier
}

// WithConversationID sets the conversation ID and returns the same *Context for chaining.
func (c *Context) WithConversationID(id string) *Context {
	c.conversationID = id
	return c
}

// WithImages sets the attached images and returns the same *Context for chaining.
func (c *Context) WithImages(imgs []ImageBlock) *Context {
	c.images = imgs
	return c
}

// WithDocuments sets the attached documents and returns the same *Context for chaining.
func (c *Context) WithDocuments(docs []DocumentBlock) *Context {
	c.documents = docs
	return c
}

// WithInferenceConfig sets the per-invocation inference config and returns the same *Context for chaining.
func (c *Context) WithInferenceConfig(cfg *InferenceConfig) *Context {
	c.inferenceConfig = cfg
	return c
}

// WithEventHook sets the per-invocation event hook and returns the same *Context for chaining.
func (c *Context) WithEventHook(h EventHook) *Context {
	c.eventHook = h
	return c
}

// EmitEvent emits a user-defined event onto the active InvokeEventStream
// channel (if any). When no event stream is active — i.e. the context has
// no EventHook, or the hook does not implement CustomEventEmitter — the
// call is a no-op. Use it from inside tool handlers, middleware, or graph
// node functions to surface domain progress (e.g. "rag.retrieved",
// "score.computed") to UIs without inventing parallel channels.
//
// name should be a short, dot-namespaced tag chosen by the emitter.
// payload is JSON-marshalled; pass any value json.Marshal can handle.
// Marshal failures silently drop the event so EmitEvent never disturbs the
// agent loop.
//
// Custom events are delivered on the same channel as the agent's built-in
// events (Type=EventCustom) and obey the same back-pressure semantics.
// Safe for concurrent use.
func (c *Context) EmitEvent(name string, payload any) {
	hook := c.EventHook()
	if hook == nil {
		return
	}
	emitter, ok := hook.(CustomEventEmitter)
	if !ok {
		return
	}
	var raw json.RawMessage
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return
		}
		raw = b
	}
	emitter.OnCustomEvent(c, name, raw)
}

// WithIdentifier sets the scoping identity and returns the same *Context for chaining.
func (c *Context) WithIdentifier(id string) *Context {
	c.identifier = id
	return c
}

// WithScope sets a named scope value for multi-scope memory operations.
// Use this when an agent needs multiple independent memory scopes (e.g. user
// preferences scoped by user ID and project notes scoped by project ID).
func (c *Context) WithScope(key, value string) *Context {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.scopes == nil {
		c.scopes = make(map[string]string)
	}
	c.scopes[key] = value
	return c
}

// Scope returns the value for a named scope, or empty string if not set.
func (c *Context) Scope(key string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.scopes == nil {
		return ""
	}
	return c.scopes[key]
}

// SetScope updates a named scope value. Same as WithScope but doesn't return
// the context — use in tool handlers where chaining isn't needed.
func (c *Context) SetScope(key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.scopes == nil {
		c.scopes = make(map[string]string)
	}
	c.scopes[key] = value
}

// allScopes returns a copy of all scope key-value pairs.
// Used internally by Background_Tool dispatch to capture the originating
// *Context's scoping identity for the eventual Re_Entry_Turn.
func (c *Context) allScopes() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.scopes) == 0 {
		return nil
	}
	cp := make(map[string]string, len(c.scopes))
	for k, v := range c.scopes {
		cp[k] = v
	}
	return cp
}

// TracingHook returns the per-invocation tracing hook, or nil if none is set.
func (c *Context) TracingHook() TracingHook {
	return c.tracingHook
}

// WithTracingHook sets the per-invocation tracing hook and returns the same *Context for chaining.
func (c *Context) WithTracingHook(h TracingHook) *Context {
	c.tracingHook = h
	return c
}

// MetricsHook returns the per-invocation metrics hook, or nil if none is set.
func (c *Context) MetricsHook() MetricsHook {
	return c.metricsHook
}

// WithMetricsHook sets the per-invocation metrics hook and returns the same *Context for chaining.
func (c *Context) WithMetricsHook(h MetricsHook) *Context {
	c.metricsHook = h
	return c
}

// LoggingHook returns the per-invocation logging hook, or nil if none is set.
func (c *Context) LoggingHook() LoggingHook {
	return c.loggingHook
}

// WithLoggingHook sets the per-invocation logging hook and returns the same *Context for chaining.
func (c *Context) WithLoggingHook(h LoggingHook) *Context {
	c.loggingHook = h
	return c
}

// SystemPromptOverride returns the per-invocation system prompt override, or
// empty string if none was set. The agent uses this in preference to its
// configured instructions when non-empty.
func (c *Context) SystemPromptOverride() string {
	return c.systemPromptOverride
}

// WithSystemPromptOverride sets a per-invocation system prompt that overrides
// the agent's configured instructions for this invocation only. Use this for
// A/B testing or other per-request prompt selection. Pass empty string to
// clear the override (the agent's instructions are used as before).
func (c *Context) WithSystemPromptOverride(s string) *Context {
	c.systemPromptOverride = s
	return c
}

// FromContext extracts a *Context from a context.Context.
// Returns nil if ctx is not a *Context. Use this in tool handlers that need
// access to invocation state without risking a panic from a direct type assertion.
func FromContext(ctx context.Context) *Context {
	c, _ := ctx.(*Context)
	return c
}

// ScopeFrom extracts a named scope value from a context.Context.
// If the scope key is set, returns its value. Otherwise falls back to
// Identifier(). Returns empty string if neither is set.
func ScopeFrom(ctx context.Context, key string) string {
	c := FromContext(ctx)
	if c == nil {
		return ""
	}
	if key != "" {
		if v := c.Scope(key); v != "" {
			return v
		}
	}
	return c.Identifier()
}

// GetTyped retrieves a typed value from the invocation-scoped key-value store.
// Returns the zero value and false if the key doesn't exist or the value is not
// assignable to T. Eliminates the need for manual type assertions on Get results.
func GetTyped[T any](c *Context, key any) (T, bool) {
	v, ok := c.Get(key)
	if !ok {
		var zero T
		return zero, false
	}
	t, ok := v.(T)
	return t, ok
}

// EmitWidget emits a WidgetBlock from inside a tool handler, middleware, or
// graph node. It validates the block, appends it to the per-call widget
// accumulator (thread-safe), and delivers an EventWidget event to the active
// InvokeEventStream channel (if any).
//
// Returns a non-nil error if block.Type is empty; in that case no event is
// emitted and the accumulator is unchanged.
//
// Safe for concurrent use when parallelTools is enabled. All synchronization
// is internal to the agent package.
func (c *Context) EmitWidget(block WidgetBlock) error {
	if err := block.Validate(); err != nil {
		return err
	}
	// Append to the per-call accumulator (see widgetAccumulatorKey).
	if acc, ok := GetTyped[*widgetAccumulator](c, widgetAccumulatorKey{}); ok {
		acc.append(block)
	}
	// Deliver the event to the stream hook if present.
	hook := c.EventHook()
	if hook == nil {
		return nil
	}
	if emitter, ok := hook.(WidgetEmitter); ok {
		emitter.OnWidget(c, block)
	}
	return nil
}

// WithValue returns a new *Context that carries the given key-value pair in the
// embedded context.Context. Use this to pass values that downstream libraries
// read via ctx.Value (e.g. request IDs, trace baggage).
func (c *Context) WithValue(key, val any) *Context {
	return c.withContext(context.WithValue(c.Context, key, val))
}

// Clone returns a new *Context that shares the parent context.Context and typed
// fields (conversation ID, images, documents, inference config, event hook,
// identifier) but has an independent key-value store. Use this when forking
// parallel sub-invocations that should not share mutable KV state.
func (c *Context) Clone() *Context {
	c.mu.RLock()
	var scopesCopy map[string]string
	if c.scopes != nil {
		scopesCopy = make(map[string]string, len(c.scopes))
		for k, v := range c.scopes {
			scopesCopy[k] = v
		}
	}
	// Copy principal if set, so it's available in the clone's independent KV store.
	var principalCopy map[any]any
	if p, ok := c.data[principalKey{}]; ok {
		principalCopy = map[any]any{principalKey{}: p}
	}
	c.mu.RUnlock()
	clone := &Context{
		Context:              c.Context,
		data:                 make(map[any]any),
		conversationID:       c.conversationID,
		images:               c.images,
		documents:            c.documents,
		inferenceConfig:      c.inferenceConfig,
		eventHook:            c.eventHook,
		identifier:           c.identifier,
		scopes:               scopesCopy,
		tracingHook:          c.tracingHook,
		metricsHook:          c.metricsHook,
		loggingHook:          c.loggingHook,
		systemPromptOverride: c.systemPromptOverride,
	}
	for k, v := range principalCopy {
		clone.data[k] = v
	}
	return clone
}

// setUsage sets the cumulative token usage. This is internal to the agent loop.
func (c *Context) setUsage(u TokenUsage) {
	c.usage = u
}

// withContext returns a shallow copy of c with a different embedded context.Context.
func (c *Context) withContext(ctx context.Context) *Context {
	return &Context{
		Context:              ctx,
		data:                 c.data,
		usage:                c.usage,
		conversationID:       c.conversationID,
		images:               c.images,
		documents:            c.documents,
		inferenceConfig:      c.inferenceConfig,
		eventHook:            c.eventHook,
		identifier:           c.identifier,
		scopes:               c.scopes,
		tracingHook:          c.tracingHook,
		metricsHook:          c.metricsHook,
		loggingHook:          c.loggingHook,
		systemPromptOverride: c.systemPromptOverride,
	}
}

// tokenUsageKey is the context key for cumulative token usage.
type tokenUsageKey struct{}

// WithTokenUsage attaches cumulative TokenUsage to the context. The agent loop
// sets this before calling Conversation.Save so that conversation strategies
// (e.g. token-aware summarization) can use actual provider-reported token counts
// to decide when to trigger compaction.
func WithTokenUsage(ctx context.Context, usage TokenUsage) context.Context {
	return context.WithValue(ctx, tokenUsageKey{}, &usage)
}

// GetTokenUsage retrieves the cumulative TokenUsage from the context.
// Returns zero value and false if none is attached (e.g. Save called outside
// the agent loop).
func GetTokenUsage(ctx context.Context) (TokenUsage, bool) {
	u, ok := ctx.Value(tokenUsageKey{}).(*TokenUsage)
	if !ok || u == nil {
		return TokenUsage{}, false
	}
	return *u, true
}
