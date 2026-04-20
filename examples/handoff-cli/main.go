// Example: Human handoff in a CLI (stdin/stdout) environment.
//
// The agent processes a refund request but pauses to ask the human
// for approval before proceeding. The conversation context is fully
// preserved across the handoff.
//
// Run:
//
//	go run ./handoff-cli

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
)

func main() {
	provider := bedrock.Must(bedrock.Standard())

	a, err := agent.Default(provider, prompt.Text(
		"You are a customer support agent. When a user asks for a refund, "+
			"look up the order first, then use request_human_input to get manager approval "+
			"before processing it.",
	), []tool.Tool{
		lookupOrderTool(),
		agent.NewHandoffTool("request_human_input", ""),
		processRefundTool(),
	})
	if err != nil {
		log.Fatal(err)
	}

	ic := agent.NewInvocationContext()
	ctx := agent.WithInvocationContext(context.Background(), ic)

	fmt.Println("Agent: Processing your request...")
	_, err = a.InvokeStream(ctx, "I need a refund for order #1234", func(chunk string) {
		fmt.Print(chunk)
	})

	if errors.Is(err, agent.ErrHandoffRequested) {
		hr, _ := agent.GetHandoffRequest(ic)
		fmt.Printf("\n\n--- HANDOFF ---\nReason: %s\nQuestion: %s\n", hr.Reason, hr.Question)
		fmt.Print("\nYour response: ")

		scanner := bufio.NewScanner(os.Stdin)
		scanner.Scan()
		humanInput := scanner.Text()

		fmt.Println("\nAgent: Resuming...")
		result, _, err := a.ResumeInvoke(ctx, hr, humanInput)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(result)
		return
	}

	if err != nil {
		log.Fatal(err)
	}
}

func lookupOrderTool() tool.Tool {
	return tool.NewRaw("lookup_order", "Look up order details by ID", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"order_id": map[string]any{"type": "string", "description": "The order ID"},
		},
		"required": []string{"order_id"},
	}, func(ctx context.Context, input json.RawMessage) (string, error) {
		return `{"order_id":"1234","amount":"$89.99","status":"delivered","item":"Wireless Headphones"}`, nil
	})
}

func processRefundTool() tool.Tool {
	return tool.NewRaw("process_refund", "Process a refund for an order", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"order_id": map[string]any{"type": "string"},
		},
		"required": []string{"order_id"},
	}, func(ctx context.Context, input json.RawMessage) (string, error) {
		return `{"status":"refunded","amount":"$89.99"}`, nil
	})
}
