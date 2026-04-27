// Example: Disk-based persistent conversations.
//
// Demonstrates the disk driver — conversations are stored as JSON files
// on the local filesystem. Messages survive restarts without any external
// infrastructure (no Redis, no database).
//
// Run it multiple times to see the conversation persist across executions.
// Check the ./tmp/conversations/ directory to inspect the raw JSON.
//
// Sample session (first run):
//
//	You: My name is Alice and I work at Acme Corp
//	Agent: Nice to meet you, Alice! How can I help you today?
//
// Sample session (second run — agent remembers):
//
//	You: What's my name?
//	Agent: Your name is Alice, and you work at Acme Corp.
//
// Run:
//
//	go run ./conversation-disk

package main

import (
	"context"
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation/disk"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/examples/utils"
)

func main() {
	provider := bedrock.Must(bedrock.Standard())

	// Store conversations as JSON files in ./tmp/conversations/
	store, err := disk.New("./tmp/conversations/")
	if err != nil {
		log.Fatal(err)
	}

	a, err := agent.Default(
		provider,
		prompt.Text("You are a helpful assistant. Remember what the user tells you. Be concise."),
		nil,
		agent.WithConversation(store, "default-session"),
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Chat agent with disk based conversations. Type 'quit' to exit, 'clear' to reset.")
	fmt.Println("Conversations are saved to ./tmp/conversations/")
	fmt.Println()

	utils.Chat(context.Background(), a, utils.ChatOptions{
		ClearFunc: utils.ClearConversation(store, "default-session"),
	})
}
