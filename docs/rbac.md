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

`Identity` partitions long-term memory. A named `Scope` partitions tenant/account data and never falls back. `ConversationID` selects transcript history. Keep all four explicit at request boundaries.

For stable security records, configure `agent.WithAudit`; content is redacted unless explicitly enabled. See [Observability](observability.md) and [Interrupts](interrupts.md).
