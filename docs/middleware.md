# Tool middleware

Middleware wraps the canonical tool pipeline with standard `context.Context` and structured call/result values:

```go
type ToolCall struct {
    ID    string
    Name  string
    Input json.RawMessage
}

type ToolResult struct {
    Text    string
    Images  []agent.ImageBlock
    IsError bool
}

type ToolHandlerFunc func(context.Context, ToolCall) (ToolResult, error)
type Middleware func(next ToolHandlerFunc) ToolHandlerFunc
```

Example timing middleware:

```go
func Timing(next agent.ToolHandlerFunc) agent.ToolHandlerFunc {
    return func(ctx context.Context, call agent.ToolCall) (agent.ToolResult, error) {
        started := time.Now()
        result, err := next(ctx, call)
        slog.InfoContext(ctx, "tool completed",
            "call_id", call.ID,
            "tool", call.Name,
            "duration", time.Since(started),
            "error", err,
        )
        return result, err
    }
}

a, err := agent.New(prov, instructions, agent.WithMiddleware(Timing))
```

Middlewares are applied in registration order; the first is outermost. They may inspect or replace `ToolCall` and `ToolResult`, but should preserve `call.ID` for correlation. Respect cancellation and avoid storing invocation state globally.

Authorization, schema validation, guards, and approval happen before the middleware/handler portion of the pipeline. Built-in lifecycle observers are usually better for telemetry because they also cover model, conversation, retrieval, and interrupt operations. See [Observability](observability.md).
