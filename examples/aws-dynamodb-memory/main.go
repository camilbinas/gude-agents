// Example: Persistent multi-turn conversation backed by Amazon DynamoDB.
//
// Each invocation saves the conversation history to a DynamoDB table so the
// agent can resume where it left off across process restarts. A 8-hour TTL
// is configured so old conversations are automatically expired by DynamoDB's
// native TTL feature.
//
// Prerequisites:
//  1. A DynamoDB table with "pk" (String) as the partition key and no sort key.
//  2. (Optional) Enable TTL on the table using the "ttl" attribute.
//
// Environment variables:
//
//	AWS_DYNAMODB_TABLE — table name (required)
//	AWS_REGION         — AWS region (falls back to the default credential chain)
//
// Run:
//
//	AWS_DYNAMODB_TABLE=gude-memory go run ./examples/aws-dynamodb-memory
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/memory/dynamodb"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
)

func main() {
	table := os.Getenv("AWS_DYNAMODB_TABLE")
	if table == "" {
		log.Fatal("AWS_DYNAMODB_TABLE environment variable is required")
	}

	ctx := context.Background()

	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("load AWS config: %v", err)
	}

	mem, err := dynamodb.NewDynamoDBMemory(cfg, table,
		dynamodb.WithKeyPrefix("example:memory:"),
		dynamodb.WithTTL(8*time.Hour),
	)
	if err != nil {
		log.Fatalf("dynamodb memory: %v", err)
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
