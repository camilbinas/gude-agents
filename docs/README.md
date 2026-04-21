# gude-agents

A Go agent framework for building LLM-powered applications. `gude-agents` provides a composable, provider-agnostic toolkit for creating conversational AI agents with tool use, memory, RAG, guardrails, and multi-agent orchestration.

```
go get github.com/camilbinas/gude-agents
```

Then add the provider(s) you need:

```
go get github.com/camilbinas/gude-agents/agent/provider/bedrock
```

Each provider and driver is a separate module — you only pull the dependencies you use. See [Getting Started](getting-started.md) for the full list.

## Supported Providers

| Provider | Models |
|----------|--------|
| **Amazon Bedrock** | Claude, Nova, Qwen, MiniMax, GPT-OSS, Nemotron, GLM |
| **Anthropic** | Claude |
| **OpenAI** | GPT, O-series |
| **Google Gemini** | Gemini 2.5, 3, 3.1 |

## Getting Started

- [Getting Started Guide](getting-started.md) — Installation, first agent, and provider setup

## Core Concepts

- [Agent API Reference](agent-api.md) — Constructor, options, invoke methods, and agent loop
- [Message Types](message-types.md) — Message, ContentBlock, ConverseParams, and related types
- [Prompt System](prompts.md) — Text, RISEN, and COSTAR prompt frameworks

## Providers

- [LLM Providers Overview](providers.md) — Interfaces, extended thinking, direct SDK access, custom providers
  - [Bedrock](providers/bedrock.md) — AWS Bedrock: Claude, Nova, Qwen, guardrails
  - [Anthropic](providers/anthropic.md) — Anthropic Messages API: Claude models
  - [OpenAI](providers/openai.md) — OpenAI Chat Completions: GPT, O-series
  - [Gemini](providers/gemini.md) — Google Gemini: model constructors
- [Fallback Provider](fallback-provider.md) — Automatic failover across providers

## Components

- [Memory System](memory.md) — Strategies (Window, Token, Filter, Summary) and composable middleware
- [Redis Providers](redis.md) — Redis-backed memory and vector store
- [Tool System](tools.md) — Typed tools, schema generation, and tool choice
- [RAG Pipeline](rag.md) — Embedders, vector stores, retrieval, and ingestion
- [Guardrails](guardrails.md) — Input and output validation
- [Middleware](middleware.md) — Tool execution middleware
- [Structured Output](structured-output.md) — Type-safe JSON responses via `InvokeStructured[T]`

## Advanced Topics

- [Structured Logging](logging.md) — `log/slog`-based structured logging for agent lifecycle events
- [OpenTelemetry Tracing](tracing.md) — Distributed tracing with spans for invocations, provider calls, and tools
- [Prometheus Metrics](metrics.md) — Counters and histograms for agent lifecycle events
- [OTEL Metrics](metrics-otel.md) — OpenTelemetry metrics exporter for OTLP-compatible backends
- [CloudWatch Metrics](metrics-cloudwatch.md) — AWS CloudWatch metrics exporter with buffered flush
- [Graph Workflows](graph.md) — DAG-based state machines with fork/join, conditional routing, and typed state
- [HTTP & Multi-Tenant Environments](http.md) — `WithSharedMemory`, `WithConversationID`, and serving multiple users
- [Multi-Agent HTTP Server with Fiber v3](fiber-multi-agent.md) — Streaming multi-agent server with per-user conversations
- [Handoffs](handoff.md) — Pausing agents for human input and resuming
- [Multi-Agent Composition](multi-agent.md) — AgentAsTool and orchestrator pattern
- [MCP Integration](mcp.md) — Connect to MCP servers and use their tools
- [InvocationContext](invocation-context.md) — Per-invocation state sharing
