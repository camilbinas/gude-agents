# OpenTelemetry Tracing

The `agent/tracing` module adds OpenTelemetry distributed tracing to gude-agents. It lives in a separate Go module with its own `go.mod`, keeping the core `agent/` package free of OTEL dependencies. You opt in by importing the tracing submodule and passing `tracing.WithTracing(tp)` as an `agent.Option`.

The module instruments the full agent lifecycle: invocations, loop iterations, provider calls, tool executions, guardrails, conversation operations, RAG retrieval, graph workflows, and multi-agent composition.

## Enabling Tracing

Pass `tracing.WithTracing` as an agent option to enable tracing. It accepts a `trace.TracerProvider` — or `nil` to use the global provider:

```go
import (
    "go.opentelemetry.io/otel/trace"
    "github.com/camilbinas/gude-agents/agent"
    "github.com/camilbinas/gude-agents/agent/tracing"
)

// With an explicit TracerProvider:
a, err := agent.New(provider, instructions, tools,
    tracing.WithTracing(tp),
)

// With the global TracerProvider (set by Setup or otel.SetTracerProvider):
a, err := agent.New(provider, instructions, tools,
    tracing.WithTracing(nil),
)
```

When tracing is not enabled, the agent creates no spans and allocates no tracing objects. The core `agent/` package has zero OpenTelemetry imports.

## Tracing Options

`WithTracing` accepts variadic `TracingOption` values to customize hook behaviour:

| Option | Default | Description |
|--------|---------|-------------|
| `WithContentCapture()` | disabled | Includes message content (prompts, responses, tool inputs/outputs, guardrail text) as span attributes. Disable in production if messages may contain PII. |
| `WithScheme(scheme AttributeScheme)` | `DefaultScheme()` | Switches the attribute naming convention. The default scheme uses `agent.*` / `tool.*` / `provider.*` keys. Pass `AgentCoreScheme()` to emit OpenTelemetry GenAI semantic convention keys (`gen_ai.*`) compatible with AWS AgentCore Observability. |

```go
a, err := agent.New(provider, instructions, tools,
    tracing.WithTracing(tp,
        tracing.WithContentCapture(),
        tracing.WithScheme(tracing.AgentCoreScheme()),
    ),
)
```

## Quick Start with Setup

The `Setup` function configures a `TracerProvider` with a batch `SpanProcessor` and OTLP gRPC exporter in one call. It reads `OTEL_EXPORTER_OTLP_ENDPOINT` from the environment, defaulting to `localhost:4317`.

```go
package main

import (
    "context"
    "log"

    "github.com/camilbinas/gude-agents/agent"
    "github.com/camilbinas/gude-agents/agent/tracing"
)

func main() {
    ctx := context.Background()

    // One-liner OTEL setup — configures global TracerProvider.
    shutdown, err := tracing.Setup(ctx)
    if err != nil {
        log.Fatal(err)
    }
    defer shutdown(ctx)

    a, err := agent.New(provider, instructions, tools,
        tracing.WithTracing(nil), // uses the global provider from Setup
    )
    if err != nil {
        log.Fatal(err)
    }

    result, err := a.Invoke(agent.NewContext(ctx), "Hello")
    if err != nil {
        log.Fatal(err)
    }
    log.Println(result)
}
```

`TracingPreset()` is a shorthand for `WithTracing(nil)`, suitable for agent preset constructors:

```go
a, err := agent.New(provider, instructions, tools,
    tracing.TracingPreset(),
)
```

## Span Hierarchy

A traced agent invocation produces the following span tree:

```
agent.invoke
├── agent.guardrail.input          (per input guardrail)
├── agent.conversation.load        (if conversation configured)
├── agent.retriever.retrieve       (if RAG configured)
├── agent.iteration                (per loop iteration)
│   ├── agent.provider.call
│   ├── agent.tool.<tool_name>     (per tool call, may be concurrent)
│   └── agent.guardrail.output     (per output guardrail, on final iteration)
└── agent.conversation.save        (if conversation configured)
```

## Attribute Reference

All attribute key constants are exported from the `tracing` package for use in custom instrumentation.

| Constant | Key | Description |
|----------|-----|-------------|
| `AttrGenAISystem` | `gen_ai.system` | Always `"gude-agents"` on the root invoke span |
| `AttrAgentMaxIterations` | `agent.max_iterations` | Configured max iterations for the agent |
| `AttrAgentModelID` | `agent.model_id` | Model ID (if provider implements `ModelIdentifier`) |
| `AttrAgentConversationID` | `agent.conversation_id` | Conversation ID (if conversation configured) |
| `AttrAgentImageCount` | `agent.image_count` | Number of images attached via `WithImages` (only set when > 0) |
| `AttrAgentDocumentCount` | `agent.document_count` | Number of documents attached via `WithDocuments` (only set when > 0) |
| `AttrAgentTokenUsageInput` | `agent.token_usage.input` | Cumulative input tokens on successful invocation |
| `AttrAgentTokenUsageOutput` | `agent.token_usage.output` | Cumulative output tokens on successful invocation |
| `AttrAgentIterationNumber` | `agent.iteration.number` | 1-based iteration number |
| `AttrAgentIterationToolCount` | `agent.iteration.tool_count` | Number of tool calls in the iteration |
| `AttrAgentIterationFinal` | `agent.iteration.final` | `true` on the final iteration (text response, no tool calls) |
| `AttrProviderModelID` | `provider.model_id` | Model ID on the provider call span |
| `AttrProviderInputTokens` | `provider.input_tokens` | Input tokens for a single provider call |
| `AttrProviderOutputTokens` | `provider.output_tokens` | Output tokens for a single provider call |
| `AttrProviderToolCalls` | `provider.tool_calls` | Number of tool calls returned by the provider |
| `AttrToolName` | `tool.name` | Name of the tool being executed |
| `AttrMemoryConversationID` | `memory.conversation_id` | Conversation ID on memory load/save spans |
| `AttrRetrieverDocumentCount` | `retriever.document_count` | Number of documents returned by the retriever |
| `AttrGraphIterations` | `graph.iterations` | Total node executions in a graph run |
| `AttrGenAITemperature` | `gen_ai.request.temperature` | Temperature parameter (when set via inference config) |
| `AttrGenAITopP` | `gen_ai.request.top_p` | Top-p / nucleus sampling parameter (when set) |
| `AttrGenAITopK` | `gen_ai.request.top_k` | Top-k parameter (when set) |
| `AttrGenAIMaxTokens` | `gen_ai.request.max_tokens` | Max tokens parameter (when set) |
| `AttrGenAIStopSequences` | `gen_ai.request.stop_sequences` | Stop sequences (when set) |

### Events

| Constant | Event Name | Description |
|----------|-----------|-------------|
| `EventMaxIterationsExceeded` | `agent.max_iterations_exceeded` | Recorded on `agent.invoke` when the iteration limit is hit |

All attribute keys follow the `<component>.<property>` dot-separated lowercase naming convention, consistent with OpenTelemetry semantic conventions.

## Graph Tracing

For graph workflows, use `WithGraphTracing` to instrument `Graph.Run` and each node execution:

```go
import (
    "github.com/camilbinas/gude-agents/agent/graph"
    "github.com/camilbinas/gude-agents/agent/tracing"
)

g, err := graph.New[graph.State](
    tracing.WithGraphTracing(tp), // or nil for global provider
)
if err != nil {
    log.Fatal(err)
}
g.Node("classify", classifyNode, graph.In("input"), graph.Out("category"))
g.Node("respond", respondNode, graph.In("category"), graph.Out("output"))
g.Start("classify")

result, err := g.Run(ctx, graph.State{"input": userMessage})
```

Graph tracing produces a parallel span hierarchy:

```
graph.run
├── graph.node.<node_name>         (per node, may be concurrent when multiple nodes become ready)
│   └── agent.invoke               (if node wraps an agent)
├── graph.checkpoint.save          (per checkpoint, with node and version attributes)
├── graph.interrupt                (when an interrupt fires)
├── graph.resume                   (when Resume is called)
└── graph.rewind                   (when RewindTo is called)
```

- `graph.run` wraps the entire graph execution and records `graph.iterations` on completion.
- Each node gets a `graph.node.<name>` child span.
- When multiple nodes become ready simultaneously, their spans are concurrent siblings under `graph.run`.
- If a node wraps an agent (via `g.Agent`), the agent's spans nest under the node span.
- `graph.checkpoint.save` records `graph.checkpoint.node` and `graph.checkpoint.version`.
- `graph.interrupt` records `graph.interrupt.node`, `graph.interrupt.type`, and `graph.interrupt.version`.
- `graph.resume` and `graph.rewind` record `graph.thread_id` and the relevant version.

See `examples/tracing-graph/`.

## Multi-Agent Trace Propagation

When using `AgentAsTool` to compose agents, traces propagate automatically through the `context.Context`. The child agent's spans appear as children of the parent's tool execution span:

```go
childAgent, _ := agent.New(childProvider, childInstructions, childTools,
    tracing.WithTracing(tp),
)

parentAgent, _ := agent.New(parentProvider, parentInstructions,
    []tool.Tool{agent.AgentAsTool("child", "A child agent", childAgent)},
    tracing.WithTracing(tp),
)

// The resulting trace tree:
// agent.invoke (parent)
// └── agent.iteration
//     └── agent.tool.child
//         └── agent.invoke (child)
//             └── agent.iteration
//                 └── agent.provider.call
```

No extra configuration is needed — the tracing context flows through the standard Go `context.Context` that `AgentAsTool` passes to the child agent's `Invoke` method.

## Custom Instrumentation

The attribute constants are exported so you can reference them in custom middleware or spans:

```go
import (
    "encoding/json"

    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/attribute"
    "github.com/camilbinas/gude-agents/agent"
    "github.com/camilbinas/gude-agents/agent/tracing"
)

func metricsMiddleware(next agent.ToolHandlerFunc) agent.ToolHandlerFunc {
    tracer := otel.Tracer("my-app")
    return func(c *agent.Context, toolName string, input json.RawMessage) (string, error) {
        ctx, span := tracer.Start(c, "custom.metrics")
        span.SetAttributes(attribute.String(tracing.AttrToolName, toolName))
        defer span.End()
        // Wrap ctx back into a *Context for the next handler
        inner := agent.NewContext(ctx)
        return next(inner, toolName, input)
    }
}
```

Because the middleware receives a context that already carries the `agent.tool.<name>` span, the custom span appears as a child of the tool span in the trace tree.

## Sentry Integration

See [Sentry Integration](tracing-sentry.md) for the dedicated Sentry tracing module that combines OTEL trace export with error capture and breadcrumbs.

## See Also

- [Structured Logging](logging.md) — `log/slog`-based structured logging for the same lifecycle points
- [Prometheus Metrics](metrics.md) — counters and histograms for agent lifecycle events
- [Middleware](middleware.md) — wrapping tool execution with cross-cutting behavior
- [Multi-Agent](multi-agent.md) — composing agents with `AgentAsTool`
- [Conversation System](conversation.md) — conversation backends that produce `agent.conversation.*` spans
- [RAG](rag.md) — retriever integration that produces `agent.retriever.retrieve` spans
- [Guardrails](guardrails.md) — input/output guardrails that produce guardrail spans
