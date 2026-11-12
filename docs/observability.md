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
- `logging/slog.WithLogging(...)` uses `log/slog`; configure `WithHandler` and `WithMinLevel`. It logs timings, token counts and errors but not user or model content; `WithContentLogging()` opts into logging the final response text and retrieval queries, matching `tracing.WithContentCapture()`. Enable it only where logs may hold user data.

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

- `metrics/prometheus.WithMetrics`, with optional namespace/registerer; the package can also return an HTTP handler via `NewHandler`.
- `metrics/otel.WithMetrics`, with a meter provider and optional namespace.

`NewHandler` (Prometheus) is all-in-one: use it *or* `WithMetrics`, not both. With no `WithRegisterer`, `NewHandler` scrapes from its own fresh registry while `WithMetrics` defaults to `prometheus.DefaultRegisterer`, so combining them installs two hooks on two registries and the handler scrapes the wrong one. Pick the handler+option pair from a single `NewHandler` call, or pass the same `WithRegisterer` to both.

### Tool-name label cardinality

The `tool_name` label (Prometheus) / attribute (OTEL) is recorded verbatim by default. For a dynamic or user-supplied tool set — for example MCP tools registered at runtime — that is an unbounded-cardinality risk that can blow up Prometheus memory. Bound it with `WithToolNameAllowlist`, available on both backends: allowlisted names are recorded as-is, any other name is bucketed under `other`.

```go
agent.New(prov, instructions,
    prometheusmetrics.WithMetrics(prometheusmetrics.WithToolNameAllowlist("search", "calculator")),
)
```

## Tracing

`tracing.WithTracing(provider, options...)` uses OpenTelemetry. `WithScopeName` and `WithScheme` customize instrumentation; `WithContentCapture` opts into prompt/response capture. `tracing/sentry.WithSentry` configures Sentry through the same lifecycle model. Content capture can contain sensitive data and is off by default.

### GenAI semantic conventions

The observer targets the OpenTelemetry GenAI semantic conventions (version pinned and documented as `tracing.SemconvGenAIVersion`; the GenAI conventions are published separately from the stable OTel semconv packages and are at Development stability, so the `gen_ai.*` keys are hand-pinned constants).

- `gen_ai.operation.name` (`invoke_agent` / `chat` / `execute_tool`) is emitted by default so GenAI-aware backends recognize the spans. Remap `RoleOperationName` in a custom scheme to change it.
- Under `WithContentCapture`, the observer emits the structured `gen_ai.input.messages` / `gen_ai.output.messages` attributes (role + typed parts, JSON encoded) alongside the deprecated `gen_ai.prompt` / `gen_ai.completion` / `gen_ai.provider.response`. `WithoutLegacyContentAttributes` drops the deprecated pair and keeps only the structured attributes.
- `WithSpecSpanNames` opts into the spec span-name format `{operation} {model}` (for example `chat gpt-4o`). It is opt-in because renaming spans breaks dashboards, alerts, and tests; the default gude-agents span names (`agent.invoke`, `agent.provider.call`, `agent.tool.<name>`) are unchanged.
- Non-content scalar attributes `agent.token_usage.total`, `agent.user_message_length`, `tool.input_length`, and `tool.output_length` are emitted by default (lengths/counts only, never the payload); `agent.error` is set on invocation failure.

### Exporter setup and transport security

`tracing.Setup` configures an OTLP gRPC exporter from `OTEL_EXPORTER_OTLP_ENDPOINT` (default `localhost:4317`). The export defaults to plaintext (insecure), matching the localhost default. To use TLS against a remote collector, set `OTEL_EXPORTER_OTLP_TRACES_INSECURE` / `OTEL_EXPORTER_OTLP_INSECURE` to `false`, or pass `tracing.WithInsecure(false)` (the option takes precedence over the environment).

### Sentry breadcrumb content

`tracing/sentry.BreadcrumbMiddleware` records tool name, duration, and error status by default; tool input and result text are omitted to avoid leaking sensitive data. Pass `sentry.WithBreadcrumbContent()` to include them (truncated on a rune boundary, so always valid UTF-8), consistent with `tracing.WithContentCapture` and `slog.WithContentLogging`.

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
