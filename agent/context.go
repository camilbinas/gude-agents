package agent

import (
	"context"
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

// WithIdentifier sets the scoping identity and returns the same *Context for chaining.
func (c *Context) WithIdentifier(id string) *Context {
	c.identifier = id
	return c
}

// FromContext extracts a *Context from a context.Context.
// Returns nil if ctx is not a *Context. Use this in tool handlers that need
// access to invocation state without risking a panic from a direct type assertion.
func FromContext(ctx context.Context) *Context {
	c, _ := ctx.(*Context)
	return c
}

// setUsage sets the cumulative token usage. This is internal to the agent loop.
func (c *Context) setUsage(u TokenUsage) {
	c.usage = u
}

// withContext returns a shallow copy of c with a different embedded context.Context.
func (c *Context) withContext(ctx context.Context) *Context {
	return &Context{
		Context:         ctx,
		data:            c.data,
		usage:           c.usage,
		conversationID:  c.conversationID,
		images:          c.images,
		documents:       c.documents,
		inferenceConfig: c.inferenceConfig,
		eventHook:       c.eventHook,
		identifier:      c.identifier,
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
