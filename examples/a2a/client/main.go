// Run after the server: go run ./a2a/client
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/a2a"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
)

func main() {
	ctx := context.Background()
	client, err := a2a.NewClient(ctx, "http://localhost:8080")
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()
	fmt.Printf("Discovered %s with %d skill(s)\n", client.Card().Name, len(client.Card().Skills))
	remoteTools, err := client.Tools(ctx)
	if err != nil {
		log.Fatal(err)
	}
	orchestrator, err := agent.New(bedrock.Must(bedrock.Standard()), "Use remote A2A skills to answer travel questions.", agent.WithTools(remoteTools...))
	if err != nil {
		log.Fatal(err)
	}
	result, err := orchestrator.Invoke(agent.NewContext(ctx), "What is the weather in Tokyo?")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result.Text)
}
