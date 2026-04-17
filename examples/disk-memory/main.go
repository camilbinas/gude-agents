// Example: Disk-based memory for persistent conversations.
//
// Demonstrates the disk memory driver — conversations are stored as JSON files
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
//	go run ./examples/disk-memory
package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/memory/disk"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
)

func main() {
	provider, err := bedrock.Standard()
	if err != nil {
		log.Fatal(err)
	}

	// Store conversations as JSON files in ./tmp/conversations/
	store, err := disk.New("./tmp/conversations/")
	if err != nil {
		log.Fatal(err)
	}

	a, err := agent.Default(
		provider,
		prompt.Text("You are a helpful assistant. Remember what the user tells you. Be concise."),
		nil,
		agent.WithMemory(store, "default-session"),
	)
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("Chat agent with disk memory. Type 'quit' to exit.")
	fmt.Println("Conversations are saved to ./tmp/conversations/")
	fmt.Println()

	for {
		fmt.Print("You: ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if strings.EqualFold(input, "quit") {
			break
		}

		fmt.Print("Agent: ")
		_, err := a.InvokeStream(ctx, input, func(chunk string) {
			fmt.Print(chunk)
		})
		fmt.Println()
		if err != nil {
			log.Printf("Error: %v\n", err)
		}
		fmt.Println()
	}
}
