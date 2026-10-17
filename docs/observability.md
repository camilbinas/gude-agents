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

- `logging/debug.WithLogging(opts...)` writes a readable lifecycle trace for local development. It prints to stdout with ANSI colors by default; `debug.WithWriter(w)` redirects it (a nil writer fails `agent.New`) and `debug.WithColor(false)` removes every escape sequence, dividers and errors included. Writes are serialized, so concurrent events never interleave. It is a development trace, not a structured log; `logging/slog` does not imitate it.
- `logging/slog.WithLogging(...)` uses `log/slog`; configure `WithHandler` and `WithMinLevel`.

```go
import (
    agentdebug "github.com/camilbinas/gude-agents/agent/logging/debug"
    agentslog "github.com/camilbinas/gude-agents/agent/logging/slog"
)

// Local development: colored trace on stdout.
a, err := agent.New(prov, instructions, agentdebug.WithLogging())

// Capture a plain trace, for example in a test.
var trace bytes.Buffer
a, err = agent.New(prov, instructions,
    agentdebug.WithLogging(agentdebug.WithWriter(&trace), agentdebug.WithColor(false)),
)

// Production: structured logs.
a, err = agent.New(prov, instructions, agentslog.WithLogging())
```

Choosing between them by environment is application code; the framework ships no environment switch.

## Metrics

- `metrics/prometheus.WithMetrics`, with optional namespace/registerer; the package can also return an HTTP handler.
- `metrics/otel.WithMetrics`, with a meter provider and optional namespace.

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
    agentslog.WithLogging(),
    tracing.WithTracing(tracerProvider),
    metricsotel.WithMetrics(meterProvider),
)
```

Use the adapter package documentation for Prometheus handlers, Sentry, OTLP exporters, and content-capture/security trade-offs.
