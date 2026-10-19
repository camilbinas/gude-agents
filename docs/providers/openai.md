# OpenAI provider

```go
import "github.com/camilbinas/gude-agents/agent/provider/openai"

// Choose the model explicitly; Gude has no default model.
prov, err := openai.New("gpt-5.6",
    openai.WithMaxTokens(4096),
)
// Or a named constructor with the same model ID and metadata.
prov, err = openai.GPT5_6()
a, err := agent.New(prov, "Answer clearly.")
```

Credentials default to `OPENAI_API_KEY`; `WithAPIKey` overrides it. `WithBaseURL` targets compatible APIs. Other options configure maximum tokens and portable thinking effort for supported models. OpenAI caches prompts automatically, so there is no caching option; cached input tokens are reported as `CacheReadTokens` in `TokenUsage`.

Compatible endpoints differ in tool calling, streaming event shape, usage reporting, images, and sampling parameters. Verify those capabilities before relying on them. Prefer the dedicated [Ollama](ollama.md) or [vLLM](vllm.md) provider when its local-server behavior is required.

OpenAI embedding/vector integrations are available under `agent/rag/openai`. See [Providers](../providers.md).
