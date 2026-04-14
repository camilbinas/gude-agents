# Getting Started

This guide walks you through installing gude-agents, running your first agent, and understanding the core concepts you'll use in every project.

## Installation

```bash
go get github.com/camilbinas/gude-agents
```

## Minimal Working Example

The simplest agent creates a provider, builds an agent with `Default`, sends a message with `Invoke`, and prints the result:

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
)

func main() {
	// 1. Create a provider — this one uses Claude Sonnet 4.6 on Bedrock.
	provider, err := bedrock.ClaudeSonnet4_6()
	if err != nil {
		log.Fatal(err)
	}

	// 2. Create an agent with sensible defaults.
	a, err := agent.Default(
		provider,
		prompt.Text("You are a helpful assistant. Be concise."),
		nil, // no tools
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
| Bedrock | `AWS_REGION`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` | Uses the standard AWS credential chain. Configure your region and credentials as you would for any AWS SDK. |
| Anthropic | `ANTHROPIC_API_KEY` | Your Anthropic API key. Can also be set programmatically with `anthropic.WithAPIKey(key)`. |
| OpenAI | `OPENAI_API_KEY` | Your OpenAI API key. Can also be set programmatically with `openai.WithAPIKey(key)`. |

Bedrock relies on the AWS SDK's default credential chain, so any method that works for AWS (environment variables, `~/.aws/credentials`, IAM roles, etc.) will work here. You can override the region with `bedrock.WithRegion("us-west-2")`.

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

## Preset Constructors

gude-agents ships six preset constructors that configure an agent for common use cases. Most accept the same base parameters — `(provider, instructions, tools, ...Option)` — and return `(*Agent, error)`. `RAGAgent` adds a required `Retriever` parameter before tools.

### Default

```go
func Default(provider Provider, instructions prompt.Instructions, tools []tool.Tool, opts ...Option) (*Agent, error)
```

Configures: 5 max iterations, no logging.

Use `Default` for standalone agents — it's the right starting point for most applications. The 5-iteration limit gives the agent enough room to call tools and refine its answer without running away. Add `agent.WithLogger(log.Default())` if you want logging.

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

Use `RAGAgent` for retrieval-augmented generation. The `Retriever` is a required parameter, making it impossible to forget. Retrieved documents are automatically prepended to the system prompt before each LLM call.

All presets accept additional `...Option` arguments that are applied after the defaults, so you can override any setting:

```go
a, err := agent.Default(provider, instructions, tools,
	agent.WithMaxIterations(10), // override the default 5
)
```

## See Also

- [Agent API Reference](agent-api.md) — full list of options and methods
- [Providers](providers.md) — Bedrock, Anthropic, and OpenAI provider details
- [Multi-Agent Composition](multi-agent.md) — orchestrator + worker patterns
