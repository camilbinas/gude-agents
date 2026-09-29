# Long-term memory

Memory stores typed facts independently from conversation transcripts. The partition key is an invocation identity or an explicitly configured strict scope.

```go
type Memory[T any] interface {
    Remember(context.Context, string, T) error
    Recall(context.Context, string, memory.RecallQuery) ([]memory.Entry[T], error)
}

type RecallQuery struct {
    Text          string
    Limit         int
    MinSimilarity float64
    Filters       []memory.Filter
    Order         []memory.Order
}
```

Optional `Updater`, `Forgetter`, and `BulkForgetter` capabilities support mutation/deletion.

```go
query := memory.RecallQuery{
    Text: "preferred deployment region",
    Limit: 5,
    MinSimilarity: 0.7,
    Filters: []memory.Filter{{
        Field: "category", Operator: memory.FilterEqual, Value: "preference",
    }},
    Order: []memory.Order{{
        Field: "created_at", Direction: memory.OrderDescending,
    }},
}
entries, err := store.Recall(ctx, "user-42", query)
```

Backends must return errors matching `memory.ErrUnsupportedFilter` or `memory.ErrUnsupportedOrder` rather than silently ignoring requested behavior. Invalid limits/thresholds match `memory.ErrInvalidRecallQuery`.

## Identity and strict scopes

Set identity on the invocation:

```go
ctx := agent.NewContext(parent).WithIdentity("user-42")
```

Memory tools without a configured scope use `agent.IdentityFrom`. A tool configured for a scope such as `account` uses only `agent.ScopeFrom(ctx, "account")`; absence is an error matching `memory.ErrMissingIdentity`. It never falls back to identity.

In-memory, PostgreSQL, and Redis implementations are available. PostgreSQL and Redis provide typed remember/update tools with naming/description options and strict `WithScope`. Treat memory data as user data: validate tenant partitions, retention, deletion, and encryption for your deployment.
