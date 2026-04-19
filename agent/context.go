package agent

import (
	"context"
	"sync"
)

// InvocationContext is a concurrency-safe typed key-value store
// scoped to a single agent invocation.
// Documented in docs/invocation-context.md — update when changing API or concurrency model.
type InvocationContext struct {
	mu   sync.RWMutex
	data map[any]any
}

// invCtxKey is the context key used to store the InvocationContext.
type invCtxKey struct{}

// NewInvocationContext creates a new empty InvocationContext.
func NewInvocationContext() *InvocationContext {
	return &InvocationContext{data: make(map[any]any)}
}

// Set stores a value. Safe for concurrent use.
func (ic *InvocationContext) Set(key, value any) {
	ic.mu.Lock()
	defer ic.mu.Unlock()
	ic.data[key] = value
}

// Get retrieves a value. Safe for concurrent use.
func (ic *InvocationContext) Get(key any) (any, bool) {
	ic.mu.RLock()
	defer ic.mu.RUnlock()
	v, ok := ic.data[key]
	return v, ok
}

// WithInvocationContext attaches an InvocationContext to a Go context.
func WithInvocationContext(ctx context.Context, ic *InvocationContext) context.Context {
	return context.WithValue(ctx, invCtxKey{}, ic)
}

// GetInvocationContext retrieves the InvocationContext from a Go context.
// Returns nil if none is attached.
func GetInvocationContext(ctx context.Context) *InvocationContext {
	ic, _ := ctx.Value(invCtxKey{}).(*InvocationContext)
	return ic
}

// inferenceConfigKey is the context key for per-invocation inference config.
type inferenceConfigKey struct{}

// WithInferenceConfig attaches an InferenceConfig to a Go context for per-invocation override.
func WithInferenceConfig(ctx context.Context, cfg *InferenceConfig) context.Context {
	return context.WithValue(ctx, inferenceConfigKey{}, cfg)
}

// GetInferenceConfig retrieves the per-invocation InferenceConfig from a Go context.
// Returns nil if none is attached.
func GetInferenceConfig(ctx context.Context) *InferenceConfig {
	cfg, _ := ctx.Value(inferenceConfigKey{}).(*InferenceConfig)
	return cfg
}
