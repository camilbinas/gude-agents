# Migration Guide: Agent Context Refactor

This guide covers migrating from the old `InvocationContext` + free-function API to the new `agent.Context` type.

## API Mapping

| Removed | Replacement |
|---------|-------------|
| `NewInvocationContext()` | `agent.NewContext(parent)` or `agent.Background()` |
| `WithInvocationContext(ctx, ic)` | Pass `*agent.Context` directly to `Invoke`/`InvokeStream` |
| `GetInvocationContext(ctx)` | Direct method access on `*Context` |
| `GetInvocationUsage(ic)` | `c.Usage()` |
| `WithConversationID(ctx, id)` | `c.WithConversationID(id)` |
| `WithEventHook(ctx, h)` | `c.WithEventHook(h)` |
| `WithInferenceConfig(ctx, cfg)` | `c.WithInferenceConfig(cfg)` |
| `WithImages(ctx, imgs)` | `c.WithImages(imgs)` |
| `WithDocuments(ctx, docs)` | `c.WithDocuments(docs)` |
| `WithIdentifier(ctx, id)` | `c.WithIdentifier(id)` |

### Signature Changes

| Type | Before | After |
|------|--------|-------|
| `Invoke` | `Invoke(ctx context.Context, msg string)` | `Invoke(c *Context, msg string)` |
| `InvokeStream` | `InvokeStream(ctx context.Context, msg string, cb StreamCallback)` | `InvokeStream(c *Context, msg string, cb StreamCallback)` |
| `ToolHandlerFunc` | `func(ctx context.Context, toolName string, input json.RawMessage) (string, error)` | `func(c *Context, toolName string, input json.RawMessage) (string, error)` |
| `InputGuardrail` | `func(ctx context.Context, msg string) (string, error)` | `func(c *Context, msg string) (string, error)` |
| `OutputGuardrail` | `func(ctx context.Context, resp string) (string, error)` | `func(c *Context, resp string) (string, error)` |
| `ToolFilter` | `func(ctx context.Context, t tool.Tool) bool` | `func(c *Context, t tool.Tool) bool` |
| `EventHook` methods | `OnToolCallStart(ctx context.Context, ...)` | `OnToolCallStart(c *Context, ...)` |

## Before/After Examples

### Basic Invoke

**Before:**

```go
import "context"

ctx := context.Background()
result, err := a.Invoke(ctx, "Hello")
```

**After:**

```go
c := agent.Background()
result, err := a.Invoke(c, "Hello")
```

### Invoke with Conversation ID

**Before:**

```go
ctx := context.Background()
ctx = agent.WithConversationID(ctx, "session-123")
result, err := a.Invoke(ctx, "Hello")
```

**After:**

```go
c := agent.Background().WithConversationID("session-123")
result, err := a.Invoke(c, "Hello")
```

### Images and Documents

**Before:**

```go
ctx := context.Background()
ctx = agent.WithImages(ctx, images)
ctx = agent.WithDocuments(ctx, docs)
result, err := a.Invoke(ctx, "Describe this")
```

**After:**

```go
c := agent.Background().
    WithImages(images).
    WithDocuments(docs)
result, err := a.Invoke(c, "Describe this")
```

### Event Hook

**Before:**

```go
ctx := context.Background()
ctx = agent.WithEventHook(ctx, myHook)
err := a.InvokeStream(ctx, msg, streamCB)
```

**After:**

```go
c := agent.Background().WithEventHook(myHook)
err := a.InvokeStream(c, msg, streamCB)
```

### EventHook Implementation

**Before:**

```go
type MyHook struct{ agent.BaseEventHook }

func (h *MyHook) OnToolCallStart(ctx context.Context, name string, input json.RawMessage) {
    // ...
}

func (h *MyHook) OnModelEnd(ctx context.Context, stopReason string) {
    // ...
}
```

**After:**

```go
type MyHook struct{ agent.BaseEventHook }

func (h *MyHook) OnToolCallStart(c *agent.Context, name string, input json.RawMessage) {
    // ...
}

func (h *MyHook) OnModelEnd(c *agent.Context, stopReason string) {
    // ...
}
```

### Middleware

**Before:**

```go
func loggingMiddleware(next agent.ToolHandlerFunc) agent.ToolHandlerFunc {
    return func(ctx context.Context, toolName string, input json.RawMessage) (string, error) {
        log.Printf("calling tool: %s", toolName)
        return next(ctx, toolName, input)
    }
}
```

**After:**

```go
func loggingMiddleware(next agent.ToolHandlerFunc) agent.ToolHandlerFunc {
    return func(c *agent.Context, toolName string, input json.RawMessage) (string, error) {
        log.Printf("calling tool: %s", toolName)
        return next(c, toolName, input)
    }
}
```

### Guardrails

**Before:**

```go
func blocklist(words ...string) agent.InputGuardrail {
    return func(ctx context.Context, msg string) (string, error) {
        // validate msg...
        return msg, nil
    }
}
```

**After:**

```go
func blocklist(words ...string) agent.InputGuardrail {
    return func(c *agent.Context, msg string) (string, error) {
        // validate msg...
        return msg, nil
    }
}
```

### Tool Filters

**Before:**

```go
roleFilter := func(ctx context.Context, t tool.Tool) bool {
    role, _ := ctx.Value(userRoleKey{}).(string)
    return role == "admin" || t.Spec.Name != "delete_account"
}
```

**After:**

```go
roleFilter := func(c *agent.Context, t tool.Tool) bool {
    role, _ := c.Value(userRoleKey{}).(string)
    return role == "admin" || t.Spec.Name != "delete_account"
}
```

Tool filters can also use the invocation-scoped key-value store:

```go
workflowFilter := func(c *agent.Context, t tool.Tool) bool {
    if t.Spec.Name == "submit_order" {
        v, ok := c.Get("cart_validated")
        return ok && v.(bool)
    }
    return true
}
```

### Reading Token Usage

**Before:**

```go
ctx := context.Background()
ic := agent.NewInvocationContext()
ctx = agent.WithInvocationContext(ctx, ic)

result, err := a.Invoke(ctx, "Hello")
usage := agent.GetInvocationUsage(ic)
fmt.Printf("Tokens: %d in / %d out\n", usage.InputTokens, usage.OutputTokens)
```

**After:**

```go
c := agent.Background()
result, err := a.Invoke(c, "Hello")
usage := c.Usage()
fmt.Printf("Tokens: %d in / %d out\n", usage.InputTokens, usage.OutputTokens)
```

### Inference Config Override

**Before:**

```go
ctx := context.Background()
ctx = agent.WithInferenceConfig(ctx, &agent.InferenceConfig{
    MaxTokens:   512,
    Temperature: 0.7,
})
result, err := a.Invoke(ctx, "Be creative")
```

**After:**

```go
c := agent.Background().WithInferenceConfig(&agent.InferenceConfig{
    MaxTokens:   512,
    Temperature: 0.7,
})
result, err := a.Invoke(c, "Be creative")
```

### HTTP Handler (Per-Request Context)

**Before:**

```go
func handleChat(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()
    ic := agent.NewInvocationContext()
    ctx = agent.WithInvocationContext(ctx, ic)
    ctx = agent.WithConversationID(ctx, sessionID)
    ctx = agent.WithEventHook(ctx, sseHook)

    err := a.InvokeStream(ctx, msg, streamCB)
    // ...
}
```

**After:**

```go
func handleChat(w http.ResponseWriter, r *http.Request) {
    c := agent.NewContext(r.Context()).
        WithConversationID(sessionID).
        WithEventHook(sseHook)

    err := a.InvokeStream(c, msg, streamCB)
    // ...
}
```

## Tool Package Handlers Are Unchanged

Tool handlers defined via the `tool` package still accept `context.Context`:

```go
func myTool(ctx context.Context, input MyInput) (string, error) {
    // This signature is unchanged.
    return "result", nil
}
```

The agent passes `*agent.Context` directly since it satisfies `context.Context` via embedding. No conversion is needed.

## Type-Assertion Pattern for Tools Needing Invocation State

If a tool handler needs access to invocation-scoped state (key-value store, conversation ID, etc.), use a type assertion:

```go
func myStatefulTool(ctx context.Context, input MyInput) (string, error) {
    // Type-assert to access invocation state.
    c := ctx.(*agent.Context)

    // Now you can use Context methods:
    c.Set("processed", true)
    convID := c.ConversationID()

    return fmt.Sprintf("processed in conversation %s", convID), nil
}
```

For defensive code that may be called outside an agent invocation, use the comma-ok form:

```go
func myTool(ctx context.Context, input MyInput) (string, error) {
    if c, ok := ctx.(*agent.Context); ok {
        c.Set("key", "value")
    }
    return "result", nil
}
```

This pattern is used internally by `AgentAsTool` for multi-agent composition:

```go
// From compose.go — wraps or asserts the context for child agent invocation.
var c *agent.Context
if ac, ok := ctx.(*agent.Context); ok {
    c = ac
} else {
    c = agent.NewContext(ctx)
}
err := child.InvokeStream(c, msg, streamCB)
```
