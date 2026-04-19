// Example: Persistent multi-turn conversation backed by S3-compatible blob storage.
//
// Each invocation saves the conversation history as a JSON object in an S3
// bucket so the agent can resume where it left off across process restarts.
//
// Prerequisites:
//  1. An S3 bucket.
//
// Environment variables:
//
//	AWS_BUCKET — bucket name (required)
//	AWS_REGION — AWS region (falls back to the default credential chain)
//
// Run:
//
//	AWS_BUCKET=my-bucket go run ./aws-s3-memory

package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/camilbinas/gude-agents/agent"
	s3mem "github.com/camilbinas/gude-agents/agent/memory/s3"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
)

func main() {
	bucket := os.Getenv("AWS_BUCKET")
	if bucket == "" {
		log.Fatal("AWS_BUCKET environment variable is required")
	}

	ctx := context.Background()

	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("load AWS config: %v", err)
	}

	mem, err := s3mem.New(cfg, bucket,
		s3mem.WithKeyPrefix("example/memory/"),
	)
	if err != nil {
		log.Fatalf("s3 memory: %v", err)
	}

	provider := bedrock.Must(bedrock.ClaudeSonnet4_6())

	a, err := agent.Default(
		provider,
		prompt.Text("You are a helpful assistant. Be concise."),
		nil,
		agent.WithMemory(mem, "demo-conversation"),
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("S3 memory chat (type 'quit' to exit, 'clear' to reset)")
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("\n> ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if input == "quit" {
			break
		}
		if input == "clear" {
			if err := mem.Delete(ctx, "demo-conversation"); err != nil {
				fmt.Printf("Error clearing: %v\n", err)
			} else {
				fmt.Println("Conversation cleared.")
			}
			continue
		}

		usage, err := a.InvokeStream(ctx, input, func(chunk string) {
			fmt.Print(chunk)
		})
		if err != nil {
			fmt.Printf("\nError: %v\n", err)
			continue
		}
		fmt.Printf("\n--- tokens: %d in / %d out ---\n", usage.InputTokens, usage.OutputTokens)
	}
}
