// Example: Tool preset constructors for common patterns.
//
// Demonstrates NewSimple, NewString, and NewConfirm — three convenience
// constructors that eliminate boilerplate for the most common tool shapes:
//
//   - NewSimple: no input parameters (e.g. "what time is it?")
//   - NewString: a single required string parameter (e.g. "look up order X")
//   - NewConfirm: a boolean confirmation gate (e.g. "approve this refund?")
//
// The agent plays a customer-support role with an interactive chat loop.
// Try asking it to look up an order, check the time, or process a refund.
//
// Sample session:
//
//	You: what time is it?
//	Agent: The current time is 2026-04-16T14:32:01+02:00.
//
//	You: look up order ORD-1234
//	Agent: Order ORD-1234 is currently shipped with a total of $42.00.
//
//	You: I'd like a refund for that
//	Agent: I've processed the refund of $42.00 for order ORD-1234.
//
// Run:
//
//	go run ./presets

package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/memory"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
)

func main() {
	provider := bedrock.Must(bedrock.ClaudeHaiku4_5())

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

	// NewConfirm — boolean confirmation parameter.
	refundTool := tool.NewConfirm("approve_refund", "Approve a refund for the most recently looked-up order",
		func(ctx context.Context, confirmed bool) (string, error) {
			if !confirmed {
				return "Refund cancelled.", nil
			}
			return "Refund of $42.00 processed successfully.", nil
		},
	)

	instructions := prompt.Text(strings.Join([]string{
		"You are a customer support assistant.",
		"You can check the current time, look up orders, and process refunds.",
		"Always look up the order before approving a refund.",
	}, " "))

	store := memory.NewStore()

	a, err := agent.Default(
		provider,
		instructions,
		[]tool.Tool{timeTool, lookupTool, refundTool},
		agent.WithMemory(store, "tool-presets-session"),
	)
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("Customer support agent ready. Try asking for a refund. Type 'quit' to exit.")
	for {
		fmt.Print("\nYou: ")
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
	}
}
