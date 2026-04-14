# InvocationContext

`InvocationContext` is a concurrency-safe key-value store scoped to a single agent invocation. It lets you share state between middleware and tool handlers without global variables or custom context plumbing.

## How It Works

Every time you call `InvokeStream` (or `Invoke`, which wraps it), the agent creates a fresh `InvocationContext` and attaches it to the Go `context.Context` that flows through middleware and tool handlers. This means:

- Each invocation gets its own isolated store — no cross-request leakage
- Middleware can write values that tool handlers read (and vice versa)
- Concurrent tool executions (when `WithParallelToolExecution` is enabled) can safely read and write to the same `InvocationContext`

## API Reference

### InvocationContext

```go
type InvocationContext struct {
    // unexported: sync.RWMutex + map[any]any
}
```

#### NewInvocationContext

```go
func NewInvocationContext() *InvocationContext
```

Creates a new empty `InvocationContext`. You typically don't need to call this yourself — the agent creates one per invocation. It's useful in tests or if you're building custom invocation flows.

#### Set

```go
func (ic *InvocationContext) Set(key, value any)
```

Stores a value under the given key. Overwrites any existing value for that key. Safe for concurrent use — guarded by a write lock.

#### Get

```go
func (ic *InvocationContext) Get(key any) (any, bool)
```

Retrieves a value by key. Returns `(value, true)` if found, or `(nil, false)` if the key doesn't exist. Safe for concurrent use — guarded by a read lock.

### Context Helpers

#### WithInvocationContext

```go
func WithInvocationContext(ctx context.Context, ic *InvocationContext) context.Context
```

Attaches an `InvocationContext` to a Go `context.Context`. The agent calls this internally at the start of each `InvokeStream`.

#### GetInvocationContext

```go
func GetInvocationContext(ctx context.Context) *InvocationContext
```

Retrieves the `InvocationContext` from a Go `context.Context`. Returns `nil` if none is attached.

## Per-Invocation Scoping

Inside `InvokeStream`, the agent does this before anything else:

```go
ic := NewInvocationContext()
ctx = WithInvocationContext(ctx, ic)
```

This `ctx` is then passed to input guardrails, the provider, tool handlers, middleware, and output guardrails. Every component in the invocation pipeline shares the same `InvocationContext` instance, but separate invocations each get their own.

## Concurrency Safety

`InvocationContext` uses a `sync.RWMutex` internally:

- `Set` acquires a write lock
- `Get` acquires a read lock

This makes it safe to use with `WithParallelToolExecution`, where multiple tool handlers run concurrently in separate goroutines. Multiple readers can access the store simultaneously, and writers are serialized.

## Code Example

This example shows a timing middleware that records when each tool call starts, and a tool handler that reads that timestamp to compute elapsed time:

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// timingMiddleware stores the call start time in InvocationContext
// so tool handlers can access it.
func timingMiddleware(next agent.ToolHandlerFunc) agent.ToolHandlerFunc {
	return func(ctx context.Context, toolName string, input json.RawMessage) (string, error) {
		ic := agent.GetInvocationContext(ctx)
		if ic != nil {
			ic.Set("call_start", time.Now())
			ic.Set("tool_name", toolName)
		}
		return next(ctx, toolName, input)
	}
}

type LookupInput struct {
	UserID string `json:"user_id" description:"The user ID to look up" required:"true"`
}

func main() {
	provider, err := bedrock.ClaudeSonnet4_6()
	if err != nil {
		log.Fatal(err)
	}

	// Tool handler reads state set by middleware.
	lookup := tool.New("lookup_user", "Looks up a user by ID", func(ctx context.Context, input LookupInput) (string, error) {
		ic := agent.GetInvocationContext(ctx)
		if ic != nil {
			if start, ok := ic.Get("call_start"); ok {
				elapsed := time.Since(start.(time.Time))
				log.Printf("tool handler reached %s after %s", input.UserID, elapsed)
			}

			// Store a result for other middleware or tools to read later.
			ic.Set("last_lookup", input.UserID)
		}

		return fmt.Sprintf("User %s: Alice (active)", input.UserID), nil
	})

	a, err := agent.Default(
		provider,
		prompt.Text("You are a helpful assistant. Use the lookup_user tool when asked about users."),
		[]tool.Tool{lookup},
		agent.WithMiddleware(timingMiddleware),
	)
	if err != nil {
		log.Fatal(err)
	}

	result, _, err := a.Invoke(context.Background(), "Look up user u-123")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result)
}
```

The flow:

1. `Invoke` creates a new `InvocationContext` and attaches it to `ctx`
2. The LLM decides to call `lookup_user`
3. `timingMiddleware` runs first — stores `call_start` and `tool_name` in the `InvocationContext`
4. The `lookup_user` handler reads `call_start` to log elapsed time, and writes `last_lookup` for downstream use
5. If there were additional middleware or tools, they could read `last_lookup` from the same `InvocationContext`

## See Also

- [Middleware](middleware.md) — defining middleware that wraps tool execution
- [Agent API Reference](agent-api.md) — `WithMiddleware` and `WithParallelToolExecution` options
- [Tools](tools.md) — defining tool handlers that receive the context
