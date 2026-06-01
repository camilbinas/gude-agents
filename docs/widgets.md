# Widget Blocks

Widget blocks let tool handlers attach structured, domain-agnostic data to assistant messages — alongside the text response the LLM uses to reason. A UI can render that data as a chart, table, progress indicator, or any other component without the LLM needing to know about it.

## How It Works

When a tool calls `c.EmitWidget(...)`, three things happen:

1. An `EventWidget` event is sent on the `InvokeEventStream` channel — before `EventToolCallEnd` for the same tool call.
2. The `WidgetBlock` is stored inline in the assistant message's `Content` slice and persisted via `Conversation.Save`.
3. Before each provider call, all `WidgetBlock` entries are stripped from the message history — the LLM only ever sees the tool's text return value.

## WidgetBlock

```go
type WidgetBlock struct {
    Type    string          // caller-defined discriminator, e.g. "chart", "table"
    Payload json.RawMessage // opaque JSON; nil is valid
}
```

`Type` is required and must be non-empty. `Payload` carries whatever JSON your UI needs — the agent package imposes no schema.

## Emitting a Widget from a Tool

Use `agent.FromContext` to get the `*agent.Context` from the stdlib `context.Context` that `tool.Handler[T]` receives, then call `EmitWidget`:

```go
type SalesInput struct {
    Year int `json:"year" description:"Year to report on" required:"true"`
}

salesTool := tool.New("get_sales", "Returns quarterly sales data.",
    func(ctx context.Context, in SalesInput) (string, error) {
        data := map[string]any{
            "labels": []string{"Q1", "Q2", "Q3", "Q4"},
            "values": []float64{142, 189, 203, 251},
        }
        payload, _ := json.Marshal(data)

        if c := agent.FromContext(ctx); c != nil {
            c.EmitWidget(agent.WidgetBlock{
                Type:    "chart",
                Payload: payload,
            })
        }

        // Return a text summary for the LLM to use in its answer.
        return "Q1 €142k, Q2 €189k, Q3 €203k, Q4 €251k. Total €785k.", nil
    },
)
```

`EmitWidget` returns an error only if `Type` is empty. All other errors are impossible by construction.

## Consuming Widget Events

Widget events arrive on the `InvokeEventStream` channel as `EventWidget`. Handle them alongside other event types:

```go
for ev := range a.InvokeEventStream(ctx, "Show me the 2024 sales report.") {
    switch ev.Type {
    case agent.EventTextChunk:
        fmt.Print(ev.TextChunk) // stream text to the user

    case agent.EventWidget:
        // Route to a chart renderer, table component, etc.
        fmt.Printf("widget type=%q payload=%s\n", ev.WidgetType, ev.WidgetPayload)

    case agent.EventInvokeEnd:
        if ev.Err != nil {
            log.Fatal(ev.Err)
        }
    }
}
```

`AgentEvent` fields populated for `EventWidget`:

| Field | Type | Description |
|---|---|---|
| `WidgetType` | `string` | Value of `WidgetBlock.Type` |
| `WidgetPayload` | `json.RawMessage` | Value of `WidgetBlock.Payload` |

Both fields use `omitempty` — they are absent from JSON for all other event types.

## Persistence

`WidgetBlock` is stored inline in the assistant message and serialized by the conversation store. It round-trips through `MarshalMessages` / `UnmarshalMessages` without data loss. When the conversation is reloaded, widgets are available in `Message.Content` alongside `TextBlock` and `ToolUseBlock` entries.

Widgets are stripped from the provider message slice before every LLM call — they are never sent to the model. The LLM reasons from the tool's text return value only.

## Multi-Turn Conversations

Widgets in history do not affect subsequent turns. The `stripWidgets` step runs on every iteration, so a conversation with many widget-bearing turns works identically to one without.

```go
a, _ := agent.New(provider, instructions, tools,
    agent.WithConversation(conversation.NewInMemory(), "session-1"),
)

ctx := agent.Background()

// Turn 1 — tool emits a chart widget.
for ev := range a.InvokeEventStream(ctx, "Show me the 2024 sales.") { ... }

// Turn 2 — no tool call; agent answers from history.
// WidgetBlocks from turn 1 are stripped before the provider call.
for ev := range a.InvokeEventStream(ctx, "Which quarter was strongest?") { ... }
```

## Parallel Tool Execution

`EmitWidget` is safe for concurrent use. When `WithParallelToolExecution` is enabled, multiple tool handlers can call `EmitWidget` simultaneously — each call is serialized internally via a per-tool-call mutex.

## WidgetEmitter Interface

Hooks that want to receive widget events implement `WidgetEmitter` alongside `EventHook`:

```go
type WidgetEmitter interface {
    OnWidget(c *Context, block WidgetBlock)
}
```

The built-in `eventStreamHook` (used by `InvokeEventStream`) implements this automatically. Custom `EventHook` implementations that do not implement `WidgetEmitter` are unaffected — widget events are silently dropped for them.

## See Also

- [Event Stream](agent-api.md#event-stream) — full list of `EventType` constants and `AgentEvent` fields
- [Tool System](tools.md) — `tool.New`, `tool.Handler[T]`, and `agent.FromContext`
- [Message Types](message-types.md) — `ContentBlock` interface and the full content model
- [Conversation System](conversation.md) — how messages (including widgets) are persisted
