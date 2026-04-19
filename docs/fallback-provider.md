# Fallback Provider

The `fallback` package wraps multiple providers into a single chain. When a call fails, the next provider in the chain is tried automatically. The agent has no knowledge of the switch — it just sees a `Provider`.

## When to Use It

- Primary provider is down or rate-limited
- You want Anthropic as the default with Bedrock as a backup
- You're testing with a fake provider that always fails
- You need multi-region or multi-vendor redundancy

## Constructor

```go
func New(primary agent.Provider, fallbacks ...agent.Provider) *Provider
```

`primary` is tried first. Each provider in `fallbacks` is tried in order if the previous one returns an error. The first successful response is returned. If every provider fails, the last error is returned wrapped in `"all providers failed: ..."`.

## Basic Usage

```go
import (
    "github.com/camilbinas/gude-agents/agent/provider/anthropic"
    "github.com/camilbinas/gude-agents/agent/provider/bedrock"
    "github.com/camilbinas/gude-agents/agent/provider/fallback"
)

primary, err := anthropic.ClaudeSonnet4_6()
backup, err  := bedrock.ClaudeSonnet4_6()

provider := fallback.New(primary, backup)

a, err := agent.Default(provider, prompt.Text("You are a helpful assistant."), nil)
```

Pass `provider` to any agent constructor — `agent.Default`, `agent.Worker`, `agent.Orchestrator`, etc. The fallback logic is fully transparent.

## Chaining More Than Two Providers

You can pass any number of fallbacks:

```go
provider := fallback.New(anthropicProvider, bedrockProvider, openaiProvider)
```

Providers are tried left to right. The first success wins.

## Capabilities

`fallback.Provider` implements `CapabilityReporter`. It returns the **intersection** of capabilities across all providers in the chain — a capability is only advertised if every provider supports it.

```go
caps := provider.Capabilities()
// caps.ToolUse    — true only if all providers support tool use
// caps.ToolChoice — true only if all providers support tool choice
// caps.TokenUsage — true only if all providers report token usage
```

If any provider in the chain does not implement `CapabilityReporter`, the fallback provider conservatively returns all-false capabilities.

This matters when mixing providers with different capabilities — for example, pairing a full-featured Claude provider with an OpenAI GPT-OSS model on Bedrock (which doesn't support tool use). The agent will correctly detect that tools aren't universally available.

## Testing with a Fake Provider

A common pattern is to use a fake provider that always fails to verify fallback behavior:

```go
type alwaysDown struct{}

func (alwaysDown) Converse(_ context.Context, _ agent.ConverseParams) (*agent.ProviderResponse, error) {
    return nil, fmt.Errorf("service unavailable")
}

func (alwaysDown) ConverseStream(_ context.Context, _ agent.ConverseParams, _ agent.StreamCallback) (*agent.ProviderResponse, error) {
    return nil, fmt.Errorf("service unavailable")
}

backup, _ := bedrock.ClaudeSonnet4_6()
provider  := fallback.New(alwaysDown{}, backup)
// Every call goes to Bedrock.
```

See `examples/fallback-provider` for a runnable version of this pattern.

## Limitations and Behavioral Notes

### Mid-stream failure produces duplicate output

`ConverseStream` tries each provider in order. If the primary fails **after** it has already delivered some text chunks to the callback, the fallback starts a **fresh request from the beginning**. The caller will receive the partial output from the failed provider followed by the full output from the fallback — effectively seeing the response twice (or a garbled mix).

If this is a concern, use `Invoke` (non-streaming) instead of `InvokeStream` — a failed `Converse` call produces no output, so the fallback is completely transparent.

### Token budget is not enforced across the failed attempt

`WithTokenBudget` tracks cumulative token usage across iterations of the agent loop. If the primary provider fails mid-call and doesn't return token usage (which is typical for error responses), those tokens are not counted. The fallback then starts with the same budget counter as if the primary never ran.

In practice this means the budget is slightly under-enforced when a provider failure occurs mid-invocation. For strict budget enforcement, use `Invoke` rather than `InvokeStream`, and accept that a failed primary attempt may consume tokens that aren't accounted for.

### Structured output requires ToolChoice support from all providers

`InvokeStructured` uses forced tool choice (`tool.ChoiceTool`) to guarantee a JSON response. If any provider in the fallback chain doesn't support `ToolChoice`, the structured output call may fail or return unstructured text on that provider.

The fallback provider's `Capabilities()` returns the intersection across all providers, so the agent will log a warning at construction time if `ToolChoice` is not universally supported — but only if every provider implements `CapabilityReporter`. If a provider doesn't implement that interface, the fallback conservatively reports all-false capabilities, which will trigger the warning regardless.
## Error Behavior

- If the primary succeeds, no fallback is attempted.
- If the primary fails, the error is recorded and the next provider is tried immediately.
- If all providers fail, the returned error is: `all providers failed: provider[N]: <last error>`.
- There is no retry delay or jitter — fallback is instantaneous.



## See Also

- [LLM Providers](providers.md) — Bedrock, Anthropic, and OpenAI provider docs
- [Implementing a Custom Provider](providers.md#implementing-a-custom-provider) — the `Provider` interface
- [Agent API Reference](agent-api.md) — agent constructors and options
- [Structured Output](structured-output.md) — `InvokeStructured` and tool choice
