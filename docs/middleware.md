# Middleware

Middleware wraps tool execution to add cross-cutting behavior like logging, metrics, retries, or authorization. Each middleware intercepts every tool call the agent makes, running logic before and/or after the underlying handler.

## ToolHandlerFunc

```go
type ToolHandlerFunc func(ctx context.Context, toolName string, input json.RawMessage) (string, error)
```

`ToolHandlerFunc` is the signature of a tool handler after middleware wrapping. It receives the Go context, the name of the tool being called, and the raw JSON input from the LLM. It returns the tool's string result or an error.

## Middleware

```go
type Middleware func(next ToolHandlerFunc) ToolHandlerFunc
```

A `Middleware` takes the next handler in the chain and returns a new handler that wraps it. Inside the wrapper you can:

- Run logic before calling `next` (pre-processing)
- Call `next(ctx, toolName, input)` to invoke the next middleware or the actual tool handler
- Run logic after `next` returns (post-processing)
- Short-circuit by returning a result without calling `next` at all

Register middleware with the `WithMiddleware` option:

```go
agent.WithMiddleware(loggingMiddleware, metricsMiddleware)
```

You can pass multiple middleware in a single call or add them with separate `WithMiddleware` calls — they accumulate.

## Execution Order

The first middleware in the slice is the outermost wrapper. Given middleware A and B registered as `WithMiddleware(A, B)`, the execution order is:

```
before-A → before-B → tool handler → after-B → after-A
```

This is the standard "onion" model — A wraps B, which wraps the handler. A sees the request first on the way in and the response last on the way out.

## Code Example

This example shows a logging middleware that records the tool name, input, duration, and any errors for every tool call:

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

// loggingMiddleware logs tool name, input, duration, and errors.
func loggingMiddleware(next agent.ToolHandlerFunc) agent.ToolHandlerFunc {
	return func(ctx context.Context, toolName string, input json.RawMessage) (string, error) {
		log.Printf("tool call: %s input=%s", toolName, string(input))
		start := time.Now()

		result, err := next(ctx, toolName, input)

		elapsed := time.Since(start)
		if err != nil {
			log.Printf("tool error: %s err=%v elapsed=%s", toolName, err, elapsed)
		} else {
			log.Printf("tool done: %s elapsed=%s", toolName, elapsed)
		}
		return result, err
	}
}

func main() {
	provider, err := bedrock.Standard()
	if err != nil {
		log.Fatal(err)
	}

	greet := tool.NewRaw("greet", "greets a user by name", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string", "description": "Name to greet"},
		},
		"required": []string{"name"},
	}, func(_ context.Context, raw json.RawMessage) (string, error) {
		var input struct{ Name string }
		json.Unmarshal(raw, &input)
		return fmt.Sprintf("Hello, %s!", input.Name), nil
	})

	a, err := agent.Default(
		provider,
		prompt.Text("You are a friendly assistant. Use the greet tool when asked to say hello."),
		[]tool.Tool{greet},
		agent.WithMiddleware(loggingMiddleware),
	)
	if err != nil {
		log.Fatal(err)
	}

	result, _, err := a.Invoke(context.Background(), "Say hello to Alice")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result)
}
```

When the agent calls the `greet` tool, the logging middleware prints:

```
tool call: greet input={"name":"Alice"}
tool done: greet elapsed=42µs
```

## See Also

- [Agent API Reference](agent-api.md) — `WithMiddleware` option function
- [Guardrails](guardrails.md) — guardrails wrap the full invocation; middleware wraps individual tool calls
- [InvocationContext](invocation-context.md) — sharing state between middleware and tool handlers
- [Tools](tools.md) — defining the tools that middleware wraps
