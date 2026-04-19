# Bedrock Provider

The `bedrock` package uses the AWS Bedrock ConverseStream / Converse APIs. It supports Claude, Amazon Nova, Qwen, MiniMax, OpenAI GPT-OSS, and other models available on Bedrock.

Import: `github.com/camilbinas/gude-agents/agent/provider/bedrock`

## Constructor

```go
func New(model string, opts ...Option) (*BedrockProvider, error)
```

Creates a provider for any Bedrock model ID. Loads AWS credentials from the default credential chain (environment variables, `~/.aws/credentials`, IAM roles, EC2 instance profiles, ECS task roles, etc.).

## Options

### `WithRegion`

```go
func WithRegion(region string) Option
```

Sets a custom AWS region. If not specified, falls back to the `AWS_REGION` environment variable, then defaults to `us-east-1`.

### `WithMaxTokens`

```go
func WithMaxTokens(n int64) Option
```

Sets the maximum number of tokens the model can generate in a response.

### `WithThinking`

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

Only supported on Claude 4-series models and Nova 2 Lite. Silently ignored for other models. See [Extended Thinking](../providers.md#extended-thinking) for details.

### `WithAPIKey`

```go
func WithAPIKey(key string) Option
```

Sets an Amazon Bedrock API key (bearer token) for authentication. This is an alternative to IAM credentials — useful for quick setup and exploratory use. If not set, the provider also checks the `AWS_BEARER_TOKEN_BEDROCK` environment variable. When neither is provided, the provider falls back to the standard AWS credential chain.

```go
provider, _ := bedrock.Standard(
    bedrock.WithAPIKey("your-bedrock-api-key"),
)
```

Or set it via environment variable:

```bash
export AWS_BEARER_TOKEN_BEDROCK=your-bedrock-api-key
```

### `WithGuardrail`

```go
func WithGuardrail(id, version string) Option
```

Enables an Amazon Bedrock Guardrail on every Converse and ConverseStream call. The guardrail is a managed resource created in the AWS console or via the Bedrock API — this option references it by ID. Use `"DRAFT"` as the version to test with the latest unpublished draft.

```go
provider, _ := bedrock.ClaudeSonnet4_6(
    bedrock.WithGuardrail("my-guardrail-id", "1"),
)
```

Guardrails can include content filters, denied topics, word filters, PII detection, and contextual grounding checks. They are applied to both input and output. See the [AWS Bedrock Guardrails documentation](https://docs.aws.amazon.com/bedrock/latest/userguide/guardrails.html) for setup instructions.

## AWS Credentials

Bedrock supports two authentication methods:

**1. API Key (Bearer Token)**

The simplest way to get started. Set the `AWS_BEARER_TOKEN_BEDROCK` environment variable or pass `bedrock.WithAPIKey("...")` directly. When an API key is provided, IAM credentials are not required — the bearer token is the sole authentication mechanism.

**2. IAM Credentials (Default)**

Bedrock uses the standard AWS SDK credential chain. Any method that works for the AWS Go SDK v2 works here:

- Environment variables: `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`
- Shared credentials file: `~/.aws/credentials`
- IAM roles (EC2, ECS, Lambda)
- SSO / AWS CLI profiles

If no API key is provided, credentials are resolved automatically through this chain.

## Model Constructors

Each function returns a `(*BedrockProvider, error)` and accepts `...Option`:

**Anthropic Claude (EU cross-region inference)**

| Function | Model ID |
|---|---|
| `ClaudeHaiku4_5()` | `eu.anthropic.claude-haiku-4-5-20251001-v1:0` |
| `ClaudeSonnet4_5()` | `eu.anthropic.claude-sonnet-4-5-20250929-v1:0` |
| `ClaudeSonnet4_6()` | `eu.anthropic.claude-sonnet-4-6` |
| `ClaudeOpus4_5()` | `eu.anthropic.claude-opus-4-5-20251101-v1:0` |
| `ClaudeOpus4_6()` | `eu.anthropic.claude-opus-4-6-v1` |
| `ClaudeOpus4_7()` | `eu.anthropic.claude-opus-4-7` |

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

> **Embedder functions** (`TitanEmbedV2`, `CohereEmbedEnglishV3`, `CohereEmbedMultilingualV3`, `CohereEmbedV4`) have moved to `github.com/camilbinas/gude-agents/agent/rag/bedrock`. See [RAG Pipeline](../rag.md) for usage.

> Note: OpenAI GPT-OSS models on Bedrock support text only — no tool use, tool choice, or token usage reporting. The provider's `Capabilities()` method reflects this automatically.

## Tier Aliases

> **These mappings change over time.** `Cheapest`, `Standard`, and `Smartest` point to whichever model currently best fits that tier. When a better model becomes available, the mapping is updated in a new release. Pin a specific model constructor (e.g., `ClaudeSonnet4_6()`) if you need a stable model across upgrades.

Convenience shortcuts that map to the Amazon Nova family:

| Function | Model | Description |
|---|---|---|
| `Cheapest()` | Nova Micro | Fastest, lowest cost, text-only |
| `Standard()` | Nova Pro | Best accuracy/speed/cost balance |
| `Smartest()` | Nova 2 Lite | Newer generation, better reasoning |

```go
provider, err := bedrock.Standard() // Nova Pro
```

## Code Example

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

## See Also

- [LLM Providers Overview](../providers.md) — interfaces, extended thinking, direct SDK access, custom providers
- [Fallback Provider](../fallback-provider.md) — automatic failover across providers
- [RAG Pipeline](../rag.md) — Bedrock embedder implementations
