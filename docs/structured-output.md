# Structured output

The `agent/structured` package returns a decoded value and the complete agent run:

```go
package main

import (
    "log"

    "github.com/camilbinas/gude-agents/agent"
    "github.com/camilbinas/gude-agents/agent/structured"
)

type Classification struct {
    Category   string  `json:"category"`
    Confidence float64 `json:"confidence"`
}

func classify(ctx *agent.Context, a *agent.Agent, text string) (Classification, error) {
    result, err := structured.Invoke[Classification](ctx, a, "Classify: "+text)
    if err != nil {
        return Classification{}, err
    }
    log.Printf("tokens=%d stop=%s", result.Run.Usage.Total(), result.Run.StopReason)
    return result.Value, nil
}
```

```go
type Result[T any] struct {
    Value T
    Run   agent.Result
}
```

The schema is generated from `T`. Structured invocation reuses the normal agent lifecycle: conversation CAS locking and persistence, RAG context, guardrails, provider retry/timeout, token limits, caching, and operational observers. When the Agent has a conversation store, a non-empty conversation ID is required; without one the call returns an error matching `agent.ErrConversationIDRequired` before the provider runs.

Token usage is accumulated before output decoding. If cumulative usage is greater than `WithTokenBudget`, invocation returns `agent.ErrTokenBudgetExceeded` before output guardrails, decoding, or conversation persistence. Usage exactly equal to the budget is allowed. On any error, the generic result still carries the underlying `Run`, including usage accumulated before the failure; callers should inspect both values when accounting matters.

Handle malformed or non-conforming model output as `*agent.StructuredOutputError`.

Use pointer/optional fields when omission is valid and JSON tags for stable names. Keep schemas reasonably small and descriptions unambiguous.
