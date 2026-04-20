# Multi-Agent HTTP Server with Fiber v3

This guide walks through building a production-ready multi-user agent server using [Fiber v3](https://docs.gofiber.io/) and the orchestrator + worker pattern. A single set of agents serves all users concurrently, with per-request conversation IDs and streaming responses.

If you haven't read [HTTP & Multi-Tenant Environments](http.md) yet, start there for the core concepts (`WithSharedMemory`, `WithConversationID`). This guide builds on those patterns with Fiber-specific implementation details.

## Architecture

```
                    ┌─────────────────────────────────┐
  POST /chat ──────▶│         Fiber v3 Server         │
  GET  /chat/stream │                                 │
                    │  ┌───────────────────────────┐  │
                    │  │      Orchestrator         │  │
                    │  │   (Sonnet, shared mem)    │  │
                    │  └──────┬──────────┬─────────┘  │
                    │         │          │            │
                    │     ┌────▼───┐ ┌────▼────┐      │
                    │     │ Worker │ │ Worker  │      │
                    │     │(Haiku) │ │(Haiku)  │      │
                    │     └────────┘ └─────────┘      │
                    └─────────────────────────────────┘
```

One orchestrator agent instance handles all requests. Each request provides a `conversation_id` via the context, so conversations are isolated without creating agents per user.

## Dependencies

```bash
go get github.com/gofiber/fiber/v3
go get github.com/camilbinas/gude-agents
go get github.com/camilbinas/gude-agents/agent/provider/bedrock
```

## Full Example

```go
package main

import (
	"bufio"
	"context"
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/memory"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/gofiber/fiber/v3"
)

func main() {
	// --- Providers ---
	haiku, err := bedrock.Cheapest()
	if err != nil {
		log.Fatal(err)
	}
	sonnet, err := bedrock.Standard()
	if err != nil {
		log.Fatal(err)
	}

	// --- Workers ---
	projectWorker, err := agent.Worker(haiku,
		prompt.Text("You are a project researcher. Look up project details and summarize them."),
		[]tool.Tool{projectSearchTool()},
	)
	if err != nil {
		log.Fatal(err)
	}

	financeWorker, err := agent.Worker(haiku,
		prompt.Text("You are a financial analyst. Look up revenue data and present clear summaries."),
		[]tool.Tool{revenueTool()},
	)
	if err != nil {
		log.Fatal(err)
	}

	// --- Shared memory ---
	store := memory.NewStore() // Use redis for production

	// --- Orchestrator (single instance, shared across all requests) ---
	orchestrator, err := agent.Orchestrator(sonnet,
		prompt.Text(
			"You are a helpful assistant for a digital agency. "+
				"Route questions to the right specialist and synthesize their responses.\n"+
				"- ask_projects: project details, statuses, deadlines\n"+
				"- ask_finance: revenue, forecasts, billing",
		),
		[]tool.Tool{
			agent.AgentAsTool("ask_projects", "Ask about project details and statuses.", projectWorker),
			agent.AgentAsTool("ask_finance", "Ask about revenue and financial data.", financeWorker),
		},
		agent.WithSharedMemory(store),
	)
	if err != nil {
		log.Fatal(err)
	}

	// --- Fiber app ---
	app := fiber.New()

	app.Post("/chat", handleChat(orchestrator))
	app.Get("/chat/stream", handleStream(orchestrator))

	log.Fatal(app.Listen(":3000"))
}
```

## Request / Response Types

```go
type ChatRequest struct {
	Message        string `json:"message"`
	ConversationID string `json:"conversation_id"`
}

type ChatResponse struct {
	Response       string `json:"response"`
	ConversationID string `json:"conversation_id"`
}
```

## JSON Endpoint (POST /chat)

The simplest approach — collect the full response and return it as JSON.

```go
func handleChat(a *agent.Agent) fiber.Handler {
	return func(c fiber.Ctx) error {
		var req ChatRequest
		if err := c.Bind().JSON(&req); err != nil {
			return c.Status(400).JSON(fiber.Map{"error": "invalid request"})
		}
		if req.ConversationID == "" {
			return c.Status(400).JSON(fiber.Map{"error": "conversation_id is required"})
		}

		ctx := agent.WithConversationID(c.Context(), req.ConversationID)

		result, _, err := a.Invoke(ctx, req.Message)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}

		return c.JSON(ChatResponse{
			Response:       result,
			ConversationID: req.ConversationID,
		})
	}
}
```

## Streaming Endpoint (GET /chat/stream)

For real-time output, use Fiber v3's `SendStreamWriter` with `InvokeStream`. Tokens are flushed to the client as they arrive from the LLM.

```go
func handleStream(a *agent.Agent) fiber.Handler {
	return func(c fiber.Ctx) error {
		msg := c.Query("message")
		convID := c.Query("conversation_id")
		if msg == "" || convID == "" {
			return c.Status(400).JSON(fiber.Map{"error": "message and conversation_id required"})
		}

		c.Set("Content-Type", "text/event-stream")
		c.Set("Cache-Control", "no-cache")
		c.Set("Connection", "keep-alive")

		return c.SendStreamWriter(func(w *bufio.Writer) {
			ctx := agent.WithConversationID(c.Context(), convID)

			_, err := a.InvokeStream(ctx, msg, func(chunk string) {
				fmt.Fprintf(w, "data: %s\n\n", chunk)
				w.Flush() //nolint:errcheck
			})
			if err != nil {
				fmt.Fprintf(w, "event: error\ndata: %s\n\n", err.Error())
				w.Flush() //nolint:errcheck
			}

			fmt.Fprintf(w, "event: done\ndata: [DONE]\n\n")
			w.Flush() //nolint:errcheck
		})
	}
}
```

### Client-Side Consumption

```javascript
const source = new EventSource("/chat/stream?conversation_id=abc&message=Hello");
source.onmessage = (e) => console.log(e.data);
source.addEventListener("done", () => source.close());
source.addEventListener("error", (e) => console.error(e.data));
```

## Fiber v3 vs v2 Streaming

If you're migrating from Fiber v2, the streaming API changed:

```go
// Fiber v2 — uses fasthttp directly
c.Context().SetBodyStreamWriter(fasthttp.StreamWriter(func(w *bufio.Writer) {
    // ...
}))

// Fiber v3 — first-class method on the context
c.SendStreamWriter(func(w *bufio.Writer) {
    // ...
})
```

`SendStreamWriter` is cleaner and returns an error, so you can handle stream setup failures. The `bufio.Writer` interface is the same — your `InvokeStream` callback code doesn't change.

## Stub Tools

For the example above, here are minimal tool stubs. Replace these with real database queries in production.

```go
func projectSearchTool() tool.Tool {
	return tool.NewString("search_projects", "Search projects by name",
		"query", "Search term to match against project names",
		func(_ context.Context, query string) (string, error) {
			return fmt.Sprintf(`[{"name": "%s", "status": "active", "deadline": "2026-06-01"}]`, query), nil
		},
	)
}

func revenueTool() tool.Tool {
	return tool.NewString("search_revenue", "Search revenue by project or customer",
		"query", "Project or customer name",
		func(_ context.Context, query string) (string, error) {
			return fmt.Sprintf(`[{"project": "%s", "revenue": "€42,000", "period": "2026-Q1"}]`, query), nil
		},
	)
}
```

Note the use of `tool.NewString` — for tools with a single string parameter, this is cleaner than defining a struct or writing a raw schema.

## Adding Redis for Production

Replace `memory.NewStore()` with Redis so conversations persist across restarts and scale horizontally:

```go
import redismemory "github.com/camilbinas/gude-agents/agent/memory/redis"

store, err := redismemory.NewRedisMemory(
    redismemory.RedisOptions{Addr: "127.0.0.1:6379"},
    redismemory.WithTTL(1 * time.Hour),
    redismemory.WithKeyPrefix("agency:memory:"),
)
if err != nil {
    log.Fatal(err)
}
```

Then wrap it with summary memory to keep long conversations manageable:

```go
summary, err := memory.NewSummary(store, 10, memory.DefaultSummaryFunc(sonnet),
    memory.WithPreserveRecentMessages(2),
)
if err != nil {
    log.Fatal(err)
}

orchestrator, err := agent.Orchestrator(sonnet, instructions, tools,
    agent.WithSharedMemory(summary),
)
```

## Thread Safety Recap

All of these are safe for concurrent use from multiple Fiber handlers:

- `Agent.Invoke` / `InvokeStream` — conversation ID resolved from context, no shared mutable state
- `memory.Store` — mutex-protected
- `redismemory.RedisMemory` — stateless, delegates to Redis
- `memory.Summary` — per-conversation summarization locks
- `AgentAsTool` — child agents are invoked independently per tool call

## See Also

- [HTTP & Multi-Tenant Environments](http.md) — core concepts for multi-user agents
- [Multi-Agent Composition](multi-agent.md) — orchestrator + worker pattern details
- [Memory](memory.md) — memory strategies and summary memory
- [Redis](redis.md) — Redis memory and connection configuration
- [Providers](providers.md) — using different models for orchestrator vs workers
