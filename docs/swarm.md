# Agent Swarms

A swarm is a group of agents that can hand off conversations to each other. Unlike the orchestrator pattern (where a parent agent delegates to children), swarm agents are peers — any agent can transfer control to any other agent at any time.

This is useful when different agents own different domains and the conversation might flow between them naturally. Think customer support: a triage agent routes to billing or technical support, and those specialists can route back if the question changes scope.

## How It Works

```
┌──────────┐  transfer_to_billing  ┌──────────┐
│  Triage  │ ────────────────────► │ Billing  │
│          │ ◄──────────────────── │          │
└──────────┘  transfer_to_triage   └──────────┘
      │
      │ transfer_to_technical
      ▼
┌──────────┐
│Technical │
│          │
└──────────┘
```

1. You define agents as `SwarmMember`s with a name, description, and `*Agent`.
2. `NewSwarm` automatically injects `transfer_to_<name>` tools into each agent for every other member.
3. When you call `swarm.Run()`, the first member starts as the active agent.
4. If the active agent calls a `transfer_to_<name>` tool, the swarm transfers the conversation (with full context) to that agent.
5. The new agent continues the conversation until it either responds or hands off again.

## Creating a Swarm

```go
triage, _ := agent.SwarmAgent(provider, triagePrompt, nil)
billing, _ := agent.SwarmAgent(provider, billingPrompt, billingTools)
tech, _ := agent.SwarmAgent(provider, techPrompt, techTools)

swarm, err := agent.NewSwarm([]agent.SwarmMember{
    {Name: "triage", Description: "Routes to the right specialist", Agent: triage},
    {Name: "billing", Description: "Handles payments and invoices", Agent: billing},
    {Name: "technical", Description: "Handles bugs and how-to questions", Agent: tech},
},
    agent.WithSwarmMaxHandoffs(5),
    agent.WithSwarmLogger(log.Default()),
)
```

The first member in the slice is the initial active agent.

## SwarmAgent Preset

`agent.SwarmAgent` is the recommended preset for swarm members. It enables logging and sets 5 max iterations. You don't need to add handoff tools manually — `NewSwarm` injects them.

```go
func SwarmAgent(provider Provider, instructions prompt.Instructions, tools []tool.Tool, opts ...Option) (*Agent, error)
```

You can also use `agent.Default`, `agent.Worker`, or `agent.New` directly — any `*Agent` works as a swarm member.

## Running the Swarm

```go
// Streaming
result, err := swarm.Run(ctx, "I need a refund", func(chunk string) {
    fmt.Print(chunk)
})

// Buffered
result, err := swarm.Invoke(ctx, "I need a refund")
```

`SwarmResult` contains:
- `Response` — the final text output
- `FinalAgent` — which agent produced the response
- `Usage` — cumulative token usage across all agents
- `HandoffHistory` — ordered list of `{From, To}` transfers

## Handoff Mechanics

When an agent calls `transfer_to_<name>`, it provides a `summary` parameter explaining why it's handing off:

```json
{
  "summary": "User is asking about a refund — routing to billing specialist"
}
```

The swarm then:
1. Stops the current agent's loop
2. Adds a context message noting the transfer
3. Passes the full conversation history to the new agent
4. The new agent continues with its own system prompt and tools

## Handoff Limits

`WithSwarmMaxHandoffs(n)` caps the number of transfers per invocation (default: 10). This prevents infinite loops where agents keep handing off to each other.

## Swarm vs Orchestrator

| | Orchestrator | Swarm |
|---|---|---|
| Topology | Parent → children (tree) | Peers (mesh) |
| Control flow | Parent always in control | Active agent shifts |
| Communication | Parent sends message, child responds | Full conversation transfers |
| Use case | Parallel delegation, synthesis | Sequential domain handoffs |

Use an orchestrator when one agent needs to coordinate multiple specialists and synthesize their outputs. Use a swarm when the conversation naturally flows between domains and each agent should fully own its turn.

## Options

| Option | Default | Description |
|---|---|---|
| `WithSwarmMaxHandoffs(n)` | 10 | Max agent-to-agent transfers per invocation. Returns an error if n < 1. |
| `WithSwarmLogger(l)` | nil | Logger for swarm-level events |
| `WithSwarmMiddleware(mws...)` | none | Middleware applied to all tool executions across the swarm |
| `WithSwarmMemory(m, id)` | nil | Enables conversation memory — persists messages and active agent across calls |

All options return errors on invalid input, which surfaces through `NewSwarm`.

## Conversation Memory

Without memory, each `Run`/`Invoke` call is stateless — the swarm starts fresh from the first member with no history. With `WithSwarmMemory`, the swarm:

1. Loads previous messages at the start of each call
2. Remembers which agent was last active and resumes there
3. Saves the updated conversation after each response

This means follow-up messages go to the right agent automatically. If billing was handling the conversation, the next message continues with billing — no re-triage needed.

```go
swarm, err := agent.NewSwarm(members,
    agent.WithSwarmMemory(memory.NewStore(), "session-123"),
)

// Turn 1: triage → billing
swarm.Invoke(ctx, "I need a refund")

// Turn 2: continues with billing (remembered)
swarm.Invoke(ctx, "My account ID is 12345")
```

For HTTP servers, use `WithConversationID` on the context to serve multiple conversations from a single Swarm:

```go
swarm, _ := agent.NewSwarm(members,
    agent.WithSwarmMemory(store, ""),
)

// Each request provides its own conversation ID.
ctx := agent.WithConversationID(r.Context(), req.ConversationID)
result, err := swarm.Invoke(ctx, req.Message)
```

See [HTTP & Multi-Tenant Environments](http.md) for the full pattern.

Any `Memory` implementation works — in-memory (`memory.NewStore()`), Redis, or your own.
