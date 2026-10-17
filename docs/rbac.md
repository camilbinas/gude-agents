# Identity and authorization

Authorization uses `agent.Principal`, independent from memory identity and strict data scopes.

```go
principal := agent.Principal{
    ID:    "user-42",
    Roles: []string{"support"},
    Attrs: map[string]string{"region": "eu"},
}
ctx := agent.NewContext(parent).
    WithPrincipal(principal).
    WithIdentity("user-42").
    WithScope("account", "acct-9")
```

`Principal.HasRole`, `HasAnyRole`, `Attr`, and `Credential` help policy code. Standard-context handlers use `agent.PrincipalFrom(ctx)`.

## Tool policy

```go
refund := tool.New("refund", "Refund an order", refundHandler,
    tool.AllowRoles("support", "admin"),
    tool.DenyRoles("suspended"),
    tool.AllowWhen(func(attrs map[string]string) bool {
        return attrs["region"] == "eu"
    }),
    tool.RequiresApproval(),
)
```

Role policy and attribute conditions run in the canonical tool pipeline before guards, approval, middleware, and the handler. A denied tool becomes a tool result for the model and is visible to observers/audit.

### Tool visibility is not authorization

`WithToolVisibility` decides which tools the model is *offered* for the current provider call. It is evaluated before every provider call, so the answer can change during the loop, and a tool must pass every rule (AND semantics). A hidden tool is left out of the provider's tool list and out of that loop's execution map, so a call to it fails as an unknown tool.

```go
a, err := agent.New(prov, instructions,
    agent.WithTools(search, refund),
    agent.WithToolVisibility(func(c *agent.Context, t tool.Tool) bool {
        p, _ := c.Principal()
        return t.Spec.Name != "refund" || p.HasRole("support")
    }),
)
```

Visibility shapes the model's choices and keeps irrelevant tools out of the prompt. It is not the security boundary:

| Concern | Mechanism | Runs |
|---|---|---|
| What the model is offered | `WithToolVisibility` | Before every provider call |
| Who may run a tool | `AllowRoles`, `DenyRoles`, `AllowWhen`, `DenyWhen` | When a call executes, including after approval and during recovery |
| Business rules for one call | `WithGuard` | When a call executes, after the role check |
| Human authorization | `RequiresApproval` | Pauses before the handler |
| Cross-cutting behavior | Middleware | Around the handler |

Two helpers wire the role policy into visibility so the model is not offered tools it could not use: `agent.RoleVisibility()` (installed by `agent.WithRoleEnforcement()`) shows a tool only when the Principal satisfies its declared policy, and `agent.RequirePrincipalVisibility()` (installed by `agent.WithRequirePrincipal()`) hides every tool when no Principal is set. Execution still re-checks the declared policy, so an application that forgets to install either helper stays protected. Write custom authorization rules (external policy engines, OPA, Casbin) as tool guards or middleware, not as visibility rules.

### Authentication boundary and framework enforcement

Your application establishes authentication at its request boundary and attaches the resulting `Principal` with `WithPrincipal`. The framework does not authenticate credentials, retrieve roles, or persist an identity for a later caller. It **does** enforce declared `AllowRoles`, `DenyRoles`, `AllowWhen`, and `DenyWhen` policies consistently: a tool with any such policy is unavailable and denied when the current invocation has no `Principal`. This applies when tools are advertised, directly executed, approved after a pause, or replayed during recovery. Tools with no declared policy remain usable without a `Principal`; use `WithRequirePrincipal` when even those tools require authentication.

Resume and recovery authorize against the `Principal` on the current caller context. Applications that need a caller to retain authority across those boundaries must authenticate that caller again and supply the appropriate `Principal`; the framework intentionally does not infer or restore one from persisted execution state.

`Identity` partitions long-term memory. A named `Scope` partitions tenant/account data and never falls back. `ConversationID` selects transcript history. Keep all four explicit at request boundaries.

For stable security records, configure `agent.WithAudit`; content is redacted unless explicitly enabled. See [Observability](observability.md) and [Interrupts](interrupts.md).
