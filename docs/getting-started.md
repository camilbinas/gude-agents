# Getting Started

This guide walks you through installing gude-agents, running your first agent, and understanding the core concepts you'll use in every project.

## Installation

The core module provides the agent framework, tools, memory, and prompt system:

```bash
go get github.com/camilbinas/gude-agents
```

Then add the provider and driver modules you need:

```bash
# Pick a provider
go get github.com/camilbinas/gude-agents/agent/provider/bedrock     # AWS Bedrock
go get github.com/camilbinas/gude-agents/agent/provider/anthropic   # Anthropic
go get github.com/camilbinas/gude-agents/agent/provider/openai      # OpenAI
go get github.com/camilbinas/gude-agents/agent/provider/gemini      # Google Gemini

# Optional: conversation drivers (in-memory and disk are included in the core)
go get github.com/camilbinas/gude-agents/agent/conversation/redis         # Redis conversation
go get github.com/camilbinas/gude-agents/agent/conversation/dynamodb      # DynamoDB conversation
go get github.com/camilbinas/gude-agents/agent/conversation/s3            # S3 conversation
go get github.com/camilbinas/gude-agents/agent/conversation/sqlite        # SQLite conversation
go get github.com/camilbinas/gude-agents/agent/conversation/postgres      # PostgreSQL conversation

# Optional: RAG embedders and vector stores
go get github.com/camilbinas/gude-agents/agent/rag/bedrock          # Bedrock knowledge bases + embedders
go get github.com/camilbinas/gude-agents/agent/rag/openai           # OpenAI embedders
go get github.com/camilbinas/gude-agents/agent/rag/gemini           # Gemini embedders
go get github.com/camilbinas/gude-agents/agent/rag/redis            # Redis vector store
go get github.com/camilbinas/gude-agents/agent/rag/postgres         # PostgreSQL + pgvector

# Optional: MCP tool integration
go get github.com/camilbinas/gude-agents/agent/mcp
```

Each module only pulls the dependencies it needs — using Bedrock won't download the OpenAI or Gemini SDKs.

## Minimal Working Example

The simplest agent creates a provider, builds an agent with `Default`, sends a message with `Invoke`, and prints the result.

Make sure `AWS_REGION` is set before running (Bedrock defaults to `us-east-1` if unset):

```bash
export AWS_REGION=eu-central-1
```

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/logging/debug"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
)

func main() {
	// 1. Create a provider.
	provider, err := bedrock.Standard()
	if err != nil {
		log.Fatal(err)
	}

	// 2. Create an agent with sensible defaults and debug logging.
	a, err := agent.Default(
		provider,
		prompt.Text("You are a helpful assistant. Be concise."),
		nil, // no tools
		debug.WithLogging(),
	)
	if err != nil {
		log.Fatal(err)
	}

	// 3. Send a message and get the response.
	result, usage, err := a.Invoke(context.Background(), "What is the capital of France?")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(result)
	fmt.Printf("Tokens: %d in, %d out\n", usage.InputTokens, usage.OutputTokens)
}
```

## Provider Configuration

Each provider reads credentials from environment variables by default. Set the variables for the provider you want to use:

| Provider | Environment Variable | Description |
|----------|---------------------|-------------|
| Bedrock | `AWS_REGION`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` | Uses the standard AWS credential chain. Configure your region and credentials as you would for any AWS SDK. Alternatively, set `AWS_BEARER_TOKEN_BEDROCK` to use an API key instead of IAM credentials. |
| Anthropic | `ANTHROPIC_API_KEY` | Your Anthropic API key. Can also be set programmatically with `anthropic.WithAPIKey(key)`. |
| OpenAI | `OPENAI_API_KEY` | Your OpenAI API key. Can also be set programmatically with `openai.WithAPIKey(key)`. |

Bedrock relies on the AWS SDK's default credential chain, so any method that works for AWS (environment variables, `~/.aws/credentials`, IAM roles, etc.) will work here. You can override the region with `bedrock.WithRegion("eu-central-1")`.

> **Important:** `AWS_REGION` must be set when using the Bedrock provider. If unset, it defaults to `us-east-1`, which may not match the region where your models are enabled. Set it via environment variable or explicitly with `bedrock.WithRegion("your-region")`:
>
> ```bash
> export AWS_REGION=eu-central-1
> ```

## Invoke vs InvokeStream

The agent provides two ways to get a response:

### Invoke

`Invoke` is a blocking call that collects the full response into a single string and returns it:

```go
func (a *Agent) Invoke(ctx context.Context, userMessage string) (string, TokenUsage, error)
```

Use `Invoke` when you want the complete answer before processing it — the simplest option for scripts, CLI tools, and backend services.

### InvokeStream

`InvokeStream` delivers the response incrementally through a callback as the LLM generates tokens:

```go
func (a *Agent) InvokeStream(ctx context.Context, userMessage string, cb StreamCallback) (TokenUsage, error)
```

`StreamCallback` is `func(chunk string)`. Each call receives a text chunk as it arrives from the provider. Use `InvokeStream` when you need real-time output — chat UIs, server-sent events, or any scenario where perceived latency matters.

```go
usage, err := a.InvokeStream(ctx, "Tell me a joke", func(chunk string) {
	fmt.Print(chunk) // prints tokens as they arrive
})
```

Under the hood, `Invoke` is a thin wrapper around `InvokeStream` that concatenates all chunks into a string.

## Logging

gude-agents ships two logging packages. Both implement the same hook interfaces so you can swap them without changing your agent code.

### debug — colored output for local development

`agent/logging/debug` prints human-readable, ANSI-colored trace output to stdout. Zero configuration required — just add `debug.WithLogging()` as an option:

```go
import "github.com/camilbinas/gude-agents/agent/logging/debug"

a, err := agent.Default(provider, instructions, tools,
    debug.WithLogging(),
)
```

Not intended for production — the colored output is designed to be read while the agent is running, not aggregated.

## Preset Constructors

gude-agents ships six preset constructors that configure an agent for common use cases. Most accept the same base parameters — `(provider, instructions, tools, ...Option)` — and return `(*Agent, error)`. `RAGAgent` adds a required `Retriever` parameter before tools.

### Default

```go
func Default(provider Provider, instructions prompt.Instructions, tools []tool.Tool, opts ...Option) (*Agent, error)
```

Configures: 5 max iterations, no logging.

Use `Default` for standalone agents — it's the right starting point for most applications. The 5-iteration limit gives the agent enough room to call tools and refine its answer without running away.

### Worker

```go
func Worker(provider Provider, instructions prompt.Instructions, tools []tool.Tool, opts ...Option) (*Agent, error)
```

Configures: logging enabled, 3 max iterations.

Use `Worker` for sub-agents in a multi-agent setup. The lower iteration limit keeps child agents focused and prevents them from consuming too many tokens on a single delegation.

### Orchestrator

```go
func Orchestrator(provider Provider, instructions prompt.Instructions, tools []tool.Tool, opts ...Option) (*Agent, error)
```

Configures: logging enabled, 5 max iterations, parallel tool execution.

Use `Orchestrator` for a parent agent that routes work to other agents. Parallel tool execution lets it dispatch to multiple child agents concurrently, which is critical when the orchestrator delegates to several specialists in a single turn.

### Testing

```go
func Testing(provider Provider, instructions prompt.Instructions, tools []tool.Tool, opts ...Option) (*Agent, error)
```

Configures: logging enabled, 3 max iterations, 4096 token budget.

Use `Testing` during development and testing. Logging is on so you can see what's happening, the low iteration limit and token budget prevent runaway costs, and the whole thing is easy to spin up in a test file or scratch script.

### Minimal

```go
func Minimal(provider Provider, instructions prompt.Instructions, tools []tool.Tool, opts ...Option) (*Agent, error)
```

Configures: 3 max iterations, no logging.

Use `Minimal` when you want the least opinionated agent possible. No logging, low iteration count, nothing extra. Good for embedding agents inside larger systems, quick scripts, or unit tests where you don't want any noise.

### RAGAgent

```go
func RAGAgent(provider Provider, instructions prompt.Instructions, r Retriever, tools []tool.Tool, opts ...Option) (*Agent, error)
```

Configures: logging enabled, 5 max iterations, retriever attached.

Use `RAGAgent` for retrieval-augmented generation. The `Retriever` is a required parameter, making it impossible to forget. Retrieved documents are automatically injected as a user/assistant message turn before each LLM call — keeping untrusted content isolated from the system prompt.

All presets accept additional `...Option` arguments that are applied after the defaults, so you can override any setting:

```go
a, err := agent.Default(provider, instructions, tools,
	agent.WithMaxIterations(10), // override the default 5
)
```

## Running the Examples

The `examples/` directory is a separate Go module with its own `go.mod`. Run examples from inside that directory:

```bash
cd examples
go run ./getting-started
```

Most examples read configuration from `examples/.env` via godotenv. Create one with your credentials:

```bash
# examples/.env
AWS_REGION=eu-central-1
ANTHROPIC_API_KEY=sk-ant-...
OPENAI_API_KEY=sk-...
POSTGRES_URL=postgres://user:pass@localhost:5432/mydb?sslmode=disable
REDIS_ADDR=localhost:6379
```

Only the variables relevant to the example you're running need to be set.

## See Also

- [Agent API Reference](agent-api.md) — full list of options and methods
- [Providers](providers.md) — Bedrock, Anthropic, and OpenAI provider details
- [Multi-Agent Composition](multi-agent.md) — orchestrator + worker patterns
