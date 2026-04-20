# Structured Output

`InvokeStructured[T]` is a generic function that forces the LLM to return a typed JSON response conforming to a Go struct. Instead of parsing free-form text, you get a fully deserialized value of type `T` back — no manual JSON handling required.

Under the hood it uses tool forcing: it generates a JSON Schema from `T`, registers a synthetic `structured_output` tool with that schema, and tells the LLM it *must* call that tool. The LLM's tool call input is then deserialized directly into `T`.

## Function Signature

```go
func InvokeStructured[T any](ctx context.Context, a *Agent, userMessage string) (T, TokenUsage, error)
```

- `T` — any Go struct type. The function generates a JSON Schema from `T` and uses it to constrain the LLM's response.
- `ctx` — standard Go context for cancellation and deadlines.
- `a` — an existing `*Agent` (created via `agent.New`, `agent.Default`, etc.). The agent's provider and system instructions are used for the LLM call.
- `userMessage` — the prompt sent to the LLM.

Returns:
- A value of type `T` populated from the LLM's JSON response.
- `TokenUsage` with `InputTokens` and `OutputTokens` from the provider call.
- An error if the provider call fails, the LLM doesn't return the expected tool call, or JSON deserialization fails.

## How It Works

1. **Schema generation** — `tool.GenerateSchema[T]()` uses reflection to build a JSON Schema from `T`'s struct tags (`json`, `description`, `required`, `enum`). See [Tool System — Struct Tag Schema Generation](tools.md) for the full tag reference.

2. **Tool registration** — A temporary `tool.Spec` named `structured_output` is created with the generated schema as its `InputSchema`.

3. **Tool forcing** — The `ConverseParams` sent to the provider include a `tool.Choice` with `Mode: tool.ChoiceTool` and `Name: "structured_output"`. This forces the LLM to call that specific tool rather than responding with free-form text.

4. **Deserialization** — The LLM's tool call input (a `json.RawMessage`) is unmarshalled into a value of type `T` and returned.

The function makes a single provider call — it does not enter the agent loop. No actual tool handler is executed; the tool is only used as a schema constraint.

## Error Conditions

| Condition | Error message |
|-----------|--------------|
| Provider call fails | `structured output: <provider error>` |
| LLM returns no tool call | `structured output: LLM did not return a tool call to structured_output` |
| LLM calls wrong tool | `structured output: LLM called tool "<name>" instead of structured_output` |
| JSON deserialization fails | `structured output: failed to deserialize response: <parse error>` |

## Code Example

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/camilbinas/gude-agents/agent"
    "github.com/camilbinas/gude-agents/agent/prompt"
    "github.com/camilbinas/gude-agents/agent/provider/bedrock"
)

// MovieReview is the structured response we want from the LLM.
// Struct tags control the generated JSON Schema — the LLM sees
// field descriptions, required markers, and enum constraints.
type MovieReview struct {
    Title     string   `json:"title"     description:"The movie title"          required:"true"`
    Rating    int      `json:"rating"    description:"Rating from 1 to 10"      required:"true"`
    Sentiment string   `json:"sentiment" description:"Overall sentiment"        enum:"positive,negative,mixed" required:"true"`
    Summary   string   `json:"summary"   description:"One-paragraph summary"    required:"true"`
    Themes    []string `json:"themes"    description:"Key themes in the movie"`
}

func main() {
    provider, err := bedrock.Standard()
    if err != nil {
        log.Fatal(err)
    }

    a, err := agent.Default(
        provider,
        prompt.Text("You are a film critic. Analyze movies and provide structured reviews."),
        nil, // no tools needed — InvokeStructured handles tool setup internally
    )
    if err != nil {
        log.Fatal(err)
    }

    review, usage, err := agent.InvokeStructured[MovieReview](
        context.Background(), a, "Review the movie Inception (2010).",
    )
    if err != nil {
        log.Fatal(err)
    }

    fmt.Printf("Title:     %s\n", review.Title)
    fmt.Printf("Rating:    %d/10\n", review.Rating)
    fmt.Printf("Sentiment: %s\n", review.Sentiment)
    fmt.Printf("Summary:   %s\n", review.Summary)
    fmt.Printf("Themes:    %v\n", review.Themes)
    fmt.Printf("Tokens:    %d in, %d out\n", usage.InputTokens, usage.OutputTokens)
}
```

The generated JSON Schema for `MovieReview` looks like:

```json
{
  "type": "object",
  "properties": {
    "title":     { "type": "string",  "description": "The movie title" },
    "rating":    { "type": "integer", "description": "Rating from 1 to 10" },
    "sentiment": { "type": "string",  "description": "Overall sentiment", "enum": ["positive", "negative", "mixed"] },
    "summary":   { "type": "string",  "description": "One-paragraph summary" },
    "themes":    { "type": "array",   "description": "Key themes in the movie", "items": { "type": "string" } }
  },
  "required": ["title", "rating", "sentiment", "summary"]
}
```

## See Also

- [Tool System](tools.md) — `tool.GenerateSchema[T]` and struct tag reference
- [Agent API Reference](agent-api.md) — `agent.New`, `agent.Default`, and `TokenUsage`
- [Providers](providers.md) — configuring the LLM provider used by `InvokeStructured`
