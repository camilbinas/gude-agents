# Ollama provider

```go
import "github.com/camilbinas/gude-agents/agent/provider/ollama"

prov, err := ollama.New("qwen2.5")
a, err := agent.New(prov, "Answer clearly.")
```

The provider connects to `OLLAMA_HOST` when set, otherwise the local default. Ensure the model is pulled before invoking it.

Local model capability varies substantially. Confirm chat formatting, tool calling, context size, structured JSON, and resource requirements for the selected model. Keep request cancellation/deadlines on `agent.Context` so disconnected clients stop local generation.

Use [vLLM](vllm.md) for a vLLM server or [OpenAI](openai.md) for another compatible endpoint. See [Providers](../providers.md) for the common contract.
