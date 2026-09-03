# Checkpointing

`agent/checkpoint` is a durable, versioned, thread-scoped state store with history and point-in-time recovery. It is a general-purpose primitive for paused handoffs or your own long-running process.

```
go get github.com/camilbinas/gude-agents/agent/checkpoint
```

## Concepts

A **thread** is a logical unit of resumable work, identified by a string you choose. Each `Save` against a thread appends a **checkpoint** and assigns it the next **version**, starting at 1. Retained versions are never reused or overwritten, so a thread accumulates an ordered timeline you can inspect with `History` and travel through with `LoadAt`. `Delete`, or Redis TTL expiration, removes that timeline; a later `Save` for the same thread starts again at version 1.

```go
type Checkpoint struct {
    ThreadID  string           // assigned on Save
    Version   int              // assigned on Save, starts at 1
    Label     string           // optional marker, surfaced in History
    State     State            // your snapshot payload (map[string]any)
    Usage     agent.TokenUsage // token accounting up to this point
    Timestamp time.Time        // defaulted to now if zero
    Extra     json.RawMessage  // opaque, consumer-owned bookkeeping
}
```

`State` is stored as structured JSON, so backends that support it (Postgres JSONB) keep it queryable and indexable. `Extra` is an opaque JSON value: backends never interpret it and preserve all consumer-owned fields, though JSONB-backed stores may normalize whitespace or key order. Put anything needed to reconstruct consumer bookkeeping there, pre-marshalled as valid JSON.

## Interface

```go
type Checkpointer interface {
    Save(ctx context.Context, threadID string, cp Checkpoint) (Checkpoint, error)
    Load(ctx context.Context, threadID string) (Checkpoint, error)
    LoadAt(ctx context.Context, threadID string, version int) (Checkpoint, error)
    History(ctx context.Context, threadID string) ([]Meta, error)
    List(ctx context.Context) ([]string, error)
    Delete(ctx context.Context, threadID string) error
}
```

`Load` and `LoadAt` return `ErrNotFound` when the thread or version does not exist. `Save` returns `ErrThreadIDRequired` for an empty thread ID. `History` on an unknown thread returns an empty slice, not an error, and `Delete` on an unknown thread is a no-op.

> **`List` is an unbounded enumeration.** Backend cost varies: DynamoDB and Postgres scan stored data, while Redis maintains an index and Memory iterates its in-process map. Treat it as an administrative or development operation, not something to call on a request path.

## Backends

| Backend | Import | Notes |
|---|---|---|
| Memory | `agent/checkpoint` (`NewMemory()`) | Tests and development; lost on exit |
| Postgres | `agent/checkpoint/postgres` | `State` queryable as JSONB |
| Redis | `agent/checkpoint/redis` | Optional thread-level TTL |
| DynamoDB | `agent/checkpoint/dynamodb` | Strongly consistent reads; shared-table namespaces |

```go
import "github.com/camilbinas/gude-agents/agent/checkpoint"

store := checkpoint.NewMemory()

cp, err := store.Save(ctx, "job-42", checkpoint.Checkpoint{
    Label: "fetched",
    State: checkpoint.State{"stage": "fetched", "rows": 128},
})
// cp.Version == 1

latest, err := store.Load(ctx, "job-42")
first, err := store.LoadAt(ctx, "job-42", 1)
timeline, err := store.History(ctx, "job-42")
```

### Postgres

The table must be created by the caller:

```sql
CREATE TABLE checkpoints (
    thread_id   TEXT NOT NULL,
    version     INTEGER NOT NULL,
    label       TEXT NOT NULL DEFAULT '',
    state       JSONB NOT NULL,
    usage       JSONB NOT NULL,
    extra       JSONB,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (thread_id, version)
);
```

```go
import cppg "github.com/camilbinas/gude-agents/agent/checkpoint/postgres"

store, err := cppg.New(pool)                              // table "checkpoints"
store, err := cppg.New(pool, cppg.WithTableName("my_cp"))
```

Because `State` is a real JSONB column, you can query into it:

```sql
SELECT thread_id FROM checkpoints WHERE state->>'stage' = 'review';
```

### Redis

```go
import cpredis "github.com/camilbinas/gude-agents/agent/checkpoint/redis"

store, err := cpredis.New(cpredis.Options{Addr: "127.0.0.1:6379"},
    cpredis.WithTTL(24*time.Hour),
    cpredis.WithKeyPrefix("myapp:cp:"),
)
```

Redis stores each thread as one hash. `WithTTL` expires the complete thread timeline after the configured duration without a successful `Save`; every save refreshes the TTL for the latest checkpoint and all earlier versions together.

### DynamoDB

The table needs partition key `thread_id` (String) and sort key `version` (Number).

```go
import cpddb "github.com/camilbinas/gude-agents/agent/checkpoint/dynamodb"

store, err := cpddb.New(awsCfg, "checkpoints",
    cpddb.WithKeyPrefix("myapp:"),
    cpddb.WithEndpoint("http://localhost:8000"), // DynamoDB Local
)
```

`WithKeyPrefix` defines a shared-table namespace. It is encoded with its length in the physical partition key, so overlapping namespaces cannot expose or overwrite each other's threads. Contract-sensitive reads use DynamoDB strong consistency so a successful `Save` is immediately visible to subsequent operations.

## Using it for handoffs

`agent.HandoffStore` persists paused agents awaiting human input. `handoffstore` implements it on top of any checkpointer:

```go
import (
    "github.com/camilbinas/gude-agents/agent/checkpoint"
    "github.com/camilbinas/gude-agents/agent/checkpoint/handoffstore"
)

store := handoffstore.New(checkpoint.NewMemory())
a, err := agent.New(prov, instructions, tools, agent.WithHandoffStore(store))
```

Each conversation maps to a checkpoint thread, prefixed with `handoff:` by default so it cannot collide with other consumers sharing the same table. Override with `handoffstore.WithThreadPrefix`.

Saves append rather than overwrite, so `LoadHandoff` returns the most recent request while earlier ones remain reachable through the checkpointer's `History` and `LoadAt` — a free audit trail of what the agent asked and when. `DeleteHandoff` discards the whole thread.

See [Handoffs](handoff.md) for the pause/resume flow itself.

## Writing your own backend

Implement `Checkpointer`, then run the shared contract suite against it:

```go
func TestConformance(t *testing.T) {
    checkpoint.RunConformance(t, func(t *testing.T) checkpoint.Checkpointer {
        return newMyStore(t)
    })
}
```

`RunConformance` covers version assignment, concurrent saves, ordering, `ErrNotFound` behaviour, thread isolation, and lossless `Extra` JSON round-tripping including map entries whose value is `false`. That last case matters: decomposing a payload into recognised fields is how a checkpointer silently loses consumer state.
