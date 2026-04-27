package agent

import (
	"context"
	"sync"
)

// InvocationContext is a concurrency-safe typed key-value store
// scoped to a single agent invocation.
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

// imagesKey is the context key for per-invocation image slices.
type imagesKey struct{}

// WithImages attaches a slice of ImageBlock values to the context for the
// current invocation. Pass nil or an empty slice to clear any previously
// attached images.
func WithImages(ctx context.Context, images []ImageBlock) context.Context {
	return context.WithValue(ctx, imagesKey{}, images)
}

// GetImages retrieves the image slice from the context.
// Returns nil if no images are attached or if an empty slice was stored.
func GetImages(ctx context.Context) []ImageBlock {
	images, _ := ctx.Value(imagesKey{}).([]ImageBlock)
	if len(images) == 0 {
		return nil
	}
	return images
}

// documentsKey is the context key for per-invocation document slices.
type documentsKey struct{}

// WithDocuments attaches a slice of DocumentBlock values to the context for the
// current invocation. Pass nil or an empty slice to clear any previously
// attached documents.
func WithDocuments(ctx context.Context, docs []DocumentBlock) context.Context {
	return context.WithValue(ctx, documentsKey{}, docs)
}

// GetDocuments retrieves the document slice from the context.
// Returns nil if no documents are attached or if an empty slice was stored.
func GetDocuments(ctx context.Context) []DocumentBlock {
	docs, _ := ctx.Value(documentsKey{}).([]DocumentBlock)
	if len(docs) == 0 {
		return nil
	}
	return docs
}

// identifierKey is the context key for per-invocation scoping identity.
type identifierKey struct{}

// WithIdentifier attaches an identifier to the context. This is used by
// memory tools to scope Remember and Recall operations to a specific
// entity (user, team, project, tenant, etc.).
func WithIdentifier(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, identifierKey{}, id)
}

// GetIdentifier retrieves the identifier from the context.
// Returns an empty string if no identifier is attached.
func GetIdentifier(ctx context.Context) string {
	id, _ := ctx.Value(identifierKey{}).(string)
	return id
}
