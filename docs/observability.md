# Observability and audit

Operational lifecycle observers are separate from application `Stream` events. Application events drive UI/SSE behavior; observers feed logs, metrics, traces, and security records whether the caller uses `Invoke`, `Stream`, structured output, or resume.

## Observer model

Register repeatable adapters at construction:

```go
a, err := agent.New(prov, instructions,
    agent.WithObserver(myObserver),
)
```

Small capability interfaces include `InvokeObserver`, `IterationObserver`, `ModelObserver`, `ToolObserver`, `GuardrailObserver`, `ConversationObserver`, `RetrievalObserver`, `AttachmentObserver`, `LimitObserver`, `ToolLogObserver`, and `InterruptObserver`. Each receives standard `context.Context` plus a record with `Phase` (`Start`/`End`), timestamps, duration, identifiers, outcome, and relevant usage/error fields. Implement only needed capabilities; methods may run concurrently and can return a derived context for matching end records.

`WithObserver` accepts `agent.Observer` (an alias for `any`, since Go cannot express "one of these interfaces" as a single type once the interfaces have methods) and rejects at construction any value implementing none of the capabilities above. Add a compile-time assertion per capability you implement so a typo'd method name fails to build instead of silently dropping that capability:

```go
var _ agent.ToolObserver = (*MyObserver)(nil)
```

Observers run in registration order at lifecycle start and reverse order at end. `Context.WithObservers(...)` replaces agent observers for one invocation; no arguments disable observation for that invocation.

## Built-in logging

- `logging/auto.WithLogging()` selects colored debug output for local environments and structured logging otherwise.
- `logging/debug.WithLogging()` writes colored development logs.
- `logging/slog.WithLogging(...)` uses `log/slog`; configure `WithHandler` and `WithMinLevel`.

```go
import autoslog "github.com/camilbinas/gude-agents/agent/logging/auto"

a, err := agent.New(prov, instructions, autoslog.WithLogging())
```

## Metrics

- `metrics/prometheus.WithMetrics`, with optional namespace/registerer; the package can also return an HTTP handler.
- `metrics/otel.WithMetrics`, with a meter provider and optional namespace.
- `metrics/cloudwatch.WithMetrics`, with namespace, client, interval, and dimensions. It also returns a shutdown function; call it to flush buffered metrics.

## Tracing

`tracing.WithTracing(provider, options...)` uses OpenTelemetry. `WithScopeName` and `WithScheme` customize instrumentation; `WithContentCapture` opts into prompt/response capture. `tracing/sentry.WithSentry` configures Sentry through the same lifecycle model. Content capture can contain sensitive data and is off by default.

## Audit

```go
type AuditSink interface {
    WriteAudit(context.Context, any)
}

a, err := agent.New(prov, instructions,
    agent.WithAudit(sink),
)
```

`WithAudit` writes stable JSON-serializable invocation, tool, and interrupt records. User messages, responses, tool input, and output are redacted by default; `WithAuditContent()` explicitly enables them. Protect audit sinks with access controls, retention limits, and backpressure appropriate to your compliance requirements.

The runnable examples intentionally do not duplicate a chatbot for every exporter. Configure one adapter on the same Agent:

```go
a, err := agent.New(prov, instructions,
    autoslog.WithLogging(),
    tracing.WithTracing(tracerProvider),
    metricsotel.WithMetrics(meterProvider),
)
```

Use the adapter package documentation for Prometheus handlers, CloudWatch flushing, Sentry, OTLP exporters, and content-capture/security trade-offs.
