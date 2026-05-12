# Message Types

Reference for the conversation data model used throughout gude-agents. These types matter when working with conversation persistence, custom providers, or middleware. All types live in the `agent` package unless otherwise noted.

## Conversation Model

A conversation is a sequence of `Message` values alternating between `RoleUser` and `RoleAssistant`. Each message contains one or more `ContentBlock` values:

```
User:      TextBlock (input) | ToolResultBlock (tool output back to LLM)
Assistant: TextBlock (response) | ToolUseBlock (tool call request) | both
```

`ContentBlock` is a sealed interface — only the implementations below satisfy it.

## Content Blocks

| Block | Role | Purpose |
|---|---|---|
| `TextBlock` | both | Plain text content |
| `ToolUseBlock` | assistant | LLM requesting a tool call (`ToolUseID`, `Name`, `Input json.RawMessage`) |
| `ToolResultBlock` | user | Tool execution result (`ToolUseID`, `Content`, `IsError`, optional `Images`) |
| `ImageBlock` | user | Image attachment (`Source ImageSource`) |
| `DocumentBlock` | user | Document attachment (`Source DocumentSource`) |

`ToolUseID` links a `ToolUseBlock` to its corresponding `ToolResultBlock`.

## Media Sources

`ImageSource` and `DocumentSource` hold media data. Set exactly one of `Data`, `Base64`, or `URL` — providers prefer them in that order.

| Field | ImageSource | DocumentSource |
|---|---|---|
| `Data []byte` | Raw image bytes | Raw document bytes |
| `Base64 string` | Pre-encoded base64 | Pre-encoded base64 |
| `URL string` | Hosted image URL | Hosted document URL |
| `MIMEType string` | Required for Data/Base64 | Required for Data/Base64 |
| `Name string` | — | Optional filename hint |

Supported image MIME types: `image/jpeg`, `image/png`, `image/gif`, `image/webp`.
Supported document MIME types: `application/pdf`, `text/plain`, `text/html`, `text/csv`, `text/markdown`, plus Office formats.

Both have a `Validate()` method and a `*MIMEFromExt(ext)` helper for extension-to-MIME mapping.

## InferenceConfig

Groups LLM sampling parameters. All fields are pointer types — `nil` means "use provider default."

| Field | Type | Valid Range |
|---|---|---|
| `Temperature` | `*float64` | [0.0, 1.0] |
| `TopP` | `*float64` | [0.0, 1.0] |
| `TopK` | `*int` | >= 1 |
| `StopSequences` | `[]string` | — |
| `MaxTokens` | `*int` | >= 1 |

Set at agent level via `WithTemperature`, `WithTopP`, etc. Override per-invocation via `WithInferenceConfig` on the context.

## ConverseParams

The input struct for `Provider.Converse` / `Provider.ConverseStream`. Constructed internally by the agent loop.

| Field | Type | Description |
|---|---|---|
| `Messages` | `[]Message` | Conversation history |
| `System` | `string` | System prompt |
| `ToolConfig` | `[]tool.Spec` | Available tools |
| `ToolChoice` | `*tool.Choice` | Tool selection behavior (`nil` = auto) |
| `ThinkingCallback` | `ThinkingCallback` | Internal; set by agent loop when EventHook is configured |
| `InferenceConfig` | `*InferenceConfig` | Sampling parameters (`nil` = provider defaults) |

## ProviderResponse

The result of an LLM call. Contains either a text reply, tool calls, or both.

| Field | Type | Description |
|---|---|---|
| `Text` | `string` | Text response (empty when only tool calls) |
| `ToolCalls` | `[]tool.Call` | Tool invocation requests |
| `Usage` | `TokenUsage` | Token consumption for this call |
| `Metadata` | `map[string]any` | Provider-specific extras (e.g. `"thinking"` key for extended thinking) |

## TokenUsage

```go
type TokenUsage struct {
    InputTokens  int
    OutputTokens int
}
```

`Total()` returns `InputTokens + OutputTokens`. Access cumulative usage via `c.Usage()` on the `*Context`.

## Callbacks

| Type | Description |
|---|---|
| `StreamCallback func(chunk string)` | Receives incremental text chunks during streaming |
| `ThinkingCallback func(chunk string)` | Receives thinking/reasoning chunks (internal, forwarded to EventHook) |

## RAG Types

| Type | Description |
|---|---|
| `Document` | Text chunk with `ID`, `Content`, and `Metadata map[string]string` |
| `ScoredDocument` | Pairs a `Document` with a `Score float64` (higher = more relevant) |

## See Also

- [Agent API Reference](agent-api.md) — constructor, options, and invoke methods
- [Tool System](tools.md) — `Tool`, `Spec`, `Call`, and `Choice` types
- [RAG Pipeline](rag.md) — `Embedder`, `VectorStore`, `Retriever` interfaces
- [Conversation System](conversation.md) — storing and loading `Message` history
- [Providers](providers.md) — `Provider` interface
