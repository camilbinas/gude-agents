# Agent Context

`agent.Context` carries invocation-scoped state through the agent pipeline. It embeds `context.Context` for stdlib compatibility while providing direct access to per-invocation state — key-value store, token usage, conversation ID, images, documents, inference config, event hook, and identifier.

## How It Works

When you call `Invoke` or `InvokeStream`, you pass a `*Context` directly. This context flows through middleware, guardrails, tool filters, and event hooks. Each invocation should get its own `*Context` — no cross-request leakage.

- Middleware can write values that tool handlers read (and vice versa) via `Set`/`Get`
- Concurrent tool executions (when `WithParallelToolExecution` is enabled) safely share the same `*Context`
- The `*Context` satisfies `context.Context`, so it works with database drivers, AWS SDKs, gRPC, and HTTP handlers

## Constructors

| Constructor | Description |
|---|---|
| `Background()` | Wraps `context.Background()`. For scripts and CLI tools. |
| `NewContext(parent)` | Wraps an existing `context.Context` (e.g. from an HTTP request). Panics if nil. |
| `FromContext(ctx)` | Extracts a `*Context` from a `context.Context`. Returns nil if not a `*Context`. |

```go
// HTTP handler — carry request deadline and cancellation
c := agent.NewContext(r.Context()).WithConversationID(req.ConversationID)
result, err := a.Invoke(c, req.Message)
```

## Key-Value Store

Concurrency-safe key-value store scoped to the invocation. Use it to share state between middleware and tool handlers.

| Method | Description |
|---|---|
| `Set(key, value any)` | Store a value. Overwrites existing. |
| `Get(key any) (any, bool)` | Retrieve by key. |
| `GetTyped[T](c, key) (T, bool)` | Type-safe retrieval — no manual assertions. |

```go
// In middleware:
c.Set("call_start", time.Now())

// In tool handler:
role, ok := agent.GetTyped[string](c, "user_role")
```

## Chainable Mutators

All `With*` methods mutate the `*Context` and return the same pointer for chaining.

| Method | Description |
|---|---|
| `WithConversationID(id)` | Route to a specific conversation in multi-tenant setups |
| `WithIdentifier(id)` | Default scoping identity for memory operations (user, tenant) |
| `WithScope(key, value)` | Named scope for multi-dimensional identity (org, project, team, etc.) |
| `WithImages(imgs)` | Attach images for vision-capable models |
| `WithDocuments(docs)` | Attach PDFs, Word docs, spreadsheets |
| `WithInferenceConfig(cfg)` | Override inference parameters for this call |
| `WithEventHook(h)` | Real-time UI event delivery (tool calls, model events, thinking) |
| `WithTracingHook(h)` | Per-invocation tracing hook (overrides agent-level) |
| `WithMetricsHook(h)` | Per-invocation metrics hook (overrides agent-level) |
| `WithLoggingHook(h)` | Per-invocation logging hook (overrides agent-level) |
| `WithValue(key, val)` | Attach to the embedded `context.Context` (for `ctx.Value` consumers) |
| `WithPrincipal(p Principal)` | Attach a `Principal` (identity + roles) for RBAC enforcement. See [RBAC & Identity](rbac.md) |
| `EmitEvent(name string, payload any)` | Emit a custom `EventCustom` event on the event stream, readable via `InvokeEventStream` |
| `WithSystemPromptOverride(s string)` | Override the agent's system prompt for this invocation only — useful for A/B testing |

```go
c := agent.Background().
    WithIdentifier("user-alice").
    WithScope("project", "proj-atlas")
```

## Clone

`Clone()` returns a new `*Context` sharing the parent context and typed fields but with an independent key-value store. Use when forking parallel sub-invocations that shouldn't share mutable KV state.

```go
for _, topic := range topics {
    sub := c.Clone().WithConversationID(topic.ID)
    go func() { a.Invoke(sub, topic.Question) }()
}
```

## Scopes

Scopes are named string values on the context for multi-dimensional identity. While `WithIdentifier` sets a single default identity (typically the user), `WithScope` attaches additional named identities accessible anywhere in the pipeline.

| Method | Description |
|---|---|
| `WithScope(key, value)` | Set a named scope (chainable) |
| `Scope(key) string` | Read a named scope value |
| `SetScope(key, value)` | Update a scope mid-invocation (e.g. from a tool handler) |
| `ScopeFrom(ctx, key) string` | Helper: reads scope from context, falls back to `Identifier()` |

```go
c := agent.Background().
    WithIdentifier("user-alice").
    WithScope("org", "acme-corp").
    WithScope("project", "proj-atlas")

// Anywhere in the pipeline (tools, middleware, guardrails, filters):
org := c.Scope("org")         // "acme-corp"
project := c.Scope("project") // "proj-atlas"

// Update mid-invocation:
c.SetScope("project", "proj-orion")
```

Scopes flow through the entire pipeline. Any code that receives the `*Context` can read them — tool handlers via `agent.FromContext(ctx)`, middleware directly on `c`, guardrails, and tool filters. Memory tools use `WithScope` on the tool option to bind to a specific scope key (see [Long-Term Memory](memory.md#multi-scope-memory)).

## Token Usage

`c.Usage()` returns cumulative `TokenUsage` after an invocation completes:

```go
c := agent.Background()
result, err := a.Invoke(c, "Hello")
fmt.Printf("Tokens: %d in, %d out\n", c.Usage().InputTokens, c.Usage().OutputTokens)
```

## Code Example

A timing middleware that records when each tool call starts, and a tool handler that reads that timestamp:

```go
func timingMiddleware(next agent.ToolHandlerFunc) agent.ToolHandlerFunc {
    return func(c *agent.Context, toolName string, input json.RawMessage) (string, error) {
        c.Set("call_start", time.Now())
        return next(c, toolName, input)
    }
}

lookup := tool.New("lookup_user", "Looks up a user by ID",
    func(ctx context.Context, input LookupInput) (string, error) {
        if c := agent.FromContext(ctx); c != nil {
            if start, ok := c.Get("call_start"); ok {
                log.Printf("reached after %s", time.Since(start.(time.Time)))
            }
        }
        return fmt.Sprintf("User %s: Alice (active)", input.UserID), nil
    },
)

a, _ := agent.Default(provider, instructions, []tool.Tool{lookup},
    agent.WithMiddleware(timingMiddleware),
)

c := agent.Background()
result, _ := a.Invoke(c, "Look up user u-123")
fmt.Printf("Tokens used: %d\n", c.Usage().Total())
```

## See Also

- [Middleware](middleware.md) — defining middleware that wraps tool execution
- [Agent API Reference](agent-api.md) — `Invoke`, `InvokeStream`, and configuration options
- [Tools](tools.md) — defining tool handlers and the `FromContext` pattern
