# OpenAI Provider

The `openai` package uses the OpenAI Chat Completions API via the official `openai-go` SDK. It also works with any OpenAI-compatible endpoint (e.g., local models, Azure OpenAI) via `WithBaseURL`.

Import: `github.com/camilbinas/gude-agents/agent/provider/openai`

## Constructor

```go
func New(model string, opts ...Option) (*OpenAIProvider, error)
```

Creates a provider for any OpenAI model name. By default, reads the API key from the `OPENAI_API_KEY` environment variable.

## Options

### `WithAPIKey`

```go
func WithAPIKey(key string) Option
```

Sets the OpenAI API key programmatically. If not set, the SDK reads from the `OPENAI_API_KEY` environment variable.

### `WithBaseURL`

```go
func WithBaseURL(url string) Option
```

Sets a custom base URL for OpenAI-compatible endpoints. Use this to point at Azure OpenAI, local model servers, or any API that implements the OpenAI Chat Completions format.

### `WithMaxTokens`

```go
func WithMaxTokens(n int64) Option
```

Sets the maximum number of tokens the model can generate in a response.

### `WithThinking`

```go
func WithThinking(effort string) Option
```

Sets the reasoning effort for o-series and reasoning-capable models. Mapped to OpenAI's `reasoning_effort` parameter. Use the shared constants from the `provider` package:

```go
import pvdr "github.com/camilbinas/gude-agents/agent/provider"

provider, _ := openai.O4Mini(openai.WithThinking(pvdr.ThinkingHigh))
```

See [Extended Thinking](../providers.md#extended-thinking) for details.

## Model Constructors

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

> **Embedder functions** (`EmbeddingSmall`, `EmbeddingLarge`) have moved to `github.com/camilbinas/gude-agents/agent/rag/openai`. See [RAG Pipeline](../rag.md) for usage.

## Tier Aliases

All three map to the GPT-5 family:

| Function | Model | Description |
|---|---|---|
| `Cheapest()` | GPT-5 Nano | Fastest, lowest cost |
| `Standard()` | GPT-5 Mini | Balanced |
| `Smartest()` | GPT-5 | Flagship, most capable |

```go
provider, err := openai.Standard() // GPT-5 Mini
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

## See Also

- [LLM Providers Overview](../providers.md) — interfaces, extended thinking, direct SDK access, custom providers
- [Fallback Provider](../fallback-provider.md) — automatic failover across providers
- [RAG Pipeline](../rag.md) — OpenAI embedder implementations
