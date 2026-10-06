# Interrupts and resume

Approvals and human questions use one pause/resume model. Normally a pause returns `Result{StopReason: agent.StopInterrupt, Interrupt: ...}` with a nil error. A stream emits `EventInterrupt`, followed by `EventEnd` carrying the same result.

## Approval interrupts

Mark a tool with `tool.RequiresApproval()`:

```go
deleteOrder := tool.New("delete_order", "Delete an order", deleteHandler,
    tool.RequiresApproval(),
)
```

An approval interrupt contains one or more `ApprovalCall` values with call ID, tool name, and JSON input. Continue it with:

```go
result, err := a.Resume(ctx, interrupt, agent.Approve())
result, err = a.Resume(ctx, interrupt, agent.Deny("policy rejected the operation"))
```

For a mixed decision, cover every pending call ID exactly with `agent.Decide`. Validation happens before any handler runs.

### Approval preflight ordering

For an initial batch, approval-required calls are validated through lookup, authorization, schema validation, and guards before **any** sibling handler runs. If one requires approval, the Agent pauses for `InterruptApproval`; a sibling `NewHumanInputTool` is deferred rather than executed. After `Approve`, `Deny`, or `Decide` resumes the batch, deferred siblings execute normally and a human-input tool may then create a new `InterruptHumanInput`.

## Human-input interrupts

Add the built-in pause tool:

```go
askHuman := agent.NewHumanInputTool("ask_human", "Ask when required account data is missing")
a, err := agent.New(prov, instructions, agent.WithTools(askHuman))
```

Continue this type only with `Respond`:

```go
result, err := a.Resume(ctx, interrupt, agent.Respond("Use account ACME-42"))
```

Approval responses are valid only for approval interrupts; `Respond` is valid only for human-input interrupts. Use `ResumeStream` when the continued run must emit application events.

## Durable execution pauses

An interrupt is a projection of a paused `Execution`, not an independently persisted record. Configure an `ExecutionStore` together with a canonical `ConversationStore` for cross-process continuation:

```go
cp := checkpoint.NewMemory()
executions := executionstore.New(cp)

a, err := agent.New(prov, instructions,
    agent.WithConversationStore(conversations),
    agent.WithExecutionStore(executions),
)

interrupt, err := a.LoadInterrupt(ctx, executionID)
result, err := a.Resume(agent.NewContext(ctx), interrupt, agent.Approve())
```

Stateful execution records contain only runtime metadata and a canonical conversation cursor:

```text
ExecutionID
ExecutionVersion
ConversationID
Revision
LastSequence
Pause
```

They never contain transcript messages. Resume reloads canonical history from `ConversationStore`—using a ContextManager range boundary when present—and verifies `Revision` and `LastSequence` before running handlers. A changed canonical cursor returns `agent.ErrConversationConflict`.

Before a durable resume executes any guardrail, tool handler, provider call, or side effect, it atomically transitions the exact observed execution version from `Paused` to `Running`. One concurrent resume wins; stale pause instances return `agent.ErrExecutionConflict`. A later pause in the same execution carries a newer `ExecutionVersion`, so old interrupt objects cannot be replayed.

The Agent commits canonical conversation changes before recording `Paused` execution state. If the conversation commit fails, no durable pause is created. If conversation commit succeeds but execution persistence fails, the error is surfaced and applications must not assume durable resume is available.

For an Agent with neither `ConversationStore` nor `ExecutionStore`, same-process `Invoke → Interrupt → Resume` remains available through private local state. Its `Interrupt.Messages` snapshot is an in-process fallback, not a public durable storage model.
