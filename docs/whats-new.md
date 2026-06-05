# What's New

A quick orientation for returning users. Each entry covers one recently added feature and links to the full reference page.

---

## Widget Blocks

Tool handlers can now attach structured data directly to assistant messages using `c.EmitWidget(...)`. The payload is carried out-of-band — the LLM never sees it, but the event stream delivers it as an `EventWidget` event so your UI can render charts, tables, or any custom component alongside the assistant's text. See [Widget Blocks](widgets.md).

## Human Handoff Improvements

`HandoffStore` — a new interface with `WithHandoffStore(s HandoffStore)` — automatically persists in-flight `HandoffRequest` values when `ErrHandoffRequested` fires and deletes them on successful `Resume`. This makes cross-process and cross-restart resumption work without manual persistence code. The conversation package exposes `MarshalHandoffRequest` / `UnmarshalHandoffRequest` for type-discriminated JSON encoding of `[]Message`. `InvokeEventStream` now emits a dedicated `EventHandoffRequested` event (with `HandoffReason` and `HandoffQuestion` fields) before the terminal `EventInvokeEnd`, so SSE consumers don't need to inspect the error. See [Handoffs](handoff.md).

## RBAC & Identity

Every invocation can now carry a `Principal` (ID, roles, attrs, and short-lived credentials) that tool-level and agent-level policies inspect before allowing execution. Attach it per-call with `WithPrincipal`, restrict individual tools with `tool.AllowRoles`/`tool.DenyRoles`, add attribute-based conditions with `tool.AllowWhen`/`tool.DenyWhen`, and plug in an external authorizer (OPA, Casbin, etc.) via `WithPolicy`. `WithNarrowedRoles` reduces permissions for a cloned context without a new principal. `WithAuditHook` records every tool call (including denials) to a structured audit log. `Principal.Credentials` carries short-lived tokens for on-behalf-of API calls — never logged or emitted by the framework. Identity propagates automatically across A2A hops via `X-Agent-Principal-*` headers, with optional verification via `NewExecutorWithVerify`. See [RBAC & Identity](rbac.md).

## Tool Approval

Mark any tool with `tool.RequiresApproval()` and the agent pauses instead of executing it, emitting an `EventToolApprovalRequired` event with the full `ApprovalRequest`. Your code can then allow or deny via `ResumeWithApproval`, making human-in-the-loop gates a first-class construct rather than a workaround. Graphs integrate via `GraphToolApprovalError` and `g.ResumeWithApproval`. See [Tool Approval](tool-approval.md).

## A2A Protocol

The new `agent/a2a` module implements the Agent-to-Agent protocol on both sides of the wire. `a2a.NewClient` discovers a remote agent's tools and surfaces them locally; `a2a.NewExecutor` exposes a local agent as an A2A-compliant HTTP server; `a2a.NewMultiServer` hosts a fleet of agents behind a single endpoint. See [A2A Protocol](a2a.md).

## AgentCore Integration

The new `agent/agentcore` module wires `gude-agents` into AWS Bedrock AgentCore. `agentcore.NewConversation` provides an AgentCore-backed conversation store; `NewBrowserTool` and `NewCodeInterpreterTool` wrap the managed browser and sandbox runtimes as drop-in `tool.Tool` values; `WithA2A`/`WithA2AAddr` connect agents to the AgentCore A2A endpoint. See [AWS Bedrock AgentCore](agentcore.md).

## Evaluation Framework

The new `agent/eval` package provides a structured pipeline for testing agent and RAG output quality. Define cases with `EvalCase`, run them through `EvalSuite`, and measure results with the three built-in evaluators — `NewFaithfulness`, `NewContextPrecision`, and `NewJSONStructure`. The `Evaluator` interface makes it straightforward to add custom scoring logic. See [Evaluation Framework](eval.md).
