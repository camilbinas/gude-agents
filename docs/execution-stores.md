# Execution stores

An `agent.ExecutionStore` holds the bounded, resumable state of unfinished agent work: a paused approval or human-input request, and the tool batch that recovery needs after a crash. It is the only persistence contract for that state.

```go
type ExecutionStore interface {
    Create(ctx context.Context, execution Execution) (Execution, error)
    Load(ctx context.Context, executionID string) (Execution, error)
    Save(ctx context.Context, execution Execution, expectedVersion uint64) (Execution, error)
}
```

- `Create` requires an absent execution ID and returns the execution at version 1. A duplicate returns `agent.ErrExecutionConflict`.
- `Load` returns the current execution, or `agent.ErrExecutionNotFound`.
- `Save` is a compare-and-swap on the exact expected version. A stale, zero, or missing version returns `agent.ErrExecutionConflict`. On success the backend assigns the next version, and the returned `Execution.Version` is that durable value. A caller-supplied `Version` field is ignored.

An execution stores status, phase, pause payloads, the active tool batch, usage, the iteration, and a cursor (`ConversationID`, `Revision`, `LastSequence`) into the canonical transcript. It never contains messages.

## Choosing a store

| Package | Use |
|---|---|
| `agent/execution` (`execution.NewMemory()`) | Tests, development, and examples. Process-local. |
| `agent/execution/sqlite` | Durable with no server: one local file. Good for single-host services, CLIs and desktop agents. |
| `agent/execution/postgres` | Durable, one row per execution. |
| `agent/execution/redis` | Durable only if Redis persistence is configured; optional TTL. |
| `agent/execution/dynamodb` | Durable, one item per execution. |

Each backend is its own module and constructs an `agent.ExecutionStore` directly. Use an import alias when you also use a conversation backend from the same provider:

```go
import (
    "github.com/camilbinas/gude-agents/agent"
    pgexec "github.com/camilbinas/gude-agents/agent/execution/postgres"
)

executions := pgexec.New(pool) // *pgxpool.Pool
a, err := agent.New(provider, instructions,
    agent.WithConversationStore(conversations),
    agent.WithExecutionStore(executions),
)
```

```go
sqliteexec.New("file:agent.db")                      // returns (*Store, error); Close when done
redisexec.New(client)                                // goredis.UniversalClient
ddbexec.New(dynamoClient, ddbexec.WithTableName("t")) // *dynamodb.Client
```

The smallest setup that survives a restart is SQLite for both halves, in one file:

```go
import (
    sqliteconv "github.com/camilbinas/gude-agents/agent/conversation/sqlite"
    sqliteexec "github.com/camilbinas/gude-agents/agent/execution/sqlite"
)

conversations, err := sqliteconv.New("file:agent.db")
executions, err := sqliteexec.New("file:agent.db")
defer executions.Close()
```

## Memory

```go
conversations := conversation.NewInMemory()
executions := execution.NewMemory()

a, err := agent.New(
    provider,
    instructions,
    agent.WithConversationStore(conversations),
    agent.WithExecutionStore(executions),
)
```

`execution.NewMemory()` is process-local development, test, and example storage. Each call returns an independent store. Every execution, including paused ones, is lost when the process exits, nothing is shared between processes, and there is no eviction, so a long-running service accumulates state until restart.

## Durable pause and resume

A pause that must survive a restart needs both halves to be durable: a durable `ConversationStore` for the transcript and a durable execution store for the paused state. A durable execution store with an in-memory conversation store loses the transcript the execution points at.

The two stores commit independently. The engine appends messages first and then records the returned cursor in the execution, so a crash between them is repaired by `RecoverExecution` rather than rolled back. This is an ordering protocol, not a distributed transaction.

A terminal non-success execution records why it ended. `Execution.FailureCode` is a bounded classification (`provider`, `guardrail`, `tool`, `max_iterations`, `token_budget`, `rate_limit`, `canceled`, `internal`) and `Execution.FailureMessage` is the truncated error text, so the durable record is inspectable after a crash without re-running anything. `canceled` is reserved for the invocation context itself being done (caller cancellation or a caller-set deadline); a provider-side `WithProviderTimeout` deadline is a `provider` failure, not a cancellation. Both fields are empty on a completed or still-running execution.

These two fields are stored inside the same JSON payload as the rest of the execution, so every backend round-trips them with no schema change. The `payload` column (SQLite, PostgreSQL), hash field (Redis) and attribute (DynamoDB) already hold the whole execution as JSON; an older row written before these fields existed simply decodes them as empty. No migration is required.

## Backend layouts

### PostgreSQL

The caller creates the table (default name `executions`, change it with `WithTableName`):

```sql
CREATE TABLE executions (
    execution_id TEXT        PRIMARY KEY,
    version      BIGINT      NOT NULL CHECK (version > 0),
    payload      JSON        NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

`Save` is a single `UPDATE ... WHERE execution_id = $1 AND version = $2 RETURNING version`. `payload` is `JSON`, not `JSONB`, so stored text round trips byte for byte.

### SQLite

`New` creates the table if it is missing (default name `executions`, change it with `WithTableName`). It uses the same layout as PostgreSQL with `payload` as JSON `TEXT`, queryable with `json_extract`. `Save` is a single `UPDATE ... RETURNING version`. Write transactions take the lock up front and wait up to `WithBusyTimeout` (default 5s), so several processes can share one file. An in-memory DSN keeps a single connection so every call sees the same database.

### Redis

One hash per execution (`<prefix><id>`, default prefix `gude:execution:`) with `version` and `payload` fields. `Create` and `Save` are Lua scripts, so the check and the write are one atomic step. `WithTTL` expires an execution that long after its last write; an expired pause can no longer be resumed.

### DynamoDB

One item per execution. Create the table with a single partition key, `execution_id` (String). Items hold `execution_id`, `version` (Number) and `payload` (String). `Create` is a conditional put requiring the item to be absent, `Save` a conditional put requiring `version = :expected`, and reads are strongly consistent. `WithKeyPrefix` isolates stores that share a table.

## Retention

`ExecutionStore` has no delete or expiry operation. Terminal executions remain in the store. Memory never evicts. Redis can expire with `WithTTL`. SQLite, PostgreSQL and DynamoDB keep rows until you remove them with your own retention job, for example deleting rows whose payload `status` is terminal and whose `updated_at` is old. Retention policy is deliberately left to the application.

## Testing a store

Every backend runs the same conformance suite (create-only semantics, exact-version CAS, concurrent single winner, caller-version rejection, full-field round trip, and value isolation) against memory, SQLite, Redis (miniredis), a DynamoDB fake, and PostgreSQL when `POSTGRES_URL` is set.
