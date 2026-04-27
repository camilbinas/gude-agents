// Example: AgentCore memory for persistent long-term knowledge.
//
// Memory backed by AWS Bedrock AgentCore with built-in semantic search —
// no embedder setup required. Set AGENTCORE_STORE_MODE=batch for direct
// storage instead of the default automatic extraction.
//
// Prerequisites:
//   - An AgentCore Memory resource created in your AWS account
//   - AGENTCORE_MEMORY_ID env var set to your Memory resource ID
//
// Run:
//
//	AGENTCORE_MEMORY_ID=<your-memory-id> go run ./memory-agentcore
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcore"
	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/logging/debug"
	"github.com/camilbinas/gude-agents/agent/memory"
	"github.com/camilbinas/gude-agents/agent/memory/agentcore"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/camilbinas/gude-agents/examples/utils"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load() //nolint

	memoryID := os.Getenv("AGENTCORE_MEMORY_ID")
	if memoryID == "" {
		log.Fatal("AGENTCORE_MEMORY_ID env var is required")
	}

	// Load AWS config and create the AgentCore client.
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatalf("aws config: %v", err)
	}
	client := bedrockagentcore.NewFromConfig(cfg)

	// Build the AgentCore memory store.
	var opts []agentcore.Option
	if strings.EqualFold(os.Getenv("AGENTCORE_STORE_MODE"), "batch") {
		opts = append(opts, agentcore.WithStoreMode(agentcore.BatchCreateMode))
		fmt.Println("Store mode: BatchCreate (direct storage)")
	} else {
		fmt.Println("Store mode: CreateEvent (automatic extraction)")
	}

	store, err := agentcore.New(client, memoryID, opts...)
	if err != nil {
		log.Fatalf("agentcore store: %v", err)
	}

	// Create the agent with memory tools.
	a, err := agent.Default(
		bedrock.Must(bedrock.Standard()),
		prompt.Text("You are a helpful assistant with long-term memory. "+
			"Use the remember tool to store important facts, preferences, and decisions the user shares. "+
			"Use the recall tool to retrieve relevant context before answering questions."),
		[]tool.Tool{
			memory.RememberTool(store),
			memory.RecallTool(store),
		},
		debug.WithLogging(),
	)
	if err != nil {
		log.Fatal(err)
	}

	ctx := agent.WithIdentifier(context.Background(), "user-123")

	fmt.Println()
	fmt.Println("Chat agent with AgentCore memory. Type 'quit' to exit.")
	fmt.Println("Try: 'Remember that I prefer dark mode' then 'What are my preferences?'")
	fmt.Println()

	utils.Chat(ctx, a)
}
