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

	mem, err := redis.NewRedisConversation(
		redis.RedisOptions{Addr: redisAddr},
		redis.WithTTL(1*time.Hour),
		redis.WithKeyPrefix("example:memory:"),
	)
	if err != nil {
		log.Fatalf("redis memory: %v", err)
	}
	defer mem.Close()

	provider := bedrock.Must(bedrock.Standard())

	a, err := agent.Default(
		provider,
		prompt.Text("You are a helpful assistant. Be concise."),
		nil,
		agent.WithConversation(mem, "demo-conversation"),
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Redis memory chat (type 'quit' to exit, 'clear' to reset)")

	utils.Chat(context.Background(), a, utils.ChatOptions{
		ClearFunc: utils.ClearMemory(mem, "demo-conversation"),
	})
}
