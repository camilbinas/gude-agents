# Agent Context

`agent.Context` is the primary type for carrying invocation-scoped state through the agent pipeline. It embeds `context.Context` for full stdlib compatibility while providing direct method access to invocation state — key-value store, token usage, conversation ID, images, documents, inference config, event hook, and identifier.

## How It Works

When you call `Invoke` or `InvokeStream`, you pass a `*Context` directly. This context flows through middleware, guardrails, tool filters, and event hooks. Each invocation should get its own `*Context` — no cross-request leakage.

- Middleware can write values that tool handlers read (and vice versa) via `Set`/`Get`
- Concurrent tool executions (when `WithParallelToolExecution` is enabled) can safely read and write to the same `*Context`
- The `*Context` satisfies `context.Context`, so it works with database drivers, AWS SDKs, gRPC, and HTTP handlers

## Constructors

### Background

```go
func Background() *Context
```

Creates a new `*Context` wrapping `context.Background()`. Use this for simple scripts and CLI tools.

```go
c := agent.Background()
result, err := a.Invoke(c, "Hello")
```

### NewContext

```go
func NewContext(parent context.Context) *Context
```

Creates a new `*Context` wrapping the given parent. Use this when you have an existing `context.Context` (e.g. from an HTTP request) and want to carry its deadline, cancellation, and values through the agent invocation.

```go
c := agent.NewContext(r.Context())
result, err := a.Invoke(c, req.Message)
```

Panics if `parent` is nil.

### FromContext

```go
func FromContext(ctx context.Context) *Context
```

Safely extracts a `*Context` from a `context.Context`. Returns nil if `ctx` is not a `*Context`. Use this in tool handlers that need invocation state without risking a panic from a direct type assertion.

```go
func myToolHandler(ctx context.Context, input MyInput) (string, error) {
    if c := agent.FromContext(ctx); c != nil {
        c.Set("last_call", time.Now())
    }
    return "done", nil
}
```

## Key-Value Store

The `*Context` includes a concurrency-safe key-value store scoped to the invocation. Use it to share state between middleware and tool handlers.

### Set

```go
func (c *Context) Set(key, value any)
```

Stores a value under the given key. Overwrites any existing value. Safe for concurrent use.

### Get

```go
func (c *Context) Get(key any) (any, bool)
```

Retrieves a value by key. Returns `(value, true)` if found, or `(nil, false)` if the key doesn't exist. Safe for concurrent use.

### GetTyped

```go
func GetTyped[T any](c *Context, key any) (T, bool)
```

Type-safe retrieval — returns the zero value and false if the key is missing or the value isn't assignable to `T`. Eliminates manual type assertions on `Get` results.

```go
role, ok := agent.GetTyped[string](c, "user_role")
count, ok := agent.GetTyped[int](c, "retry_count")
```

## Token Usage

### Usage

```go
func (c *Context) Usage() TokenUsage
```

Returns the cumulative token usage after an invocation completes. Call this after `Invoke`/`InvokeStream` returns:

```go
c := agent.Background()
result, err := a.Invoke(c, "Hello")
usage := c.Usage()
fmt.Printf("Tokens: %d in, %d out\n", usage.InputTokens, usage.OutputTokens)
```

## Chainable Mutators

All `With*` methods mutate the `*Context` and return the same pointer for chaining:

```go
c := agent.Background().
    WithConversationID("conv-123").
    WithEventHook(myHook).
    WithInferenceConfig(&agent.InferenceConfig{Temperature: &temp})
```

### WithConversationID

```go
func (c *Context) WithConversationID(id string) *Context
```

Sets the conversation ID for this invocation. Used with `WithSharedConversation` to route to the correct conversation in multi-tenant setups.

### WithImages

```go
func (c *Context) WithImages(imgs []ImageBlock) *Context
```

Attaches images for vision-capable models.

### WithDocuments

```go
func (c *Context) WithDocuments(docs []DocumentBlock) *Context
```

Attaches documents (PDFs, Word docs, spreadsheets) for document reasoning.

### WithInferenceConfig

```go
func (c *Context) WithInferenceConfig(cfg *InferenceConfig) *Context
```

Overrides inference parameters for this invocation. Non-nil fields take precedence over agent-level values.

```go
temp := 0.9
c := agent.Background().WithInferenceConfig(&agent.InferenceConfig{
    Temperature: &temp,
})
result, err := a.Invoke(c, "Be creative!")
```

### WithEventHook

```go
func (c *Context) WithEventHook(h EventHook) *Context
```

Attaches an event hook for real-time UI event delivery (tool call start/end, model start/end, thinking chunks).

### WithTracingHook

```go
func (c *Context) WithTracingHook(h TracingHook) *Context
```

Sets a per-invocation tracing hook that takes precedence over the agent-level hook. Used by graph agent nodes to pass bridge tracing hooks without mutating the shared agent.

### WithMetricsHook

```go
func (c *Context) WithMetricsHook(h MetricsHook) *Context
```

Sets a per-invocation metrics hook that takes precedence over the agent-level hook.

### WithLoggingHook

```go
func (c *Context) WithLoggingHook(h LoggingHook) *Context
```

Sets a per-invocation logging hook that takes precedence over the agent-level hook.

### WithIdentifier

```go
func (c *Context) WithIdentifier(id string) *Context
```

Sets the scoping identity for memory operations (user ID, tenant ID, etc.).

### WithValue

```go
func (c *Context) WithValue(key, val any) *Context
```

Attaches a key-value pair to the embedded `context.Context` (readable via `ctx.Value`). Use this to pass request IDs, trace baggage, or other values that downstream libraries expect on the stdlib context. Unlike `Set`/`Get` which use the invocation-scoped store, `WithValue` returns a new `*Context` wrapping a derived context.

```go
c := agent.Background().
    WithValue(requestIDKey{}, "req-abc").
    WithConversationID("conv-1")
```

## Clone

```go
func (c *Context) Clone() *Context
```

Returns a new `*Context` that shares the parent `context.Context` and typed fields (conversation ID, images, documents, inference config, event hook, identifier) but has an independent key-value store. Use this when forking parallel sub-invocations that should not share mutable KV state.

```go
for _, topic := range topics {
    sub := c.Clone().WithConversationID(topic.ID)
    go func() { agent.Invoke(sub, topic.Question) }()
}
```

## Accessors

| Method | Returns | Description |
|--------|---------|-------------|
| `ConversationID()` | `string` | Conversation ID for this invocation |
| `Images()` | `[]ImageBlock` | Attached images |
| `Documents()` | `[]DocumentBlock` | Attached documents |
| `InferenceConfig()` | `*InferenceConfig` | Per-invocation inference config |
| `EventHook()` | `EventHook` | Per-invocation event hook |
| `Identifier()` | `string` | Scoping identity for memory |
| `Usage()` | `TokenUsage` | Cumulative token usage (populated after invocation) |

## Code Example

This example shows a timing middleware that records when each tool call starts, and a tool handler that reads that timestamp:

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// timingMiddleware stores the call start time in the Context
// so tool handlers can access it.
func timingMiddleware(next agent.ToolHandlerFunc) agent.ToolHandlerFunc {
	return func(c *agent.Context, toolName string, input json.RawMessage) (string, error) {
		c.Set("call_start", time.Now())
		c.Set("tool_name", toolName)
		return next(c, toolName, input)
	}
}

type LookupInput struct {
	UserID string `json:"user_id" description:"The user ID to look up" required:"true"`
}

func main() {
	provider, err := bedrock.Standard()
	if err != nil {
		log.Fatal(err)
	}

	// Tool handler reads state set by middleware via FromContext.
	lookup := tool.New("lookup_user", "Looks up a user by ID", func(ctx context.Context, input LookupInput) (string, error) {
		if c := agent.FromContext(ctx); c != nil {
			if start, ok := c.Get("call_start"); ok {
				elapsed := time.Since(start.(time.Time))
				log.Printf("tool handler reached %s after %s", input.UserID, elapsed)
			}
			c.Set("last_lookup", input.UserID)
		}
		return fmt.Sprintf("User %s: Alice (active)", input.UserID), nil
	})

	a, err := agent.Default(
		provider,
		prompt.Text("You are a helpful assistant. Use the lookup_user tool when asked about users."),
		[]tool.Tool{lookup},
		agent.WithMiddleware(timingMiddleware),
	)
	if err != nil {
		log.Fatal(err)
	}

	c := agent.Background()
	result, err := a.Invoke(c, "Look up user u-123")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result)
	fmt.Printf("Tokens used: %d\n", c.Usage().Total())
}
```

The flow:

1. `Invoke` receives the `*Context` directly
2. The LLM decides to call `lookup_user`
3. `timingMiddleware` runs first — stores `call_start` and `tool_name` in the `*Context`
4. The `lookup_user` handler uses `agent.FromContext(ctx)` to safely access the `*Context` and read `call_start`
5. After `Invoke` returns, `c.Usage()` contains the cumulative token usage

## See Also

- [Middleware](middleware.md) — defining middleware that wraps tool execution
- [Agent API Reference](agent-api.md) — `Invoke`, `InvokeStream`, and configuration options
- [Tools](tools.md) — defining tool handlers and the `FromContext` pattern
