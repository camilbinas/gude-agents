# HTTP services

Construct agents and stores once, then create a new `agent.Context` for every request. Derive all caller-controlled IDs from authenticated server state.

```go
func handle(a *agent.Agent) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        ctx := agent.NewContext(r.Context()).
            WithConversationID(authenticatedThreadID(r)).
            WithIdentity(authenticatedUserID(r)).
            WithPrincipal(authenticatedPrincipal(r)).
            WithScope("account", authenticatedAccountID(r))

        result, err := a.Invoke(ctx, r.FormValue("message"))
        if errors.Is(err, agent.ErrConversationConflict) {
            http.Error(w, "conversation changed; retry", http.StatusConflict)
            return
        }
        if err != nil {
            http.Error(w, "agent failed", http.StatusBadGateway)
            return
        }
        writeJSON(w, result)
    }
}
```

Do not accept identity, principal roles, account scope, or unrestricted conversation IDs directly from an untrusted body.

When the Agent has a ConversationStore, every invocation requires a non-empty ConversationID. If a request can arrive without one, reject it at the boundary (for example with `400 Bad Request`) or map `agent.ErrConversationIDRequired` to a client error; the agent fails before any model or tool work. Endpoints that are meant to be stateless should use an Agent constructed without a ConversationStore.

## Streaming

Encode each `agent.Event` from `a.Stream(ctx, input)` as SSE or WebSocket JSON. Flush after each event and stop when the request context is canceled. `EventInterrupt` is an ordinary paused outcome; `EventEnd.Result` is authoritative. Breaking iteration cancels execution, so do not stop immediately after the last text chunk if conversation persistence matters.

For text-only endpoints, range over `TextStream`. Output guardrails run after live chunks have been emitted; use non-streaming `Invoke` where every byte must be validated before delivery.

## Resume endpoints

Persist interrupts with `WithInterruptStore`, accept an interrupt ID plus a validated decision/answer, load it with `Agent.LoadInterrupt`, then call `Resume` or `ResumeStream`. Authorize that the caller owns the interrupt's conversation before resuming.

Call `Agent.Shutdown` during graceful server shutdown and also close provider-specific clients/exporters where required.

[`examples/http-server`](../examples/http-server/) provides a compact `net/http` reference with `POST /chat` SSE streaming, request-context cancellation, conversation IDs, an interrupt resume endpoint, and graceful shutdown. It is intentionally not a production authentication system.
