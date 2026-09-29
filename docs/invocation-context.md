# Invocation context

`agent.Context` implements the standard `context.Context` interface and adds invocation configuration. Create one from an existing cancellation/deadline context:

```go
ctx := agent.NewContext(request.Context()).
    WithConversationID("thread-42").
    WithIdentity("user-7").
    WithScope("account", "acct-9").
    WithInstructions("Answer as the billing specialist.").
    WithDetailedEvents()
```

`agent.Background()` is shorthand for a context rooted at `context.Background()`.

## Mutable configuration before invocation

`With…` methods mutate and return the same pointer for chaining. Finish configuring a context before passing it to `Invoke`, `Stream`, `TextStream`, or `Resume`. `Clone` creates an independently configurable copy.

Available configuration includes conversation ID, identity, named scopes, principal, images, documents, inference settings, instruction override, detailed stream events, and per-invocation observers. `WithObservers()` with no arguments disables operational observation for that invocation.

## Standard context interoperability

Tools, middleware, stores, providers, and observers accept `context.Context`. Use the package helpers to recover agent data safely:

```go
func handle(ctx context.Context) error {
    invocation := agent.FromContext(ctx)
    identity := agent.IdentityFrom(ctx)
    account, ok := agent.ScopeFrom(ctx, "account")
    principal, principalOK := agent.PrincipalFrom(ctx)
    _, _, _, _ = invocation, identity, principal, principalOK
    if !ok {
        return errors.New("account scope required")
    }
    return nil
}
```

Cancellation, deadlines, tracing values, and ordinary parent-context values remain available through the standard interface.

## Identity, principal, scope, and conversation ID

These fields are intentionally distinct:

- **Identity** is the stable subject used to partition long-term memory.
- **Principal** is authorization data such as subject, roles, and attributes.
- **Scope** is a named, strict partition key. `Scope` and `ScopeFrom` return `(string, bool)` and never substitute identity when a scope is absent.
- **ConversationID** selects persisted conversational history. It is supplied only by the invocation context, never by the store or agent option.

Do not derive authorization from a conversation ID. Do not silently use identity for a missing tenant/account scope.

## Invocation KV

`Set`, `Get`, and `GetTyped` provide a thread-safe invocation-scoped key/value map shared with all tool calls:

```go
type selectedRegionKey struct{}
ctx.Set(selectedRegionKey{}, "eu-west-1")
region, ok := agent.GetTyped[string](ctx, selectedRegionKey{})
```

This KV is for application coordination, including a tool writing data that a later tool filter reads. It is **not framework scratch space**: framework interrupt state, usage, call IDs, widget accumulation, and guard state are isolated internally. Parallel tool calls receive child contexts with shared user KV but separate per-call runtime state.

Usage belongs to `Result.Usage`.
