# Anthropic Provider

The `anthropic` package uses the Anthropic Messages API via the official `anthropic-sdk-go`.

Import: `github.com/camilbinas/gude-agents/agent/provider/anthropic`

## Constructor

```go
func New(model string, opts ...Option) (*AnthropicProvider, error)
```

Creates a provider for any Anthropic model name. By default, reads the API key from the `ANTHROPIC_API_KEY` environment variable.

## Options

### `WithAPIKey`

```go
func WithAPIKey(key string) Option
```

Sets the Anthropic API key programmatically. If not set, the SDK reads from the `ANTHROPIC_API_KEY` environment variable.

### `WithMaxTokens`

```go
func WithMaxTokens(n int64) Option
```

Sets the maximum number of tokens the model can generate in a response.

### `WithThinking`

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

See [Extended Thinking](../providers.md#extended-thinking) for details.

## Model Constructors

The API key comes from the environment. All convenience functions accept `...Option`:

| Function | Model ID |
|---|---|
| `ClaudeHaiku4_5()` | `claude-haiku-4-5` |
| `ClaudeSonnet4_5()` | `claude-sonnet-4-5` |
| `ClaudeSonnet4_6()` | `claude-sonnet-4-6` |
| `ClaudeOpus4_5()` | `claude-opus-4-5` |
| `ClaudeOpus4_6()` | `claude-opus-4-6` |

```go
anthropic.ClaudeSonnet4_6(anthropic.WithAPIKey("..."), anthropic.WithMaxTokens(8000))
```

## Tier Aliases

| Function | Model | Description |
|---|---|---|
| `Cheapest()` | Claude Haiku 4.5 | Fastest model with near-frontier intelligence |
| `Standard()` | Claude Sonnet 4.6 | Best combination of speed and intelligence |
| `Smartest()` | Claude Opus 4.6 | Most intelligent model for agents and coding |

```go
provider, err := anthropic.Standard() // Claude Sonnet 4.6
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

## See Also

- [LLM Providers Overview](../providers.md) — interfaces, extended thinking, direct SDK access, custom providers
- [Fallback Provider](../fallback-provider.md) — automatic failover across providers
