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
| `WithRateLimiter(rl)` | no limiter | Rate limiter enforcing RPM/TPM limits. Per-conversation when conversation IDs are used, shared otherwise. See [Rate Limiting](#rate-limiting) |

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

c := agent.NewContext(r.Context()).WithConversationID(req.ConversationID)
result, err := a.Invoke(c, req.Message)
```

### Inference Parameters

| Option | Description |
|---|---|
| `WithTemperature(v)` | Randomness of LLM output. Range: [0.0, 1.0] |
| `WithTopP(v)` | Nucleus sampling cutoff. Range: [0.0, 1.0] |
| `WithTopK(v)` | Max highest-probability tokens considered. Must be >= 1. Note: ignored by OpenAI |
| `WithMaxTokens(n)` | Max tokens the LLM can generate per response. Overrides provider-level default |
| `WithStopSequences(s)` | Strings that cause the LLM to stop generating |

When none are set, the provider uses its own defaults. Per-invocation overrides via `WithInferenceConfig` on the `*Context`. See [Agent Context](invocation-context.md).

### Retrieval

| Option | Description |
|---|---|
| `WithRetriever(r)` | Automatic RAG — retrieves documents once per invocation before the first provider call |
| `WithContextFormatter(f)` | Customizes how retrieved documents are rendered (default: numbered items in XML tags) |

See [RAG Pipeline](rag.md) for details.

### Rate Limiting

`WithRateLimiter(rl)` attaches a `*RateLimiter` that enforces RPM and TPM limits on provider calls. Each conversation ID gets its own independent budget; calls without a conversation ID share a single default bucket. A single `*RateLimiter` instance can be shared across multiple agents.

```go
rl, _ := agent.NewRateLimiter(60, 100000)

a, _ := agent.New(provider, instructions, tools, agent.WithRateLimiter(rl))
// Shared budget when no conversation ID is set.
// Per-conversation budget when WithConversationID is used.
// Call rl.Purge(convID) when a conversation ends to free resources.
```

| RateLimiterOption | Default | Description |
|---|---|---|
| `WithSlidingWindow()` | ✓ | Tracks consumption over a continuously advancing 60-second window |
| `WithFixedWindow()` | — | Resets counters at fixed 60-second intervals |
| `WithFailFast()` | ✓ | Returns `ErrRateLimitExceeded` immediately when a limit is exceeded |
| `WithBlock()` | — | Waits until capacity is available (respects context cancellation) |

`ErrRateLimitExceeded` short-circuits retries — if the limiter rejects a call during a retry attempt, the error propagates immediately.

### Guardrails & Middleware

| Option | Description |
|---|---|
| `WithInputGuardrail(g...)` | Functions that validate/transform the user message before it reaches the provider. Any error aborts the invocation |
| `WithOutputGuardrail(g...)` | Functions that validate/transform the final response. With `InvokeStream`, chunks stream in real-time and guardrails run after completion — a `GuardrailError` is returned if rejected. With `Invoke`, the returned text is always guardrail-processed |
| `WithMiddleware(mws...)` | Functions that wrap tool execution. Applied in order (first = outermost). Accumulates across multiple calls |

### Tool Filtering

| Option | Description |
|---|---|
| `WithToolFilter(filters...)` | Controls which tools are visible to the LLM per provider call. Multiple filters use AND semantics — a tool must pass all filters. Evaluated before each call in the loop. Accumulates across multiple calls. |

Each filter receives the `*Context` and a tool; return `true` to include it.

```go
a, err := agent.New(provider, instructions, tools,
    agent.WithToolFilter(
        func(c *agent.Context, t tool.Tool) bool { return isAdmin(c) || t.Spec.Name != "delete_account" },
        func(c *agent.Context, t tool.Tool) bool {
            if t.Spec.Name == "submit_order" {
                v, ok := c.Get("cart_validated")
                return ok && v.(bool)
            }
            return true
        },
    ),
)
```

### Thinking

Thinking chunks are delivered through `EventHook.OnThinking`. Only fires when the provider has thinking enabled:

```go
c := agent.Background().WithEventHook(myEventHook)
a.InvokeStream(c, message, streamCB) // OnThinking fires during streaming
```

### Event Hook

`WithEventHook` on the `*Context` attaches an `EventHook` to a specific invocation. Designed for real-time UI event delivery — tool call start/end, model start/end, and thinking chunks. Invocation-scoped, so it's naturally concurrent-safe across HTTP handlers.

```go
c := agent.Background().WithEventHook(myHook)
result, err := a.Invoke(c, message)
```

| Method | Description |
|---|---|
| `OnToolCallStart(c, toolName, input)` | Called before a tool handler is invoked |
| `OnToolCallEnd(c, toolName, output, err, duration)` | Called after a tool handler completes |
| `OnThinking(c, chunk)` | Called for each thinking/reasoning chunk |
| `OnModelStart(c)` | Called before the provider call |
| `OnModelEnd(c, stopReason)` | Called after the provider call. `stopReason`: `"end_turn"`, `"tool_use"`, or `"error"` |

Embed `agent.BaseEventHook` to only override the methods you need:

```go
type myHook struct {
    agent.BaseEventHook
}

func (h *myHook) OnThinking(_ *agent.Context, chunk string) {
    fmt.Print(chunk)
}
```

## Invocation

### Invoke

```go
func (a *Agent) Invoke(c *Context, userMessage string) (string, error)
```

Runs the agent loop and returns the complete text response. Convenience wrapper over `InvokeStream` that collects all chunks.

### InvokeStream

```go
func (a *Agent) InvokeStream(c *Context, userMessage string, cb StreamCallback) error
```

Runs the agent loop, streaming the final text response via the callback. The callback receives chunks in real-time unless output guardrails are configured (in which case chunks are buffered).

### InvokeStructured

For structured output, see [Structured Output](structured-output.md).

### Token Usage

Cumulative token usage is stored on the `*Context` after each invocation. Read it via `c.Usage()`:

```go
c := agent.Background()
result, err := a.Invoke(c, "Hello")
usage := c.Usage()
fmt.Printf("Tokens: %d in, %d out\n", usage.InputTokens, usage.OutputTokens)
```

### Per-Invocation Context

Use chainable `With*` methods on the `*Context` to set per-invocation overrides. See [Agent Context](invocation-context.md) for the full list.

```go
c := agent.Background().
    WithConversationID("conv-123").
    WithImages([]agent.ImageBlock{{Source: agent.ImageSource{Data: imageBytes, MIMEType: "image/jpeg"}}})
result, err := a.Invoke(c, "What is in this image?")
```

### RunLoop

Low-level entry point that runs the agent's iteration loop with caller-supplied messages. Unlike `Invoke`/`InvokeStream`, it skips input guardrails, conversation loading, RAG retrieval, and image/document attachment — the caller owns all of that.

```go
func (a *Agent) RunLoop(c *Context, params LoopParams) (TokenUsage, string, error)
```

`LoopParams` fields:

| Field | Description |
|---|---|
| `Messages` | Conversation history to send to the provider |
| `SystemPrompt` | Overrides the agent's instructions if non-empty |
| `InferenceConfig` | Overrides the agent's inference config if non-nil |
| `StreamCallback` | Receives streamed text chunks |
| `Config` | Optional `LoopConfig` for behavior overrides |

`LoopConfig` fields:

| Field | Description |
|---|---|
| `ExtraMiddleware` | Prepended (outermost) to the agent's middleware chain without mutating it |
| `ToolResultInterceptor` | Called after each tool batch; return `true` to stop the loop (returns `ErrLoopStopped`) |
| `SkipConversationSave` | Prevents the loop from persisting conversation history |

```go
usage, text, err := a.RunLoop(c, agent.LoopParams{
    Messages:       messages,
    StreamCallback: cb,
    Config: agent.LoopConfig{
        ExtraMiddleware:      extraMiddleware,
        SkipConversationSave: true,
        ToolResultInterceptor: func(results []agent.ToolResultBlock) bool {
            return containsHandoff(results)
        },
    },
})
if errors.Is(err, agent.ErrLoopStopped) {
    // interceptor signaled stop
}
```

### Resume / ResumeInvoke

Continue an agent invocation after a human handoff:

```go
func (a *Agent) Resume(c *Context, hr *HandoffRequest, humanResponse string, cb StreamCallback) error
func (a *Agent) ResumeInvoke(c *Context, hr *HandoffRequest, humanResponse string) (string, error)
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
4. **Image injection** — if `WithImages` was called on the `*Context`, images are prepended to the first user message.
5. **Document injection** — if `WithDocuments` was called on the `*Context`, documents are prepended before images in the first user message.
6. **Agent loop** (up to `maxIterations`):
   - If a `ToolFilter` is set, it is evaluated to determine which tools are available for this call.
   - If a `RateLimiter` is configured, `Acquire` is called before each provider call (including retries). In block mode it waits for capacity; in fail-fast mode it returns `ErrRateLimitExceeded`.
   - The provider is called with the current messages, system prompt, and tool specs.
   - If the provider returns **tool calls**: the agent executes them and loops back.
   - If the provider returns a **text response**: the loop exits. Token usage is recorded against the rate limiter.
   - If a token budget is set and exceeded, the loop aborts with `ErrTokenBudgetExceeded`.
7. **Output guardrails** — the final text passes through all configured `OutputGuardrail` functions. With `InvokeStream`, chunks have already been delivered; a `GuardrailError` is returned if rejected. With `Invoke`, the returned text is always guardrail-processed.
8. **Conversation save** — if `WithConversation` is configured, the full conversation is saved.

If the loop reaches `maxIterations` without a text response, it returns `ErrMaxIterationsExceeded` (detectable via `errors.Is`).

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
