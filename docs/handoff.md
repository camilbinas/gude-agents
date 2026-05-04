# Handoffs

Handoffs let an agent pause mid-conversation and transfer control to a human (or external system), preserving full conversation context. When the agent needs information, a decision, or approval it can't determine on its own, it calls the `request_human_input` tool. The framework breaks out of the agent loop and returns `ErrHandoffRequested` to the caller.

## Quick Start

```go
a, _ := agent.New(provider, instructions, []tool.Tool{
    agent.NewHandoffTool("request_human_input", "Hand off when you need approval before processing a refund."),
    // ... other tools
})

c := agent.Background()

_, err := a.InvokeStream(c, "Process refund for order #1234", nil)

if errors.Is(err, agent.ErrHandoffRequested) {
    hr, _ := agent.GetHandoffRequest(c)
    fmt.Printf("Agent asks: %s\n", hr.Question)

    // Collect human input however you want
    humanInput := collectInput()

    // Resume where the agent left off
    result, err := a.ResumeInvoke(c, hr, humanInput)
    fmt.Println(result)
}
```

## How It Works

1. You add `agent.NewHandoffTool(name, description)` to your agent's tool list
2. The LLM decides it needs human input and calls the tool
3. `InvokeStream` / `Invoke` returns `agent.ErrHandoffRequested`
4. You extract the `HandoffRequest` from the `*Context` — it contains the reason, question, and full `[]Message` history
5. You collect the human's response (stdin, HTTP, Slack, email, whatever)
6. You call `agent.Resume()` or `agent.ResumeInvoke()` with the `HandoffRequest` and the human's answer
7. The agent continues from where it stopped, with full context

## API Reference

### `NewHandoffTool(name, description string) tool.Tool`

Creates a handoff tool with a custom name and description. The base description is always:
> "Pause execution and ask a human for input, a decision, or approval. Use when you need information you cannot determine on your own."

The `description` parameter is appended to that base, letting you define exactly when the handoff should occur. The LLM provides:
- `reason` — why it needs human input
- `question` — the specific ask

### `GetHandoffRequest(c *Context) (*HandoffRequest, bool)`

Extracts the handoff details after `ErrHandoffRequested` is returned. The `HandoffRequest` contains:
- `Reason` — why the agent paused
- `Question` — the specific ask for the human
- `ConversationID` — the conversation this handoff belongs to (resolved from context or agent default)
- `Messages` — full conversation state at the point of handoff

### `(*Agent) Resume(c, hr, humanResponse, cb) error`

Continues the agent loop from the saved conversation state, appending the human's response as a new user message.

### `(*Agent) ResumeInvoke(c, hr, humanResponse) (string, error)`

Convenience wrapper that collects streamed output into a string.

## Compatibility

| Agent Type | Handoff Support |
|---|---|
| Default, Worker | Yes |
| Orchestrator | Yes |
| RAGAgent | Yes — RAG runs on the initial query, context carries through |
| InvokeStructured | No — single-shot, no iterative loop |

## Environment Patterns

### CLI (stdin)

```go
c := agent.Background()
err := a.InvokeStream(c, userMsg, streamToStdout)
if errors.Is(err, agent.ErrHandoffRequested) {
    hr, _ := agent.GetHandoffRequest(c)
    fmt.Printf("Agent asks: %s\n> ", hr.Question)
    scanner.Scan()
    result, _ := a.ResumeInvoke(c, hr, scanner.Text())
}
```

### HTTP API

Use `WithConversationID` on the `*Context` so the handoff targets the correct conversation. The `HandoffRequest.ConversationID` is set automatically and used by `Resume`:

```go
// POST /chat → 202 Accepted + handoff JSON
// POST /chat/resume → 200 OK + agent response

c := agent.NewContext(r.Context()).WithConversationID(req.ConversationID)

_, err := a.Invoke(c, req.Message)
if errors.Is(err, agent.ErrHandoffRequested) {
    hr, _ := agent.GetHandoffRequest(c)
    // hr.ConversationID is set automatically
    store[req.ConversationID] = hr
    w.WriteHeader(http.StatusAccepted)
    json.NewEncoder(w).Encode(handoffPayload(hr))
    return
}
```

### Async (queues, email, Slack)

Same pattern — persist the `HandoffRequest.Messages` to your store, and call `Resume` whenever the human responds, even hours or days later. If using `Conversation`, the conversation is already saved automatically on handoff.

See `examples/handoff-cli/` and `examples/handoff-http/` for complete working examples.
