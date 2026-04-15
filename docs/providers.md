# LLM Providers

gude-agents ships with three built-in LLM providers: AWS Bedrock, Anthropic, and OpenAI. Each provider implements the `Provider` interface, so they're interchangeable — swap one for another without changing your agent code.

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

All three built-in providers implement this interface. The agent constructor uses it to log warnings — for example, if you register tools with a model that doesn't support tool use, or set a token budget with a model that doesn't report usage.

## ModelIdentifier Interface

Providers can optionally implement `ModelIdentifier` to expose the underlying model ID:

```go
type ModelIdentifier interface {
    ModelId() string
}
```

All three built-in providers implement this interface. Useful for logging, routing, and debugging:

```go
if mi, ok := provider.(agent.ModelIdentifier); ok {
    fmt.Println("Using model:", mi.ModelId())
}
```

## Bedrock

The `bedrock` package uses the AWS Bedrock ConverseStream / Converse APIs. It supports Claude, Amazon Nova, Qwen, MiniMax, OpenAI GPT-OSS, and other models available on Bedrock.

### Constructor

```go
func New(model string, opts ...Option) (*BedrockProvider, error)
```

Creates a provider for any Bedrock model ID. Loads AWS credentials from the default credential chain (environment variables, `~/.aws/credentials`, IAM roles, EC2 instance profiles, ECS task roles, etc.).

### Options

#### `WithRegion`

```go
func WithRegion(region string) Option
```

Sets a custom AWS region. If not specified, falls back to the `AWS_REGION` environment variable, then defaults to `us-east-1`.

#### `WithMaxTokens`

```go
func WithMaxTokens(n int64) Option
```

Sets the maximum number of tokens the model can generate in a response.

#### `WithThinking`

```go
func WithThinking(effort string) Option
```

Enables extended thinking at the given effort level. Use the shared constants from the `provider` package:

```go
import pvdr "github.com/camilbinas/gude-agents/agent/provider"

provider, _ := bedrock.ClaudeSonnet4_6(bedrock.WithThinking(pvdr.ThinkingHigh))
```

| Constant | Value | Token budget |
|---|---|---|
| `pvdr.ThinkingLow` | `"low"` | 2 048 |
| `pvdr.ThinkingMedium` | `"medium"` | 8 192 |
| `pvdr.ThinkingHigh` | `"high"` | 16 384 |

Only supported on Claude 4-series models and Nova 2 Lite. Silently ignored for other models. See [Extended Thinking](#extended-thinking) for details.

### AWS Credentials

Bedrock uses the standard AWS SDK credential chain. Any method that works for the AWS Go SDK v2 works here:

- Environment variables: `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`
- Shared credentials file: `~/.aws/credentials`
- IAM roles (EC2, ECS, Lambda)
- SSO / AWS CLI profiles

No API key option is needed — credentials are resolved automatically.

### Convenience Model Functions

Each function returns a `(*BedrockProvider, error)` and accepts `...Option`:

**Anthropic Claude (EU cross-region inference)**

| Function | Model ID |
|---|---|
| `ClaudeHaiku4_5()` | `eu.anthropic.claude-haiku-4-5-20251001-v1:0` |
| `ClaudeSonnet4_5()` | `eu.anthropic.claude-sonnet-4-5-20250929-v1:0` |
| `ClaudeSonnet4_6()` | `eu.anthropic.claude-sonnet-4-6` |
| `ClaudeOpus4_5()` | `eu.anthropic.claude-opus-4-5-20251101-v1:0` |
| `ClaudeOpus4_6()` | `eu.anthropic.claude-opus-4-6-v1` |

**Amazon Nova (EU cross-region inference)**

| Function | Model ID |
|---|---|
| `NovaMicro()` | `eu.amazon.nova-micro-v1:0` |
| `NovaLite()` | `eu.amazon.nova-lite-v1:0` |
| `Nova2Lite()` | `eu.amazon.nova-2-lite-v1:0` |
| `NovaPro()` | `eu.amazon.nova-pro-v1:0` |

**Qwen (on-demand)**

| Function | Model ID |
|---|---|
| `Qwen3_235B()` | `qwen.qwen3-235b-a22b-2507-v1:0` |
| `Qwen3_32B()` | `qwen.qwen3-32b-v1:0` |
| `Qwen3Coder30B()` | `qwen.qwen3-coder-30b-a3b-v1:0` |

**MiniMax (on-demand)**

| Function | Model ID |
|---|---|
| `MiniMaxM2_5()` | `minimax.minimax-m2.5` |
| `MiniMaxM2_1()` | `minimax.minimax-m2.1` |

**OpenAI GPT-OSS (on-demand)**

| Function | Model ID |
|---|---|
| `GPT_OSS_120B()` | `openai.gpt-oss-120b-1:0` |
| `GPT_OSS_20B()` | `openai.gpt-oss-20b-1:0` |

**Other**

| Function | Model ID |
|---|---|
| `NemotronSuper120B()` | `nvidia.nemotron-super-3-120b` |
| `GLM4_7Flash()` | `zai.glm-4.7-flash` |

> **Embedder functions** (`TitanEmbedV2`, `CohereEmbedEnglishV3`, `CohereEmbedMultilingualV3`, `CohereEmbedV4`) have moved to `github.com/camilbinas/gude-agents/agent/rag/bedrock`. See [RAG Pipeline](rag.md) for usage.

> Note: OpenAI GPT-OSS models on Bedrock support text only — no tool use, tool choice, or token usage reporting. The provider's `Capabilities()` method reflects this automatically.

### Tier Aliases

Convenience shortcuts that map to the Amazon Nova family:

| Function | Model | Description |
|---|---|---|
| `Cheapest()` | Nova Micro | Fastest, lowest cost, text-only |
| `Standard()` | Nova Pro | Best accuracy/speed/cost balance |
| `Smartest()` | Nova 2 Lite | Newer generation, better reasoning |

```go
provider, err := bedrock.Standard() // Nova Pro
```

### Bedrock Code Example

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
)

func main() {
	// Use a convenience function — credentials come from the AWS credential chain.
	provider, err := bedrock.ClaudeSonnet4_6()
	if err != nil {
		log.Fatal(err)
	}

	a, err := agent.Default(
		provider,
		prompt.Text("You are a helpful assistant."),
		nil,
	)
	if err != nil {
		log.Fatal(err)
	}

	result, _, err := a.Invoke(context.Background(), "Explain goroutines in two sentences.")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result)
}
```

## Anthropic

The `anthropic` package uses the Anthropic Messages API via the official `anthropic-sdk-go`.

### Constructor

```go
func New(model string, opts ...Option) (*AnthropicProvider, error)
```

Creates a provider for any Anthropic model name. By default, reads the API key from the `ANTHROPIC_API_KEY` environment variable.

### Options

#### `WithAPIKey`

```go
func WithAPIKey(key string) Option
```

Sets the Anthropic API key programmatically. If not set, the SDK reads from the `ANTHROPIC_API_KEY` environment variable.

#### `WithMaxTokens`

```go
func WithMaxTokens(n int64) Option
```

Sets the maximum number of tokens the model can generate in a response.

#### `WithThinking`

```go
func WithThinking(effort string) Option
```

Enables extended thinking at the given effort level. Internally maps the effort level to a token budget sent to the Anthropic API. Use the shared constants from the `provider` package:

```go
import pvdr "github.com/camilbinas/gude-agents/agent/provider"

provider, _ := anthropic.New("claude-sonnet-4-6",
    anthropic.WithThinking(pvdr.ThinkingHigh),
    anthropic.WithMaxTokens(16000), // must exceed the thinking budget
)
```

See [Extended Thinking](#extended-thinking) for details.

### Convenience Model Functions (the API key comes from the environment):

| Function | Model ID |
|---|---|
| `ClaudeHaiku4_5()` | `claude-haiku-4-5` |
| `ClaudeSonnet4_5()` | `claude-sonnet-4-5` |
| `ClaudeSonnet4_6()` | `claude-sonnet-4-6` |
| `ClaudeOpus4_5()` | `claude-opus-4-5` |
| `ClaudeOpus4_6()` | `claude-opus-4-6` |

> Note: The Anthropic convenience functions don't accept options. To customize the API key or max tokens, use `anthropic.New(model, anthropic.WithAPIKey("..."))` directly.

### Tier Aliases

| Function | Model | Description |
|---|---|---|
| `Cheapest()` | Claude Haiku 4.5 | Fastest model with near-frontier intelligence |
| `Standard()` | Claude Sonnet 4.6 | Best combination of speed and intelligence |
| `Smartest()` | Claude Opus 4.6 | Most intelligent model for agents and coding |

```go
provider, err := anthropic.Standard() // Claude Sonnet 4.6
```

### Anthropic Code Example

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/anthropic"
)

func main() {
	// Uses ANTHROPIC_API_KEY from the environment.
	provider, err := anthropic.ClaudeSonnet4_6()
	if err != nil {
		log.Fatal(err)
	}

	a, err := agent.Default(
		provider,
		prompt.Text("You are a helpful assistant."),
		nil,
	)
	if err != nil {
		log.Fatal(err)
	}

	result, _, err := a.Invoke(context.Background(), "What are Go interfaces?")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result)
}
```

## OpenAI

The `openai` package uses the OpenAI Chat Completions API via the official `openai-go` SDK. It also works with any OpenAI-compatible endpoint (e.g., local models, Azure OpenAI) via `WithBaseURL`.

### Constructor

```go
func New(model string, opts ...Option) (*OpenAIProvider, error)
```

Creates a provider for any OpenAI model name. By default, reads the API key from the `OPENAI_API_KEY` environment variable.

### Options

#### `WithAPIKey`

```go
func WithAPIKey(key string) Option
```

Sets the OpenAI API key programmatically. If not set, the SDK reads from the `OPENAI_API_KEY` environment variable.

#### `WithBaseURL`

```go
func WithBaseURL(url string) Option
```

Sets a custom base URL for OpenAI-compatible endpoints. Use this to point at Azure OpenAI, local model servers, or any API that implements the OpenAI Chat Completions format.

#### `WithMaxTokens`

```go
func WithMaxTokens(n int64) Option
```

Sets the maximum number of tokens the model can generate in a response.

#### `WithThinking`

```go
func WithThinking(effort string) Option
```

Sets the reasoning effort for o-series and reasoning-capable models. Mapped to OpenAI's `reasoning_effort` parameter. Use the shared constants from the `provider` package:

```go
import pvdr "github.com/camilbinas/gude-agents/agent/provider"

provider, _ := openai.O4Mini(openai.WithThinking(pvdr.ThinkingHigh))
```

See [Extended Thinking](#extended-thinking) for details.

### Convenience Model Functions

**GPT Models**

| Function | Model ID |
|---|---|
| `GPT4o()` | `gpt-4o` |
| `GPT4oMini()` | `gpt-4o-mini` |
| `GPT4_1()` | `gpt-4.1` |
| `GPT4_1Mini()` | `gpt-4.1-mini` |
| `GPT4_1Nano()` | `gpt-4.1-nano` |
| `GPT5()` | `gpt-5` |
| `GPT5Mini()` | `gpt-5-mini` |
| `GPT5Nano()` | `gpt-5-nano` |

**Reasoning Models**

| Function | Model ID |
|---|---|
| `O3()` | `o3` |
| `O3Mini()` | `o3-mini` |
| `O4Mini()` | `o4-mini` |

> **Embedder functions** (`EmbeddingSmall`, `EmbeddingLarge`) have moved to `github.com/camilbinas/gude-agents/agent/rag/openai`. See [RAG Pipeline](rag.md) for usage.

### Tier Aliases

All three map to the GPT-5 family:

| Function | Model | Description |
|---|---|---|
| `Cheapest()` | GPT-5 Nano | Fastest, lowest cost |
| `Standard()` | GPT-5 Mini | Balanced |
| `Smartest()` | GPT-5 | Flagship, most capable |

```go
provider, err := openai.Standard() // GPT-5 Mini
```

### OpenAI Code Example

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/openai"
)

func main() {
	// Uses OPENAI_API_KEY from the environment.
	provider, err := openai.GPT4_1(
		openai.WithMaxTokens(2048),
	)
	if err != nil {
		log.Fatal(err)
	}

	a, err := agent.Default(
		provider,
		prompt.Text("You are a helpful assistant."),
		nil,
	)
	if err != nil {
		log.Fatal(err)
	}

	result, _, err := a.Invoke(context.Background(), "Explain channels in Go.")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result)
}
```

## Extended Thinking

Extended thinking lets models reason internally before producing a final answer. The reasoning process is separate from the response text and can be streamed in real-time via `WithThinkingCallback`.

### Enabling Thinking

All three providers use the same option:

```go
import pvdr "github.com/camilbinas/gude-agents/agent/provider"

// Anthropic
provider, _ := anthropic.New("claude-sonnet-4-6",
    anthropic.WithThinking(pvdr.ThinkingHigh),
    anthropic.WithMaxTokens(16000),
)

// Bedrock (Claude or Nova 2 Lite)
provider, _ := bedrock.ClaudeSonnet4_6(bedrock.WithThinking(pvdr.ThinkingMedium))

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

## See Also

- [Getting Started](getting-started.md) — installation and first agent
- [Fallback Provider](fallback-provider.md) — automatic failover across providers
- [Agent API Reference](agent-api.md) — full list of options and methods
- [Message Types](message-types.md) — `ConverseParams`, `ProviderResponse`, `StreamCallback`
- [RAG Pipeline](rag.md) — embedder implementations in `agent/rag/bedrock` and `agent/rag/openai`
