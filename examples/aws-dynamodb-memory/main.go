// Example: Persistent multi-turn conversation backed by Amazon DynamoDB.
//
// Each invocation saves the conversation history to a DynamoDB table so the
// agent can resume where it left off across process restarts. An 8-hour TTL
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
//	AWS_DYNAMODB_TABLE=gude-memory go run ./aws-dynamodb-memory

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
	"github.com/camilbinas/gude-agents/examples/utils"
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

	fmt.Println("DynamoDB memory chat (type 'quit' to exit, 'clear' to reset)")

	utils.Chat(ctx, a, utils.ChatOptions{
		ClearFunc: utils.ClearMemory(mem, "demo-conversation"),
	})
}
