# HTTP & Multi-Tenant Environments

This guide covers how to use gude-agents in HTTP servers where a single process serves multiple concurrent users and conversations.

## The Problem

By default, `WithConversation(store, "conversation-123")` binds a conversation ID to the agent at construction time. This means one Agent instance = one conversation. In an HTTP server, you'd need to create a new Agent per request, which wastes resources since the provider, tools, instructions, and middleware are identical across conversations.

## The Solution: Per-Request Conversation IDs

Two APIs solve this:

### `WithSharedConversation(c Conversation)`

Configures conversation persistence without a default conversation ID. Each request must provide one via context.

```go
store := conversation.NewInMemory() // or redis.NewRedisConversation(...)

a, err := agent.New(provider, instructions, tools,
    agent.WithSharedConversation(store),
)
```

### `WithConversationID(ctx, id)`

Sets the conversation ID for a single invocation via the Go context.

```go
ctx := agent.WithConversationID(r.Context(), req.ConversationID)
result, _, err := a.Invoke(ctx, req.Message)
```

The agent resolves the conversation ID at invocation time: context override first, then the construction-time default. If neither is set and conversation persistence is configured, the conversation ID is an empty string (which still works but all requests share one conversation — probably not what you want).

## HTTP Server Pattern

```go
func main() {
    provider, _ := bedrock.Standard()
    store := conversation.NewInMemory()

    // One agent instance for the entire server.
    a, _ := agent.New(provider,
        prompt.Text("You are a helpful assistant."),
        tools,
        agent.WithSharedConversation(store),
        agent.WithMaxIterations(10),
    )

    http.HandleFunc("/chat", handleChat(a))
    http.ListenAndServe(":8080", nil)
}

func handleChat(a *agent.Agent) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        var req ChatRequest
        json.NewDecoder(r.Body).Decode(&req)

        // Each request gets its own conversation ID.
        ctx := agent.WithConversationID(r.Context(), req.ConversationID)

        result, _, err := a.Invoke(ctx, req.Message)
        if err != nil {
            http.Error(w, err.Error(), 500)
            return
        }

        json.NewEncoder(w).Encode(ChatResponse{Response: result})
    }
}
```

## Swarm in HTTP

Swarms also support per-request conversation IDs. The `WithSwarmConversation` default is overridden by `WithConversationID` on the context:

```go
swarm, _ := agent.NewSwarm(members,
    agent.WithSwarmConversation(store, ""), // empty default, override per-request
)

// In handler:
ctx := agent.WithConversationID(r.Context(), req.ConversationID)
result, err := swarm.Invoke(ctx, req.Message)
```

The swarm persists both the conversation history and the active agent per conversation ID, so follow-up requests route to the correct agent automatically.

## Handoffs in HTTP

When an agent triggers a handoff, the `HandoffRequest` includes the `ConversationID` so `Resume` targets the correct conversation:

```go
func handleChat(a *agent.Agent) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        var req ChatRequest
        json.NewDecoder(r.Body).Decode(&req)

        ic := agent.NewInvocationContext()
        ctx := agent.WithConversationID(r.Context(), req.ConversationID)
        ctx = agent.WithInvocationContext(ctx, ic)

        _, _, err := a.Invoke(ctx, req.Message)

        if errors.Is(err, agent.ErrHandoffRequested) {
            hr, _ := agent.GetHandoffRequest(ic)
            // hr.ConversationID == req.ConversationID
            // Store hr for the resume endpoint...
            w.WriteHeader(http.StatusAccepted)
            return
        }
        // ...
    }
}

func handleResume(a *agent.Agent) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        // Load hr from storage...
        // Resume uses hr.ConversationID automatically.
        result, _, _ := a.ResumeInvoke(r.Context(), hr, req.HumanResponse)
        // ...
    }
}
```

## Backward Compatibility

The original `WithConversation(store, "conv-id")` still works exactly as before. `WithConversationID` on the context overrides it, so you can migrate incrementally:

```go
// Old pattern — still works, single conversation per agent.
a, _ := agent.New(provider, instructions, tools,
    agent.WithConversation(store, "session-1"),
)

// New pattern — same agent, multiple conversations.
ctx := agent.WithConversationID(ctx, userSessionID)
a.Invoke(ctx, msg)
```

## Thread Safety

All components are safe for concurrent use from multiple goroutines. A single `Agent`, `Swarm`, or conversation store can handle many simultaneous requests — conversation isolation comes from the per-request conversation ID, not from separate instances.

## Production Recommendations

- Use `redis.NewRedisConversation` instead of `conversation.NewInMemory` for persistence across restarts and horizontal scaling
- Set `WithTTL` on Redis conversation store to auto-expire idle conversations
- Use `r.Context()` as the base context so request cancellation propagates to LLM calls
- Set `WithTokenBudget` to prevent runaway costs from a single request

See `examples/handoff-http/` for a complete working multi-tenant HTTP server.
