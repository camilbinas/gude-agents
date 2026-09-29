# Anthropic provider

```go
import "github.com/camilbinas/gude-agents/agent/provider/anthropic"

prov, err := anthropic.New("claude-sonnet-4-20250514",
    anthropic.WithMaxTokens(4096),
)
a, err := agent.New(prov, "Answer clearly.")
```

Credentials default to `ANTHROPIC_API_KEY`; `WithAPIKey` overrides it. Options include maximum tokens, portable thinking effort, explicit thinking budget, and system-prompt caching. Thinking events flow through `Agent.Stream`; final provider metadata remains in `Result.Metadata`.

Choose a currently available model ID for your account and region. Tool support, image/document limits, thinking, caching, and token pricing differ by model; keep these as deployment configuration rather than hard-coding them into reusable agent logic.

See [Providers](../providers.md) for the shared request/response contract.
