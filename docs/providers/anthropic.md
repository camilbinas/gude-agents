# Anthropic provider

```go
import "github.com/camilbinas/gude-agents/agent/provider/anthropic"

// Choose the model explicitly; Gude has no default model.
prov, err := anthropic.ClaudeSonnet5_5(
    anthropic.WithMaxTokens(4096),
)
// The same model by ID; known IDs get the same capability metadata.
prov, err = anthropic.New("claude-sonnet-5-5")
a, err := agent.New(prov, "Answer clearly.")
```

Credentials default to `ANTHROPIC_API_KEY`; `WithAPIKey` overrides it. Options include maximum tokens, portable thinking effort, explicit thinking budget, and `WithPromptCaching()`, which marks the system prompt and document blocks for Anthropic prompt caching. Cache read and write token counts are reported in `TokenUsage` whether or not caching is enabled. Thinking events flow through `Agent.Stream`; final provider metadata remains in `Result.Metadata`.

Choose a currently available model ID for your account and region. Tool support, image/document limits, thinking, caching, and token pricing differ by model; keep these as deployment configuration rather than hard-coding them into reusable agent logic.

See [Providers](../providers.md) for the shared request/response contract.

## Extended thinking and streaming

Thinking is provider/model configuration, not a separate application shape. Import the shared provider type as `pvdr`, enable it on a compatible Claude model, leave enough answer headroom in `max_tokens`, and consume `EventThinking` from the normal stream:

```go
import pvdr "github.com/camilbinas/gude-agents/agent/provider"

// ...
prov := anthropic.Must(anthropic.New(
    "claude-sonnet-4-6",
    anthropic.WithMaxTokens(32_000),
    anthropic.WithThinking(pvdr.ThinkingLow),
))

a, err := agent.New(prov, "Work carefully.")
for event, err := range a.Stream(agent.Background(), "Solve the problem") {
    if err != nil { return err }
    switch event.Type {
    case agent.EventThinking:
        fmt.Print(event.Thinking.Content)
    case agent.EventText:
        fmt.Print(event.Text.Content)
    }
}
```

Claude thinking budget is added to `max_tokens`; choose values that stay under the selected model's output ceiling. Treat thinking output as sensitive diagnostic material and do not expose or persist it unless that is an explicit product decision.
