# Conversation persistence

Conversation persistence uses revisioned compare-and-swap (CAS). Conversation IDs come only from `agent.Context.WithConversationID`.

```go
type ConversationSnapshot struct {
    Messages []agent.Message
    Revision uint64
}

type ConversationStore interface {
    Load(context.Context, string) (agent.ConversationSnapshot, error)
    Save(context.Context, string, []agent.Message, uint64) (uint64, error)
}

type ConversationManager interface {
    agent.ConversationStore
    List(context.Context) ([]string, error)
    Delete(context.Context, string) error
}

type Flusher interface {
    Flush(context.Context) error
}
```

A missing conversation loads as an empty snapshot at revision 0. `Save` succeeds only if `expectedRevision` equals the current revision and returns the new revision. Stale writes return an error matching `agent.ErrConversationConflict`; the agent never silently overwrites a winning concurrent turn.

## Configure an agent

```go
store := conversation.NewInMemory()
a, err := agent.New(prov, instructions,
    agent.WithConversationStore(store),
)
ctx := agent.NewContext(context.Background()).WithConversationID("thread-42")
result, err := a.Invoke(ctx, "Remember that my timezone is UTC+1")
```

When a ConversationStore is configured, every invocation requires a non-empty ConversationID. Forgetting it returns an error matching `agent.ErrConversationIDRequired` before any work runs: no guardrails, conversation load, retrieval, provider call, tool execution, background dispatch, or save. This applies equally to `Invoke`, `Stream`, `TextStream`, and `structured.Invoke`.

```go
_, err := a.Invoke(agent.Background(), "hello")
errors.Is(err, agent.ErrConversationIDRequired) // true
```

There is no per-invocation stateless opt-out. Stateless Agents should be constructed without a ConversationStore; on such an Agent a conversation ID is accepted but has no persistence effect.

Resume is bound to the conversation ID captured by its interrupt; the resume Context's ID is never substituted. Resuming an interrupt without a conversation ID through an Agent with a store fails with `agent.ErrConversationIDRequired` and leaves the interrupt unclaimed.

`WithSyncConversation` calls `Flush` after each successful save when supported. `Agent.Shutdown` also flushes after waiting for background work.

## Implement a store

```go
snapshot, err := store.Load(ctx, id)
if err != nil { return err }
nextRevision, err := store.Save(ctx, id, updated, snapshot.Revision)
if errors.Is(err, agent.ErrConversationConflict) {
    // reload, reconcile, or ask the caller to retry
}
_ = nextRevision
```

Copy message slices at the storage boundary. Make revision checks and writes atomic. A `ConversationManager` is optional and enables administrative list/delete operations. `agent.ForkConversation` copies a snapshot to a new ID at revision 0.

## Strategies

The `conversation` package provides window, token, filter, summary, and token-summary wrappers. Wrappers preserve the inner store's revision and manager capabilities. Background summarizers reload and retry on conflicts rather than replacing newer turns. Flush asynchronous strategies before shutdown.

## Backends and schema migration

Available implementations include memory, Redis, PostgreSQL, SQLite, and DynamoDB. Production backends must persist both serialized messages and a monotonic revision:

- **Redis**: update messages and revision in one atomic script/transaction.
- **PostgreSQL/SQLite**: keep a non-null revision column and use conditional insert/update.
- **DynamoDB**: use a condition expression on the revision attribute.

When upgrading an existing schema, add/initialize revision as `0` for legacy rows and deploy the conditional write path before allowing concurrent writers. Do not synthesize revision from timestamps or message counts. Back up persistent data, test the migration against a copy, and verify a second save with the same expected revision fails.

Backend constructors and options live in `agent/conversation/<backend>`. Keep credentials in the backend client's standard configuration rather than conversation IDs or context KV.
