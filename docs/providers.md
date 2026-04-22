# LLM Providers

gude-agents ships with four built-in LLM providers: AWS Bedrock, Anthropic, OpenAI, and Google Gemini. It also supports local model servers (Ollama, vLLM) via the OpenAI provider's compatible endpoint constructors. Each provider implements the `Provider` interface, so they're interchangeable — swap one for another without changing your agent code.

| Provider | Import | Details |
|----------|--------|---------|
| AWS Bedrock | `agent/provider/bedrock` | [Bedrock docs](providers/bedrock.md) |
| Anthropic | `agent/provider/anthropic` | [Anthropic docs](providers/anthropic.md) |
| OpenAI | `agent/provider/openai` | [OpenAI docs](providers/openai.md) |
| Google Gemini | `agent/provider/gemini` | [Gemini docs](providers/gemini.md) |
| Ollama (local) | `agent/provider/ollama` | [Ollama docs](providers/ollama.md) |
| vLLM (local) | `agent/provider/vllm` | [vLLM docs](providers/vllm.md) |

## Quick Start

```go
bedrock.GlobalClaudeSonnet4_6()    // AWS Bedrock (uses AWS credential chain)
anthropic.ClaudeSonnet4_6()  // Anthropic API (uses ANTHROPIC_API_KEY)
openai.GPT4_1()              // OpenAI API (uses OPENAI_API_KEY)
gemini.Gemini25Flash()       // Gemini API (uses GEMINI_API_KEY)
ollama.New("qwen2.5")        // Local Ollama server (uses OLLAMA_HOST)
vllm.New("mistral")          // Local vLLM server (uses VLLM_BASE_URL)
```

Each provider has `Cheapest()`, `Standard()`, `Smartest()` tier aliases. These mappings change over time as better models become available — pin a specific constructor (e.g., `GlobalClaudeSonnet4_6()`) if you need a stable model across upgrades.

## Provider Interface

Every provider implements these two methods:

```go
type Provider interface {
    Converse(ctx context.Context, params ConverseParams) (*ProviderResponse, error)
    ConverseStream(ctx context.Context, params ConverseParams, cb StreamCallback) (*ProviderResponse, error)
}
```

`Converse` sends messages and returns a complete response. `ConverseStream` does the same but delivers text chunks incrementally through the `StreamCallback`. Both return a `ProviderResponse` containing the text, any tool calls, and token usage.

You don't call these methods directly — the agent loop handles that. But if you're building a custom provider, this is the interface to implement.

## CapabilityReporter Interface

Providers can optionally implement `CapabilityReporter` to advertise what their model supports:

```go
type CapabilityReporter interface {
    Capabilities() Capabilities
}

type Capabilities struct {
    ToolUse    bool // model supports tool/function calling
    ToolChoice bool // model supports tool choice modes (auto, any, specific)
    TokenUsage bool // model reports token usage in responses
}
```

All four built-in providers implement this interface. The agent constructor uses it to log warnings — for example, if you register tools with a model that doesn't support tool use, or set a token budget with a model that doesn't report usage.

## ModelIdentifier Interface

Providers can optionally implement `ModelIdentifier` to expose the underlying model ID:

```go
type ModelIdentifier interface {
    ModelID() string
}
```

All four built-in providers implement this interface. Useful for logging, routing, and debugging:

```go
if mi, ok := provider.(agent.ModelIdentifier); ok {
    fmt.Println("Using model:", mi.ModelID())
}
```

## Extended Thinking

Extended thinking lets models reason internally before producing a final answer. The reasoning process is separate from the response text and can be streamed in real-time via `WithThinkingCallback`.

### Enabling Thinking

All providers use the same option pattern:

```go
import pvdr "github.com/camilbinas/gude-agents/agent/provider"

// Anthropic
provider, _ := anthropic.New("claude-sonnet-4-6",
    anthropic.WithThinking(pvdr.ThinkingHigh),
    anthropic.WithMaxTokens(16000),
)

// Bedrock (Claude or Nova 2 Lite)
provider, _ := bedrock.GlobalClaudeSonnet4_6(bedrock.WithThinking(pvdr.ThinkingMedium))

// OpenAI (o-series models)
provider, _ := openai.O4Mini(openai.WithThinking(pvdr.ThinkingHigh))
```

### Effort Levels

| Constant | Value | Anthropic/Bedrock Claude | Bedrock Nova 2 | OpenAI |
|---|---|---|---|---|
| `pvdr.ThinkingLow` | `"low"` | 2 048 token budget | `low` effort | `low` effort |
| `pvdr.ThinkingMedium` | `"medium"` | 8 192 token budget | `medium` effort | `medium` effort |
| `pvdr.ThinkingHigh` | `"high"` | 16 384 token budget | `high` effort | `high` effort |

### Supported Models

| Provider | Supported models |
|---|---|
| Anthropic | All Claude 4-series (`claude-haiku-4-5`, `claude-sonnet-4-*`, `claude-opus-4-*`) |
| Bedrock | Same Claude 4-series via Bedrock + `Nova2Lite` |
| OpenAI | `o3`, `o3-mini`, `o4-mini` and other o-series reasoning models |

`WithThinking` is silently ignored for models that don't support it.

### Streaming Thinking Output

Use `WithThinkingCallback` on the agent to receive reasoning chunks in real-time:

```go
a, _ := agent.Default(provider, instructions, nil,
    agent.WithThinkingCallback(func(chunk string) {
        fmt.Print(chunk) // stream reasoning to the user
    }),
)
```

The thinking callback fires before the answer streams. OpenAI does not expose reasoning tokens in the stream, so the callback never fires for OpenAI providers.

See `examples/thinking` for a complete working example.

## Direct SDK Access

Every built-in provider exposes a `Client()` method that returns the underlying SDK client. Use this when you need provider-specific features not available through the `agent.Provider` interface — for example, Bedrock guardrail configs, Anthropic metadata, or OpenAI response formats.

```go
// Bedrock — returns *bedrockruntime.Client
provider, _ := bedrock.GlobalClaudeSonnet4_6()
bedrockClient := provider.Client()

// Anthropic — returns *anthropicsdk.Client
provider, _ := anthropic.ClaudeSonnet4_6()
anthropicClient := provider.Client()

// OpenAI — returns *openaisdk.Client
provider, _ := openai.GPT4_1()
openaiClient := provider.Client()

// Gemini — returns *genai.Client
provider, _ := gemini.Gemini25Flash()
geminiClient := provider.Client()
```

This lets you share a single set of credentials and configuration between the agent loop and direct SDK calls:

```go
provider, _ := bedrock.GlobalClaudeSonnet4_6()

// Normal agent usage for most calls
a, _ := agent.Default(provider, instructions, tools)
result, _, _ := a.Invoke(ctx, "normal question")

// Drop to raw SDK for provider-specific features
resp, err := provider.Client().Converse(ctx, &bedrockruntime.ConverseInput{
    ModelId:         aws.String("global.anthropic.claude-sonnet-4-6"),
    Messages:        myMessages,
    GuardrailConfig: &types.GuardrailConfiguration{
        GuardrailIdentifier: aws.String("my-guardrail"),
        GuardrailVersion:    aws.String("1"),
    },
})
```

Note: direct SDK calls bypass the agent loop entirely — no memory, tools, guardrails, middleware, or tracing. Use this as an escape hatch, not the default path.

## Implementing a Custom Provider

To create your own provider, implement the `Provider` interface:

```go
type Provider interface {
    Converse(ctx context.Context, params ConverseParams) (*ProviderResponse, error)
    ConverseStream(ctx context.Context, params ConverseParams, cb StreamCallback) (*ProviderResponse, error)
}
```

`ConverseParams` contains the messages, system prompt, tool configuration, and tool choice. `ProviderResponse` contains the text response, tool calls, and token usage. See [Message Types](message-types.md) for full type definitions.

Optionally implement `CapabilityReporter` to let the agent know what your model supports:

```go
func (p *MyProvider) Capabilities() agent.Capabilities {
    return agent.Capabilities{
        ToolUse:    true,
        ToolChoice: true,
        TokenUsage: true,
    }
}
```

## Provider Registry

The `registry` package provides environment-driven provider selection. Instead of hardcoding a specific provider, you register factories and select at runtime by name and tier.

Import: `github.com/camilbinas/gude-agents/agent/provider/registry`

### Setup

Call `RegisterBuiltins()` once at startup to register all built-in providers:

```go
import "github.com/camilbinas/gude-agents/agent/provider/registry"

func init() {
    registry.RegisterBuiltins() // registers bedrock, anthropic, openai, gemini, ollama, vllm
}
```

### Creating a Provider by Name and Tier

```go
provider, err := registry.New("bedrock", registry.Standard)
```

Available tiers: `registry.Cheapest`, `registry.Standard`, `registry.Smartest`. These map to each provider's tier aliases.

### Environment-Driven Selection

`FromEnv` reads `MODEL_PROVIDER` and `MODEL_TIER` from the environment:

```go
provider, err := registry.FromEnv()
// MODEL_PROVIDER=anthropic MODEL_TIER=smartest → anthropic.Smartest()
```

Defaults to `bedrock` / `standard` when the variables are unset.

### Custom Providers

Register your own provider with factories for each tier:

```go
registry.Register("my-provider",
    func() (agent.Provider, error) { return myProvider("cheap") },
    func() (agent.Provider, error) { return myProvider("standard") },
    func() (agent.Provider, error) { return myProvider("smart") },
)
```

Pass `nil` for any tier your provider doesn't support.

## See Also

- [Bedrock Provider](providers/bedrock.md) — AWS Bedrock configuration, models, guardrails
- [Anthropic Provider](providers/anthropic.md) — Anthropic API configuration and models
- [OpenAI Provider](providers/openai.md) — OpenAI and compatible endpoints
- [Gemini Provider](providers/gemini.md) — Google Gemini configuration and models
- [Ollama Provider](providers/ollama.md) — local Ollama model server
- [vLLM Provider](providers/vllm.md) — local vLLM model server
- [Getting Started](getting-started.md) — installation and first agent
- [Fallback Provider](fallback-provider.md) — automatic failover across providers
- [Agent API Reference](agent-api.md) — full list of options and methods
- [Message Types](message-types.md) — `ConverseParams`, `ProviderResponse`, `StreamCallback`
- [RAG Pipeline](rag.md) — embedder implementations in `agent/rag/bedrock`, `agent/rag/openai`, and `agent/rag/gemini`
