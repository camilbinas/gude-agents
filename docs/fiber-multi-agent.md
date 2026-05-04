# Multi-Agent HTTP Server with Fiber v3

This guide walks through building a production-ready multi-user agent server using [Fiber v3](https://docs.gofiber.io/) and the orchestrator + worker pattern. A single set of agents serves all users concurrently, with per-request conversation IDs and streaming responses.

If you haven't read [HTTP & Multi-Tenant Environments](http.md) yet, start there for the core concepts (`WithSharedConversation`, `WithConversationID`). This guide builds on those patterns with Fiber-specific implementation details.

## Architecture

```
                    ┌─────────────────────────────────┐
  POST /chat ──────▶│         Fiber v3 Server         │
                    │         (SSE streaming)         │
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

## Full Example

```go
package main

import (
	"bufio"
	"context"
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/gofiber/fiber/v3"
)

type ChatRequest struct {
	Message        string `json:"message"`
	ConversationID string `json:"conversation_id"`
}

func main() {
	haiku, err := bedrock.Cheapest()
	if err != nil {
		log.Fatal(err)
	}
	sonnet, err := bedrock.Standard()
	if err != nil {
		log.Fatal(err)
	}

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

	store := conversation.NewInMemory() // Use redis, postgres... for production

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
		agent.WithSharedConversation(store),
	)
	if err != nil {
		log.Fatal(err)
	}

	app := fiber.New()
	app.Post("/chat", handleChat(orchestrator))
	log.Fatal(app.Listen(":3000"))
}

func handleChat(a *agent.Agent) fiber.Handler {
	return func(c fiber.Ctx) error {
		var req ChatRequest
		if err := c.Bind().JSON(&req); err != nil {
			return c.Status(400).JSON(fiber.Map{"error": "invalid request"})
		}
		if req.ConversationID == "" {
			return c.Status(400).JSON(fiber.Map{"error": "conversation_id is required"})
		}

		c.Set("Content-Type", "text/event-stream")
		c.Set("Cache-Control", "no-cache")
		c.Set("Connection", "keep-alive")

		return c.SendStreamWriter(func(w *bufio.Writer) {
			ctx := agent.NewContext(c.Context()).WithConversationID(req.ConversationID)

			err := a.InvokeStream(ctx, req.Message, func(chunk string) {
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

### Client-Side Consumption

```javascript
const res = await fetch("/chat", {
  method: "POST",
  headers: {"Content-Type": "application/json"},
  body: JSON.stringify({message: "Hello", conversation_id: "abc"}),
});
const reader = res.body.getReader();
const decoder = new TextDecoder();
while (true) {
  const {done, value} = await reader.read();
  if (done) break;
  console.log(decoder.decode(value));
}
```

Wrap with summary conversation to keep long conversations manageable:

```go
summary, err := conversation.NewSummary(store, 10, conversation.DefaultSummaryFunc(sonnet),
    conversation.WithPreserveRecentMessages(2),
)

orchestrator, err := agent.Orchestrator(sonnet, instructions, tools,
    agent.WithSharedConversation(summary),
)
```

## Thread Safety

All of these are safe for concurrent use from multiple Fiber handlers:

- `Agent.Invoke` / `InvokeStream` — conversation ID resolved from context, no shared mutable state
- `conversation.InMemory` — mutex-protected
- `redismemory.RedisConversation` — stateless, delegates to Redis
- `conversation.Summary` — per-conversation summarization locks
- `AgentAsTool` — child agents are invoked independently per tool call

## See Also

- [HTTP & Multi-Tenant Environments](http.md) — core concepts for multi-user agents
- [Multi-Agent Composition](multi-agent.md) — orchestrator + worker pattern details
- [Conversation System](conversation.md) — conversation strategies and summary
- [Redis](redis.md) — Redis conversation store and connection configuration
- [Providers](providers.md) — using different models for orchestrator vs workers
