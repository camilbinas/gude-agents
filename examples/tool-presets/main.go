// Example: Typed tool constructors for common patterns.
//
// Demonstrates tool.New with an empty input struct and with a single required
// string field. The agent plays a customer-support role with an interactive
// chat loop.
//
// Run:
//
//	go run ./tool-presets

package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/camilbinas/gude-agents/examples/utils"
)

func main() {
	provider := bedrock.Must(bedrock.Cheapest())

	// Typed constructors cover both no-input and single-string tools.
	type timeInput struct{}
	timeTool := tool.New("current_time", "Returns the current server time",
		func(context.Context, timeInput) (string, error) {
			return time.Now().Format(time.RFC3339), nil
		},
	)

	type lookupInput struct {
		OrderID string `json:"order_id" description:"The order ID to look up" required:"true"`
	}
	lookupTool := tool.New("lookup_order", "Look up an order by ID",
		func(_ context.Context, in lookupInput) (string, error) {
			return fmt.Sprintf(`{"order_id": %q, "status": "shipped", "total": "$42.00"}`, in.OrderID), nil
		},
	)

	instructions := prompt.Text(strings.Join([]string{
		"You are a customer support assistant.",
		"You can check the current time and look up orders.",
	}, " "))

	store := conversation.NewInMemory()

	a, err := agent.New(
		provider,
		instructions.String(),
		agent.WithTools(timeTool, lookupTool),
		agent.WithConversationStore(store),
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Customer support agent ready. Try looking up an order. Type 'quit' to exit.")

	utils.Chat(agent.Background().WithConversationID("tool-presets-session"), a)
}
