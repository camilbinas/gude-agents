# Tools

Tools expose a JSON schema to the model and execute through one authorization, guard, approval, middleware, handler, event, audit, and result pipeline.

## Constructors

There are four constructors. Every constructor accepts named `tool.Option` values.

```go
func New[T any](name, description string, handler tool.Handler[T], opts ...tool.Option) tool.Tool
func NewRaw(name, description string, handler tool.Handler[json.RawMessage], opts ...tool.Option) tool.Tool
func NewRich[T any](name, description string, handler tool.RichHandler[T], opts ...tool.Option) tool.Tool
func NewBackground[T any](name, description, ack string, handler tool.BackgroundHandler[T], opts ...tool.Option) tool.Tool
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

Use `NewRaw` with `tool.WithSchema(schema)` when input must remain raw JSON. Use the same `json.RawMessage` type parameter plus `WithSchema` for rich/background raw input.

`NewRich` returns `*tool.Output` with text and optional images. `NewBackground` acknowledges immediately, runs the handler on a detached context outside the originating model turn, persists completion, and triggers a conversation re-entry turn. It requires a non-empty acknowledgement and an agent conversation store (enforced by `agent.New`). Because a store-backed Agent requires a non-empty conversation ID on every invocation, an invocation without one fails with `agent.ErrConversationIDRequired` before the model runs, so a background tool can never be dispatched without a conversation.

## Options

- `RequiresApproval()` pauses before execution.
- `WithGuard(fn)` performs tool-specific policy validation.
- `AllowRoles(...)`, `DenyRoles(...)`, and `AllowWhen(...)` apply authorization policy.
- `WithSchema(schema)` replaces generated/default JSON schema.

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
