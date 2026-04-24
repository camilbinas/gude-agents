// Example: Tool preset constructors for common patterns.
//
// Demonstrates NewSimple and NewString — convenience constructors that
// eliminate boilerplate for the most common tool shapes:
//
//   - NewSimple: no input parameters (e.g. "what time is it?")
//   - NewString: a single required string parameter (e.g. "look up order X")
//
// The agent plays a customer-support role with an interactive chat loop.
// Try asking it to look up an order or check the time.
//
// Run:
//
//	go run ./presets

package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/memory"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/camilbinas/gude-agents/examples/utils"
)

func main() {
	provider := bedrock.Must(bedrock.Cheapest())

	// NewSimple — no input parameters.
	timeTool := tool.NewSimple("current_time", "Returns the current server time",
		func(ctx context.Context) (string, error) {
			return time.Now().Format(time.RFC3339), nil
		},
	)

	// NewString — single required string parameter.
	lookupTool := tool.NewString("lookup_order", "Look up an order by ID", "order_id", "The order ID to look up",
		func(ctx context.Context, orderID string) (string, error) {
			return fmt.Sprintf(`{"order_id": %q, "status": "shipped", "total": "$42.00"}`, orderID), nil
		},
	)

	instructions := prompt.Text(strings.Join([]string{
		"You are a customer support assistant.",
		"You can check the current time and look up orders.",
	}, " "))

	store := memory.NewStore()

	a, err := agent.Default(
		provider,
		instructions,
		[]tool.Tool{timeTool, lookupTool},
		agent.WithMemory(store, "tool-presets-session"),
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Customer support agent ready. Try looking up an order. Type 'quit' to exit.")

	utils.Chat(context.Background(), a)
}
