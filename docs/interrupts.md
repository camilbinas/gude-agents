# Interrupts and resume

Approvals and human questions use one pause/resume model. Normally a pause returns `Result{StopReason: agent.StopInterrupt, Interrupt: ...}` with a nil error. A stream emits `EventInterrupt`, followed by `EventEnd` carrying the same result.

A pause can also return that usable interrupt result **and** an error when its conversation commit succeeded but a later synchronous flush or durable interrupt operation failed. The interrupt event and observer still fire so the stream truthfully reports that execution paused. If the conversation commit itself fails, no interrupt is emitted or returned.

## Approval interrupts

Mark a tool with `tool.RequiresApproval()`:

```go
deleteOrder := tool.New("delete_order", "Delete an order", deleteHandler,
    tool.RequiresApproval(),
)
```

An approval interrupt contains one or more `ApprovalCall` values with `CallID`, tool name, and JSON input. Continue all calls with:

```go
result, err := a.Resume(ctx, interrupt, agent.Approve())
result, err = a.Resume(ctx, interrupt, agent.Deny("policy rejected the operation"))
```

For a mixed decision, cover every pending call ID exactly:

```go
decisions := map[string]tool.Decision{
    interrupt.Approval.Calls[0].CallID: tool.Allow(),
    interrupt.Approval.Calls[1].CallID: tool.Deny("amount exceeds limit"),
}
result, err := a.Resume(ctx, interrupt, agent.Decide(decisions))
```

Validation happens before any handler runs.

### Approval preflight ordering

For an initial tool batch, approval-required calls are validated through lookup, authorization, schema validation, and guards before **any** sibling handler runs. If one requires approval, the Agent pauses for `InterruptApproval`; a sibling `NewHumanInputTool` is deferred rather than executed. After `Approve`, `Deny`, or `Decide` resumes the batch, deferred siblings execute normally and a human-input tool may then create a new `InterruptHumanInput`.

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

## Durable interrupts

Configure an `InterruptStore` to resume from another process:

```go
type InterruptStore interface {
    Save(context.Context, *agent.Interrupt) error
    Load(context.Context, string) (*agent.Interrupt, error)
    Claim(context.Context, string) (*agent.Interrupt, error)
}
```

`Save` is create-only for an interrupt ID. `Load` returns only pending interrupts. `Claim` atomically consumes and returns the canonical stored interrupt; exactly one concurrent claimant may succeed, while missing or consumed IDs wrap `agent.ErrInterruptNotFound`.

```go
a, err := agent.New(prov, instructions, agent.WithInterruptStore(store))
in, err := a.LoadInterrupt(ctx, interruptID)
result, err := a.Resume(agent.NewContext(ctx), in, agent.Approve())
```

The agent commits the conversation snapshot first, then creates the durable interrupt. Resume loads the canonical record, validates the response, atomically claims it, validates the claimed record again, and only then runs guardrails, tools, or providers. A claimed interrupt remains consumed even if later work fails; this favors at-most-once side effects over automatic retry. If resume pauses again, it creates a new interrupt ID—the predecessor stays consumed.

Conversation-save failures suppress the pause. A later flush or interrupt-save failure returns the pause as a recovery snapshot with the error. With an explicitly configured store, that store remains authoritative: confirm or repair durable persistence before attempting resume rather than assuming the recovery snapshot is executable. Loading without a configured store returns `agent.ErrNoInterruptStore`.

For a stateful conversation, the durable interrupt normally stores the canonical cursor rather than a transcript:

```text
ConversationID
Revision
LastSequence
Messages = empty
```

Resume reloads canonical history from the `ConversationStore`—using a range boundary when a ContextManager supplies one—and verifies the canonical cursor before handlers run. A changed revision or last sequence returns `agent.ErrConversationConflict`.

For a stateless invocation, there is no canonical store to reload. In that case `Interrupt.Messages` contains the resumable snapshot and remains present in the durable envelope for compatibility. The durable serializer therefore supports `Messages`, but callers should not expect populated messages for a normal persisted conversation.

Use `conversation.MarshalInterrupt` and `conversation.UnmarshalInterrupt` when implementing durable stores; their JSON representation carries the conversation cursor and conditionally carries stateless messages. Alternatively, adapt a checkpointer with [`checkpoint/interruptstore`](checkpoint.md#interrupt-storage).
