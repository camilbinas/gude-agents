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

Handle malformed or non-conforming model output as `*agent.StructuredOutputError`. After output guardrails run, the JSON is checked in this order, all before the turn is persisted:

1. It must be valid JSON. Otherwise `Reason` is `"deserialize"`.
2. It must satisfy the schema within the supported subset. Otherwise `Reason` is `"schema_validation"`.
3. It must decode into `T`. Otherwise `Reason` is `"deserialize"`.

Schema validation uses `agent.ValidateToolInput`, which enforces a subset of JSON Schema, not the full specification. Enforced keywords: `type` (including unions such as `["string", "null"]`), `enum`, `required`, `properties`, and `items`, applied recursively. Other keywords (for example `minimum`, `maxLength`, `pattern`, `additionalProperties`, `oneOf`) are sent to the model as guidance but are not enforced. For schemas generated from `T`, Go decoding still rejects values whose types don't fit `T`, such as map values of the wrong type. Check any other constraints yourself after `Invoke` returns.

Use pointer/optional fields when omission is valid and JSON tags for stable names. Keep schemas reasonably small and descriptions unambiguous.
