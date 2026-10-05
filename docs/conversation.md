# Conversation persistence and model context

Conversation history is an append-only canonical event log. Model context is a disposable projection of that log; summaries, windows, RAG content and provider normalization are never written back as conversation messages.

```go
type ConversationCursor struct {
    Revision     uint64 // successful append operations
    LastSequence uint64 // immutable canonical messages
}

type ConversationSnapshot struct {
    Messages     []agent.Message
    Revision     uint64
    LastSequence uint64
}

type ConversationStore interface {
    Load(ctx context.Context, conversationID string) (ConversationSnapshot, error)
    LoadAfter(ctx context.Context, conversationID string, afterSequence uint64) (ConversationSnapshot, error)
    Append(ctx context.Context, conversationID string, messages []agent.Message, expectedRevision uint64) (ConversationCursor, error)
}
```

Sequences begin at 1, are contiguous, and are assigned by the store. `Revision` and `LastSequence` are deliberately different: appending three messages advances the revision once and the sequence three times. `Append` is atomic: a stale revision returns `agent.ErrConversationConflict` and writes no messages. An empty append is a checked no-op.

`Load` returns the complete transcript for exports, audits, debugging and `agent.ForkConversation`. Runtime calls should use `LoadAfter` so a context strategy can avoid loading history already represented by a durable summary. A range read past the end returns no messages but still reports current revision and last sequence.

## Derived context state

`ContextStateStore` holds namespaced, versioned *derived* state:

```go
type ContextStateStore interface {
    LoadContextState(ctx context.Context, conversationID, key string) (agent.ContextStateSnapshot, error)
    SaveContextState(ctx context.Context, conversationID, key string, data json.RawMessage, expectedRevision uint64) (uint64, error)
}
```

Its revisions are independent from conversation CAS. Saving summary state must not change canonical revision, sequence, messages, or `List()` activity ordering. `Delete` removes both canonical events and same-backend state; `ForkConversation` copies canonical events but not derived state.

## Configure an agent

```go
store := conversation.NewInMemory()
manager, _ := contextmanager.NewRollingSummary(
    store,
    contextmanager.ProviderSummarizer(provider, "Preserve facts and decisions."),
    contextmanager.WithMaxInputTokens(100_000),
    contextmanager.WithPreserveRecentTurns(10),
)
a, _ := agent.New(provider, instructions,
    agent.WithConversationStore(store),
    agent.WithContextManager(manager),
)
```

`ContextManager.HistoryBoundary` runs before `LoadAfter`; a rolling summary with `CoveredThrough: 850` therefore causes a normal request to read only messages after 850. The manager receives recent canonical messages, current unsaved messages and transient RAG separately. Its output is provider-only. The agent appends only genuine new user/assistant/tool messages at commit time.

`contextmanager.Window`, `contextmanager.Filter`, and `contextmanager.RollingSummary` replace the old persistence wrappers. `RollingSummary` retains a verbatim recent tail and does not summarize unresolved tool calls or unsaved current input. Its synthetic summary marker is never appended to `ConversationStore`.

## Backends and migration

Memory, PostgreSQL, SQLite, Redis and DynamoDB expose the same append/range/CAS semantics with backend-native storage. PostgreSQL and SQLite migrate legacy `messages` snapshots once into ordered message rows and no longer read/rewrite the blob at runtime. Redis stores metadata plus a same-slot stream. DynamoDB requires a HASH+RANGE layout with `META` and `MSG#...` items; append batches above its 24-message transactional limit fail explicitly rather than partially writing.

For Redis and DynamoDB, deploy the new layout with a migration plan before switching traffic. Existing snapshot keys/items are not silently destroyed or dual-read forever.

### Backend setup

The canonical runnable workflow is [`examples/conversation`](../examples/conversation/). Choose a durable backend by constructing the store, then pass it to `agent.WithConversationStore`; the Agent and ContextManager setup is otherwise unchanged:

```go
store, err := postgres.New(pool) // agent/conversation/postgres
// or: redis.New(opts), dynamodb.New(cfg, table)
if err != nil { return err }
a, err := agent.New(provider, instructions, agent.WithConversationStore(store))
```

PostgreSQL and SQLite store metadata plus append-only message rows. Redis uses same-slot metadata and stream keys. DynamoDB requires the documented partition/sort-key layout. See each backend package's Go documentation for options such as credentials, TTL, key prefixes, and table names.
