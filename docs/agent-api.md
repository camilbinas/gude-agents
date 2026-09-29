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
    agent.WithConversationStore(store),
)
```

Common options include `WithToolRegistry`, `WithMaxIterations`, `WithParallelTools`, `WithProviderTimeout`, `WithProviderRetry`, `WithMaxOutputTokens`, `WithTemperature`, `WithTopP`, `WithTopK`, `WithStopSequences`, `WithTokenBudget`, `WithSyncConversation`, `WithRetriever`, `WithContextFormatter`, `WithNormalization`, `WithoutNormalization`, `WithInterruptStore`, `WithRateLimiter`, `WithMiddleware`, guardrails, `WithToolFilter`, `WithCaching`, and `WithObserver`. The iteration default is 10; parallel tool execution is off.

## Invocation

```go
func (a *Agent) Invoke(ctx *agent.Context, input string) (agent.Result, error)
func (a *Agent) Stream(ctx *agent.Context, input string) iter.Seq2[agent.Event, error]
func (a *Agent) TextStream(ctx *agent.Context, input string) iter.Seq2[string, error]
func (a *Agent) Resume(ctx *agent.Context, in *agent.Interrupt, response agent.ResumeResponse) (agent.Result, error)
func (a *Agent) ResumeStream(ctx *agent.Context, in *agent.Interrupt, response agent.ResumeResponse) iter.Seq2[agent.Event, error]
```

All methods share one execution lifecycle. `Invoke` drains `Stream`; `TextStream` projects only live text events. `Resume` and `ResumeStream` continue the exact snapshot carried by an interrupt.

## Result

```go
type Result struct {
    Text       string
    Usage      TokenUsage
    StopReason StopReason
    Interrupt  *Interrupt
    Metadata   map[string]any
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

## Shutdown

```go
err := a.Shutdown(ctx)
```

Shutdown waits for background tools and re-entry turns, then flushes a conversation store that implements `agent.Flusher`.
