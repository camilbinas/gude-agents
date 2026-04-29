// Example: Typed Redis memory with struct-to-field mapping as agent tools.
//
// Demonstrates using Redis TypedStore with Remember/Recall tools so the LLM
// can store and retrieve structured user preferences with filtered recall.
//
// Prerequisites:
//   - Redis Stack running locally (NOT standard Redis — requires RediSearch)
//
// Run:
//
//	go run ./memory-typed-redis
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
	"github.com/camilbinas/gude-agents/agent/logging/debug"
	memoryredis "github.com/camilbinas/gude-agents/agent/memory/redis"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/camilbinas/gude-agents/examples/utils"
	"github.com/joho/godotenv"
)

// Preference represents a user preference or setting.
// Same struct works for both Redis and Postgres — only the store differs.
type Preference struct {
	ID       string    `json:"id" db:"id,pk"`
	UserID   string    `json:"user_id" db:"user_id,identifier"`
	Title    string    `json:"title" db:"title,content" description:"Short description of the preference" required:"true"`
	Category string    `json:"category" db:"category" description:"Category: appearance, workflow, communication, tools" required:"true"`
	Value    string    `json:"value" db:"value" description:"The preference value or setting" required:"true"`
	Priority float64   `json:"priority" db:"priority,numeric" description:"How important this preference is 0.0-1.0" required:"true"`
	SavedAt  time.Time `json:"saved_at" db:"saved_at,noinput"`
}

func main() {
	godotenv.Load() //nolint

	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "127.0.0.1:6379"
	}

	embedder := bedrock.MustEmbedder(bedrock.TitanEmbedV2())

	mem, err := memoryredis.NewStore[Preference](
		memoryredis.Options{Addr: addr},
		embedder, 1024,
		memoryredis.WithIndexName("preferences_idx"),
		memoryredis.WithKeyPrefix("pref:"),
	)
	if err != nil {
		log.Fatalf("redis typed store: %v", err)
	}
	defer mem.Close()

	// Create tools — recall filters by priority.
	rememberTool := memoryredis.NewRememberTool(mem,
		memoryredis.WithToolName("save_preference"),
		memoryredis.WithToolDescription("Store a user preference or setting for later recall."),
	)
	recallTool := memoryredis.NewRecallTool(mem,
		memoryredis.WithToolName("get_preferences"),
		memoryredis.WithToolDescription("Retrieve relevant user preferences by semantic similarity."),
		memoryredis.WithFieldGT("priority", 0.2),
	)

	store := conversation.NewWindow(conversation.NewInMemory(), 20)

	a, err := agent.Default(
		bedrock.Must(bedrock.Standard()),
		prompt.Text(
			"You are a personal assistant that remembers user preferences. "+
				"Use save_preference to store preferences the user shares (appearance, workflow, tools, communication style). "+
				"Use get_preferences to retrieve relevant preferences when answering questions. "+
				"Always check preferences before making suggestions.",
		),
		[]tool.Tool{rememberTool, recallTool},
		agent.WithConversation(store, "preferences-session"),
		debug.WithLogging(),
	)
	if err != nil {
		log.Fatal(err)
	}

	ctx := agent.WithIdentifier(context.Background(), "user_1")

	fmt.Println()
	fmt.Println("Personal assistant with typed Redis preference memory.")
	fmt.Println("Try: 'I prefer dark mode and vim keybindings' then 'What are my editor preferences?'")
	fmt.Println()

	utils.Chat(ctx, a)
}
