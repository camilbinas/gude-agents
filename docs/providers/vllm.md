# vLLM provider

```go
import "github.com/camilbinas/gude-agents/agent/provider/vllm"

prov, err := vllm.New("mistralai/Mistral-7B-Instruct-v0.2")
a, err := agent.New(prov, "Answer clearly.")
```

The provider targets `VLLM_BASE_URL` when configured. The model argument is the served Hugging Face model ID and must match the server deployment.

Validate the server's chat template, tool parser, context length, tensor parallelism, and structured-output support. Model/server combinations may accept the transport while differing in tool-call and usage semantics.

Use [Ollama](ollama.md) for an Ollama daemon or [OpenAI](openai.md) for other compatible endpoints. See [Providers](../providers.md) for the common contract.
