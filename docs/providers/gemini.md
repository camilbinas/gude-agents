# Gemini Provider

The `gemini` package uses the Google GenAI SDK (`google.golang.org/genai`) for Gemini models via the Gemini API.

Import: `github.com/camilbinas/gude-agents/agent/provider/gemini`

## Constructor

```go
func New(model string, opts ...Option) (*GeminiProvider, error)
```

Creates a provider for any Gemini model by ID. Reads the API key from `GEMINI_API_KEY`, falling back to `GOOGLE_API_KEY`.

## Options

| Option | Description |
|--------|-------------|
| `WithAPIKey(key string)` | Gemini API key (defaults to `GEMINI_API_KEY` → `GOOGLE_API_KEY` env vars) |
| `WithMaxTokens(n int64)` | Max output tokens (default: 4096) |
| `WithThinking(effort string)` | Enable extended thinking: `provider.ThinkingLow`, `ThinkingMedium`, `ThinkingHigh` |

## Model Constructors

| Constructor | Model ID |
|-------------|----------|
| `Gemini25Pro()` | `gemini-2.5-pro` |
| `Gemini25Flash()` | `gemini-2.5-flash` |
| `Gemini25FlashLite()` | `gemini-2.5-flash-lite` |
| `Gemini3Flash()` | `gemini-3-flash-preview` |
| `Gemini31Pro()` | `gemini-3.1-pro-preview` |
| `Gemini31FlashLite()` | `gemini-3.1-flash-lite-preview` |

## Tier Aliases

| Alias | Model |
|-------|-------|
| `Cheapest()` | `gemini-2.5-flash-lite` |
| `Standard()` | `gemini-2.5-flash` |
| `Smartest()` | `gemini-2.5-pro` |

> **Embedder functions** (`GeminiEmbedding001`, `GeminiEmbedding002`) are in `github.com/camilbinas/gude-agents/agent/rag/gemini`. See [RAG Pipeline](../rag.md) for usage.

## Code Example

```go
provider, err := gemini.Gemini25Flash()
if err != nil {
    log.Fatal(err)
}

a, err := agent.Default(
    provider,
    prompt.Text("You are a helpful assistant."),
    nil,
)
```

## See Also

- [LLM Providers Overview](../providers.md) — interfaces, extended thinking, direct SDK access, custom providers
- [RAG Pipeline](../rag.md) — Gemini embedder implementations
