# vLLM Provider

The `vllm` package provides a provider for [vLLM](https://docs.vllm.ai) model servers. It delegates to the OpenAI provider since vLLM exposes an OpenAI-compatible Chat Completions API.

Import: `github.com/camilbinas/gude-agents/agent/provider/vllm`

## Constructor

```go
func New(model string, opts ...Option) (*VLLMProvider, error)
```

Creates a provider targeting a vLLM server. The `model` parameter is the HuggingFace model ID (e.g. `"mistralai/Mistral-7B-Instruct-v0.2"`).

The server address is read from the `VLLM_BASE_URL` environment variable, defaulting to `http://localhost:8000/v1`.

## Options

### `WithBaseURL`

```go
var WithBaseURL = openai.WithBaseURL
```

Overrides the vLLM server URL. Takes precedence over `VLLM_BASE_URL`.

### `WithMaxTokens`

```go
var WithMaxTokens = openai.WithMaxTokens
```

Sets the maximum number of tokens the model can generate in a response.

## Helper Functions

### `Must`

```go
func Must(p *VLLMProvider, err error) *VLLMProvider
```

Wraps a call to `New` and panics on error. Useful for examples and scripts.

## Environment Variables

| Variable | Description | Default |
|---|---|---|
| `VLLM_BASE_URL` | vLLM server address (including `/v1`) | `http://localhost:8000/v1` |

## Code Example

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/vllm"
)

func main() {
	provider, err := vllm.New("mistralai/Mistral-7B-Instruct-v0.2")
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

## vLLM vs Ollama

vLLM is better suited for production deployments:

- **Request batching** — handles concurrent requests efficiently
- **Higher throughput** — optimized for serving, not just inference
- **PagedAttention** — better GPU memory utilization

Ollama is better for local development — simpler setup, built-in model management.

## See Also

- [LLM Providers Overview](../providers.md) — interfaces, registry, custom providers
- [OpenAI Provider](openai.md) — the underlying provider implementation
- [Ollama Provider](ollama.md) — alternative local model server
