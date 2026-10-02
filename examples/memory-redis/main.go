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
//	go run ./memory-redis

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/contextmanager"
	"github.com/camilbinas/gude-agents/agent/conversation"
	"github.com/camilbinas/gude-agents/agent/logging/auto"
	"github.com/camilbinas/gude-agents/agent/memory"
	memoryredis "github.com/camilbinas/gude-agents/agent/memory/redis"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/camilbinas/gude-agents/agent/tool/webfetch"
	"github.com/camilbinas/gude-agents/agent/tool/websearch/tavily"
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

type recallInput struct {
	Query string `json:"query" description:"A natural-language query describing what to recall." required:"true"`
	Limit int    `json:"limit" description:"Maximum number of results to return. Defaults to 5."`
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

	rememberTool := memoryredis.NewRememberTool(mem,
		memoryredis.WithToolName("save_preference"),
		memoryredis.WithToolDescription("Store any user information: name, preferences, settings, facts, decisions, or context they share."),
	)
	updateTool := memoryredis.NewUpdateTool(mem,
		memoryredis.WithToolName("update_preference"),
		memoryredis.WithToolDescription("Update an existing memory entry by its ID when the user corrects or changes a preference."),
	)
	recallTool := newRecallTool(mem)
	forgetTool := memoryredis.NewForgetTool(mem,
		memoryredis.WithToolName("forget_preference"),
		memoryredis.WithToolDescription("Remove a specific memory entry when the user asks to forget something."),
	)

	store := conversation.NewInMemory()
	manager, err := contextmanager.NewWindow(store, 20)
	if err != nil {
		log.Fatal(err)
	}

	a, err := agent.New(
		bedrock.Must(bedrock.Standard()),
		"You are a personal assistant that remembers everything the user tells you about themselves. Use save_preference to store ANY personal information: name, preferences, settings, facts about the user, decisions, or context they share. Use update_preference to correct or change an existing preference by its ID when the user updates something. Use get_preferences to retrieve relevant information when answering questions or before making suggestions. ALWAYS save when the user shares personal info (name, role, preferences, tools they use, etc). ALWAYS recall before answering questions about the user.",
		agent.WithTools(rememberTool, updateTool, recallTool, forgetTool, tavily.New(os.Getenv("TAVILY_API_KEY")), webfetch.New()),
		agent.WithConversationStore(store),
		agent.WithContextManager(manager),
		auto.WithLogging(),
	)
	if err != nil {
		log.Fatal(err)
	}

	ctx := agent.Background().WithIdentity("user-123").WithConversationID("preferences-session")

	fmt.Println()
	fmt.Println("Personal assistant with Redis memory. Type 'quit' to exit, 'clear' to forget all.")
	fmt.Println("Try: 'My name is Alice' then 'What do you know about me?'")
	fmt.Println()

	utils.Chat(ctx, a, utils.ChatOptions{
		ClearFunc: func(ctx context.Context) error {
			if err := store.Delete(ctx, "preferences-session"); err != nil {
				return err
			}
			return mem.ForgetAll(ctx, "user-123")
		},
	})
}

func newRecallTool(mem *memoryredis.Store[Preference]) tool.Tool {
	return tool.New("get_preferences", "Retrieve relevant user information and preferences by semantic similarity.",
		func(ctx context.Context, input recallInput) (string, error) {
			identity, err := memory.ResolveIdentity(ctx, "")
			if err != nil {
				return "", err
			}
			limit := input.Limit
			if limit == 0 {
				limit = 5
			}
			results, err := mem.Recall(ctx, identity, memory.RecallQuery{
				Text:  input.Query,
				Limit: limit,
				Filters: []memory.Filter{{
					Field: "priority", Operator: memory.FilterGreaterThan, Value: 0.2,
				}},
			})
			if err != nil {
				return "", err
			}
			if len(results) == 0 {
				return "No relevant memories found.", nil
			}
			data, err := json.Marshal(results)
			if err != nil {
				return "", fmt.Errorf("marshal recalled preferences: %w", err)
			}
			return string(data), nil
		},
	)
}
