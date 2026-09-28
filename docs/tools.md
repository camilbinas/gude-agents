# Tool System

The tool system lets you define functions that the LLM can invoke during a conversation. You describe a tool's name, purpose, and input schema — the framework handles marshalling, unmarshalling, and wiring it into the agent loop.

| Constructor | Input | Use Case |
|-------------|-------|----------|
| `tool.New[T]` | Auto-generated from struct tags | Most tools — typed, safe, concise |
| `tool.NewRaw` | Hand-crafted JSON Schema + `json.RawMessage` | Full schema control |
| `tool.NewSimple` | None | Tools with no parameters (e.g. current time) |
| `tool.NewString` | Single required string | Simple query tools |
| `tool.NewAsync` | Same as `New[T]` | Fire-and-forget side effects |
| `tool.NewBackground` | Same as `New[T]` | Long-running work with result re-injection |
| `tool.NewRich` | Same as `New[T]` | Tools that return text + images |

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

## tool.NewBackground — Background Tools (Long-Running with Re-Entry)

```go
func NewBackground[T any](name, description, ack string, handler BackgroundHandler[T]) Tool
```

`NewBackground` creates a tool whose handler runs in a detached goroutine. The LLM receives the `ack` string immediately (like `NewAsync`), but when the handler completes, the result is automatically injected back into the conversation and a new LLM turn is triggered so the agent can react. The application receives the reactive response via `WithBackgroundNotify`.

Requires a conversation store (`WithConversation` or `WithSharedConversation`). The handler runs on `context.Background()` — it survives request cancellation.

```go
type DeployInput struct {
    Service string `json:"service" description:"Service to deploy" required:"true"`
    Version string `json:"version" description:"Target version"    required:"true"`
}

deployTool := tool.NewBackground("deploy", "Deploy a service version",
    "Deployment started — I'll notify you when it completes.",
    func(ctx context.Context, in DeployInput) (string, error) {
        result, err := ci.Deploy(ctx, in.Service, in.Version)
        if err != nil {
            return "", err
        }
        return fmt.Sprintf("Deployed %s@%s: %s", in.Service, in.Version, result), nil
    },
)

a, _ := agent.New(provider, instructions, []tool.Tool{deployTool},
    agent.WithConversation(store, "conv-1"),
    agent.WithBackgroundNotify(func(convID, msg string) {
        pushToUser(convID, msg) // SSE, websocket, queue, etc.
    }),
)
```

`NewBackgroundRaw` is the raw JSON variant:

```go
func NewBackgroundRaw(name, description, ack string, schema map[string]any, handler func(ctx context.Context, input json.RawMessage) (string, error)) Tool
```

See `examples/background-deploy/`.

## Constructor Options

All constructors (`New`, `NewRaw`, `NewSimple`, `NewString`, `NewAsync`, `NewBackground`, `NewRich`, and their `*Raw` variants) accept a variadic `opts ...func(*Tool)` parameter. Pass any combination of the following options:

| Option | Description |
|--------|-------------|
| `tool.RequiresApproval()` | Marks the tool as requiring explicit human approval before execution. See [Tool Approval](tool-approval.md) |
| `tool.AllowRoles(roles ...string)` | Restricts calls to principals that hold at least one of the given roles. See [RBAC & Identity](rbac.md) |
| `tool.DenyRoles(roles ...string)` | Blocks callers that hold any of the given roles. See [RBAC & Identity](rbac.md) |
| `tool.WithGuard[T](guard)` | Runs a typed guard function before the handler for per-call authorization. See below |

### tool.WithGuard[T] — Per-Call Authorization

```go
func WithGuard[T any](guard func(ctx context.Context, input T) (Decision, error)) func(*Tool)
```

`WithGuard` attaches an inline authorization check that runs before the tool handler. The guard receives the fully deserialized input and returns a `Decision`. If the guard denies, the handler is not invoked and the LLM receives a structured denial result.

```go
deleteTool := tool.New("delete_record", "Delete a record by ID",
    func(ctx context.Context, in DeleteInput) (string, error) {
        return deleteRecord(in.ID)
    },
    tool.WithGuard(func(ctx context.Context, in DeleteInput) (tool.Decision, error) {
        c := agent.FromContext(ctx)
        if c == nil {
            return tool.Deny("no agent context"), nil
        }
        p, ok := agent.PrincipalFrom(c)
        if !ok || !p.HasRole("admin") {
            return tool.Denyf("role admin required, got %v", p.Roles), nil
        }
        return tool.Allow(), nil
    }),
)
```

Use `WithGuard` for synchronous, per-call checks. For asynchronous human approval flows (Slack, HTTP callbacks) use `tool.RequiresApproval()` instead.

### tool.Decision

```go
type Decision struct {
    Allow  bool
    Reason string
}
```

`Decision` is the return type of guard functions and the type passed to `Agent.ResumeWithApproval`. Set `Allow: true` to permit the call, or `Allow: false` with a human-readable `Reason` to deny it. See [Tool Approval](tool-approval.md) for the full approval flow.

Convenience constructors:

| Function | Result |
|----------|--------|
| `tool.Allow()` | `Decision{Allow: true}` |
| `tool.Deny(reason)` | `Decision{Allow: false, Reason: reason}` |
| `tool.Denyf(format, ...)` | `Decision{Allow: false, Reason: fmt.Sprintf(...)}` |

## Tool Introspection

`Tool` exposes read-only methods for inspecting its configuration:

| Method | Returns | Description |
|--------|---------|-------------|
| `NeedsApproval() bool` | `bool` | Reports whether the tool was tagged with `RequiresApproval()` |
| `IsBackground() bool` | `bool` | Reports whether the tool was created via `NewBackground` / `NewBackgroundRaw` |
| `Ack() string` | `string` | Returns the acknowledgement string supplied at construction time (empty for non-background tools) |
| `RolesAllowed(roles []string) bool` | `bool` | Reports whether the given roles satisfy the tool's role policy; returns `true` when no policy is set |

These are useful for middleware, routers, and approval handlers that need to inspect a tool's properties before or after dispatch.

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

Typed tool with struct tags that the LLM can call:

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

a, _ := agent.Default(provider,
    prompt.Text("You are a helpful assistant with access to weather data."),
    []tool.Tool{weatherTool},
)
result, _ := a.Invoke(agent.Background(), "What's the weather in Berlin?")
```

See `examples/tool-presets/`, `examples/tool-filter/`, `examples/tool-images/`.

The generated JSON Schema for `WeatherInput`:

```json
{
  "type": "object",
  "properties": {
    "city": {"type": "string", "description": "The city to get weather for"},
    "units": {"type": "string", "description": "Temperature units", "enum": ["celsius", "fahrenheit"]}
  },
  "required": ["city"]
}
```

## Accessing Invocation State from Tool Handlers

Tool handlers keep `context.Context` in their signature to avoid import cycles between the `tool` and `agent` packages. At runtime, the agent passes `*agent.Context` (which embeds `context.Context`) directly to tool handlers. Use `agent.FromContext` to safely extract it:

```go
lookup := tool.New("lookup_user", "Looks up a user by ID",
    func(ctx context.Context, input LookupInput) (string, error) {
        if c := agent.FromContext(ctx); c != nil {
            userRole, _ := c.Get("user_role")
            c.Set("last_lookup", input.UserID)
        }
        return fmt.Sprintf("User %s found", input.UserID), nil
    },
)
```

`FromContext` returns nil if `ctx` is not a `*Context` — no panic risk. Most tools don't need this; they just use `context.Context` for cancellation and deadlines.

`agent.ToolLoggerFrom(ctx)` extracts a logger for emitting messages during tool execution. Returns a no-op logger when no logging hook is configured. Tool packages that cannot import `agent` (e.g., `tool/webfetch`) use `tool.LoggerFrom(ctx)` directly.

```go
search := tool.New("search", "Search the knowledge base",
    func(ctx context.Context, in SearchInput) (string, error) {
        log := agent.ToolLoggerFrom(ctx)
        log.Logf("querying %q", in.Query)
        results := doSearch(in.Query)
        log.Logf("found %d results", len(results))
        return formatResults(results), nil
    },
)
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

#### Serper

```go
import "github.com/camilbinas/gude-agents/agent/tool/websearch/serper"

searchTool := serper.New(os.Getenv("SERPER_API_KEY"))
```

Requires `SERPER_API_KEY` from [serper.dev](https://serper.dev).

| Option | Description | Default |
|--------|-------------|---------|
| `WithMaxResults(n)` | Max search results | 5 |
| `WithTimeout(d)` | HTTP request timeout | 10s |
| `WithMaxCharsPerResult(n)` | Max chars per snippet | 300 |
| `WithClient(c)` | Custom `*http.Client` | — |

#### SerpAPI

SerpAPI supports 80+ search engines including Google, Google Scholar, Google News, Google Finance, and more.

```go
import "github.com/camilbinas/gude-agents/agent/tool/websearch/serpapi"

// Web search (Google)
searchTool := serpapi.New(os.Getenv("SERPAPI_API_KEY"))

// Specialized engines
newsTool    := serpapi.NewNews(os.Getenv("SERPAPI_API_KEY"))
scholarTool := serpapi.NewScholar(os.Getenv("SERPAPI_API_KEY"))
financeTool := serpapi.NewFinance(os.Getenv("SERPAPI_API_KEY"))

// Generic escape hatch for any engine
flightsTool := serpapi.NewEngine(apiKey, "google_flights", serpapi.EngineConfig{
    ToolName:    "flights_search",
    Description: "Search Google Flights for routes and prices.",
})
```

Requires `SERPAPI_API_KEY` from [serpapi.com/manage-api-key](https://serpapi.com/manage-api-key).

| Constructor | Tool Name | Engine | Description |
|-------------|-----------|--------|-------------|
| `New` | `web_search` | `google` | General web search |
| `NewNews` | `news_search` | `google_news` | Current news articles |
| `NewScholar` | `scholar_search` | `google_scholar` | Academic papers with citations |
| `NewFinance` | `finance_search` | `google_finance` | Stock prices and market data |
| `NewEngine` | configurable | any | Generic escape hatch for all 80+ engines |

| Option | Description | Default |
|--------|-------------|---------|
| `WithMaxResults(n)` | Max search results | 5 |
| `WithTimeout(d)` | HTTP request timeout | 10s (20s for Scholar) |
| `WithMaxCharsPerResult(n)` | Max chars per snippet | 300 |
| `WithEngine(e)` | Search engine (for `New` only) | `"google"` |
| `WithClient(c)` | Custom `*http.Client` | — |

## See Also

- [Agent API Reference](agent-api.md) — `agent.New` constructor and how tools are passed to the agent
- [Middleware](middleware.md) — wrapping tool execution with cross-cutting concerns
- [Structured Output](structured-output.md) — `InvokeStructured[T]` uses `tool.GenerateSchema[T]` under the hood
- [Multi-Agent Composition](multi-agent.md) — `AgentAsTool` wraps a child agent as a tool
- [Message Types](message-types.md) — `ToolUseBlock` and `ToolResultBlock` content blocks
