# OpenAI provider

```go
import "github.com/camilbinas/gude-agents/agent/provider/openai"

prov, err := openai.New("gpt-4o-mini",
    openai.WithMaxTokens(4096),
)
a, err := agent.New(prov, "Answer clearly.")
```

Credentials default to `OPENAI_API_KEY`; `WithAPIKey` overrides it. `WithBaseURL` targets compatible APIs. Other options configure maximum tokens, portable thinking effort for supported models, and system-prompt caching usage reporting.

Compatible endpoints differ in tool calling, streaming event shape, usage reporting, images, and sampling parameters. Verify those capabilities before relying on them. Prefer the dedicated [Ollama](ollama.md) or [vLLM](vllm.md) provider when its local-server behavior is required.

OpenAI embedding/vector integrations are available under `agent/rag/openai`. See [Providers](../providers.md).
