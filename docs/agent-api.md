# Agent API

The `agent` package orchestrates LLM calls, tool execution, memory, guardrails, RAG, and middleware into a single loop driven by the `Agent` type.

## Creating an Agent

All constructors take a provider, system prompt, tools, and optional configuration:

```go
a, err := agent.New(provider, prompt.Text("You are a helpful assistant."), tools,
    agent.WithMaxIterations(15),
)
```

| Constructor | Description |
|---|---|
| `agent.New` | Base constructor — full control over all options |
| `agent.Default` | Convenience wrapper with sensible defaults for a single-turn agent |
| `agent.Worker` | Creates a worker agent for use inside an orchestrator |
| `agent.Orchestrator` | Creates an orchestrator that delegates to worker agents |
| `agent.RAGAgent` | Convenience wrapper that pre-configures a retriever |

Use `agent.Default` for most cases. Use `agent.Orchestrator` + `agent.Worker` when you need multi-agent delegation. Use `agent.RAGAgent` when every invocation needs document context.

## Configuration Options

### Core Options

| Option | Default | Description |
|---|---|---|
| `WithName(name)` | — | Agent name for metrics/tracing dimensions (`agent_name` label) |
| `WithMaxIterations(n)` | 10 | Max LLM call → tool execution iterations per invocation |
| `WithParallelToolExecution()` | off | Run multiple tool calls concurrently within a single iteration |
| `WithTimeout(d)` | no timeout | Per-call timeout for provider calls. Prevents hung connections in HTTP servers |
| `WithRetry(maxRetries, baseDelay)` | no retry | Exponential backoff for transient provider errors |
| `WithTokenBudget(maxTokens)` | no budget | Max cumulative tokens (input + output) per invocation |

`WithTimeout` and `WithRetry` compose naturally — each retry attempt gets its own fresh timeout:

```go
a, err := agent.Default(provider, instructions, tools,
    agent.WithTimeout(30 * time.Second),
    agent.WithRetry(2, 1 * time.Second),
)
```

### Conversation

| Option | Description |
|---|---|
| `WithConversation(c, conversationID)` | Attach a conversation store with a default conversation ID for multi-turn support |
| `WithSharedConversation(c)` | Attach a conversation store without a default ID — each invocation must provide one via `WithConversationID` on the context |

`WithSharedConversation` is the recommended pattern for HTTP servers where a single Agent instance serves multiple concurrent conversations:

```go
a, _ := agent.New(provider, instructions, tools, agent.WithSharedConversation(store))

ctx := agent.WithConversationID(r.Context(), req.ConversationID)
result, _, err := a.Invoke(ctx, req.Message)
```

### Inference Parameters

| Option | Description |
|---|---|
| `WithTemperature(v)` | Randomness of LLM output. Range: [0.0, 1.0] |
| `WithTopP(v)` | Nucleus sampling cutoff. Range: [0.0, 1.0] |
| `WithTopK(v)` | Max highest-probability tokens considered. Must be >= 1. Note: ignored by OpenAI |
| `WithMaxTokens(n)` | Max tokens the LLM can generate per response. Overrides provider-level default |
| `WithStopSequences(s)` | Strings that cause the LLM to stop generating |

```go
a, err := agent.Default(provider, instructions, tools,
    agent.WithTemperature(0.7),
    agent.WithTopP(0.9),
    agent.WithMaxTokens(2048),
)
```

These populate an `InferenceConfig` on the agent. When none are set, the provider uses its own defaults. Per-invocation overrides are supported via `WithInferenceConfig` on the context:

```go
ctx := agent.WithInferenceConfig(ctx, &agent.InferenceConfig{
    Temperature: ptrFloat(0.9),
})
result, _, err := a.Invoke(ctx, "Be creative!")
```

### Retrieval

`WithRetriever(r)` attaches a `Retriever` for automatic RAG. The retriever fetches documents once per invocation (before the first provider call) and injects them as context. See [RAG Pipeline](rag.md) for details.

`WithContextFormatter(f)` customizes how retrieved documents are rendered. Defaults to numbered items in `<retrieved_context>` XML tags.

### Guardrails & Middleware

| Option | Description |
|---|---|
| `WithInputGuardrail(g...)` | Functions that validate/transform the user message before it reaches the provider. Any error aborts the invocation |
| `WithOutputGuardrail(g...)` | Functions that validate/transform the final response. When configured, streamed chunks are buffered until guardrails pass |
| `WithMiddleware(mws...)` | Functions that wrap tool execution. Applied in order (first = outermost). Accumulates across multiple calls |

### Thinking

`WithThinkingCallback(cb)` receives the model's internal reasoning chunks in real-time. Only fires when the provider has thinking enabled:

```go
provider, _ := anthropic.New("claude-sonnet-4-6",
    anthropic.WithThinking(pvdr.ThinkingHigh),
    anthropic.WithMaxTokens(16000),
)

a, _ := agent.Default(provider, instructions, nil,
    agent.WithThinkingCallback(func(chunk string) {
        fmt.Print(chunk)
    }),
)
```

## Invocation

### Invoke

```go
func (a *Agent) Invoke(ctx context.Context, userMessage string) (string, TokenUsage, error)
```

Runs the agent loop and returns the complete text response. Convenience wrapper over `InvokeStream` that collects all chunks.

### InvokeStream

```go
func (a *Agent) InvokeStream(ctx context.Context, userMessage string, cb StreamCallback) (TokenUsage, error)
```

Runs the agent loop, streaming the final text response via the callback. The callback receives chunks in real-time unless output guardrails are configured (in which case chunks are buffered).

### InvokeStructured

For structured output, see [Structured Output](structured-output.md).

### Per-Invocation Context

These functions return a new context with per-invocation overrides:

| Function | Description |
|---|---|
| `WithConversationID(ctx, id)` | Override the default conversation ID for this call |
| `WithIdentifier(ctx, id)` | Set the identifier for memory scoping (user, tenant, etc.) |
| `WithImages(ctx, images)` | Attach images for vision-capable models |
| `WithDocuments(ctx, docs)` | Attach PDFs, Word docs, spreadsheets for document reasoning |
| `WithInferenceConfig(ctx, cfg)` | Override inference parameters for this call |

```go
// Attach an image for vision.
img := agent.ImageBlock{
    Source: agent.ImageSource{Data: imageBytes, MIMEType: "image/jpeg"},
}
ctx := agent.WithImages(ctx, []agent.ImageBlock{img})
result, _, err := a.Invoke(ctx, "What is in this image?")
```

Images and documents can also be passed by URL:

```go
img := agent.ImageBlock{
    Source: agent.ImageSource{URL: "https://example.com/photo.jpg"},
}
```

### Resume / ResumeInvoke

Continue an agent invocation after a human handoff:

```go
func (a *Agent) Resume(ctx context.Context, hr *HandoffRequest, humanResponse string, cb StreamCallback) (TokenUsage, error)
func (a *Agent) ResumeInvoke(ctx context.Context, hr *HandoffRequest, humanResponse string) (string, TokenUsage, error)
```

See [Handoffs](handoff.md) for the full workflow.

### Close

```go
func (a *Agent) Close()
```

Performs graceful cleanup. Call before process exit to ensure pending background work (e.g. conversation summarization) is flushed:

```go
a, _ := agent.Default(provider, instructions, tools,
    agent.WithConversation(summaryConversation, "conv-1"),
)
defer a.Close()
```

## Agent Loop Behavior

Each call to `Invoke` or `InvokeStream` runs the following steps:

1. **Input guardrails** — the user message passes through all configured `InputGuardrail` functions in order. Any error aborts the invocation.
2. **Conversation load** — if `WithConversation` is configured, conversation history is loaded.
3. **RAG retrieval** — if `WithRetriever` is configured, relevant documents are retrieved and injected as context before the user message.
4. **Image injection** — if `WithImages` was called on the context, images are prepended to the first user message.
5. **Document injection** — if `WithDocuments` was called on the context, documents are prepended before images in the first user message.
6. **Agent loop** (up to `maxIterations`):
   - The provider is called with the current messages, system prompt, and tool specs.
   - If the provider returns **tool calls**: the agent executes them and loops back.
   - If the provider returns a **text response**: the loop exits.
   - If a token budget is set and exceeded, the loop aborts with `ErrTokenBudgetExceeded`.
7. **Output guardrails** — the final text passes through all configured `OutputGuardrail` functions.
8. **Conversation save** — if `WithConversation` is configured, the full conversation is saved.

If the loop reaches `maxIterations` without a text response, an error is returned.

## See Also

- [Getting Started](getting-started.md) — installation and first agent
- [HTTP & Multi-Tenant Environments](http.md) — `WithSharedConversation`, `WithConversationID`, and HTTP server patterns
- [Handoffs](handoff.md) — human handoff workflow
- [Prompt System](prompts.md) — `Text`, `RISEN`, `COSTAR` prompt types
- [Tool System](tools.md) — defining tools for the agent
- [Conversation System](conversation.md) — conversation persistence and strategies
- [Long-Term Memory](memory.md) — long-term user-scoped knowledge storage and retrieval
- [Guardrails](guardrails.md) — input and output validation
- [Middleware](middleware.md) — wrapping tool execution
- [RAG Pipeline](rag.md) — retrieval-augmented generation
- [Message Types](message-types.md) — `Message`, `ContentBlock`, `ConverseParams`
