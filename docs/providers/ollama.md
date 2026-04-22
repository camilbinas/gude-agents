# Ollama Provider

The `ollama` package provides a provider for local [Ollama](https://ollama.com) model servers. It delegates to the OpenAI provider since Ollama exposes an OpenAI-compatible Chat Completions API.

Import: `github.com/camilbinas/gude-agents/agent/provider/ollama`

## Constructor

```go
func New(model string, opts ...Option) (*OllamaProvider, error)
```

Creates a provider targeting a local Ollama server. The `model` parameter is the Ollama model name (e.g. `"llama3.2"`, `"qwen2.5"`, `"mistral"`).

The server address is read from the `OLLAMA_HOST` environment variable, defaulting to `http://localhost:11434`. The `/v1` path is appended automatically.

## Options

### `WithBaseURL`

```go
var WithBaseURL = openai.WithBaseURL
```

Overrides the Ollama server URL. Takes precedence over `OLLAMA_HOST`.

### `WithMaxTokens`

```go
var WithMaxTokens = openai.WithMaxTokens
```

Sets the maximum number of tokens the model can generate in a response.

## Helper Functions

### `Must`

```go
func Must(p *OllamaProvider, err error) *OllamaProvider
```

Wraps a call to `New` and panics on error. Useful for examples and scripts.

## Environment Variables

| Variable | Description | Default |
|---|---|---|
| `OLLAMA_HOST` | Ollama server address (without `/v1`) | `http://localhost:11434` |

## Tier Aliases

| Function | Model | Description |
|---|---|---|
| `Cheapest()` | `qwen2.5:3b` | Fast, small, decent tool calling |
| `Standard()` | `qwen2.5:7b` | Best tool calling at 7B |
| `Smartest()` | `qwen2.5:32b` | Best quality with tool support |

```go
provider, err := ollama.Standard() // qwen2.5:7b
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
	"github.com/camilbinas/gude-agents/agent/provider/ollama"
)

func main() {
	provider, err := ollama.New("qwen2.5")
	if err != nil {
		log.Fatal(err)
	}

	a, err := agent.Default(
		provider,
		prompt.Text("You are a helpful assistant. Be concise."),
		nil,
	)
	if err != nil {
		log.Fatal(err)
	}

	result, _, err := a.Invoke(context.Background(), "What is the capital of France?")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result)
}
```

## Tool Calling Support

Tool calling support varies by model. These models handle it reliably:

- `qwen2.5` — best tool calling support among open models
- `llama3.2` — good tool calling, fast on small instances
- `mistral-nemo` — solid tool calling
- `mistral` — basic tool calling

Older or smaller models may ignore tool specs or hallucinate the format. Test with a simple single-tool agent first.

## See Also

- [LLM Providers Overview](../providers.md) — interfaces, registry, custom providers
- [OpenAI Provider](openai.md) — the underlying provider implementation
- [vLLM Provider](vllm.md) — alternative local model server
