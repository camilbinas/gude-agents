# Tool System

The tool system lets you define functions that the LLM can invoke during a conversation. You describe a tool's name, purpose, and input schema — the framework handles marshalling, unmarshalling, and wiring it into the agent loop.

## tool.New[T] — Typed Constructor

```go
func New[T any](name, description string, handler Handler[T]) Tool
```

The recommended way to create tools. Auto-generates a JSON Schema from the struct type `T` using struct tags, and handles JSON unmarshalling automatically.

```go
type WeatherInput struct {
    City  string `json:"city"  description:"The city to get weather for" required:"true"`
    Units string `json:"units" description:"Temperature units"           enum:"celsius,fahrenheit"`
}

weatherTool := tool.New("get_weather", "Get the current weather for a city.",
    func(ctx context.Context, in WeatherInput) (string, error) {
        return fmt.Sprintf("Weather in %s: 22°C, sunny", in.City), nil
    },
)
```

### Struct Tag Schema Generation

Four tags control the generated JSON Schema:

| Tag | Purpose | Example |
|-----|---------|---------|
| `json` | Sets the property name. Use `"-"` to exclude a field. | `json:"city"` |
| `description` | Adds a description visible to the LLM. | `description:"The target city name"` |
| `required` | When `"true"`, marks the field as required. | `required:"true"` |
| `enum` | Restricts to a comma-separated list of values. | `enum:"celsius,fahrenheit"` |

Go types map to JSON Schema types automatically (`string`, `int`/`int64` → `integer`, `float64` → `number`, `bool`, slices → `array`, structs → `object`).

## tool.NewRaw — Manual Schema Constructor

```go
func NewRaw(name, description string, schema map[string]any, handler func(ctx context.Context, input json.RawMessage) (string, error)) Tool
```

`NewRaw` creates a `Tool` with a hand-crafted JSON Schema and a raw handler that receives `json.RawMessage` directly. Use this when you need full control over the schema or when the input doesn't map cleanly to a Go struct.

```go
weatherTool := tool.NewRaw(
    "get_weather",
    "Get current weather for a location",
    map[string]any{
        "type": "object",
        "properties": map[string]any{
            "location": map[string]any{
                "type":        "string",
                "description": "City name",
            },
        },
        "required": []string{"location"},
    },
    func(ctx context.Context, input json.RawMessage) (string, error) {
        // parse input manually
        var params struct{ Location string `json:"location"` }
        json.Unmarshal(input, &params)
        return fmt.Sprintf("Weather in %s: 22°C, sunny", params.Location), nil
    },
)
```

## tool.NewSimple — No-Input Constructor

```go
func NewSimple(name, description string, handler func(ctx context.Context) (string, error)) Tool
```

`NewSimple` creates a `Tool` that takes no input parameters. It uses an empty object schema automatically, so you don't need to pass `map[string]any{"type": "object"}` yourself. The handler receives only a `context.Context`.

```go
timeTool := tool.NewSimple("current_time", "Returns the current server time",
    func(ctx context.Context) (string, error) {
        return time.Now().Format(time.RFC3339), nil
    },
)
```

## tool.NewString — Single String Parameter

```go
func NewString(name, description, paramName, paramDesc string, handler func(ctx context.Context, value string) (string, error)) Tool
```

`NewString` creates a `Tool` that takes a single required string parameter. You provide the parameter name and description — the schema is built for you. The handler receives the extracted string directly.

```go
searchTool := tool.NewString("search", "Search the knowledge base", "query", "The search query",
    func(ctx context.Context, query string) (string, error) {
        results := doSearch(query)
        return results, nil
    },
)
```

## tool.NewConfirm — Boolean Confirmation

```go
func NewConfirm(name, description string, handler func(ctx context.Context, confirmed bool) (string, error)) Tool
```

`NewConfirm` creates a `Tool` with a single required `confirm` boolean parameter. Useful for approval flows where the LLM must explicitly confirm an action before it proceeds.

```go
refundTool := tool.NewConfirm("approve_refund", "Approve the pending refund",
    func(ctx context.Context, confirmed bool) (string, error) {
        if !confirmed {
            return "Refund cancelled.", nil
        }
        return processRefund()
    },
)
```

## tool.NewAsync — Async Side Effects (Fire-and-Forget)

```go
func NewAsync[T any](name, description, ack string, handler AsyncHandler[T], errLogger ErrorLogger) Tool
```

`NewAsync` creates a `Tool` whose handler runs in a background goroutine. The LLM receives the `ack` string immediately without waiting for the handler to complete — a fire-and-forget pattern. Use this for side effects that don't affect the conversation: CRM updates, webhooks, audit logs, notifications, cache warming, etc.

The handler signature is `func(ctx context.Context, input T)` — no return value. The background goroutine gets a detached `context.Background()` so it isn't cancelled when the request finishes. Panics are recovered and reported via the optional `ErrorLogger`.

```go
type CRMUpdate struct {
    ContactID string `json:"contact_id" description:"The CRM contact ID" required:"true"`
    Note      string `json:"note"       description:"Note to add to the contact" required:"true"`
}

crmTool := tool.NewAsync("update_crm", "Add a note to a CRM contact",
    "CRM update queued.",
    func(ctx context.Context, in CRMUpdate) {
        // This runs in the background — the LLM already got "CRM update queued."
        crm.AddNote(ctx, in.ContactID, in.Note)
    },
    log.Printf, // or nil to silently drop errors
)
```

`NewAsyncRaw` is the raw JSON variant:

```go
func NewAsyncRaw(name, description, ack string, schema map[string]any, handler func(ctx context.Context, input json.RawMessage), errLogger ErrorLogger) Tool
```

## ChoiceMode and Choice

`ChoiceMode` controls how the LLM selects tools during a conversation:

```go
type ChoiceMode string

const (
    ChoiceAuto ChoiceMode = "auto" // LLM decides whether to call a tool (default)
    ChoiceAny  ChoiceMode = "any"  // LLM must call some tool
    ChoiceTool ChoiceMode = "tool" // LLM must call a specific named tool
)
```

`Choice` directs the LLM's tool selection behavior:

```go
type Choice struct {
    Mode ChoiceMode
    Name string // Only used when Mode == ChoiceTool
}
```

- `ChoiceAuto` — the LLM decides on its own whether a tool call is appropriate
- `ChoiceAny` — forces the LLM to call at least one tool (useful when you know a tool call is needed)
- `ChoiceTool` — forces the LLM to call a specific tool by name (set `Name` to the tool's name)

Tool choice is passed to the provider via `ConverseParams.ToolChoice`. When `nil`, the provider uses its default behavior (typically auto).

## Code Example

This example defines a typed tool with struct tags that the LLM can call to look up weather data:

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

// WeatherInput defines the tool's parameters via struct tags.
// The json tag sets the property name, description explains it to the LLM,
// required marks mandatory fields, and enum restricts allowed values.
type WeatherInput struct {
    City  string `json:"city"  description:"The city to get weather for" required:"true"`
    Units string `json:"units" description:"Temperature units"           enum:"celsius,fahrenheit"`
}

func main() {
    provider, err := bedrock.Standard()
    if err != nil {
        log.Fatal(err)
    }

    // tool.New auto-generates the JSON Schema from WeatherInput's struct tags.
    weatherTool := tool.New("get_weather",
        "Get the current weather for a city.",
        func(ctx context.Context, in WeatherInput) (string, error) {
            // In a real app, call a weather API here.
            return fmt.Sprintf("Weather in %s: 22°C, sunny", in.City), nil
        },
    )

    a, err := agent.Default(
        provider,
        prompt.Text("You are a helpful assistant with access to weather data."),
        []tool.Tool{weatherTool},
    )
    if err != nil {
        log.Fatal(err)
    }

    result, _, err := a.Invoke(context.Background(), "What's the weather in Berlin?")
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println(result)
}
```

The generated JSON Schema for `WeatherInput` looks like:

```json
{
  "type": "object",
  "properties": {
    "city": {
      "type": "string",
      "description": "The city to get weather for"
    },
    "units": {
      "type": "string",
      "description": "Temperature units",
      "enum": ["celsius", "fahrenheit"]
    }
  },
  "required": ["city"]
}
```

## tool.NewRich — Rich Output (Text + Images)

```go
func NewRich[T any](name, description string, handler RichHandler[T]) Tool
```

`NewRich` creates a `Tool` whose handler returns `*Output` — text plus optional images. Use this for tools that need to return images to the LLM, such as screenshot tools, chart generators, or image search.

```go
type Output struct {
    Text   string
    Images []Image
}

type Image struct {
    Data     []byte // raw image bytes
    Base64   string // pre-encoded base64 string
    URL      string // publicly accessible image URL
    MIMEType string // e.g. "image/png", "image/jpeg"
}
```

```go
screenshotTool := tool.NewRich("screenshot", "Take a screenshot of a URL",
    func(ctx context.Context, in ScreenshotInput) (*tool.Output, error) {
        png, err := takeScreenshot(in.URL)
        if err != nil {
            return nil, err
        }
        return &tool.Output{
            Text:   fmt.Sprintf("Screenshot of %s captured", in.URL),
            Images: []tool.Image{{Data: png, MIMEType: "image/png"}},
        }, nil
    },
)
```

`NewRichRaw` is the manual-schema variant:

```go
func NewRichRaw(name, description string, schema map[string]any, handler func(ctx context.Context, input json.RawMessage) (*Output, error)) Tool
```

Provider support for images in tool results:

| Provider | Image support |
|----------|--------------|
| Bedrock | Native image content blocks in tool results |
| Anthropic | Native image content blocks in tool results |
| Gemini | Images appended as inline parts after function response |
| OpenAI | Text fallback (OpenAI does not support images in tool results) |

Existing `(string, error)` handlers continue to work unchanged. The `RichHandler` is optional — when set, it takes precedence over `Handler`.

## Built-in Tools

The framework ships ready-to-use tools for common agent capabilities. Each is a separate package — import only what you need.

### webfetch — Fetch Web Pages

`agent/tool/webfetch` provides a production-ready `web_fetch` tool that retrieves a URL and returns clean text.

```go
import "github.com/camilbinas/gude-agents/agent/tool/webfetch"

fetchTool := webfetch.New()
```

Features: configurable timeout, body size limit, redirect limit, content-type filtering (text only), SSRF protection (blocks private IPs), HTML stripping, and character truncation.

| Option | Description | Default |
|--------|-------------|---------|
| `WithTimeout(d)` | HTTP request timeout | 15s |
| `WithMaxBytes(n)` | Max response body size | 32 KB |
| `WithMaxRedirects(n)` | Max redirects to follow | 3 |
| `WithMaxChars(n)` | Max characters in output | 4000 |
| `WithFormatter(f)` | Custom HTML-to-text formatter | regex strip |
| `WithClient(c)` | Custom `*http.Client` | — |

#### Markdown Formatter

The optional `webfetch/markdown` sub-module converts HTML to clean markdown instead of stripping tags. This preserves structure (headings, links, lists, code blocks) which can improve answer quality. Note that markdown output uses more input tokens than plain text — benchmark both for your use case.

```go
import (
    "github.com/camilbinas/gude-agents/agent/tool/webfetch"
    "github.com/camilbinas/gude-agents/agent/tool/webfetch/markdown"
)

fetchTool := webfetch.New(webfetch.WithFormatter(markdown.Formatter()))
```

### websearch — Web Search

Search tools live under `agent/tool/websearch/`.

#### Tavily

```go
import "github.com/camilbinas/gude-agents/agent/tool/websearch/tavily"

searchTool := tavily.New(os.Getenv("TAVILY_API_KEY"))
```

Requires `TAVILY_API_KEY` from [app.tavily.com](https://app.tavily.com).

| Option | Description | Default |
|--------|-------------|---------|
| `WithMaxResults(n)` | Max search results | 5 |
| `WithTimeout(d)` | HTTP request timeout | 10s |
| `WithMaxCharsPerResult(n)` | Max chars per snippet | 300 |
| `WithSearchDepth(s)` | `"basic"` or `"advanced"` | `"basic"` |
| `WithIncludeAnswer()` | Include AI-generated answer | false |
| `WithClient(c)` | Custom `*http.Client` | — |

#### Brave

```go
import "github.com/camilbinas/gude-agents/agent/tool/websearch/brave"

searchTool := brave.New(os.Getenv("BRAVE_API_KEY"))
```

Requires `BRAVE_API_KEY` from [brave.com/search/api](https://brave.com/search/api/).

| Option | Description | Default |
|--------|-------------|---------|
| `WithMaxResults(n)` | Max search results | 5 |
| `WithTimeout(d)` | HTTP request timeout | 10s |
| `WithMaxCharsPerResult(n)` | Max chars per snippet | 300 |
| `WithClient(c)` | Custom `*http.Client` | — |

## See Also

- [Agent API Reference](agent-api.md) — `agent.New` constructor and how tools are passed to the agent
- [Middleware](middleware.md) — wrapping tool execution with cross-cutting concerns
- [Structured Output](structured-output.md) — `InvokeStructured[T]` uses `tool.GenerateSchema[T]` under the hood
- [Multi-Agent Composition](multi-agent.md) — `AgentAsTool` wraps a child agent as a tool
- [Message Types](message-types.md) — `ToolUseBlock` and `ToolResultBlock` content blocks
