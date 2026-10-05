// Run in one terminal: go run ./a2a/server
package main

import (
	"context"
	"fmt"
	"log"
	"os/signal"
	"syscall"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/a2a"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
)

func main() {
	type weatherInput struct {
		City string `json:"city" description:"City name" required:"true"`
	}
	weather := tool.New("get_weather", "Get demo weather for a city", func(_ context.Context, in weatherInput) (string, error) {
		return fmt.Sprintf("Weather in %s: 22°C and sunny", in.City), nil
	})
	a, err := agent.New(bedrock.Must(bedrock.Standard()), "You are a travel assistant.", agent.WithName("travel-assistant"), agent.WithTools(weather))
	if err != nil {
		log.Fatal(err)
	}
	server, err := a2a.NewServer(a, []a2a.CardOption{a2a.WithCardURL("http://localhost:8080"), a2a.WithCardDescription("Travel assistant with weather skills")})
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	log.Println("A2A card: http://localhost:8080/.well-known/agent-card.json")
	if err := server.ListenAndServe(ctx, ":8080"); err != nil {
		log.Fatal(err)
	}
}
