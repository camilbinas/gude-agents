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
func WithThinking(effort pvdr.ThinkingEffort) Option
```

Enables extended thinking at the given effort level. Use the shared constants from the `provider` package:

```go
import pvdr "github.com/camilbinas/gude-agents/agent/provider"

provider, _ := bedrock.Standard(bedrock.WithThinking(pvdr.ThinkingHigh))
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
provider, _ := bedrock.GlobalClaudeSonnet4_6(
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

Each function returns a `(*BedrockProvider, error)` and accepts `...Option`.

Bedrock uses [cross-region inference profiles](https://docs.aws.amazon.com/bedrock/latest/userguide/inference-profiles-support.html) to route requests across AWS Regions for higher throughput. The prefix in the model ID determines the routing scope:

- **`global.`** — routes to any commercial AWS Region worldwide. Best throughput, widest availability.
- **`eu.`** — routes only within EU Regions. Use when data must stay in Europe.
- **`us.`** — routes only within US Regions. Use when data must stay in the US.
- No prefix (e.g. `amazon.nova-micro-v1:0`) — on-demand, single-region. Runs in whichever region your AWS credentials target.

### Anthropic Claude (Global)

| Function | Model ID |
|---|---|
| `GlobalClaudeHaiku4_5()` | `global.anthropic.claude-haiku-4-5-20251001-v1:0` |
| `GlobalClaudeSonnet4_5()` | `global.anthropic.claude-sonnet-4-5-20250929-v1:0` |
| `GlobalClaudeSonnet4_6()` | `global.anthropic.claude-sonnet-4-6` |
| `GlobalClaudeOpus4_5()` | `global.anthropic.claude-opus-4-5-20251101-v1:0` |
| `GlobalClaudeOpus4_6()` | `global.anthropic.claude-opus-4-6-v1` |
| `GlobalClaudeOpus4_7()` | `global.anthropic.claude-opus-4-7` |
| `GlobalClaudeOpus4_8()` | `global.anthropic.claude-opus-4-8` |
| `GlobalClaudeSonnet5()` | `global.anthropic.claude-sonnet-5` |
| `GlobalClaudeOpus5()` | `global.anthropic.claude-opus-5` |
| `GlobalClaudeFable5()` | `global.anthropic.claude-fable-5` |

Claude Fable 5 needs the account's data retention mode set to `provider_data_share` via the Data Retention API before it can be invoked. It also ships blocking classifiers for dual-use cybersecurity and biology content; blocked requests return success with a `refusal` stop reason rather than an error.

### Anthropic Claude (US)

| Function | Model ID |
|---|---|
| `US_ClaudeHaiku4_5()` | `us.anthropic.claude-haiku-4-5-20251001-v1:0` |
| `US_ClaudeSonnet4_5()` | `us.anthropic.claude-sonnet-4-5-20250929-v1:0` |
| `US_ClaudeSonnet4_6()` | `us.anthropic.claude-sonnet-4-6` |
| `US_ClaudeOpus4_5()` | `us.anthropic.claude-opus-4-5-20251101-v1:0` |
| `US_ClaudeOpus4_6()` | `us.anthropic.claude-opus-4-6-v1` |
| `US_ClaudeOpus4_7()` | `us.anthropic.claude-opus-4-7` |
| `US_ClaudeOpus4_8()` | `us.anthropic.claude-opus-4-8` |
| `US_ClaudeSonnet5()` | `us.anthropic.claude-sonnet-5` |
| `US_ClaudeOpus5()` | `us.anthropic.claude-opus-5` |
| `US_ClaudeFable5()` | `us.anthropic.claude-fable-5` |

### Anthropic Claude (EU)

| Function | Model ID |
|---|---|
| `EU_ClaudeHaiku4_5()` | `eu.anthropic.claude-haiku-4-5-20251001-v1:0` |
| `EU_ClaudeSonnet4_5()` | `eu.anthropic.claude-sonnet-4-5-20250929-v1:0` |
| `EU_ClaudeSonnet4_6()` | `eu.anthropic.claude-sonnet-4-6` |
| `EU_ClaudeOpus4_5()` | `eu.anthropic.claude-opus-4-5-20251101-v1:0` |
| `EU_ClaudeOpus4_6()` | `eu.anthropic.claude-opus-4-6-v1` |
| `EU_ClaudeOpus4_7()` | `eu.anthropic.claude-opus-4-7` |
| `EU_ClaudeOpus4_8()` | `eu.anthropic.claude-opus-4-8` |
| `EU_ClaudeSonnet5()` | `eu.anthropic.claude-sonnet-5` |
| `EU_ClaudeOpus5()` | `eu.anthropic.claude-opus-5` |

Claude Fable 5 has no EU geo route. Use `GlobalClaudeFable5()` or `US_ClaudeFable5()`.

### Amazon Nova (Global)

| Function | Model ID |
|---|---|
| `GlobalNova2Lite()` | `global.amazon.nova-2-lite-v1:0` |

### Amazon Nova (US)

| Function | Model ID |
|---|---|
| `US_Nova2Lite()` | `us.amazon.nova-2-lite-v1:0` |

### Amazon Nova (EU)

| Function | Model ID |
|---|---|
| `EU_NovaMicro()` | `eu.amazon.nova-micro-v1:0` |
| `EU_NovaLite()` | `eu.amazon.nova-lite-v1:0` |
| `EU_NovaPro()` | `eu.amazon.nova-pro-v1:0` |
| `EU_Nova2Lite()` | `eu.amazon.nova-2-lite-v1:0` |

### Amazon Nova (On-demand)

| Function | Model ID |
|---|---|
| `NovaMicro()` | `amazon.nova-micro-v1:0` |
| `NovaLite()` | `amazon.nova-lite-v1:0` |
| `NovaPro()` | `amazon.nova-pro-v1:0` |

### Qwen (on-demand)

| Function | Model ID |
|---|---|
| `Qwen3_235B()` | `qwen.qwen3-235b-a22b-2507-v1:0` |
| `Qwen3_32B()` | `qwen.qwen3-32b-v1:0` |
| `Qwen3Coder30B()` | `qwen.qwen3-coder-30b-a3b-v1:0` |
| `Qwen3CoderNext()` | `qwen.qwen3-coder-next` |
| `Qwen3Next80B()` | `qwen.qwen3-next-80b-a3b` |

### DeepSeek (on-demand)

| Function | Model ID |
|---|---|
| `DeepSeekV3_2()` | `deepseek.v3.2` |

### Moonshot AI (on-demand)

| Function | Model ID |
|---|---|
| `KimiK2_5()` | `moonshotai.kimi-k2.5` |
| `KimiK2Thinking()` | `moonshot.kimi-k2-thinking` |

The prefix differs between these two (`moonshotai.` vs `moonshot.`). That matches the AWS model cards.

### Mistral AI (on-demand)

| Function | Model ID |
|---|---|
| `MistralLarge3()` | `mistral.mistral-large-3-675b-instruct` |
| `Devstral2_123B()` | `mistral.devstral-2-123b` |

### MiniMax (on-demand)

| Function | Model ID |
|---|---|
| `MiniMaxM2_1()` | `minimax.minimax-m2.1` |
| `MiniMaxM2_5()` | `minimax.minimax-m2.5` |

### OpenAI GPT-OSS (on-demand)

| Function | Model ID |
|---|---|
| `GPT_OSS_20B()` | `openai.gpt-oss-20b-1:0` |
| `GPT_OSS_120B()` | `openai.gpt-oss-120b-1:0` |

### Nvidia (on-demand)

| Function | Model ID |
|---|---|
| `NemotronNano2_9B()` | `nvidia.nemotron-nano-9b-v2` |
| `NemotronNano2_12B()` | `nvidia.nemotron-nano-12b-v2` |
| `NemotronNano3_35B()` | `nvidia.nemotron-nano-3-30b` |
| `NemotronSuper3_120B()` | `nvidia.nemotron-super-3-120b` |

### Z.AI (on-demand)

| Function | Model ID |
|---|---|
| `GLM4_7Flash()` | `zai.glm-4.7-flash` |
| `GLM4_7()` | `zai.glm-4.7` |
| `GLM5()` | `zai.glm-5` |

### Regional availability

On-demand models are offered in different sets per region. When a model ID is valid but not offered in the region you are calling, Bedrock returns `ValidationException: The provided model identifier is invalid.` — the same message it uses for a genuinely malformed ID. If you hit that error, check the region before assuming the ID is wrong:

```
aws bedrock list-foundation-models --region eu-central-1 --query 'modelSummaries[].modelId'
```

As of 2026-07-30, `eu-central-1` serves only `mistral.devstral-2-123b`, `zai.glm-4.7-flash` and the older Qwen 3 models from the on-demand list above. DeepSeek V3.2, Kimi, GLM 4.7 / GLM 5, Mistral Large 3, Qwen3 Coder Next and Qwen3 Next are `us-east-1` only.

### Not available through this provider

OpenAI GPT-5.5, OpenAI GPT-5.4 and xAI Grok 4.3 are in the Bedrock catalog but are reachable only via the `bedrock-mantle` endpoint — their model cards list no `bedrock-runtime` model ID. This provider speaks Converse/ConverseStream on `bedrock-runtime`, so there are no constructors for them. Use the direct `openai` provider, or a Bedrock Mantle client, instead.

Amazon Nova Premier is omitted because it is marked Legacy with an EOL of 2026-09-14.

> **Embedder functions** (`TitanEmbedV2`, `CohereEmbedEnglishV3`, `CohereEmbedMultilingualV3`, `CohereEmbedV4`) have moved to `github.com/camilbinas/gude-agents/agent/rag/bedrock`. See [RAG Pipeline](../rag.md) for usage.

> Note: OpenAI GPT-OSS models on Bedrock support text only — no tool use, tool choice, or token usage reporting. The provider's `Capabilities()` method reflects this automatically.

## Tier Aliases

> **These mappings change over time.** `Cheapest`, `Standard`, and `Smartest` point to whichever model currently best fits that tier. When a better model becomes available, the mapping is updated in a new release. Pin a specific model constructor (e.g., `GlobalClaudeSonnet4_6()`) if you need a stable model across upgrades.

| Function | Maps to | Description |
|---|---|---|
| `Cheapest()` | `GlobalClaudeHaiku4_5()` | Fastest, lowest cost |
| `Standard()` | `GlobalClaudeSonnet5()` | Best accuracy/speed/cost balance |
| `Smartest()` | `GlobalClaudeOpus5()` | Best reasoning capability |

Claude Opus 5 requires an AWS Marketplace subscription on the calling account. Without it, `Smartest()` fails with `AccessDeniedException` referencing `aws-marketplace:Subscribe`.

```go
provider, err := bedrock.Standard() // Claude Sonnet 5 (global)
```

## Code Example

```go
package main

import (
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
)

func main() {
	// Global profile — routes to any commercial AWS Region.
	provider, err := bedrock.GlobalClaudeSonnet4_6()
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

	result, err := a.Invoke(agent.Background(), "Explain goroutines in two sentences.")
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
