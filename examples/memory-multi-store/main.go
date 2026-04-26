// Example: Multi-store memory with distinct tool names (Redis-backed).
//
// Demonstrates an agent with two separate Redis-backed memory stores — one for
// user preferences and one for project context — each with its own tool names
// and its own RediSearch index. The LLM sees four tools and decides which store
// to use based on the tool descriptions.
//
// Because each store is a separate rag/redis.VectorStore with its own index,
// preferences and project facts are fully isolated at the Redis level. The
// ScopedStore wrapper adds per-user partitioning within each index using native
// TAG filtering.
//
// This pattern is only possible with the composable building blocks
// (NewRememberTool / NewRecallTool + WithToolName). The EpisodicMemory
// interface always produces tools named "remember" and "recall", so you
// can't have multiple stores in one agent with that approach.
//
// Prerequisites:
//   - AWS credentials configured (env vars, ~/.aws/credentials, or IAM role)
//   - Redis Stack running locally (NOT standard Redis — requires RediSearch)
//
// Start Redis Stack:
//
//	docker run -p 6379:6379 redis/redis-stack-server:latest
//
// Optional env vars:
//   - REDIS_ADDR: Redis address (default: 127.0.0.1:6379)
//
// Sample session:
//
//	You: Remember that I prefer dark mode.
//	Agent: (uses remember_preferences) Stored your preference.
//
//	You: Remember that Project Alpha uses PostgreSQL 16.
//	Agent: (uses remember_projects) Stored that project detail.
//
//	You: What database does Project Alpha use?
//	Agent: (uses recall_projects) Project Alpha uses PostgreSQL 16.
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
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/rag"
	ragredis "github.com/camilbinas/gude-agents/agent/rag/redis"
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
	redisOpts := ragredis.Options{Addr: addr}

	// 1. Create separate Redis VectorStores with distinct indexes.
	prefVS, err := ragredis.New(redisOpts, "gude_preferences", 1024)
	if err != nil {
		log.Fatalf("preferences vectorstore: %v", err)
	}
	defer prefVS.Close()

	projVS, err := ragredis.New(redisOpts, "gude_projects", 1024)
	if err != nil {
		log.Fatalf("projects vectorstore: %v", err)
	}
	defer projVS.Close()

	// 2. Wrap each in a ScopedStore for per-user partitioning.
	//    ScopedStore detects the Redis ScopedSearcher interface and uses
	//    native TAG filtering automatically.
	prefStore := rag.NewScopedStore(prefVS)
	projStore := rag.NewScopedStore(projVS)

	// 3. Create tools with distinct names and descriptions per domain.
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

	// 4. Build the agent with all four tools.
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

	// 5. Set the identifier on the context.
	ctx := agent.WithIdentifier(context.Background(), "user-456")

	fmt.Println("Multi-store Redis memory agent. Type 'quit' to exit.")
	fmt.Println("Try: 'I prefer dark mode' then 'Project Alpha uses PostgreSQL 16'")
	fmt.Println("Then: 'What database does Project Alpha use?'")
	fmt.Println()

	utils.Chat(ctx, a)
}
