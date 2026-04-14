# Prompt System

The prompt system provides structured ways to define agent instructions. Every agent needs a system prompt — the `Instructions` interface lets you pass anything from a plain string to a full prompt framework like RISEN or COSTAR.

## Instructions Interface

```go
type Instructions interface {
    String() string
}
```

`Instructions` is the interface for anything that can produce a system prompt string. All prompt types in the `prompt` package implement this interface, and you can implement it yourself for custom prompt builders.

The agent constructors (`agent.New`, `agent.Default`, `agent.Worker`, `agent.Orchestrator`) all accept an `Instructions` value as their second parameter.

## prompt.Text

```go
type Text string
```

`Text` is the simplest prompt type — a plain string that implements `Instructions`. Use it when your system prompt is a single block of text and you don't need structured sections.

```go
func (t Text) String() string
```

`String()` returns the underlying string value.

## prompt.RISEN

```go
type RISEN struct {
    Role         string
    Instructions string
    Steps        string
    EndGoal      string
    Narrowing    string
}
```

`RISEN` builds a system prompt using the RISEN framework: Role, Instructions, Steps, End goal, Narrowing. Each non-empty field is rendered as a labeled section in the output string.

| Field          | Purpose                                                    |
|----------------|------------------------------------------------------------|
| `Role`         | Who the agent is (persona or job title)                    |
| `Instructions` | What the agent should do                                   |
| `Steps`        | Ordered steps the agent should follow                      |
| `EndGoal`      | The desired outcome of the interaction                     |
| `Narrowing`    | Constraints, scope limits, or guardrails on the response   |

```go
func (r RISEN) String() string
```

`String()` concatenates the non-empty fields with labels like `Role:`, `Instructions:`, `Steps:`, `End goal:`, and `Narrowing:`, separated by newlines.

## prompt.COSTAR

```go
type COSTAR struct {
    Context   string
    Objective string
    Style     string
    Tone      string
    Audience  string
    Response  string
}
```

`COSTAR` builds a system prompt using the CO-STAR framework: Context, Objective, Style, Tone, Audience, Response format. It's well-suited for user-facing assistants where tone and audience matter.

| Field       | Purpose                                                  |
|-------------|----------------------------------------------------------|
| `Context`   | Background information or situational context            |
| `Objective` | The primary goal the agent should accomplish             |
| `Style`     | Writing style (e.g., structured, conversational, formal) |
| `Tone`      | Emotional tone (e.g., friendly, professional, empathetic)|
| `Audience`  | Who the agent is talking to                              |
| `Response`  | Output format constraints (length, structure, etc.)      |

```go
func (c COSTAR) String() string
```

`String()` concatenates the non-empty fields with labels like `Context:`, `Objective:`, `Style:`, `Tone:`, `Audience:`, and `Response format:`, separated by newlines.

## Code Examples

### Text

A minimal agent with a plain-text system prompt:

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

func main() {
	provider, err := bedrock.ClaudeSonnet4_6()
	if err != nil {
		log.Fatal(err)
	}

	a, err := agent.Default(
		provider,
		prompt.Text("You are a helpful assistant that answers questions concisely."),
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

### RISEN

A task-focused travel planning agent using the RISEN framework:

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

func main() {
	provider, err := bedrock.ClaudeSonnet4_6()
	if err != nil {
		log.Fatal(err)
	}

	a, err := agent.Default(
		provider,
		prompt.RISEN{
			Role:         "You are a travel planning assistant.",
			Instructions: "Help users plan trips by suggesting destinations, activities, and logistics.",
			Steps:        "1) Ask about preferences. 2) Suggest destinations. 3) Outline a day-by-day itinerary.",
			EndGoal:      "Provide a practical, ready-to-use travel plan.",
			Narrowing:    "Focus on Europe. Budget-friendly options. Keep it under 7 days.",
		},
		nil,
	)
	if err != nil {
		log.Fatal(err)
	}

	result, _, err := a.Invoke(context.Background(), "I want a short trip from Munich.")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result)
}
```

### COSTAR

A customer support assistant using the CO-STAR framework for tone control:

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

func main() {
	provider, err := bedrock.ClaudeSonnet4_6()
	if err != nil {
		log.Fatal(err)
	}

	a, err := agent.Default(
		provider,
		prompt.COSTAR{
			Context:   "You are a customer support assistant for a SaaS product.",
			Objective: "Help users troubleshoot issues and find answers quickly.",
			Style:     "Clear and structured. Use numbered steps for instructions.",
			Tone:      "Friendly and patient. Never blame the user.",
			Audience:  "Non-technical users who may be frustrated.",
			Response:  "Keep answers under 3 paragraphs. Use bullet points for lists.",
		},
		nil,
	)
	if err != nil {
		log.Fatal(err)
	}

	result, _, err := a.Invoke(context.Background(), "I can't log in to my account.")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result)
}
```

## See Also

- [Agent API Reference](agent-api.md) — agent constructors that accept `Instructions`
- [Getting Started](getting-started.md) — `Default`, `Worker`, and `Orchestrator` preset constructors
- [Structured Output](structured-output.md) — combining prompts with typed responses
