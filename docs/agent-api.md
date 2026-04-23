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
- No memory, middleware, guardrails, token budget, retriever, or context formatter

## Option Functions

Each `Option` is a `func(*Agent) error` that configures the agent at construction time.

### `WithName`

```go
func WithName(name string) Option
```

Sets an optional name for the agent. The name is used as a dimension/attribute in metrics and tracing hooks, making it possible to distinguish telemetry from different agents in the same process. When set, metrics exporters add an `agent_name` (OTEL/Prometheus) or `AgentName` (CloudWatch) label, and the tracing hook adds an `agent.name` span attribute on the root invoke span.

```go
a, err := agent.Default(provider, instructions, tools,
    agent.WithName("order-agent"),
    otelmetrics.WithMetrics(mp),
)
```

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

### `WithTemperature`

```go
func WithTemperature(v float64) Option
```

Sets the temperature inference parameter on the agent. Temperature controls randomness of LLM output. Valid range: [0.0, 1.0]. Returns an error if the value is outside this range.

```go
a, err := agent.Default(provider, instructions, tools,
    agent.WithTemperature(0.7),
)
```

### `WithTopP`

```go
func WithTopP(v float64) Option
```

Sets the top_p (nucleus sampling) inference parameter on the agent. Controls the cumulative probability cutoff for token selection. Valid range: [0.0, 1.0]. Returns an error if the value is outside this range.

### `WithTopK`

```go
func WithTopK(v int) Option
```

Sets the top_k inference parameter on the agent. Limits the number of highest-probability tokens considered during sampling. Must be >= 1. Returns an error if the value is less than 1.

Note: OpenAI does not support top_k — the parameter is silently ignored for OpenAI providers.

### `WithStopSequences`

```go
func WithStopSequences(s []string) Option
```

Sets the stop sequences inference parameter on the agent. When the LLM generates any of these strings, it stops producing further tokens.

```go
a, err := agent.Default(provider, instructions, tools,
    agent.WithTemperature(0.7),
    agent.WithTopP(0.9),
    agent.WithTopK(50),
    agent.WithStopSequences([]string{"END", "STOP"}),
)
```

### `WithMaxTokens`

```go
func WithMaxTokens(n int) Option
```

Sets the maximum number of tokens the LLM can generate in a response, at the agent level. Must be >= 1. When set, this overrides the provider-level max tokens for every call. Can be overridden per-invocation via `WithInferenceConfig`.

```go
a, err := agent.Default(provider, instructions, tools,
    agent.WithMaxTokens(2048),
)
```

Note: this is distinct from the provider-level `WithMaxTokens` option (e.g., `bedrock.WithMaxTokens`). The agent-level option flows through `InferenceConfig` and takes precedence over the provider constructor default when set.

All four inference parameter options populate an `InferenceConfig` on the agent. When no inference options are set, the agent passes `nil` to the provider, which uses its own defaults. Per-invocation overrides are supported via `WithInferenceConfig` on the context — see [InvocationContext](invocation-context.md).

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

### `InferenceConfig`

```go
func (a *Agent) InferenceConfig() *InferenceConfig
```

Returns the agent's inference config, or `nil` if no inference parameters were set at construction time. Used internally by the swarm to forward inference config to provider calls.

### `WithInferenceConfig`

```go
func WithInferenceConfig(ctx context.Context, cfg *InferenceConfig) context.Context
```

Returns a context that attaches a per-invocation `InferenceConfig` override. When the agent runs, it merges per-invocation values over agent-level values field by field — non-nil per-invocation fields take precedence, nil fields fall back to the agent-level value.

```go
ctx := agent.WithInferenceConfig(context.Background(), &agent.InferenceConfig{
    Temperature: ptrFloat(0.9), // override just temperature for this call
})
result, _, err := a.Invoke(ctx, "Be creative!")
```

The merged config is validated before the provider is called. If any field is invalid (e.g., temperature > 1.0), the invocation returns an error without calling the provider.

### `GetInferenceConfig`

```go
func GetInferenceConfig(ctx context.Context) *InferenceConfig
```

Retrieves the per-invocation `InferenceConfig` from a context. Returns `nil` if none is attached.

### `WithImages`

```go
func WithImages(ctx context.Context, images []ImageBlock) context.Context
```

Returns a context that attaches a slice of `ImageBlock` values for the current invocation. The agent loop reads these images and prepends them to the first user message before calling the provider, enabling vision-capable models to reason over visual content alongside text.

```go
img := agent.ImageBlock{
    Source: agent.ImageSource{
        Data:     imageBytes,
        MIMEType: "image/jpeg",
    },
}
ctx := agent.WithImages(context.Background(), []agent.ImageBlock{img})
result, _, err := a.Invoke(ctx, "What is in this image?")
```

Pass `nil` or an empty slice to clear any previously attached images. When multiple `WithImages` calls are chained on the same context, the innermost (most recent) call wins.

### `GetImages`

```go
func GetImages(ctx context.Context) []ImageBlock
```

Retrieves the image slice from a context. Returns `nil` if no images are attached or if an empty slice was stored.

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

### `Close`

```go
func (a *Agent) Close()
```

Performs graceful cleanup. If the agent's memory implements `MemoryWaiter` (e.g. the `Summary` strategy with background summarization), `Close` blocks until all background work is complete. Safe to call multiple times. No-op if no cleanup is needed.

Call `Close` before process exit to ensure pending summarizations are flushed:

```go
a, _ := agent.Default(provider, instructions, tools,
    agent.WithMemory(summaryMemory, "conv-1"),
)
defer a.Close()
```

#### Error Conditions

Both `Invoke` and `InvokeStream` can return errors for:

- **Input guardrail failure**: a guardrail returned an error before the provider was called
- **Inference config validation failure**: a per-invocation `InferenceConfig` contains invalid values (e.g., temperature outside [0.0, 1.0])
- **Invalid image MIME type**: an `ImageBlock` attached via `WithImages` has an unsupported `MIMEType`
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

Each call to `Invoke` or `InvokeStream` runs the following steps:

1. **Input guardrails** — the user message passes through all configured `InputGuardrail` functions in order. Any error aborts the invocation.
2. **Memory load** — if `WithMemory` is configured, conversation history is loaded.
3. **RAG retrieval** — if `WithRetriever` is configured, relevant documents are retrieved and injected as context before the user message.
4. **Image injection** — if `WithImages` was called on the context, each `ImageBlock`'s MIME type is validated and the images are prepended to the first user message's content slice. An invalid MIME type returns an error before the provider is called.
5. **Agent loop** (up to `maxIterations`):
   - The provider is called with the current messages, system prompt, and tool specs.
   - If the provider returns **tool calls**: the agent executes them and loops back.
   - If the provider returns a **text response**: the loop exits.
   - If a token budget is set and exceeded, the loop aborts with `ErrTokenBudgetExceeded`.
6. **Output guardrails** — the final text passes through all configured `OutputGuardrail` functions.
7. **Memory save** — if `WithMemory` is configured, the full conversation (including any `ImageBlock` values) is saved.

If the loop reaches `maxIterations` without a text response, an error is returned.

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
