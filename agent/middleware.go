package agent

import (
	"context"
	"encoding/json"
)

// ToolCall is the canonical middleware view of a provider tool call.
type ToolCall struct {
	ID    string
	Name  string
	Input json.RawMessage
}

// ToolResult is the canonical result passed through middleware.
type ToolResult struct {
	Text    string
	Images  []ImageBlock
	IsError bool
}

// ToolHandlerFunc executes one canonical tool call.
type ToolHandlerFunc func(context.Context, ToolCall) (ToolResult, error)

// Middleware wraps canonical tool execution.
type Middleware func(next ToolHandlerFunc) ToolHandlerFunc

// ChainMiddleware composes middlewares with the first middleware outermost.
func ChainMiddleware(handler ToolHandlerFunc, mws ...Middleware) ToolHandlerFunc {
	for i := len(mws) - 1; i >= 0; i-- {
		handler = mws[i](handler)
	}
	return handler
}
