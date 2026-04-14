package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/redis"
)

func main() {
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}

	mem, err := redis.NewRedisMemory(
		redis.RedisOptions{Addr: redisAddr},
		redis.WithTTL(1*time.Hour),
		redis.WithKeyPrefix("example:memory:"),
	)
	if err != nil {
		log.Fatalf("redis memory: %v", err)
	}
	defer mem.Close()

	provider, err := bedrock.ClaudeSonnet4_6()
	if err != nil {
		log.Fatal(err)
	}

	a, err := agent.Default(
		provider,
		prompt.Text("You are a helpful assistant. Be concise."),
		nil,
		agent.WithMemory(mem, "demo-conversation"),
	)
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()

	result, _, err := a.Invoke(ctx, "My name is Alice. Remember that.")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Turn 1:", result)

	result, _, err = a.Invoke(ctx, "What is my name?")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Turn 2:", result)
}
