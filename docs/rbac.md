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

### Authentication boundary and framework enforcement

Your application establishes authentication at its request boundary and attaches the resulting `Principal` with `WithPrincipal`. The framework does not authenticate credentials, retrieve roles, or persist an identity for a later caller. It **does** enforce declared `AllowRoles`, `DenyRoles`, `AllowWhen`, and `DenyWhen` policies consistently: a tool with any such policy is unavailable and denied when the current invocation has no `Principal`. This applies when tools are advertised, directly executed, approved after a pause, or replayed during recovery. Tools with no declared policy remain usable without a `Principal`; use `WithRequirePrincipal` when even those tools require authentication.

Resume and recovery authorize against the `Principal` on the current caller context. Applications that need a caller to retain authority across those boundaries must authenticate that caller again and supply the appropriate `Principal`; the framework intentionally does not infer or restore one from persisted execution state.

`Identity` partitions long-term memory. A named `Scope` partitions tenant/account data and never falls back. `ConversationID` selects transcript history. Keep all four explicit at request boundaries.

For stable security records, configure `agent.WithAudit`; content is redacted unless explicitly enabled. See [Observability](observability.md) and [Interrupts](interrupts.md).
