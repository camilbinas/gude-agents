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

## Capability metadata

`agent.Provider` remains the only required provider contract. Providers may additionally implement `agent.CapabilityProvider` to report the effective capabilities of their configured model, provider API, and Gude adapter:

```go
caps := agent.CapabilitiesOf(prov)
fmt.Println(caps.ContextWindowTokens, caps.MaxOutputTokens)
```

The capability values `agent.Supported`, `agent.Unsupported`, and `agent.Unknown` have distinct meanings:

- `Supported`: Gude can confidently use that feature through this configured provider.
- `Unsupported`: Gude knows the provider/model/adapter combination cannot satisfy it and may fail early for a feature that requires it.
- `Unknown`: Gude has no reliable metadata and normally attempts the operation. Unknown is **not** an allow-list rejection.

Named model constructors supply only best-known defaults. Their metadata can become stale as providers evolve, so every concrete provider exposes caller overrides. `WithCapabilities` is a partial convenience override: non-zero numeric fields and non-`Unknown` capabilities replace defaults without erasing unrelated fields. Fine-grained options can explicitly reset a value to `Unknown`.

```go
// A newly released or private model can carry application-owned metadata.
prov := bedrock.Must(bedrock.New(
    "vendor.new-model",
    bedrock.WithCapabilities(agent.ModelCapabilities{
        ContextWindowTokens: 200_000,
        MaxOutputTokens:     16_000,
        ToolUse:             agent.Supported,
        ToolChoice: agent.ToolChoiceCapabilities{
            Auto:     agent.Supported,
            Required: agent.Supported,
            Specific: agent.Unsupported,
        },
    }),
))

// A caller correction changes only Specific; named-constructor defaults for
// limits, ToolUse, Auto, Required, and native output remain intact.
prov = bedrock.Must(bedrock.GlobalClaudeSonnet5_5(
    bedrock.WithToolChoice(agent.ToolChoiceCapabilities{
        Specific: agent.Supported,
    }),
))

// Per-field options permit an explicit reset when metadata is uncertain again.
prov = bedrock.Must(bedrock.GlobalClaudeSonnet5_5(
    bedrock.WithToolChoiceSpecific(agent.Unknown),
))
```

`ContextWindowTokens` and `MaxOutputTokens` are advisory sizing hints, not engine limits. Gude does not reject a request based on local estimates; the live provider remains authoritative. `fallback.Provider` reports a conservative intersection across every provider that may serve a request: a feature is supported only when all delegates support it, and numeric limits are reported only when every delegate reports a known value (using the smallest limit).

### Current researched defaults

The catalog intentionally covers only current models with primary-source evidence. Unlisted and custom models remain `Unknown` and can be configured with caller overrides.

| Provider constructor/model ID | Context window | Max output | Additional effective metadata | Primary source |
| --- | ---: | ---: | --- | --- |
| Bedrock Claude Sonnet 4.6 (`global.`, `us.`, `eu.`) | 1,000,000 | 64,000 | Tool use + named forced tool choice supported; native schema output unavailable through Gude | [AWS model card](https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-sonnet-4-6.html) |
| Bedrock Claude Sonnet 5.5 (`global.`) | 1,000,000 | 128,000 | Auto tool use supported; Bedrock rejects required/named forced choice; native schema output unavailable through Gude | [AWS model card](https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-sonnet-5-5.html) |
| Bedrock Claude Opus 5.5 (`global.`, `us.`, `eu.`, `au.`, `jp.`) | 1,000,000 | 128,000 | Native schema output unavailable through Gude | [AWS model card](https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-opus-5-5.html) |
| Direct Anthropic Claude Haiku 4.5 | 200,000 | 64,000 | Native schema output unavailable through Gude | [Anthropic model overview](https://platform.claude.com/docs/en/models/sonnet-5-5/overview) |
| Direct Anthropic Claude Sonnet 4.6, Sonnet 5, Sonnet 5.5, Opus 5.5 | 1,000,000 | 128,000 | Native schema output unavailable through Gude | [Anthropic model overview](https://platform.claude.com/docs/en/models/sonnet-5-5/overview) |
| OpenAI GPT-5.6 / GPT-5.6 Sol | 1,050,000 | 128,000 | Function calling supported; native structured outputs exist upstream but are not yet implemented by Gude's Chat Completions adapter | [OpenAI model page](https://platform.openai.com/docs/models/gpt-5.6) |
| Gemini 3.8 Flash | 1,048,576 | 65,536 | Function calling supported; native structured outputs exist upstream but are not yet implemented by Gude | [Google model page](https://ai.google.dev/gemini-api/docs/models/gemini-3.8-flash) |

The native structured-output column describes what can be used through the configured Gude provider, not the underlying model in isolation. This distinction is why OpenAI and Gemini remain `agent.Unsupported` for `NativeStructuredOutput` until their adapters implement a native response-schema path.


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
