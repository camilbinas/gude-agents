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

`emit` may be nil. Providers emit `ModelEventText` and `ModelEventThinking`; the returned `ModelResponse` contains final text, tool calls, usage, and provider metadata. `ModelRequest` contains messages, system instructions, tool specs/choice, and inference configuration.

## Choosing a provider and model

Gude never chooses a provider or a "best" model for you. There is no framework-wide default provider, no global provider registry, no environment variable that selects a provider, and no `Cheapest`, `Standard` or `Smartest` alias. Your application constructs one provider with an explicit model and passes it to `agent.New`:

```go
// A named model constructor: the model ID and Gude's best-known metadata.
prov := bedrock.Must(bedrock.GlobalClaudeSonnet5_5())

// Or an explicit model ID. Known IDs get the same metadata as the constructor.
prov, err := openai.New("gpt-5.6")

a, err := agent.New(prov, "You are helpful.", agent.WithTools(tools...))
result, err := a.Invoke(agent.NewContext(ctx), "Hello")
```

Choose a model that is available in your account and region. The subjective aliases were removed because they changed over time, hid cost and behavior, and made examples silently change when an alias was repointed. `Must` remains available for short programs: `anthropic.Must(anthropic.ClaudeSonnet5_5())`.

Each provider package keeps a private model catalogue keyed by model ID. `New(modelID, opts...)` looks the ID up before applying your options, so a known model starts from Gude's best-known capability metadata whether you use its named constructor or pass the ID yourself. Any other model ID is valid too: `New("my-custom-model")` reports only the adapter baseline, and everything Gude cannot verify stays `Unknown`.

Agent-level inference options provide portable temperature, top-p/top-k, stop sequences, and output-token limits. Provider options configure credentials, endpoints, provider-specific thinking, prompt caching, or guardrails. `ModelIdentifier` and token-estimator interfaces are optional capabilities.

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

Catalogue metadata is a best-known default, applied to a known model ID however you construct the provider (named constructor or `New("known-id")`). It can become stale as providers evolve, so every concrete provider exposes caller overrides, applied after the catalogue. `WithCapabilities` is a partial convenience override: non-zero numeric fields and non-`Unknown` capabilities replace defaults without erasing unrelated fields. Fine-grained options can explicitly reset a value to `Unknown`.

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

// A caller correction changes only Specific; catalogue defaults for
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

`ContextWindowTokens` and `MaxOutputTokens` are advisory sizing hints, not engine limits. Gude does not reject a request based on local estimates; the live provider remains authoritative. `fallback.Provider` reports a conservative intersection across every provider that may serve a request: a feature is supported only when every delegate supports it, unsupported when any delegate explicitly rejects it, and otherwise unknown. Numeric limits are reported only when every delegate reports a known value (using the smallest limit).

### What the catalogues contain

A catalogue entry exists for a model Gude actively supports, and a field is present only when it is documented by the provider and implemented by Gude's adapter. Everything else stays `Unknown`. Catalogue limits are advisory metadata, not limits Gude enforces.

| Provider | Catalogue holds | Primary source |
| --- | --- | --- |
| Anthropic | Context window and output ceiling for every named Claude model; tool use and automatic-only tool choice for the 5.5 models | [Anthropic model pages](https://platform.claude.com/docs/en/about-claude/models/overview) |
| Amazon Bedrock | Limits for the named Claude, Nova 2 Lite, gpt-oss, GPT-6, Grok 4.6 and Qwen3 235B models, tool-choice facts for Claude Sonnet 4.6 and the 5.5 models, and the reasoning request shape (Claude, Nova 2, Grok) each model needs. Entries apply to every inference profile of a model (`global.`, `us.`, `eu.`, `au.`, `jp.`, ...) | [Bedrock model cards](https://docs.aws.amazon.com/bedrock/latest/userguide/model-cards.html) |
| OpenAI | Context window, output ceiling and function calling for the GPT-4o, GPT-4.1, GPT-5, GPT-5.4, GPT-5.5, GPT-5.6, GPT-6 Astra and o-series models. GPT-6.1 Sol is recorded as not supporting tool calls through Chat Completions, and GPT-6 Sol and Luna leave tool use unknown because their pages restrict function calling on Chat Completions to `reasoning_effort: none` | [OpenAI model pages](https://developers.openai.com/api/docs/models) |
| Ollama | Tool use for models whose library page lists the `tools` capability. Context windows stay unknown because the effective window is the server's `num_ctx` | [Ollama library](https://ollama.com/library) |
| vLLM | Nothing. Served model names are chosen by the operator | n/a |

Models that appear to be missing a value are intentionally `Unknown`. Add the value yourself with `WithCapabilities` when you have verified it for your deployment.

OpenAI-compatible servers (Ollama, vLLM, gateways) never consult the OpenAI catalogue, because a server can serve any model under any name, including one that looks like an OpenAI model. `openai.NewCompatible` is the constructor they share.

The native structured-output column describes what can be used through the configured Gude provider, not the underlying model in isolation. This distinction is why OpenAI remains `agent.Unsupported` for `NativeStructuredOutput` until its adapter implements a native response-schema path.


## Built-ins

- [Anthropic](providers/anthropic.md)
- [Amazon Bedrock](providers/bedrock.md)
- [OpenAI and compatible endpoints](providers/openai.md)
- [Ollama](providers/ollama.md)
- [vLLM](providers/vllm.md)

The fallback provider accepts a primary plus ordered fallbacks; it changes provider only if failure occurs before any event is emitted, avoiding mixed streams.

## Implementing a provider

Map every provider request to `ModelRequest`, emit live text/thinking when available, and return authoritative aggregate text, tool calls, and `TokenUsage`. Respect context cancellation. Wrap backend failures with enough context for diagnosis but never log credentials or raw content by default. Keep provider streaming callbacks synchronous unless the implementation documents ordering and safe shutdown.
