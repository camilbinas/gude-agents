# Tool Approval

Tool approval lets the agent pause before executing a specific tool and wait for an explicit human allow/deny decision. When the LLM calls a tool marked with `RequiresApproval`, the agent loop stops and returns `ErrToolApprovalRequired`. The caller inspects the pending call, collects a decision, and resumes via `ResumeWithApproval`.

## How It Differs from Handoffs

| | Handoffs | Tool Approval |
|---|---|---|
| Triggered by | LLM calling `NewHandoffTool` | LLM calling any `RequiresApproval` tool |
| What pauses | The whole agent loop | The loop, before one tool runs |
| Human provides | Free-form text response | `tool.Decision{Allow: bool, Reason: string}` |
| Resume method | `Agent.ResumeInvoke` / `Agent.Resume` | `Agent.ResumeWithApprovalInvoke` / `Agent.ResumeWithApproval` |
| LLM chose it? | Yes — the LLM decided to hand off | Yes — but approval intercepts *before* the handler runs |
| Async friendly? | Yes | Yes |

Use handoffs when the agent needs a human-provided answer to continue. Use tool approval when you want a gate that either lets the tool run or injects a denial, regardless of what the LLM intended.

## Marking a Tool for Approval

Pass `tool.RequiresApproval()` as a constructor option on any `tool.New`, `tool.NewRaw`, or other constructor:

```go
deleteTool := tool.NewRaw("delete_order", "Permanently cancel and delete an order",
    map[string]any{
        "type": "object",
        "properties": map[string]any{
            "order_id": map[string]any{"type": "string", "description": "Order ID to delete"},
        },
        "required": []string{"order_id"},
    },
    func(ctx context.Context, input json.RawMessage) (string, error) {
        // only reached when approved
        return doDelete(input)
    },
    tool.RequiresApproval(), // <-- marks the tool
)
```

`tool.NeedsApproval() bool` lets you inspect the flag at runtime if needed.

## The Approval Flow

### 1. Agent pauses and returns `ErrToolApprovalRequired`

```go
c := agent.Background()
err := a.InvokeStream(c, "delete order #5678", func(chunk string) {
    fmt.Print(chunk)
})
if errors.Is(err, agent.ErrToolApprovalRequired) {
    // paused — tool handler has NOT run yet
}
```

`ErrToolApprovalRequired` is a sentinel defined in the `agent` package. Test with `errors.Is`.

### 2. Extract the `ApprovalRequest`

```go
ar, ok := agent.GetApprovalRequest(c)
```

`GetApprovalRequest` returns `(*ApprovalRequest, bool)` — `false` when no approval is pending. The struct contains everything needed to make a decision:

| Field | Type | Description |
|-------|------|-------------|
| `ToolName` | `string` | Name of the tool that needs approval |
| `ToolInput` | `json.RawMessage` | Exact input the LLM passed to the tool |
| `ToolUseID` | `string` | Provider-assigned unique ID for this tool call |
| `ConversationID` | `string` | Conversation this call belongs to |
| `Messages` | `[]agent.Message` | Full conversation snapshot at the point of pause |
| `NodeName` | `string` | Set by the graph layer to scope approval to the correct node |

### 3. Collect the decision with `tool.Decision`

```go
type Decision struct {
    Allow  bool
    Reason string
}
```

Three convenience constructors are available:

| Constructor | Result |
|-------------|--------|
| `tool.Allow()` | `Decision{Allow: true}` |
| `tool.Deny(reason string)` | `Decision{Allow: false, Reason: reason}` |
| `tool.Denyf(format string, a ...any)` | `Decision{Allow: false, Reason: fmt.Sprintf(...)}` |

### 4. Resume execution

```go
// streaming variant — collects chunks via callback
err = a.ResumeWithApproval(c, ar, decision, func(chunk string) {
    fmt.Print(chunk)
})

// convenience variant — returns the full response string
result, err := a.ResumeWithApprovalInvoke(c, ar, decision)
```

**On allow** — the tool handler runs, its output is added to the conversation, and the agent loop continues.

**On deny** — a structured denial JSON is injected as the tool result (`{"error":"tool_call_denied","tool":"...","reason":"..."}`) and the loop continues without calling the handler. The LLM sees the denial and responds accordingly.

`ErrToolCallDenied` is reported to the `MetricsHook` when a denial occurs (either from `tool.Decision.Allow == false` here, or from a `WithGuard` check). It is not returned to the caller of `ResumeWithApproval`.

## Environment Patterns

### CLI

```go
c := agent.Background()
err := a.InvokeStream(c, userMsg, func(chunk string) { fmt.Print(chunk) })

if errors.Is(err, agent.ErrToolApprovalRequired) {
    ar, _ := agent.GetApprovalRequest(c)
    fmt.Printf("\nApprove %s %s? [y/N]: ", ar.ToolName, ar.ToolInput)
    scanner.Scan()
    var d tool.Decision
    if strings.TrimSpace(scanner.Text()) == "y" {
        d = tool.Allow()
    } else {
        d = tool.Deny("operator rejected")
    }
    result, err := a.ResumeWithApprovalInvoke(c, ar, d)
    fmt.Println(result)
}
```

### HTTP API

Use `WithConversationID` so the approval targets the correct conversation. Store the `ApprovalRequest` between the two HTTP requests:

```go
// POST /chat → 202 Accepted when approval is required
c := agent.NewContext(r.Context()).WithConversationID(req.ConversationID)

result, err := a.Invoke(c, req.Message)
if errors.Is(err, agent.ErrToolApprovalRequired) {
    ar, _ := agent.GetApprovalRequest(c)
    pendingApprovals[req.ConversationID] = ar  // persist to Redis/DB in production
    w.WriteHeader(http.StatusAccepted)
    json.NewEncoder(w).Encode(map[string]any{
        "conversation_id": req.ConversationID,
        "tool_name":       ar.ToolName,
        "tool_input":      string(ar.ToolInput),
    })
    return
}

// POST /chat/approve → 200 OK after human decision
ar := pendingApprovals[req.ConversationID]
delete(pendingApprovals, req.ConversationID)

c := agent.NewContext(r.Context())
var d tool.Decision
if req.Allow {
    d = tool.Allow()
} else {
    d = tool.Deny(req.Reason)
}
result, err := a.ResumeWithApprovalInvoke(c, ar, d)
json.NewEncoder(w).Encode(map[string]any{"response": result})
```

### Async (queues, Slack, email)

Persist the `ApprovalRequest.Messages` to your store (Redis, DynamoDB, Postgres) and call `ResumeWithApproval` when the decision arrives, even hours later. The conversation state is fully preserved in `ar.Messages` — no in-memory state required between the pause and the resume.

See `examples/approval-cli/` for a complete working example.

## Graph Integration

When running an agent inside a graph, tool approval surfaces as a typed error rather than as a sentinel.

### `GraphToolApprovalError`

`graph.GraphToolApprovalError` is returned by `Graph.Run` when an agent node calls a `RequiresApproval` tool:

```go
type GraphToolApprovalError struct {
    Approval  *agent.ApprovalRequest
    Interrupt InterruptResult
}
```

`Interrupt.NodeName` identifies which node triggered the pause. `Interrupt.Checkpoint` holds the checkpoint version used for resumption — a checkpointer must be configured on the graph for `ResumeWithApproval` to work.

```go
result, err := g.Run(ctx, initialState, graph.WithThreadID("thread-1"))

var ae *graph.GraphToolApprovalError
if errors.As(err, &ae) {
    fmt.Printf("approval required: tool=%s input=%s\n",
        ae.Approval.ToolName, ae.Approval.ToolInput)
    // collect decision...
}
```

### `g.ResumeWithApproval`

```go
func (g *Graph[S]) ResumeWithApproval(
    ctx context.Context,
    ae *GraphToolApprovalError,
    decision tool.Decision,
    opts ...RunOption,
) (Result[S], error)
```

Resumes graph execution from the checkpoint stored in `ae`. Returns `ErrNoCheckpointer` if no checkpointer was configured and `ErrThreadIDRequired` if the thread ID is missing from the checkpoint.

```go
result, err := g.ResumeWithApproval(ctx, ae, tool.Allow())
if err != nil {
    log.Fatal(err)
}
fmt.Println(result.State["output"])
```

The graph replays from the saved checkpoint, runs the approved (or denied) tool result through the agent loop, and continues until the graph completes or another pause occurs.

## Code Example

CLI approval flow with a destructive delete tool:

```go
package main

import (
    "bufio"
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "log"
    "os"
    "strings"

    "github.com/camilbinas/gude-agents/agent"
    "github.com/camilbinas/gude-agents/agent/prompt"
    "github.com/camilbinas/gude-agents/agent/provider/bedrock"
    "github.com/camilbinas/gude-agents/agent/tool"
)

func main() {
    provider := bedrock.Must(bedrock.Standard())

    deleteTool := tool.NewRaw("delete_order", "Permanently cancel and delete an order",
        map[string]any{
            "type": "object",
            "properties": map[string]any{
                "order_id": map[string]any{"type": "string", "description": "Order ID to delete"},
            },
            "required": []string{"order_id"},
        },
        func(_ context.Context, input json.RawMessage) (string, error) {
            var p struct{ OrderID string `json:"order_id"` }
            json.Unmarshal(input, &p)
            return fmt.Sprintf(`{"deleted":true,"order_id":%q}`, p.OrderID), nil
        },
        tool.RequiresApproval(),
    )

    a, err := agent.New(provider,
        prompt.Text("You are an order management assistant."),
        []tool.Tool{deleteTool},
    )
    if err != nil {
        log.Fatal(err)
    }

    c := agent.Background()
    scanner := bufio.NewScanner(os.Stdin)

    err = a.InvokeStream(c, "delete order #5678", func(chunk string) {
        fmt.Print(chunk)
    })

    if errors.Is(err, agent.ErrToolApprovalRequired) {
        ar, _ := agent.GetApprovalRequest(c)
        fmt.Printf("\n\n[approval required] tool=%s input=%s\n", ar.ToolName, ar.ToolInput)
        fmt.Print("Approve? [y/N]: ")
        scanner.Scan()

        var decision tool.Decision
        if strings.TrimSpace(strings.ToLower(scanner.Text())) == "y" {
            decision = tool.Allow()
        } else {
            decision = tool.Deny("operator rejected")
        }

        result, err := a.ResumeWithApprovalInvoke(c, ar, decision)
        if err != nil {
            log.Fatal(err)
        }
        fmt.Println("\nAgent:", result)
        return
    }
    if err != nil {
        log.Fatal(err)
    }
}
```

## See Also

- [Handoffs](handoff.md) — LLM-initiated pauses that ask humans for input rather than gating tool execution
- [Tools](tools.md) — tool constructors, `WithGuard`, `Decision`, and introspection methods
- [Graph Workflows](graph.md) — `GraphToolApprovalError` and graph-layer resumption
- [Invocation Context](invocation-context.md) — `WithConversationID` for multi-tenant HTTP flows
- [Event Stream](agent-api.md#invokeeventstream) — `EventToolApprovalRequired` event emitted on the event stream channel
