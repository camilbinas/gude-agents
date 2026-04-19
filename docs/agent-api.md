# Agent API Reference

The `agent` package is the core of gude-agents. It orchestrates LLM calls, tool execution, memory, guardrails, RAG, and middleware into a single loop driven by the `Agent` type.

## Constructor

### `agent.New`

```go
func New(provider Provider, instructions prompt.Instructions, tools []tool.Tool, opts ...Option) (*Agent, error)
```

Creates a new `Agent`. Returns an error if any tool is missing a name, description, or handler, if duplicate tool names are found, or if any `Option` returns an error.

| Parameter | Type | Description |
|---|---|---|
| `provider` | `Provider` | LLM backend (Bedrock, Anthropic, OpenAI, or custom) |
| `instructions` | `prompt.Instructions` | System prompt — use `prompt.Text`, `prompt.RISEN`, or `prompt.COSTAR` |
| `tools` | `[]tool.Tool` | Tools the LLM can invoke during the agent loop |
| `opts` | `...Option` | Functional options to configure agent behavior |

Defaults:
- `maxIterations`: 10
- `parallelTools`: false
- No memory, middleware, guardrails, logger, token budget, retriever, or context formatter

If the provider implements `CapabilityReporter`, the constructor logs warnings when tools are registered but the model doesn't support tool use, or when a token budget is set but the model doesn't report token usage.

## Option Functions

Each `Option` is a `func(*Agent) error` that configures the agent at construction time.

### `WithMaxIterations`

```go
func WithMaxIterations(n int) Option
```

Sets the maximum number of LLM call → tool execution iterations per invocation. Default is 10. If the agent reaches this limit without producing a text response, `Invoke`/`InvokeStream` returns an error.

### `WithParallelToolExecution`

```go
func WithParallelToolExecution() Option
```

Enables concurrent execution of tool calls within a single iteration. When the LLM returns multiple tool calls, they run in parallel goroutines instead of sequentially. Results are returned in the same order as the input calls.

### `WithMemory`

```go
func WithMemory(m Memory, conversationID string) Option
```

Attaches a `Memory` implementation and a default conversation ID for multi-turn support. On each invocation the agent loads history before the provider call and saves the full conversation (including the new exchange) after a successful response.

The conversation ID can be overridden per-invocation using `WithConversationID` on the context — see [HTTP & Multi-Tenant Environments](http.md).

### `WithSharedMemory`

```go
func WithSharedMemory(m Memory) Option
```

Attaches a `Memory` implementation without a default conversation ID. Each invocation must provide a conversation ID via `WithConversationID` on the context. This is the recommended pattern for HTTP servers where a single Agent instance serves multiple concurrent conversations.

```go
a, _ := agent.New(provider, instructions, tools, agent.WithSharedMemory(store))

ctx := agent.WithConversationID(r.Context(), req.ConversationID)
result, _, err := a.Invoke(ctx, req.Message)
```

### `WithMiddleware`

```go
func WithMiddleware(mws ...Middleware) Option
```

Adds one or more middleware functions that wrap tool execution. Middleware is applied in order — the first middleware in the slice is the outermost wrapper. Can be called multiple times; middlewares accumulate.

### `WithInputGuardrail`

```go
func WithInputGuardrail(g ...InputGuardrail) Option
```

Adds input guardrails that run before the user message reaches the provider. Guardrails execute in order. If any guardrail returns an error, the invocation is aborted immediately.

```go
type InputGuardrail func(ctx context.Context, message string) (string, error)
```

### `WithOutputGuardrail`

```go
func WithOutputGuardrail(g ...OutputGuardrail) Option
```

Adds output guardrails that run on the final text response before it is returned to the caller. When output guardrails are configured, streamed chunks are buffered until all guardrails pass. If a guardrail modifies the text, the modified version is streamed as a single chunk.

```go
type OutputGuardrail func(ctx context.Context, response string) (string, error)
```

### `WithLogger`

```go
func WithLogger(l Logger) Option
```

Sets a logger for the agent. The agent logs iteration counts, tool names, tool errors, and tool completions.

```go
type Logger interface {
    Printf(format string, v ...any)
}
```

### `WithTokenBudget`

```go
func WithTokenBudget(maxTokens int) Option
```

Sets a maximum cumulative token budget per invocation. After each provider call, the agent checks whether total tokens (input + output) exceed the budget. If so, the invocation is aborted with `ErrTokenBudgetExceeded`. A value of 0 means no budget (default).

### `WithTimeout`

```go
func WithTimeout(d time.Duration) Option
```

Sets a per-call timeout for provider calls. Each call to `ConverseStream` gets a context with this deadline. If the provider doesn't respond in time, the call is cancelled and returns a `context.DeadlineExceeded` error wrapped in a `ProviderError`. A value of 0 means no timeout (default).

Use this to prevent hung provider connections from blocking goroutines indefinitely in HTTP servers:

```go
a, err := agent.Default(provider, instructions, tools,
    agent.WithTimeout(120 * time.Second),
)
```

### `WithRetry`

```go
func WithRetry(maxRetries int, baseDelay time.Duration) Option
```

Enables automatic retry with exponential backoff for transient provider errors. When a provider call fails, the agent retries up to `maxRetries` times with delays of `baseDelay`, `2*baseDelay`, `4*baseDelay`, etc. Context cancellation from the caller stops retries immediately. A `maxRetries` of 0 means no retry (default).

```go
a, err := agent.Default(provider, instructions, tools,
    agent.WithRetry(3, 500 * time.Millisecond),
)
```

`WithTimeout` and `WithRetry` compose naturally — each retry attempt gets its own fresh timeout:

```go
a, err := agent.Default(provider, instructions, tools,
    agent.WithTimeout(30 * time.Second),   // each attempt times out after 30s
    agent.WithRetry(2, 1 * time.Second),   // up to 2 retries with 1s/2s backoff
)
```

### `WithRetriever`

```go
func WithRetriever(r Retriever) Option
```

Attaches a `Retriever` for automatic RAG. Before the agent loop starts, the retriever fetches documents relevant to the user message. Retrieved content is injected as a user/assistant message turn in the conversation using the configured `ContextFormatter` (or `DefaultContextFormatter`). The context is not persisted to memory.

```go
type Retriever interface {
    Retrieve(ctx context.Context, query string) ([]Document, error)
}
```

### `WithThinkingCallback`

```go
func WithThinkingCallback(cb ThinkingCallback) Option
```

Sets a callback that receives the model's internal reasoning chunks in real-time, streamed before the final answer. Only fires when the provider has thinking enabled via `WithThinking`.

```go
type ThinkingCallback func(chunk string)
```

```go
provider, _ := anthropic.New("claude-sonnet-4-6",
    anthropic.WithThinking(pvdr.ThinkingHigh),
    anthropic.WithMaxTokens(16000),
)

a, _ := agent.Default(provider, instructions, nil,
    agent.WithThinkingCallback(func(chunk string) {
        fmt.Print(chunk) // stream reasoning to stdout
    }),
)
```

See [Providers](providers.md#extended-thinking) for which models support thinking.

### `WithContextFormatter`

```go
func WithContextFormatter(f ContextFormatter) Option
```

Sets a custom formatter for RAG-retrieved documents. When not set, `DefaultContextFormatter` is used, which renders documents as numbered items.

```go
type ContextFormatter func(docs []Document) string
```

## Methods

### `WithConversationID`

```go
func WithConversationID(ctx context.Context, id string) context.Context
```

Returns a context that overrides the agent's default conversation ID for this invocation. This allows a single Agent instance to serve multiple concurrent conversations. The agent checks for this context value first, then falls back to the construction-time default.

### `Invoke`

```go
func (a *Agent) Invoke(ctx context.Context, userMessage string) (string, TokenUsage, error)
```

Runs the agent loop and returns the complete text response as a single string. This is a convenience wrapper over `InvokeStream` that collects all streamed chunks.

| Return | Type | Description |
|---|---|---|
| response | `string` | The agent's final text response |
| usage | `TokenUsage` | Cumulative token usage across all provider calls in this invocation |
| err | `error` | Non-nil on guardrail failure, provider error, memory error, token budget exceeded, or max iterations reached |

### `InvokeStream`

```go
func (a *Agent) InvokeStream(ctx context.Context, userMessage string, cb StreamCallback) (TokenUsage, error)
```

Runs the agent loop, streaming the final text response via the callback. Returns cumulative token usage on success.

```go
type StreamCallback func(chunk string)
```

The callback receives text chunks in real-time as the provider streams them — unless output guardrails are configured, in which case chunks are buffered and delivered after guardrails pass.

| Return | Type | Description |
|---|---|---|
| usage | `TokenUsage` | Cumulative token usage across all provider calls |
| err | `error` | Non-nil on failure (see error conditions below) |

### `Resume`

```go
func (a *Agent) Resume(ctx context.Context, hr *HandoffRequest, humanResponse string, cb StreamCallback) (TokenUsage, error)
```

Continues an agent invocation after a human handoff. Restores the conversation from the `HandoffRequest`, appends the human's response as a new user message, and re-enters the agent loop. The `HandoffRequest.ConversationID` is used for memory operations.

### `ResumeInvoke`

```go
func (a *Agent) ResumeInvoke(ctx context.Context, hr *HandoffRequest, humanResponse string) (string, TokenUsage, error)
```

Convenience wrapper over `Resume` that collects streamed chunks into a single string.

See [Handoffs](handoff.md) for the full handoff workflow.

#### Error Conditions

Both `Invoke` and `InvokeStream` can return errors for:

- **Input guardrail failure**: a guardrail returned an error before the provider was called
- **Memory load failure**: the configured `Memory` failed to load conversation history
- **Retriever failure**: the configured `Retriever` returned an error
- **Provider error**: the LLM backend returned an error
- **Token budget exceeded**: cumulative usage exceeded the configured budget (`ErrTokenBudgetExceeded`)
- **Output guardrail failure**: a guardrail returned an error on the final response
- **Memory save failure**: the configured `Memory` failed to persist the conversation
- **Max iterations exceeded**: the agent reached `maxIterations` without producing a text-only response
- **Handoff requested**: the agent called the handoff tool (`ErrHandoffRequested`) — see [Handoffs](handoff.md)

## TokenUsage

```go
type TokenUsage struct {
    InputTokens  int
    OutputTokens int
}

func (u TokenUsage) Total() int
```

`TokenUsage` records cumulative token consumption across all provider calls within a single invocation. `Total()` returns `InputTokens + OutputTokens`.

Both `Invoke` and `InvokeStream` return a `TokenUsage` value — even when they also return an error (e.g., on token budget exceeded, the usage reflects the amount consumed up to that point).

## ErrTokenBudgetExceeded

```go
var ErrTokenBudgetExceeded = fmt.Errorf("token budget exceeded")
```

Sentinel error returned when cumulative token usage exceeds the budget set via `WithTokenBudget`. Use `errors.Is` to check:

```go
result, usage, err := myAgent.Invoke(ctx, "Hello")
if errors.Is(err, agent.ErrTokenBudgetExceeded) {
    fmt.Printf("Budget exceeded after %d tokens\n", usage.Total())
}
```

## Agent Loop Behavior

Each call to `Invoke` or `InvokeStream` runs the following loop:

1. **Input guardrails** — the user message passes through all configured `InputGuardrail` functions in order. Any error aborts the invocation.

2. **Memory load** — if `WithMemory` is configured, conversation history is loaded and prepended to the message list.

3. **RAG retrieval** — if `WithRetriever` is configured, relevant documents are retrieved once and injected as a user/assistant message turn in the conversation (not into the system prompt).

4. **Agent loop** (up to `maxIterations`):
   - The provider is called with the current messages, system prompt, and tool specs.
   - Token usage is accumulated. If a token budget is set and exceeded, the loop aborts with `ErrTokenBudgetExceeded`.
   - If the provider returns **tool calls**: the agent executes them (sequentially or in parallel depending on `WithParallelToolExecution`), appends the assistant message and tool results to the conversation, and loops back to step 4.
   - If the provider returns a **text response**: the loop exits.

5. **Output guardrails** — the final text passes through all configured `OutputGuardrail` functions. Any error aborts the invocation.

6. **Memory save** — if `WithMemory` is configured, the full conversation (including the new exchange) is saved.

7. **Return** — the text response and cumulative `TokenUsage` are returned.

If the loop reaches `maxIterations` without the provider producing a text-only response, an error is returned.

## See Also

- [Getting Started](getting-started.md) — installation and first agent
- [HTTP & Multi-Tenant Environments](http.md) — `WithSharedMemory`, `WithConversationID`, and HTTP server patterns
- [Handoffs](handoff.md) — human handoff workflow, `Resume`, `ResumeInvoke`
- [Prompt System](prompts.md) — `Text`, `RISEN`, `COSTAR` prompt types
- [Tool System](tools.md) — defining tools for the agent
- [Memory System](memory.md) — conversation persistence and strategies
- [Guardrails](guardrails.md) — input and output validation
- [Middleware](middleware.md) — wrapping tool execution
- [RAG Pipeline](rag.md) — retrieval-augmented generation
- [Message Types](message-types.md) — `Message`, `ContentBlock`, `ConverseParams`
