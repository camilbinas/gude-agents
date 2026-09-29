# Providers

Agents depend on one streaming provider contract:

```go
type Provider interface {
    Name() string
    Stream(
        ctx context.Context,
        req agent.ModelRequest,
        emit func(agent.ModelEvent),
    ) (*agent.ModelResponse, error)
}
```

`emit` may be nil. Providers emit `ModelEventText` and `ModelEventThinking`; the returned `ModelResponse` contains final text, tool calls, usage, and provider metadata. `ModelRequest` contains messages, system instructions, tool specs/choice, inference configuration, and caching preference.

Applications normally construct a provider and pass it to `agent.New`:

```go
prov, err := openai.New("gpt-4o-mini")
a, err := agent.New(prov, "You are helpful.", agent.WithTools(tools...))
result, err := a.Invoke(agent.NewContext(ctx), "Hello")
```

Agent-level inference options provide portable temperature, top-p/top-k, stop sequences, and output-token limits. Provider options configure credentials, endpoints, provider-specific thinking, caching, or guardrails. `ModelIdentifier` and token-estimator interfaces are optional capabilities.

## Built-ins

- [Anthropic](providers/anthropic.md)
- [Amazon Bedrock](providers/bedrock.md)
- [Gemini](providers/gemini.md)
- [OpenAI and compatible endpoints](providers/openai.md)
- [Ollama](providers/ollama.md)
- [vLLM](providers/vllm.md)

The provider registry can create a registered provider by name and tier. The fallback provider accepts a primary plus ordered fallbacks; it changes provider only if failure occurs before any event is emitted, avoiding mixed streams.

## Implementing a provider

Map every provider request to `ModelRequest`, emit live text/thinking when available, and return authoritative aggregate text, tool calls, and `TokenUsage`. Respect context cancellation. Wrap backend failures with enough context for diagnosis but never log credentials or raw content by default. Keep provider streaming callbacks synchronous unless the implementation documents ordering and safe shutdown.
