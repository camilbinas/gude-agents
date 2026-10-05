# Tools

Tools expose a JSON schema to the model and execute through one authorization, guard, approval, middleware, handler, event, audit, and result pipeline.

## Constructors

There are five constructors. Every constructor accepts named `tool.Option` values, and every tool runs through the same pipeline.

```go
func NewSimple(name, description string, handler func(context.Context) (string, error), opts ...tool.Option) tool.Tool
func New[T any](name, description string, handler tool.Handler[T], opts ...tool.Option) tool.Tool
func NewRich[T any](name, description string, handler tool.RichHandler[T], opts ...tool.Option) tool.Tool
func NewBackground[T any](name, description, ack string, handler tool.BackgroundHandler[T], opts ...tool.Option) tool.Tool
func NewRaw(name, description string, schema map[string]any, handler tool.Handler[json.RawMessage], opts ...tool.Option) tool.Tool
```

Choose in this order:

| Constructor | Use for |
|---|---|
| `NewSimple` | no input |
| `New[T]` | ordinary structured Go input |
| `NewRich[T]` | structured input + text/image output |
| `NewBackground[T]` | structured input + background execution |
| `NewRaw` | manually controlled JSON and schema (dynamic schemas, adapters, MCP-style tools) |

`NewSimple` exposes the schema `{"type":"object"}`:

```go
health := tool.NewSimple("health", "Check service health",
    func(ctx context.Context) (string, error) {
        return "healthy", nil
    },
)
```

Typed tools generate schema from `T`:

```go
type SearchInput struct {
    Query string `json:"query" description:"Search query" required:"true"`
}

search := tool.New("search", "Search the catalog",
    func(ctx context.Context, in SearchInput) (string, error) {
        return searchCatalog(ctx, in.Query)
    },
    tool.RequiresApproval(),
    tool.WithGuard(validateSearch),
    tool.AllowRoles("support", "admin"),
)
```

Use `NewRaw` when input must remain raw JSON. For a raw tool the schema is part of the definition, so it is a positional argument; `nil` becomes `{"type":"object"}`:

```go
deleteOrder := tool.NewRaw("delete_order", "Delete an order permanently",
    map[string]any{
        "type": "object",
        "properties": map[string]any{
            "order_id": map[string]any{"type": "string"},
        },
        "required": []string{"order_id"},
    },
    func(ctx context.Context, input json.RawMessage) (string, error) {
        return deleteOrderFromJSON(ctx, input)
    },
    tool.RequiresApproval(),
)
```

For rich or background tools that need raw input, use `json.RawMessage` as the type parameter plus `WithSchema`.

`NewRich` returns `*tool.Output` with text and optional images. `NewBackground` acknowledges immediately, runs the handler on a detached context outside the originating model turn, persists completion, and triggers a conversation re-entry turn. It requires a non-empty acknowledgement and an agent conversation store (enforced by `agent.New`). Because a store-backed Agent requires a non-empty conversation ID on every invocation, an invocation without one fails with `agent.ErrConversationIDRequired` before the model runs, so a background tool can never be dispatched without a conversation.

## Options

- `RequiresApproval()` pauses before execution.
- `WithGuard(fn)` performs tool-specific policy validation.
- `AllowRoles(...)`, `DenyRoles(...)`, and `AllowWhen(...)` apply authorization policy.
- `WithSchema(schema)` replaces the generated/default JSON schema. Options apply after the constructor, so on `NewRaw` a `WithSchema` option overrides the positional schema.

## Registration

A fixed set is configured at construction:

```go
a, err := agent.New(prov, instructions, agent.WithTools(search, lookup))
```

For live changes, supply the concurrency-safe registry:

```go
registry := &tool.Registry{}
if err := registry.Register(search); err != nil { /* handle */ }
a, err := agent.New(prov, instructions, agent.WithToolRegistry(registry))
registry.Unregister("search")
tools := registry.List() // deterministic snapshot sorted by name
```

`Register` validates tools and rejects duplicate names. `Lookup`, `List`, and `Unregister` are safe during invocations.

## Context and emissions

Handlers receive standard `context.Context`, including cancellation, tracing, and invocation data. Use `agent.IdentityFrom`, `agent.ScopeFrom`, and `agent.PrincipalFrom`; emit UI or application events with `agent.EmitWidget` and `agent.EmitEvent`.

See [Middleware](middleware.md), [RBAC](rbac.md), and [Interrupts](interrupts.md).

## Policy and observability

Constructor choice is independent from policy: `ToolFilter` controls what the model can see, RBAC/ABAC controls who may execute it, `WithGuard` enforces runtime business rules, `RequiresApproval` pauses for human authorization, and input/output guardrails control content. Observers and audit record what happened. See [RBAC](rbac.md), [Interrupts](interrupts.md), [Guardrails](guardrails.md), and [Observability](observability.md).

The runnable constructor workflow is [`examples/tools`](../examples/tools/). MCP, A2A, web-search, rich-image, background, and policy variations are documented in their respective feature pages rather than duplicated as standalone applications.
