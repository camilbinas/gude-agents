package agent

import (
	"context"
	"encoding/json"
)

// ToolHandlerFunc is the signature of a tool handler after middleware wrapping.
// Documented in docs/middleware.md — update when changing signature.
type ToolHandlerFunc func(ctx context.Context, toolName string, input json.RawMessage) (string, error)

// Middleware wraps a tool handler to add cross-cutting behavior.
// Documented in docs/middleware.md — update when changing type or ordering semantics.
type Middleware func(next ToolHandlerFunc) ToolHandlerFunc

// chainMiddleware composes middlewares so that the first in the slice is the outermost wrapper.
func chainMiddleware(handler ToolHandlerFunc, mws ...Middleware) ToolHandlerFunc {
	for i := len(mws) - 1; i >= 0; i-- {
		handler = mws[i](handler)
	}
	return handler
}
