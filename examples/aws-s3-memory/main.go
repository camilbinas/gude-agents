// Example: Persistent multi-turn conversation backed by S3-compatible blob storage.
//
// Each invocation saves the conversation history as a JSON object in an S3
// bucket so the agent can resume where it left off across process restarts.
//
// Works with AWS S3, Cloudflare R2, MinIO, DigitalOcean Spaces, Backblaze B2,
// and other S3-compatible providers.
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
//	AWS_BUCKET=my-bucket go run ./examples/aws-s3-memory
package main

import (
	"context"
	"fmt"
	"log"
	"os"

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

	// Turn 1 — introduce a fact.
	result, _, err := a.Invoke(ctx, "My name is Alice. Remember that.")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Turn 1:", result)

	// Turn 2 — verify the agent recalls it.
	result, _, err = a.Invoke(ctx, "What is my name?")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Turn 2:", result)

	// Show all conversation IDs stored under the configured prefix.
	ids, err := mem.List(ctx)
	if err != nil {
		log.Fatalf("list conversations: %v", err)
	}
	fmt.Printf("Stored conversations: %v\n", ids)
}
