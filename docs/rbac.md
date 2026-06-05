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
    ID          string            // unique identifier (user ID, service account, etc.)
    Roles       []string          // roles assigned to this caller
    Attrs       map[string]string // arbitrary key-value attributes (org_id, tier, region, etc.)
    Credentials map[string]string // short-lived tokens (access_token, id_token, etc.) — never logged by the framework
}
```

Helper methods on `Principal`:

| Method | Description |
|--------|-------------|
| `HasRole(role string) bool` | Reports whether the principal holds the given role |
| `HasAnyRole(roles ...string) bool` | Reports whether the principal holds at least one of the given roles |
| `Attr(key string) string` | Returns an attribute value, or empty string if not set |
| `Credential(key string) string` | Returns a credential value, or empty string if not set |

`Credentials` is intended for short-lived tokens that tool handlers use to call external APIs on behalf of the caller. The framework never logs, emits, or transmits credential values — they are invisible to the LLM, the event stream, and the audit hook.

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

## Attribute-Based Access Control (ABAC)

Role checks are coarse — the same role grants the same access everywhere. ABAC lets you add fine-grained conditions on `Principal.Attrs`.

### `tool.AllowWhen(cond func(attrs map[string]string) bool) func(*Tool)`

Adds an attribute condition that must pass for the tool to be allowed. Multiple `AllowWhen` conditions are **AND**ed — all must return `true`.

```go
// Only enterprise orgs with a verified account can run this tool
tool.NewRaw("batch_export", "...", schema, handler,
    tool.AllowRoles("analyst", "admin"),
    tool.AllowWhen(func(attrs map[string]string) bool {
        return attrs["plan"] == "enterprise"
    }),
    tool.AllowWhen(func(attrs map[string]string) bool {
        return attrs["account_verified"] == "true"
    }),
)
```

### `tool.DenyWhen(cond func(attrs map[string]string) bool) func(*Tool)`

Adds an attribute condition that, when it returns `true`, denies the call regardless of role checks or `AllowWhen` conditions. Multiple `DenyWhen` conditions are **OR**ed — any single match denies.

```go
// Block access from restricted regions even for admins
tool.NewRaw("restricted_api", "...", schema, handler,
    tool.AllowRoles("admin"),
    tool.DenyWhen(func(attrs map[string]string) bool {
        return attrs["region"] == "restricted"
    }),
)
```

Evaluation order: `DenyWhen` → role checks → `AllowWhen`. Deny always wins.

### `(Tool).AllowedWithAttrs(roles []string, attrs map[string]string) bool`

Reports whether the given roles and attribute map satisfy the tool's full policy (role allowlist/denylist + ABAC conditions). Used internally by `RoleFilter` — rarely called directly.

### `(Tool).RolesAllowed(roles []string) bool`

Reports whether the given roles satisfy the tool's role policy. Returns `true` when no policy is set. Does not evaluate ABAC conditions.

## Scope Narrowing

When delegating to a sub-task or sub-agent, you may want to reduce the principal's permissions without creating a new principal.

### `(*Context).WithNarrowedRoles(allowed ...string) *Context`

Returns a cloned context with the principal's roles replaced by the intersection of the current roles and the `allowed` set. If no principal is on the context, returns `c` unchanged.

```go
adminCtx := agent.Background().WithPrincipal(agent.Principal{
    ID:    "alice",
    Roles: []string{"admin", "support"},
})

// Sub-task runs with only read access
readOnlyCtx := adminCtx.WithNarrowedRoles("viewer")

summary, _ := summaryAgent.Invoke(readOnlyCtx, "summarize the last 7 days")
// adminCtx still has admin + support — the clone is independent
```

## Audit Hook

`AuditHook` receives structured audit events for every tool call, invocation lifecycle, handoff, and approval pause.

```go
type AuditRecord struct {
    Principal      Principal       // zero value if no principal was set
    ToolName       string
    ToolInput      json.RawMessage // nil when CaptureContent is false
    ToolOutput     string          // empty when CaptureContent is false or on denial
    Err            error           // nil on success
    Allowed        bool            // false when denied by role/attr policy or guard
    DenialReason   string          // "role_policy", "attr_condition", "guard", or "tool_approval_denied"
    ConversationID string          // empty when no conversation ID is resolved
    Duration       time.Duration
    Timestamp      time.Time
}

// AuditHook receives audit events from the agent.
// Embed NoopAuditHook to satisfy the interface without implementing every method.
type AuditHook interface {
    OnToolCall(record AuditRecord)                // called after every tool call
    OnInvokeStart(record InvokeAuditRecord)       // called at the start of Invoke / InvokeStream
    OnInvokeEnd(record InvokeAuditRecord)         // called at the end of Invoke / InvokeStream
    OnHandoff(record HandoffAuditRecord)          // called when the agent pauses for human input
    OnApprovalRequest(record ApprovalAuditRecord) // called when a tool requires explicit approval
}
```

`NoopAuditHook` provides empty implementations of all five methods — embed it to satisfy the interface without implementing everything:

```go
type MyHook struct {
    agent.NoopAuditHook  // satisfies OnInvokeStart, OnInvokeEnd, OnHandoff, OnApprovalRequest
}

func (h *MyHook) OnToolCall(r agent.AuditRecord) {
    // only implement what you need
}
```

### `WithAuditHook(cfg AuditConfig) Option`

Installs an `AuditHook` on the agent. Returns an error if `cfg.Hook` is nil.

`AuditConfig.CaptureContent` controls whether `ToolInput`, `ToolOutput`, `UserMessage`, and `Response` are populated in audit records. Default is `false` — omit content to avoid logging sensitive data in production; set to `true` for debug environments where you need full payloads.

```go
type SIEMHook struct {
    agent.NoopAuditHook
    log *slog.Logger
}

func (h *SIEMHook) OnToolCall(r agent.AuditRecord) {
    h.log.Info("tool_call",
        "user",    r.Principal.ID,
        "tool",    r.ToolName,
        "allowed", r.Allowed,
        "reason",  r.DenialReason,
        "conv",    r.ConversationID,
        "ms",      r.Duration.Milliseconds(),
    )
}

func (h *SIEMHook) OnInvokeEnd(r agent.InvokeAuditRecord) {
    h.log.Info("invoke_end",
        "agent",  r.AgentName,
        "conv",   r.ConversationID,
        "tokens", r.Usage.Total(),
        "ms",     r.Duration.Milliseconds(),
        "err",    r.Err,
    )
}

a, _ := agent.New(provider, instructions, tools,
    agent.WithRoleEnforcement(),
    agent.WithAuditHook(agent.AuditConfig{
        Hook:           &SIEMHook{log: slog.Default()},
        CaptureContent: false, // omit payloads in production
    }),
)
```

> **Note:** The audit hook is called at execution time — after the tool handler runs (or is denied at the execution gate). Tools filtered out by `WithRoleEnforcement` before the provider call are not visible to the LLM and produce no `AuditRecord`. This is by design: filtering is access design, not a denial event.

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

## A2A Identity Propagation

When an agent node calls a sub-agent via the A2A protocol, the `Principal` on the context is automatically forwarded as HTTP headers by `a2a.Client`. The sub-agent's `Executor` reads those headers and sets `WithPrincipal` on its agent context before invoking, so the sub-agent can enforce its own RBAC policies against the same principal.

| Header | Contents |
|--------|----------|
| `X-Agent-Principal-ID` | `Principal.ID` |
| `X-Agent-Principal-Roles` | `Principal.Roles` joined with commas |
| `X-Agent-Principal-Attrs` | `Principal.Attrs` as a JSON object |

No code is needed on the orchestrator side — the `a2a.Client` tool handler picks up the principal from the context automatically.

### Trust model

By default the sub-agent trusts these headers as-is — safe for internal service meshes. For internet-facing deployments, use `a2a.NewExecutorWithVerify`:

```go
executor := a2a.NewExecutorWithVerify(a, nil, func(p agent.Principal) (agent.Principal, error) {
    // validate a signature, look up the principal in an identity store, etc.
    claims, err := verifyIDToken(p.Attrs["id_token"])
    if err != nil {
        return agent.Principal{}, err  // task fails with a failed status
    }
    return agent.Principal{ID: claims.Sub, Roles: claims.Roles}, nil
})
```

If `verify` returns an error, the A2A task immediately fails with a failed status event. If it returns a modified principal, that principal is used instead of the raw headers.

### `PrincipalFromRequest(r *http.Request) (Principal, bool)`

Helper for plain HTTP servers (non-A2A) that want to extract a propagated principal from an inbound request:

```go
p, ok := a2a.PrincipalFromRequest(r)
if ok {
    c = c.WithPrincipal(p)
}
```

## See Also

- [Tool System](tools.md) — `tool.AllowRoles`, `tool.DenyRoles`, `tool.AllowWhen`, `tool.DenyWhen`, `tool.WithGuard`, and tool constructors
- [Invocation Context](invocation-context.md) — `WithPrincipal` in the chainable mutators table
- [Handoffs](handoff.md) — pausing the agent loop for human approval (compare with tool approval)
- [Tool Approval](tool-approval.md) — per-tool call approval flow (complement to RBAC)
- [Middleware](middleware.md) — `ToolFilter` and how filters compose
- [A2A Protocol](a2a.md) — `NewExecutorWithVerify` and identity propagation across agent hops
