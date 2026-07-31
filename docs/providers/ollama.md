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
| `Cheapest()` | `qwen3.5:2b` | Fast, small, decent tool calling |
| `Standard()` | `qwen3.5:9b` | Balanced tool calling |
| `Smartest()` | `qwen3.6:27b` | Best quality with tool support |

These moved from Qwen 2.5 to Qwen 3.5/3.6. Pull the tag before first use — an unpulled tag fails at call time, it is not downloaded automatically.

```go
provider, err := ollama.Standard() // qwen3.5:9b
```

## Code Example

```go
package main

import (
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

	result, err := a.Invoke(agent.Background(), "What is the capital of France?")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result)
}
```

## Tool Calling Support

Tool calling support varies by model. Current generation:

- `qwen3.6` (`Qwen36`) — strongest agentic and coding performance in the Qwen line
- `qwen3.5` (`Qwen35`) — multimodal, broad size range
- `qwen3-coder` (`Qwen3Coder`) — long-context coding and agentic tasks
- `gpt-oss` (`GPTOSS`, `GPTOSS_20B`, `GPTOSS_120B`) — open-weight OpenAI models

Previous generation, still available:

- `qwen2.5` — reliable tool calling
- `llama3.2` — good tool calling, fast on small instances
- `mistral-nemo` — solid tool calling
- `mistral` — basic tool calling

`gemma4` (`Gemma4`) is exposed but tool calling has been reported broken on some Ollama releases, where the parser drops tool calls while streaming. Verify against your runtime version before using it for agent workloads.

Older or smaller models may ignore tool specs or hallucinate the format. Test with a simple single-tool agent first.

## See Also

- [LLM Providers Overview](../providers.md) — interfaces, registry, custom providers
- [OpenAI Provider](openai.md) — the underlying provider implementation
- [vLLM Provider](vllm.md) — alternative local model server
