# Interrupts and resume

Approvals and human questions use one pause/resume model. Normally a pause returns `Result{StopReason: agent.StopInterrupt, Interrupt: ...}` with a nil error. A stream emits `EventInterrupt`, followed by `EventEnd` carrying the same result.

A paused run is persisted execution state. Pausing and resuming therefore require both a `ConversationStore` (the canonical transcript) and an `ExecutionStore` (the resumable state); see [Execution pauses](#execution-pauses). An agent without both stores cannot pause: the run fails with `agent.ErrPauseStorageRequired`.

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
a, err := agent.New(prov, instructions,
    agent.WithTools(askHuman),
    agent.WithConversationStore(conversations),
    agent.WithExecutionStore(executions),
)
```

Continue this type only with `Respond`:

```go
result, err := a.Resume(ctx, interrupt, agent.Respond("Use account ACME-42"))
```

Approval responses are valid only for approval interrupts; `Respond` is valid only for human-input interrupts. Use `ResumeStream` when the continued run must emit application events.

## Typed human input

`agent.NewHumanInput[R]` asks for a typed answer of type `R` instead of free text. The pause carries `R`'s JSON schema in `Interrupt.Input.Schema`, so a UI can render a form, and resume with `agent.RespondWith`:

```go
type Review struct {
    Approved bool   `json:"approved" required:"true"`
    Edited   string `json:"edited"`
}

askReview := agent.NewHumanInput("ask_review", "approve or edit the draft",
    func(ctx *agent.Context, r Review) (string, error) {
        if !r.Approved {
            return "Human rejected the draft.", nil
        }
        return "Human approved: " + r.Edited, nil
    })

// ... run pauses with InterruptHumanInput; in.Input.Schema is R's schema ...
result, err := a.Resume(ctx, in, agent.RespondWith(Review{Approved: true, Edited: "final"}))
```

`RespondWith` marshals the value and validates it against `in.Input.Schema` (the framework's [supported JSON Schema subset](structured-output.md)) before the run continues. A schema-invalid answer returns an `*agent.ResumeValidationError` and leaves the run paused, so the caller can fix the value and resume again. On a valid answer the handler receives the typed `R` and its result is appended to the transcript. The schema and the pending answer survive `ExecutionStore` persistence and `Snapshot`/`Restore`. Resume a typed pause with `RespondWith`, not `Respond`; the free-text `NewHumanInputTool` is unchanged and still resumes with `Respond`.

## Execution pauses
An interrupt is a projection of a paused `Execution`, not an independently persisted record. There is one persistence model: configure an `ExecutionStore` together with a canonical `ConversationStore`. The package `agent/execution` provides the stores:

```go
// Memory-backed, explicit, and process-local (examples, tests, development):
conversations := conversation.NewInMemory()
executions := execution.NewMemory()

a, err := agent.New(prov, instructions,
    agent.WithConversationStore(conversations),
    agent.WithExecutionStore(executions),
)
interrupt, err := a.LoadInterrupt(ctx, executionID)
result, err := a.Resume(agent.NewContext(ctx), interrupt, agent.Approve())
```

For a pause that survives a restart with no server, `sqlite.Open` sets up both durable halves on one file:

```go
import sqlitestores "github.com/camilbinas/gude-agents/agent/execution/sqlite"

stores, err := sqlitestores.Open("file:agent.db")
if err != nil { ... }
defer stores.Close()

a, err := agent.Agent(prov, instructions, stores.Options()...)
```

Other durable backends (`pgexec`, `redis`, `dynamodb`) are wired per store; see [Execution stores](execution-stores.md).

See [Execution stores](execution-stores.md) for the backends and their table or key layouts. Memory-backed stores lose the transcript and every paused execution when the process exits, are not shared between processes, and never evict. The agent never creates them on its own; pick them explicitly. A conversation store without an execution store is fine for ordinary runs that never pause.

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

The Agent commits canonical conversation changes before recording `Paused` execution state. If the conversation commit fails, no pause is created. If the conversation commit succeeds but execution persistence fails, the error is surfaced and applications must not assume resume is available. This ordering is not a distributed transaction: the two stores commit independently.

The Agent keeps no process-local pause state. If a run needs to pause (approval or human input) and either store is missing, it returns `agent.ErrPauseStorageRequired` instead of creating a pause that could not be resumed. Resuming without both stores returns the same error. The same rule applies to a child agent used through `AgentAsTool`: a child pause is not propagated to the parent, and a child without both stores cannot pause at all.

## Snapshot and restore

A pause normally lives in two stores. For a small setup that pauses in one process and resumes in another, `Snapshot` serializes a pause as one portable value and `Restore` loads it back:

```go
blob, err := a.Snapshot(ctx, interrupt) // []byte: the Execution plus the transcript it points at
// ... hand blob to another process, a queue, a file ...
in, err := a.Restore(ctx, blob)         // loads it into this Runtime's stores
result, err := a.Resume(agent.NewContext(ctx), in, agent.Approve())
```

The blob bundles the paused `Execution` record with the canonical messages its cursor references, carries a `format` version, and is checksummed. `Restore` rejects a blob that is empty or of an unknown format (`agent.ErrSnapshotFormat`), whose checksum or cursor does not match or whose execution is not a resumable pause (`agent.ErrSnapshotIntegrity`), and a conversation that already exists with different history (`agent.ErrConversationConflict`). It writes create-only, so a duplicate ID conflicts rather than overwriting; `Snapshot`/`Restore` never weaken the CAS guarantees of a real store.

In-memory stores are enough on both sides, because the blob carries the state:

```go
a, err := agent.Agent(provider, instructions,
    agent.WithConversationStore(conversation.NewInMemory()),
    agent.WithExecutionStore(execution.NewMemory()),
)
```

Without both stores, `Snapshot` and `Restore` return `agent.ErrSnapshotStoreRequired`.

### Frontend version safety

A UI must submit a decision against the `ExecutionVersion` displayed with the pause. Never reload a newer pause and silently apply an answer that was reviewed for an older version. HTTP/API handlers should load the pause, compare the submitted version, return `409 Conflict` on mismatch, then call `Resume`; the execution CAS remains the final race-safe guard.
