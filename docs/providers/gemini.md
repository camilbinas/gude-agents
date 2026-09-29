# Gemini provider

```go
import "github.com/camilbinas/gude-agents/agent/provider/gemini"

prov, err := gemini.New("gemini-2.0-flash",
    gemini.WithMaxTokens(4096),
)
a, err := agent.New(prov, "Answer clearly.")
```

Credentials default to `GEMINI_API_KEY`, then `GOOGLE_API_KEY`; `WithAPIKey` overrides them. Options include maximum tokens, portable thinking effort, explicit thinking budget, and system-prompt caching usage reporting.

Select a current model that supports the modalities and tool behavior your agent needs. Thinking and model availability vary; live thinking is represented as model/application thinking events, not answer text.

Gemini embedding support is available under `agent/rag/gemini`. See [Providers](../providers.md) and [RAG](../rag.md).
