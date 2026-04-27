// Example: Multi-store memory with distinct tool names (Redis-backed).
//
// Two separate Redis-backed memory stores — one for user preferences, one for
// project context — each with its own RediSearch index and tool names.
//
// Prerequisites:
//   - Redis Stack running locally (NOT standard Redis — requires RediSearch)
//
// Run:
//
//	go run ./memory-multi-store
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
	"github.com/camilbinas/gude-agents/agent/logging/debug"
	"github.com/camilbinas/gude-agents/agent/memory"
	memredis "github.com/camilbinas/gude-agents/agent/memory/redis"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/camilbinas/gude-agents/examples/utils"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load() //nolint

	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "127.0.0.1:6379"
	}

	embedder := bedrock.MustEmbedder(bedrock.TitanEmbedV2())
	redisOpts := memredis.Options{Addr: addr}

	// 1. Create separate Redis memory stores with distinct indexes.
	prefStore, err := memredis.New(redisOpts, embedder, 1024,
		memredis.WithIndexName("gude_preferences"),
		memredis.WithKeyPrefix("gude:preferences:"),
	)
	if err != nil {
		log.Fatalf("preferences memory store: %v", err)
	}
	defer prefStore.Close()

	projStore, err := memredis.New(redisOpts, embedder, 1024,
		memredis.WithIndexName("gude_projects"),
		memredis.WithKeyPrefix("gude:projects:"),
	)
	if err != nil {
		log.Fatalf("projects memory store: %v", err)
	}
	defer projStore.Close()

	// 2. Create tools with distinct names and descriptions per domain.
	//    Each Store implements MemoryStore and uses a native TAG field for
	//    per-user partitioning within its index.
	tools := []tool.Tool{
		// Preferences
		memory.NewRememberTool(prefStore, embedder,
			memory.WithToolName("remember_preferences"),
			memory.WithToolDescription("Store a user preference or setting for later recall."),
		),
		memory.NewRecallTool(prefStore, embedder,
			memory.WithToolName("recall_preferences"),
			memory.WithToolDescription("Retrieve user preferences and settings."),
		),
		// Projects
		memory.NewRememberTool(projStore, embedder,
			memory.WithToolName("remember_projects"),
			memory.WithToolDescription("Store a project decision, detail, or context for later recall."),
		),
		memory.NewRecallTool(projStore, embedder,
			memory.WithToolName("recall_projects"),
			memory.WithToolDescription("Retrieve project decisions, details, and context."),
		),
	}

	store, _ := conversation.NewSummary(
		conversation.NewInMemory(), 40,
		conversation.DefaultSummaryFunc(bedrock.Must(bedrock.Cheapest())),
	)

	// 3. Build the agent with all four tools.
	a, err := agent.Default(
		bedrock.Must(bedrock.Standard()),
		prompt.RISEN{
			Role:         "You are a friendly project assistant who remembers everything about the user and their projects.",
			Instructions: "You have two memory stores: one for user preferences and one for project context. Use the right store for each piece of information. Always recall before answering so you can reference what you already know.",
			Steps:        "1) When the user shares a preference, store it with remember_preferences. 2) When the user shares project info, store it with remember_projects. 3) Before answering questions, recall from the relevant store. 4) Respond naturally, weaving recalled context into the conversation.",
			EndGoal:      "Be a knowledgeable assistant who never forgets and always connects the dots between what the user has shared.",
			Narrowing:    "Keep responses conversational and concise. Don't list raw tool output — synthesize it into a natural answer.",
		},
		tools,
		debug.WithLogging(),
		agent.WithConversation(store, "multi-store-session"),
	)
	if err != nil {
		log.Fatal(err)
	}

	// 4. Set the identifier on the context.
	ctx := agent.WithIdentifier(context.Background(), "user-456")

	fmt.Println("Multi-store Redis memory agent. Type 'quit' to exit.")
	fmt.Println("Try: 'I prefer dark mode' then 'Project Alpha uses PostgreSQL 16'")
	fmt.Println("Then: 'What database does Project Alpha use?'")
	fmt.Println()

	utils.Chat(ctx, a)
}
