# Checkpointing and execution state

`agent/checkpoint` is a generic versioned state primitive. It knows nothing about agents, conversations, tools, A2A, or pauses.

```go
type Checkpointer interface {
    Save(context.Context, string, checkpoint.Checkpoint) (checkpoint.Checkpoint, error)
    SaveIfVersion(context.Context, string, checkpoint.Checkpoint, int) (checkpoint.Checkpoint, error)
    Load(context.Context, string) (checkpoint.Checkpoint, error)
    LoadAt(context.Context, string, int) (checkpoint.Checkpoint, error)
    History(context.Context, string) ([]checkpoint.Meta, error)
    List(context.Context) ([]string, error)
    Delete(context.Context, string) error
}
```

`SaveIfVersion` conditionally appends only when the latest version matches. Expected version `0` requires an absent thread; stale expectations wrap `checkpoint.ErrConflict`. The package provides an in-memory implementation; backend modules provide durable alternatives.

## Execution store adapter

An `ExecutionStore` is the agent-level semantic adapter for durable runtime state. It uses a Checkpointer for CAS and version history while keeping the generic Checkpointer unaware of agent concepts.

```go
import (
    "github.com/camilbinas/gude-agents/agent"
    "github.com/camilbinas/gude-agents/agent/checkpoint"
    "github.com/camilbinas/gude-agents/agent/checkpoint/executionstore"
)

conversations := conversation.NewInMemory()
cp := checkpoint.NewMemory()
executions := executionstore.New(cp)

a, err := agent.New(prov, instructions,
    agent.WithConversationStore(conversations),
    agent.WithExecutionStore(executions),
)
```

An execution stores only runtime metadata: status, phase, pause data, usage, iteration, and the canonical conversation cursor (`ConversationID`, `Revision`, `LastSequence`). It never stores a conversation transcript. Conversation history remains owned by `ConversationStore`; rolling-summary state remains owned by `ContextStateStore`.

The adapter namespaces execution threads as `execution:<executionID>` by default. `executionstore.WithThreadPrefix` changes that namespace. The Checkpointer history remains useful for auditing execution transitions, but applications normally interact only with `ExecutionStore` and `Agent.Resume`.

`Execution.Phase` is the latest **durably recorded** engine boundary, not a live worker heartbeat. This first model records creation, pause/resume, and terminal boundaries; it does not write execution state around every model or tool transition. Consumers must not treat a polled phase as proof that a particular handler is currently running.
