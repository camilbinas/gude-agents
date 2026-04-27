# Multi-Agent Composition

Multi-agent composition lets you build an orchestrator agent that delegates work to specialist child agents. Each child agent is wrapped as a regular tool using `AgentAsTool`, so the parent can invoke it the same way it invokes any other tool — by sending a message and getting a text response back.

This pattern is useful when different tasks require different system prompts, tools, or even different LLM providers. The orchestrator decides which specialist to call (and can call several in parallel), then synthesizes their responses into a single answer.

## AgentAsTool

```go
func AgentAsTool(name, description string, child *Agent) tool.Tool
```

`AgentAsTool` wraps a child `*Agent` as a `tool.Tool` that a parent agent can invoke. The returned tool has a single `message` input parameter — the parent sends a natural-language message, and the child agent processes it through its own agent loop (system prompt, tools, iterations) and returns the result as text.

- `name` — the tool name the parent LLM sees (e.g., `"ask_researcher"`). Use a descriptive verb-noun pattern so the LLM knows when to route to this agent.
- `description` — natural language explanation of what the child agent does. The parent LLM reads this to decide which specialist to call.
- `child` — a fully configured `*Agent` (created via `agent.Worker`, `agent.Default`, etc.) with its own provider, instructions, and tools.

The generated tool schema looks like:

```json
{
  "type": "object",
  "properties": {
    "message": {
      "type": "string",
      "description": "The message to send to the sub-agent"
    }
  },
  "required": ["message"]
}
```

Internally, `AgentAsTool` calls `child.InvokeStream` with the message, collects the streamed chunks into a single string, and returns it as the tool result.

## Orchestrator Pattern

The orchestrator pattern uses a parent agent that has no domain-specific tools of its own — its only tools are `AgentAsTool` wrappers pointing to specialist children. The parent's system prompt describes the available specialists and when to use each one.

```
┌─────────────────────────────────┐
│         Orchestrator            │
│  (routes questions to children) │
│                                 │
│  Tools:                         │
│   - ask_researcher              │
│   - ask_analyst                 │
│   - ask_writer                  │
└──────┬──────────┬───────────────┘
       │          │
       ▼          ▼
┌────────────┐ ┌────────────┐
│ Researcher │ │  Analyst   │
│  (Worker)  │ │  (Worker)  │
│  own tools │ │  own tools │
└────────────┘ └────────────┘
```

Key design points:

- **Use `Orchestrator` for the parent** — `agent.Orchestrator` enables parallel tool execution, so the parent can call multiple children simultaneously when a question spans domains.
- **Use `Worker` for children** — `agent.Worker` uses fewer iterations (3 vs 5) and is optimized for focused, single-task execution.
- **Different providers per agent** — children can use cheaper/faster models (e.g., Haiku) while the orchestrator uses a more capable model (e.g., Sonnet) for reasoning and synthesis.
- **Describe specialists clearly** — the orchestrator's system prompt should list each specialist by tool name and explain what it handles. The LLM uses this to route correctly.

## Child Error Handling

When a child agent encounters an error during execution, `AgentAsTool` propagates it as a Go error. This means:

- The error is returned from the tool handler, and the agent loop handles it according to its normal error handling behavior.
- The parent agent's `Invoke`/`InvokeStream` call may fail if the child error is not recoverable.

```go
// Inside AgentAsTool (simplified):
result, err := child.InvokeStream(ctx, message, callback)
if err != nil {
    return "", fmt.Errorf("child agent %q: %w", name, err) // error propagated
}
return result, nil
```

## Code Example

This example builds a two-agent system: an orchestrator that routes questions to a research specialist.

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/camilbinas/gude-agents/agent"
    "github.com/camilbinas/gude-agents/agent/prompt"
    "github.com/camilbinas/gude-agents/agent/provider/bedrock"
    "github.com/camilbinas/gude-agents/agent/tool"
)

// SearchInput defines the parameters for the project search tool.
type SearchInput struct {
    Query string `json:"query" description:"Search term for projects" required:"true"`
}

func main() {
    // Use a fast model for the child agent.
    haiku, err := bedrock.Cheapest()
    if err != nil {
        log.Fatal(err)
    }

    // Use a more capable model for the orchestrator.
    sonnet, err := bedrock.Standard()
    if err != nil {
        log.Fatal(err)
    }

    // --- Child: project researcher ---
    searchTool := tool.New("search_projects",
        "Search for projects by name or keyword.",
        func(ctx context.Context, in SearchInput) (string, error) {
            // In a real app, query a database here.
            return fmt.Sprintf("Found project: %s — status: active, deadline: 2025-03-01", in.Query), nil
        },
    )

    researcher, err := agent.Worker(
        haiku,
        prompt.Text("You are a project researcher. Use the search tool to find project details and summarize them clearly."),
        []tool.Tool{searchTool},
    )
    if err != nil {
        log.Fatal(err)
    }

    // --- Orchestrator ---
    orchestrator, err := agent.Orchestrator(
        sonnet,
        prompt.Text(`You are a helpful assistant. You have one specialist you can consult:
- ask_researcher: project details, statuses, and deadlines

Route the user's question to the right specialist and synthesize the response.`),
        []tool.Tool{
            agent.AgentAsTool("ask_researcher",
                "Ask the project researcher about project details, statuses, and deadlines.",
                researcher),
        },
    )
    if err != nil {
        log.Fatal(err)
    }

    result, _, err := orchestrator.Invoke(context.Background(), "What's the status of the Atlas project?")
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println(result)
}
```

When the user asks "What's the status of the Atlas project?", the orchestrator:

1. Reads its system prompt and sees `ask_researcher` handles project details.
2. Calls the `ask_researcher` tool with a message like "Look up the Atlas project status."
3. The researcher child agent runs its own loop — calls `search_projects`, gets results, and formulates a response.
4. The orchestrator receives the researcher's response as a tool result and synthesizes the final answer.

## Scaling to Multiple Specialists

The pattern extends naturally. Add more children by creating additional `AgentAsTool` wrappers:

```go
specialistTools := []tool.Tool{
    agent.AgentAsTool("ask_researcher",
        "Ask about project details, statuses, and deadlines.",
        researcherAgent),
    agent.AgentAsTool("ask_analyst",
        "Ask about revenue, forecasts, and financial summaries.",
        financeAgent),
    agent.AgentAsTool("ask_people",
        "Ask about team assignments and workload.",
        peopleAgent),
}

orchestrator, err := agent.Orchestrator(sonnet, instructions, specialistTools)
```

Because `Orchestrator` enables parallel tool execution, the parent can call multiple specialists simultaneously when a question spans domains (e.g., "What's the Atlas project status and how much revenue has it generated?").

## See Also

- [Getting Started](getting-started.md) — `Default`, `Worker`, and `Orchestrator` preset descriptions
- [Tool System](tools.md) — `tool.NewRaw` (used internally by `AgentAsTool`) and tool schema reference
- [Providers](providers.md) — configuring different providers for parent and child agents
- [Prompt System](prompts.md) — `RISEN` and `COSTAR` frameworks for agent instructions
