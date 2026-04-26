# Guardrails

Guardrails let you validate and transform messages at the boundaries of an agent invocation. Input guardrails run before the user message reaches the LLM provider. Output guardrails run after the LLM produces its final text response. Both can modify the content passing through them or abort the invocation by returning an error.

## InputGuardrail

```go
type InputGuardrail func(ctx context.Context, message string) (string, error)
```

An `InputGuardrail` receives the user's message before it is sent to the provider. It can:

- Return the message unchanged (pass-through validation)
- Return a modified message (content transformation, sanitization)
- Return an error to abort the invocation immediately

Input guardrails run at the very start of `InvokeStream`, before conversation history is loaded and before any provider call is made.

Register input guardrails with the `WithInputGuardrail` option:

```go
agent.WithInputGuardrail(myGuardrail)
```

You can pass multiple guardrails in a single call or add them with separate `WithInputGuardrail` calls — they accumulate.

## OutputGuardrail

```go
type OutputGuardrail func(ctx context.Context, response string) (string, error)
```

An `OutputGuardrail` receives the LLM's final text response before it is returned to the caller. It can:

- Return the response unchanged (pass-through validation)
- Return a modified response (redaction, formatting)
- Return an error to abort the invocation

Output guardrails run only on the final text response — they are not invoked on intermediate tool-call iterations.

Register output guardrails with the `WithOutputGuardrail` option:

```go
agent.WithOutputGuardrail(myGuardrail)
```

## Execution Order

Multiple guardrails of the same type execute in the order they were registered. The output of one guardrail becomes the input to the next:

```go
a, err := agent.New(provider, instructions, tools,
    agent.WithInputGuardrail(first),   // runs first
    agent.WithInputGuardrail(second),  // runs second, receives output of first
)
```

This applies to both input and output guardrails independently.

## Error Aborts Invocation

If any guardrail returns a non-nil error, the invocation stops immediately. No further guardrails run, and the error propagates back to the caller.

For input guardrails, the error is wrapped as `"input guardrail: <err>"`. For output guardrails, it is wrapped as `"output guardrail: <err>"`.

```go
// This guardrail blocks messages containing "forbidden"
func blockForbidden(_ context.Context, msg string) (string, error) {
    if strings.Contains(msg, "forbidden") {
        return "", fmt.Errorf("message contains blocked content")
    }
    return msg, nil
}
```

When this guardrail returns an error, `Invoke` returns an error like:

```
input guardrail: message contains blocked content
```

## Output Guardrail Buffering

When output guardrails are configured, the agent buffers streamed response chunks instead of forwarding them to the `StreamCallback` in real time. This is necessary because output guardrails need the complete response text to validate or transform it.

Once all output guardrails pass:

- If the guardrails did not modify the response, the original buffered chunks are flushed to the callback in order — preserving the original chunking
- If any guardrail modified the response, the final transformed text is sent as a single chunk

This means adding output guardrails trades real-time streaming for the ability to validate the full response before the caller sees it. If you only need input validation, use `InputGuardrail` alone to keep streaming behavior intact.

## Code Example

This example shows an input guardrail that sanitizes user messages and an output guardrail that enforces a response length policy:

```go
package main

import (
    "context"
    "fmt"
    "log"
    "strings"

    "github.com/camilbinas/gude-agents/agent"
    "github.com/camilbinas/gude-agents/agent/prompt"
    "github.com/camilbinas/gude-agents/agent/provider/bedrock"
)

// inputSanitizer strips leading/trailing whitespace and lowercases the message.
func inputSanitizer(_ context.Context, msg string) (string, error) {
    return strings.ToLower(strings.TrimSpace(msg)), nil
}

// maxLengthGuardrail rejects responses longer than 500 characters.
func maxLengthGuardrail(_ context.Context, resp string) (string, error) {
    if len(resp) > 500 {
        return "", fmt.Errorf("response too long: %d characters", len(resp))
    }
    return resp, nil
}

func main() {
    provider, err := bedrock.Standard()
    if err != nil {
        log.Fatal(err)
    }

    a, err := agent.Default(
        provider,
        prompt.Text("You are a concise assistant. Keep answers brief."),
        nil, // no tools
        agent.WithInputGuardrail(inputSanitizer),
        agent.WithOutputGuardrail(maxLengthGuardrail),
    )
    if err != nil {
        log.Fatal(err)
    }

    result, _, err := a.Invoke(context.Background(), "  What is Go?  ")
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println(result)
}
```

In this example:

1. The input guardrail trims and lowercases `"  What is Go?  "` to `"what is go?"` before the provider sees it
2. The LLM generates a response
3. The output guardrail checks the response length — if it exceeds 500 characters, the invocation returns an error instead of the response

## See Also

- [Agent API Reference](agent-api.md) — `WithInputGuardrail` and `WithOutputGuardrail` option functions
- [Middleware](middleware.md) — wrapping tool execution (guardrails wrap the full invocation instead)
- [Getting Started](getting-started.md) — basic agent setup
