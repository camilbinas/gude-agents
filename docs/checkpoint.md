# Checkpointing

`agent/checkpoint` is a general-purpose versioned state store. It is separate from conversation persistence: checkpoints keep successive snapshots and history; conversations use CAS revisions for transcripts.

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

`SaveIfVersion` atomically appends only when the latest version equals the expected version. Expected version `0` requires an absent thread; stale expectations wrap `checkpoint.ErrConflict`. This conditional append powers one-shot durable interrupt claims.

`Checkpoint` contains thread ID, version, label, caller-owned `State`, token usage, timestamp, and opaque JSON `Extra`. Versions start at 1 and increase per thread. Use `checkpoint.CopyState` before retaining maps that callers might mutate.

The package provides an in-memory implementation; backend modules provide durable alternatives. `List` can require a storage scan and belongs on administrative paths.

## Interrupt storage

Adapt a checkpointer to the agent's durable pause contract:

```go
import (
    "github.com/camilbinas/gude-agents/agent"
    "github.com/camilbinas/gude-agents/agent/checkpoint"
    "github.com/camilbinas/gude-agents/agent/checkpoint/interruptstore"
)

cp := checkpoint.NewMemory()
interrupts := interruptstore.New(cp)
a, err := agent.New(prov, instructions,
    agent.WithInterruptStore(interrupts),
)
```

The adapter namespaces interrupt IDs, stores resumable snapshots in `Checkpoint.Extra`, and implements `agent.InterruptStore`. `interruptstore.WithThreadPrefix` changes its namespace. See [Interrupts](interrupts.md).
