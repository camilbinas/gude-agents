# Tool interception model

gude-agents exposes several mechanisms that sit around tool execution. They
look similar but answer different questions. Reach for the one whose intent
matches yours:

| Intent | Mechanism | Type / option | Where it runs |
| --- | --- | --- | --- |
| **See** what happens (logging, metrics, tracing) without changing it | Observer | `WithObserver`, `ToolObserver` and the other capability interfaces (`observer.go`) | Wraps the whole call, start and end; never alters input, result or the allow/deny decision |
| **Transform** the call or its result (retry, cache, rewrite input/output, inject behavior) | Middleware | `WithMiddleware`, `Middleware` / `ToolHandlerFunc` (`middleware.go`) | Wraps the handler; outermost middleware first |
| **Hide** a tool from the model | Tool visibility | `WithToolVisibility`, `ToolVisibility`; `RoleVisibility` / `WithRoleEnforcement`, `RequirePrincipalVisibility` / `WithRequirePrincipal` (`tool_visibility.go`, `identity.go`) | Before each provider call; a hidden tool is left out of the tool list and the loop's execution map |
| **Authorize** by principal role/attribute | Role & attribute policy | `tool.AllowRoles` / `tool.DenyRoles` / `tool.AllowWhen` on the tool, enforced via `RoleVisibility` and re-checked at execution | At execution (the security boundary); also shapes visibility when `RoleVisibility` is installed |
| **Authorize** by arbitrary logic over the decoded input | Guard | `tool.Guard` on the tool (`tool_guard.go`) | At execution, after schema validation |
| **Require a human** to approve a call before it runs | Approval | `tool.RequiresApproval` (pauses the run with an approval `Interrupt`) | At execution, after the guard passes |

## Visibility is not authorization

`WithToolVisibility` only controls what the model is offered. It shapes the
model's choices; it is not a security boundary. Every role/attribute policy,
guard and approval gate is enforced again when a call actually executes, and
that execution-time check is the boundary. A tool call that reaches the engine
despite being hidden (for example a replayed or hand-crafted call) is still
subject to the full authorization pipeline below.

## Evaluation order at execution

When a tool call executes, the engine runs this pipeline in order (verified
against `tool_exec.go`, `executeCall`). The first step that denies the call
short-circuits the rest:

1. **Lookup** — an unknown tool name fails as `unknown_tool`.
2. **Role / attribute policy (RBAC / ABAC)** — the principal must satisfy the
   tool's `AllowRoles` / `DenyRoles` / `AllowWhen` declarations. Any declared
   policy requires a `Principal`. Denial reason: `role_policy` or
   `attr_condition`. This runs as defense in depth even when visibility already
   hid the tool.
3. **Schema validation** — the input is checked against the supported JSON
   Schema subset (`ValidateToolInput`). Failure is `invalid_input`.
4. **Guard** — `tool.Guard` runs against the raw input; a denial (or a guard
   error) stops the call. Denial reason: `guard`.
5. **Approval gate** — if the tool `NeedsApproval` and no human decision is
   present yet, the run pauses with an approval `Interrupt` before any sibling
   handler starts. On resume, a human `Deny` short-circuits with denial reason
   `tool_approval_denied`; an `Allow` lets the call proceed.
6. **Middleware + handler** — the handler is wrapped by the configured
   middleware chain (outermost first) and executed.

Observers (including the built-in audit observer, `WithAudit`) wrap this whole
pipeline through the lifecycle hooks: they are notified at start and end and
receive the denial reason, but they do not participate in the decision.

### Quick guidance

- Logging a tool call? Use a `ToolObserver`, not middleware.
- Caching or retrying a call, or rewriting its input/output? Use middleware.
- Keeping a tool out of the model's reach for a given principal? Use
  `WithToolVisibility` (or `WithRoleEnforcement`) — and remember the same
  policy is enforced again at execution.
- Enforcing "only admins may call this"? Declare roles on the tool
  (`tool.AllowRoles`) and install `WithRoleEnforcement`; do not try to deny
  inside an Observer — Observers cannot block a call.
- Blocking on arbitrary input logic? Use a `tool.Guard`.
- Needing a human in the loop? Use `tool.RequiresApproval`.
