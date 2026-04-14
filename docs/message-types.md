# Message Types

The `agent` package defines the conversation data model used throughout gude-agents. These types represent messages, content blocks, provider call parameters, and RAG documents. Understanding them is essential when working with memory, custom providers, or middleware.

All types below live in the `agent` package (`github.com/camilbinas/gude-agents/agent`) unless otherwise noted.

## Message

```go
type Message struct {
    Role    Role
    Content []ContentBlock
}
```

A `Message` is a single turn in the conversation. Each message has a `Role` indicating who sent it and a slice of `ContentBlock` values representing the message body.

A user message typically contains a single `TextBlock`. An assistant message may contain a `TextBlock` (text response), one or more `ToolUseBlock` values (tool call requests), or both. Tool results are sent back as user messages containing `ToolResultBlock` values.

| Field | Type | Description |
|---|---|---|
| `Role` | `Role` | Sender of the message (`RoleUser` or `RoleAssistant`) |
| `Content` | `[]ContentBlock` | One or more content blocks forming the message body |

## Role

```go
type Role string

const (
    RoleUser      Role = "user"
    RoleAssistant Role = "assistant"
)
```

`Role` is a string type that identifies the sender of a `Message`. The framework defines two constants:

| Constant | Value | Description |
|---|---|---|
| `RoleUser` | `"user"` | Messages from the user or tool results sent back to the LLM |
| `RoleAssistant` | `"assistant"` | Messages from the LLM |

## ContentBlock

```go
type ContentBlock interface {
    contentBlock() // sealed marker
}
```

`ContentBlock` is a sealed interface — only the three implementations defined in the `agent` package satisfy it. The unexported `contentBlock()` marker method prevents external types from implementing the interface.

### TextBlock

```go
type TextBlock struct {
    Text string
}
```

Plain text content. Used in both user messages (the user's input) and assistant messages (the LLM's text response).

| Field | Type | Description |
|---|---|---|
| `Text` | `string` | The text content |

### ToolUseBlock

```go
type ToolUseBlock struct {
    ToolUseID string
    Name      string
    Input     json.RawMessage
}
```

Represents the LLM requesting a tool call. Appears in assistant messages when the model decides to invoke a tool. The `Input` field contains the raw JSON arguments for the tool.

| Field | Type | Description |
|---|---|---|
| `ToolUseID` | `string` | Unique identifier linking this request to its `ToolResultBlock` |
| `Name` | `string` | Name of the tool to invoke |
| `Input` | `json.RawMessage` | JSON-encoded tool input arguments |

### ToolResultBlock

```go
type ToolResultBlock struct {
    ToolUseID string
    Content   string
    IsError   bool
}
```

Holds the result of a tool execution. Sent back to the LLM as part of a user-role message so the model can incorporate the tool's output into its next response.

| Field | Type | Description |
|---|---|---|
| `ToolUseID` | `string` | Matches the `ToolUseID` from the corresponding `ToolUseBlock` |
| `Content` | `string` | The tool's text output |
| `IsError` | `bool` | `true` if the tool returned an error |

## ConverseParams

```go
type ConverseParams struct {
    Messages   []Message
    System     string
    ToolConfig []tool.Spec
    ToolChoice *tool.Choice
}
```

`ConverseParams` holds the inputs for a `Provider.Converse` or `Provider.ConverseStream` call. The agent constructs this struct internally before each provider call.

| Field | Type | Description |
|---|---|---|
| `Messages` | `[]Message` | The conversation history |
| `System` | `string` | System prompt text |
| `ToolConfig` | `[]tool.Spec` | Tool specifications the LLM can choose from |
| `ToolChoice` | `*tool.Choice` | Controls tool selection behavior; `nil` means provider default (auto) |

`tool.Spec` and `tool.Choice` are defined in the `tool` sub-package (`github.com/camilbinas/gude-agents/agent/tool`). See [Tool System](tools.md) for details.

## ProviderResponse

```go
type ProviderResponse struct {
    Text      string
    ToolCalls []tool.Call
    Usage     TokenUsage
}
```

`ProviderResponse` is the result of an LLM call. A response contains either a text reply, one or more tool calls, or both.

| Field | Type | Description |
|---|---|---|
| `Text` | `string` | The LLM's text response (empty when the model only returns tool calls) |
| `ToolCalls` | `[]tool.Call` | Tool invocation requests from the LLM |
| `Usage` | `TokenUsage` | Token consumption for this single provider call |

`tool.Call` is defined as:

```go
// In package tool
type Call struct {
    ToolUseID string
    Name      string
    Input     json.RawMessage
}
```

## TokenUsage

```go
type TokenUsage struct {
    InputTokens  int
    OutputTokens int
}

func (u TokenUsage) Total() int
```

Records token consumption for a single provider call. `Total()` returns `InputTokens + OutputTokens`. See [Agent API Reference](agent-api.md#tokenusage) for details on cumulative usage across an invocation.

## StreamCallback

```go
type StreamCallback func(chunk string)
```

`StreamCallback` receives incremental text chunks during streaming. Passed to `Provider.ConverseStream` and `Agent.InvokeStream` to deliver the LLM's response in real-time.

When output guardrails are configured on the agent, chunks are buffered until all guardrails pass. See [Guardrails](guardrails.md) for details.

## Document

```go
type Document struct {
    Content  string
    Metadata map[string]string
}
```

A text chunk with associated metadata, used throughout the RAG pipeline. Documents are stored in a `VectorStore`, returned by a `Retriever`, and formatted by a `ContextFormatter`.

| Field | Type | Description |
|---|---|---|
| `Content` | `string` | The text content of the document |
| `Metadata` | `map[string]string` | Arbitrary key-value metadata (e.g., source, title) |

## ScoredDocument

```go
type ScoredDocument struct {
    Document Document
    Score    float64
}
```

Pairs a `Document` with its similarity score. Returned by `VectorStore.Search` to rank results by relevance.

| Field | Type | Description |
|---|---|---|
| `Document` | `Document` | The matched document |
| `Score` | `float64` | Similarity score (higher is more relevant) |

## See Also

- [Agent API Reference](agent-api.md) — `Agent` constructor, options, and methods
- [Tool System](tools.md) — `Tool`, `Spec`, `Call`, and `Choice` types
- [RAG Pipeline](rag.md) — `Embedder`, `VectorStore`, `Retriever` interfaces
- [Memory System](memory.md) — storing and loading `Message` history
- [Providers](providers.md) — `Provider` and `CapabilityReporter` interfaces
