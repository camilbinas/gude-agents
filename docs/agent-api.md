# Agent API

## Mental model

- **Agent**: long-lived, immutable configuration.
- **Context**: one invocation's cancellation and mutable pre-run configuration.
- **Result**: the completed or interrupted outcome.

```go
func New(provider Provider, instructions string, opts ...Option) (*Agent, error)
```

A typical construction combines tools and conversation persistence:

```go
a, err := agent.New(prov, "You are a support assistant.",
    agent.WithName("support"),
    agent.WithTools(lookup, refund),
    agent.WithConversationStore(conversations),
    agent.WithExecutionStore(executions),
)
```

When a ConversationStore is configured, every invocation requires a non-empty ConversationID; forgetting it returns an error matching `agent.ErrConversationIDRequired` before any model or tool work. Stateless Agents should be constructed without a ConversationStore.

`agent.RAGAgent(prov, instructions, retriever, opts...)` is the one convenience constructor: it requires a non-nil retriever (`agent.ErrRetrieverRequired` otherwise) and is otherwise identical to `agent.New` with `agent.WithRetriever(retriever)`. See [RAG](rag.md).

Common options include `WithToolRegistry`, `WithMaxIterations`, `WithToolExecution`, `WithMaxConcurrentTools`, `WithProviderTimeout`, `WithMaxOutputTokens`, `WithTemperature`, `WithTopP`, `WithTopK`, `WithStopSequences`, `WithTokenBudget`, `WithRetriever`, `WithContextFormatter`, `WithNormalization`, `WithoutNormalization`, `WithExecutionStore`, `WithRateLimiter`, `WithRateLimitKey`, `WithMiddleware`, guardrails, `WithToolVisibility`, and `WithObserver`. The iteration default is 10.

## Inspecting an Agent

An Agent exposes three read-only inspection methods, used by first-party extension packages such as telemetry:

```go
func (a *Agent) Name() string
func (a *Agent) Instructions() string
func (a *Agent) ToolSpecs() []tool.Spec
```

Everything else about an Agent's configuration is private. There is no configuration snapshot API; keep your own copy of the values you passed to `agent.New` if your application needs them.

## Tool execution

The tool calls of one model response run in parallel by default and their results always reach the model in the provider's original call order. Two options control scheduling:

```go
a, err := agent.New(prov, instructions,
    agent.WithTools(tools...),
    agent.WithMaxConcurrentTools(4), // at most 4 handlers at once
)

a, err = agent.New(prov, instructions,
    agent.WithTools(tools...),
    agent.WithToolExecution(agent.ToolExecutionSequential), // one at a time, in provider order
)
```

| Option | Meaning |
|---|---|
| `WithToolExecution(ToolExecutionParallel)` | Default. Handlers of a batch run concurrently. |
| `WithToolExecution(ToolExecutionSequential)` | Handlers run one at a time, strictly in provider order. Use it when handlers are not safe to run together or must observe each other's side effects. |
| `WithMaxConcurrentTools(n)` | Parallel mode runs at most `n` handlers at once (`n >= 1`; `1` is strictly in-order). |

An unknown mode or `n < 1` fails `agent.New`. `ToolExecutionSequential` combined with `WithMaxConcurrentTools(n)` for `n > 1` also fails `agent.New`, since silently ignoring one option would hide a mistake. The policy applies to every path that runs tools: normal calls, durable execution, approval resume and recovery. A batch that needs approval still persists its pause before any sibling handler runs.

## Invocation

```go
func (a *Agent) Invoke(ctx *agent.Context, input string) (agent.Result, error)
func (a *Agent) Stream(ctx *agent.Context, input string) iter.Seq2[agent.Event, error]
func (a *Agent) TextStream(ctx *agent.Context, input string) iter.Seq2[string, error]
func (a *Agent) Resume(ctx *agent.Context, in *agent.Interrupt, response agent.ResumeResponse) (agent.Result, error)
func (a *Agent) ResumeStream(ctx *agent.Context, in *agent.Interrupt, response agent.ResumeResponse) iter.Seq2[agent.Event, error]
```

All methods share one execution lifecycle. `Invoke` drains `Stream`; `TextStream` projects only live text events. `Resume` and `ResumeStream` continue a paused execution loaded from the `ExecutionStore`; pausing and resuming need both a `ConversationStore` and an `ExecutionStore`.

## Result

```go
type Result struct {
    ExecutionID string
    Text        string
    Usage       TokenUsage
    StopReason  StopReason
    Interrupt   *Interrupt
    Metadata    map[string]any
}
```

`StopEndTurn` means a final answer is available. `StopInterrupt` means execution paused successfully and `Interrupt` is populated. Provider, guardrail, budget, iteration-limit, cancellation, and persistence failures are errors; a result returned with an error can still contain usage accumulated before failure.

## Events

`Event` is a JSON-serializable tagged union. Inspect `Type`, then the corresponding pointer field.

| Type | Payload | Meaning |
|---|---|---|
| `EventStart` | — | first event |
| `EventText` | `Text` | live answer text |
| `EventThinking` | `Thinking` | provider reasoning text |
| `EventToolStart` / `EventToolEnd` | `Tool` | matching tool call, keyed by `CallID` |
| `EventWidget` | `Widget` | widget emitted by a tool |
| `EventInterrupt` | `Interrupt` | paused invocation |
| `EventCustom` | `Custom` | application-defined tool event |
| `EventEnd` | `Result`, optionally `Error` | canonical terminal outcome |

`ToolEvent.Duration` and lifecycle durations serialize as `duration_ms`. `EventEnd.Result` is identical to the result `Invoke` would return. On failure, the iterator yields the Go error and `EventEnd.Error` contains transport-friendly code and message data.

`ctx.WithDetailedEvents()` additionally enables iteration/model lifecycle events. These application stream events are not operational observers; see [Observability](observability.md).

## Messages and provider calls

Conversation messages contain `TextBlock`, `ToolUseBlock`, or `ToolResultBlock` values. Providers receive a `ModelRequest` and return a `ModelResponse`; applications normally interact with `Result`, not provider internals. See [Providers](providers.md).

## Attachments and emitted content

Configure images and documents on `Context` before invocation. Tools can call `agent.EmitWidget(ctx, block)` and `agent.EmitEvent(ctx, name, payload)` using the standard `context.Context` they receive. Emissions appear in `Stream`; widgets are also associated with their tool call for persistence.

## Stopping
An Agent owns no goroutines or write buffers and has no `Close` or `Shutdown` method. To stop serving, stop admitting new invocations (for example `http.Server.Shutdown`), let in-flight invocations finish or cancel their contexts, then close the resources you own: provider clients, stores, and metric or trace exporters.

`ConversationStore.Append` is committed when it returns, so there is nothing for the Agent to flush at shutdown. Memory-backed stores satisfy that within the running process only.

### Thinking events

`Agent.Stream` can yield `EventThinking` when the configured provider/model has reasoning enabled. Handle it alongside `EventText`; enabling reasoning is provider-specific configuration (for Anthropic, see [Extended thinking](providers/anthropic.md#extended-thinking-and-streaming)).
