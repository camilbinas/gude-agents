// Run:
//
//	REDIS_ADDR=localhost:6379 go run ./conversation-redis

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation/redis"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/examples/utils"
)

func main() {
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}

	store, err := redis.New(
		redis.RedisOptions{Addr: redisAddr},
		redis.WithTTL(1*time.Hour),
		redis.WithKeyPrefix("example:comnversation:"),
	)
	if err != nil {
		log.Fatalf("redis store: %v", err)
	}
	defer store.Close()

	provider := bedrock.Must(bedrock.Standard())

	a, err := agent.Default(
		provider,
		prompt.Text("You are a helpful assistant. Be concise."),
		nil,
		agent.WithConversation(store, "demo-conversation"),
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Redis chat (type 'quit' to exit, 'clear' to reset)")

	utils.Chat(context.Background(), a, utils.ChatOptions{
		ClearFunc: utils.ClearConversation(store, "demo-conversation"),
	})
}
