# RBAC & Identity

Every invocation can carry a `Principal` — the identity of the caller. Tool-level role policies and agent-level filters use that principal to decide whether a given tool call is allowed. This page covers the full identity and RBAC system: attaching a principal, declaring per-tool role policies, enforcing them agent-wide, and plugging in external authorization services.

## Overview

The RBAC system has three independent layers that compose together:

| Layer | How it works |
|-------|--------------|
| Tool-level policies | `tool.AllowRoles` / `tool.DenyRoles` — declared on each tool at construction time |
| Agent-level enforcement | `WithRoleEnforcement()` installs `RoleFilter` — evaluates tool policies against the principal on each invocation |
| Custom policies | `WithPolicy(fn PolicyFunc)` — arbitrary logic (OPA, Casbin, database lookups) |

All three are `ToolFilter` variants and compose additively — a tool call is allowed only when every active filter passes.

## Principal

`Principal` represents the caller's identity for one invocation.

```go
type Principal struct {
    ID    string            // unique identifier (user ID, service account, etc.)
    Roles []string          // roles assigned to this caller
    Attrs map[string]string // arbitrary key-value attributes (org_id, tier, region, etc.)
}
```

Helper methods on `Principal`:

| Method | Description |
|--------|-------------|
| `HasRole(role string) bool` | Reports whether the principal holds the given role |
| `HasAnyRole(roles ...string) bool` | Reports whether the principal holds at least one of the given roles |
| `Attr(key string) string` | Returns an attribute value, or empty string if not set |

## Attaching a Principal

Call `WithPrincipal` on the `*Context` before invoking the agent. Each invocation gets its own principal — there is no global state.

```go
c := agent.Background().WithPrincipal(agent.Principal{
    ID:    "user-alice",
    Roles: []string{"admin"},
    Attrs: map[string]string{"org_id": "acme"},
})

result, err := a.Invoke(c, "Process refund for order #1234")
```

### `(*Context).WithPrincipal(p Principal) *Context`

Attaches a `Principal` to the invocation context. Tool role policies, `ToolFilter` functions, and middleware can retrieve it via `PrincipalFrom`.

### `PrincipalFrom(ctx context.Context) (Principal, bool)`

Extracts the `Principal` from any `context.Context`. Returns a zero `Principal` and `false` if none was set.

```go
lookup := tool.New("lookup_order", "Look up an order",
    func(ctx context.Context, in LookupInput) (string, error) {
        p, ok := agent.PrincipalFrom(ctx)
        if ok {
            log.Printf("caller: %s roles: %v", p.ID, p.Roles)
        }
        return fetchOrder(in.OrderID), nil
    },
)
```

## Tool-Level Role Policies

Declare role requirements directly on a tool at construction time. These are evaluated before the tool handler runs.

### `tool.AllowRoles(roles ...string) func(*Tool)`

Restricts the tool to callers holding at least one of the listed roles. Callers without any matching role receive a structured denial result — the LLM is informed the tool is unavailable without calling the handler.

```go
refundTool := tool.NewRaw("process_refund", "Process a refund",
    schema,
    handler,
    tool.AllowRoles("support", "admin"),
)
```

### `tool.DenyRoles(roles ...string) func(*Tool)`

Blocks any caller holding one of the listed roles, regardless of the allowlist. Deny is evaluated first.

```go
viewLogsTool := tool.NewRaw("view_logs", "View system logs",
    schema,
    handler,
    tool.DenyRoles("guest"),
)
```

### Composition rules

When both `AllowRoles` and `DenyRoles` are set:

1. If the caller holds any denied role → **denied**
2. If an allowlist exists and the caller holds no allowed role → **denied**
3. Otherwise → **allowed**

Tools with no role policy are always allowed (open by default).

### `(Tool).RolesAllowed(roles []string) bool`

Reports whether the given roles satisfy the tool's role policy. Returns `true` when no policy is set. Rarely called directly — `RoleFilter` handles this automatically.

## Agent-Level Enforcement

### `WithRoleEnforcement() Option`

Installs `RoleFilter` on the agent. Tool role policies declared via `AllowRoles` / `DenyRoles` are then automatically enforced on every invocation using the `Principal` on the context.

```go
a, err := agent.New(provider, instructions, tools,
    agent.WithRoleEnforcement(),
)

// Per invocation — attach the caller's identity:
result, err := a.Invoke(
    agent.Background().WithPrincipal(agent.Principal{
        ID:    "bob",
        Roles: []string{"support"},
    }),
    "Process refund for order #1234",
)
```

### `RoleFilter() ToolFilter`

Returns the `ToolFilter` that `WithRoleEnforcement` installs. Use this directly when you need to compose it with other filters via `WithToolFilter`.

```go
agent.New(provider, instructions, tools,
    agent.WithToolFilter(agent.RoleFilter()),
)
```

Behaviour when no principal is on the context: tools with an allowlist are denied; tools with only a denylist or no policy are allowed.

### `RequirePrincipal() ToolFilter`

Returns a `ToolFilter` that denies all tool calls when no `Principal` is set on the context. Use this when every invocation must be authenticated before tools can run — the LLM receives "unknown tool" errors for every call on unauthenticated requests.

### `WithRequirePrincipal() Option`

Installs `RequirePrincipal` as a `ToolFilter`. Combine with `WithRoleEnforcement` for a fully authenticated, role-enforced agent:

```go
a, err := agent.New(provider, instructions, tools,
    agent.WithRequirePrincipal(), // all invocations must have a principal
    agent.WithRoleEnforcement(),  // tool role policies are enforced
)
```

## Custom Policies

### `PolicyFunc`

```go
type PolicyFunc func(c *Context, t tool.Tool) bool
```

The function signature for a custom authorization policy. Return `true` to allow the tool call, `false` to deny.

### `WithPolicy(fn PolicyFunc) Option`

Installs a `PolicyFunc` as a `ToolFilter`. Use this for authorization logic that goes beyond simple role checks — external services, OPA, Casbin, attribute-based rules, etc.

```go
// OPA example
a, err := agent.New(provider, instructions, tools,
    agent.WithPolicy(func(c *agent.Context, t tool.Tool) bool {
        p, ok := agent.PrincipalFrom(c)
        if !ok {
            return false
        }
        allowed, _ := opaClient.Decision(c, "authz/allow", map[string]any{
            "user":   p.ID,
            "roles":  p.Roles,
            "action": t.Spec.Name,
            "org":    p.Attr("org_id"),
        })
        return allowed
    }),
)
```

```go
// Casbin example
a, err := agent.New(provider, instructions, tools,
    agent.WithPolicy(func(c *agent.Context, t tool.Tool) bool {
        p, ok := agent.PrincipalFrom(c)
        if !ok {
            return false
        }
        ok, _ = enforcer.Enforce(p.ID, "tools", t.Spec.Name)
        return ok
    }),
)
```

`WithPolicy` and `WithRoleEnforcement` can be combined — a tool call must pass both.

## Code Example

An agent with three tools at different permission levels. The same agent instance serves callers with different roles — no agent-per-role needed.

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "log"

    "github.com/camilbinas/gude-agents/agent"
    "github.com/camilbinas/gude-agents/agent/prompt"
    "github.com/camilbinas/gude-agents/agent/provider/bedrock"
    "github.com/camilbinas/gude-agents/agent/tool"
)

func main() {
    provider := bedrock.Must(bedrock.Standard())

    schema := map[string]any{
        "type":       "object",
        "properties": map[string]any{"order_id": map[string]any{"type": "string"}},
        "required":   []string{"order_id"},
    }

    rawHandler := func(_ context.Context, input json.RawMessage) (string, error) {
        return `{"ok":true}`, nil
    }

    a, err := agent.New(provider, prompt.Text("You are a support assistant."),
        []tool.Tool{
            // open to all authenticated callers
            tool.NewRaw("lookup_order", "Look up an order", schema, rawHandler),
            // support and admin only
            tool.NewRaw("process_refund", "Refund an order", schema, rawHandler,
                tool.AllowRoles("support", "admin"),
            ),
            // admin only
            tool.NewRaw("delete_account", "Delete a customer account", schema, rawHandler,
                tool.AllowRoles("admin"),
            ),
        },
        agent.WithRequirePrincipal(), // all callers must have a principal
        agent.WithRoleEnforcement(),  // tool policies are enforced
    )
    if err != nil {
        log.Fatal(err)
    }

    // support user — can see lookup and refund, not delete
    c := agent.Background().WithPrincipal(agent.Principal{
        ID:    "bob",
        Roles: []string{"support"},
    })
    result, err := a.Invoke(c, "Process refund for order #1234")
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println(result)
}
```

See `examples/rbac-basic/` for a complete example including an admin, support, and guest caller.
See `examples/tool-auth/` for the `tool.WithGuard` pattern — per-call authorization on deserialized input.

## See Also

- [Tool System](tools.md) — `tool.AllowRoles`, `tool.DenyRoles`, `tool.WithGuard`, and tool constructors
- [Invocation Context](invocation-context.md) — `WithPrincipal` in the chainable mutators table
- [Handoffs](handoff.md) — pausing the agent loop for human approval (compare with tool approval)
- [Tool Approval](tool-approval.md) — per-tool call approval flow (complement to RBAC)
- [Middleware](middleware.md) — `ToolFilter` and how filters compose
