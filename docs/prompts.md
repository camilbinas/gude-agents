# Prompt System

The `prompt` package provides structured ways to define agent system prompts. All types implement the `Instructions` interface and can be passed as the second argument to any agent constructor.

## Instructions Interface

```go
type Instructions interface {
    String() string
}
```

Implement this for custom prompt builders. The built-in types below all satisfy it.

## prompt.Text

```go
type Text string
```

Plain string prompt. Use when you don't need structured sections.

## prompt.RISEN

```go
type RISEN struct {
    Role         string
    Instructions string
    Steps        []string
    EndGoal      string
    Narrowing    string
}
```

| Field          | Purpose                                                  |
|----------------|----------------------------------------------------------|
| `Role`         | Who the agent is                                         |
| `Instructions` | What the agent should do                                 |
| `Steps`        | Ordered steps to follow (rendered as a numbered list)    |
| `EndGoal`      | Desired outcome                                          |
| `Narrowing`    | Constraints or guardrails                                |

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

| Field       | Purpose                                    |
|-------------|--------------------------------------------|
| `Context`   | Background or situational context          |
| `Objective` | Primary goal                               |
| `Style`     | Writing style                              |
| `Tone`      | Emotional tone                             |
| `Audience`  | Who the agent is talking to                |
| `Response`  | Output format constraints                  |

## prompt.APE

```go
type APE struct {
    Action      string
    Purpose     string
    Expectation string
}
```

| Field         | Purpose                              |
|---------------|--------------------------------------|
| `Action`      | What the agent should do             |
| `Purpose`     | Why (the business goal)              |
| `Expectation` | What good output looks like          |

## prompt.TRACE

```go
type TRACE struct {
    Task    string
    Request string
    Action  string
    Context string
    Example string
}
```

| Field     | Purpose                                          |
|-----------|--------------------------------------------------|
| `Task`    | The agent's role or identity                     |
| `Request` | What the agent is being asked to do              |
| `Action`  | How to approach the task                         |
| `Context` | Domain background                                |
| `Example` | Concrete input/output example                    |

## Example

```go
a, _ := agent.Default(provider,
    prompt.RISEN{
        Role:         "You are a travel planning assistant.",
        Instructions: "Help users plan trips by suggesting destinations and logistics.",
        Steps:        []string{"Ask about preferences", "Suggest destinations", "Outline itinerary"},
        EndGoal:      "Provide a practical, ready-to-use travel plan.",
        Narrowing:    "Focus on Europe. Budget-friendly. Under 7 days.",
    },
    nil,
)
```

## Rendering

All types render non-empty fields as labeled sections separated by newlines. Empty/nil fields are omitted. The `String()` method on each type produces the final system prompt string.

## See Also

- [Agent API Reference](agent-api.md) — agent constructors that accept `Instructions`
- [Getting Started](getting-started.md) — `Default`, `Worker`, and `Orchestrator` preset constructors
