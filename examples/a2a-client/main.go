// Example: Discover a remote A2A agent and use its skills as tools.
//
// The Client fetches the Agent Card, maps skills to tool.Tool values, and
// lets a local orchestrator agent call the remote agent as a tool.
//
// Prerequisites:
//   - A running A2A server (e.g. the a2a-server example on :8080)
//
// Run:
//
//	go run ./a2a-client

package main

import (
	"context"
	"fmt"
	"log"
	"os/signal"
	"syscall"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/a2a"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Connect to a remote A2A agent and discover its skills.
	client, err := a2a.NewClient(ctx, "http://localhost:8080")
	if err != nil {
		log.Fatalf("failed to connect to remote agent: %v", err)
	}
	defer client.Close()

	fmt.Printf("Discovered agent: %s\n", client.Card().Name)
	fmt.Printf("  Skills: %d\n", len(client.Card().Skills))

	// Get all remote skills as tools (or filter with IncludeSkills/ExcludeSkills).
	remoteTools, err := client.Tools(ctx)
	if err != nil {
		log.Fatal(err)
	}

	for _, t := range remoteTools {
		fmt.Printf("  - %s: %s\n", t.Spec.Name, t.Spec.Description)
	}

	// Wire the remote tools into a local orchestrator agent.
	provider := bedrock.Must(bedrock.Standard())

	orchestrator, err := agent.Default(
		provider,
		prompt.Text("You are an orchestrator. Use the available tools to answer questions."),
		remoteTools,
		agent.WithName("orchestrator"),
	)
	if err != nil {
		log.Fatal(err)
	}

	// The orchestrator can now call the remote agent's skills as tools.
	result, err := orchestrator.Invoke(agent.NewContext(ctx), "What's the weather in Tokyo?")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("\nOrchestrator response: %s\n", result)
}
