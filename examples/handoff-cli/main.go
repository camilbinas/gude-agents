// Example: Human input in a CLI (stdin/stdout) environment.
//
// The agent processes a refund request but pauses to ask a human for approval
// before proceeding. The invocation is fully preserved across the interrupt.
//
// Run:
//
//	go run ./handoff-cli

package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
)

func main() {
	provider := bedrock.Must(bedrock.Standard())

	a, err := agent.New(
		provider,
		"You are a customer support agent. When a user asks for a refund, look up the order first, then use request_human_input to get manager approval before processing it.",
		agent.WithTools(
			lookupOrderTool(),
			agent.NewHumanInputTool("request_human_input", ""),
			processRefundTool(),
		),
	)
	if err != nil {
		log.Fatal(err)
	}

	c := agent.Background()

	fmt.Println("Agent: Processing your request...")
	result, err := streamResult(a, c, "I need a refund for order #1234")
	if err != nil {
		log.Fatal(err)
	}

	if result.StopReason != agent.StopInterrupt || result.Interrupt == nil {
		return
	}
	if result.Interrupt.Type != agent.InterruptHumanInput || result.Interrupt.Input == nil {
		log.Fatalf("unexpected interrupt type: %s", result.Interrupt.Type)
	}

	input := result.Interrupt.Input
	fmt.Printf("\n\n--- HUMAN INPUT ---\nReason: %s\nQuestion: %s\n", input.Reason, input.Question)
	fmt.Print("\nYour response: ")

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()

	fmt.Println("\nAgent: Resuming...")
	resumed, err := a.Resume(c, result.Interrupt, agent.Respond(scanner.Text()))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(resumed.Text)
}

func streamResult(a *agent.Agent, c *agent.Context, input string) (agent.Result, error) {
	var result agent.Result
	for event, err := range a.Stream(c, input) {
		if event.Type == agent.EventText && event.Text != nil {
			fmt.Print(event.Text.Content)
		}
		if event.Type == agent.EventEnd && event.Result != nil {
			result = *event.Result
		}
		if err != nil {
			return result, err
		}
	}
	return result, nil
}

// orderInput is the typed input shared by both order tools.
type orderInput struct {
	OrderID string `json:"order_id" description:"The order ID" required:"true"`
}

func lookupOrderTool() tool.Tool {
	return tool.New(
		"lookup_order",
		"Look up order details by ID",
		func(_ context.Context, _ orderInput) (string, error) {
			return `{"order_id":"1234","amount":"$89.99","status":"delivered","item":"Wireless Headphones"}`, nil
		},
	)
}

func processRefundTool() tool.Tool {
	return tool.New(
		"process_refund",
		"Process a refund for an order",
		func(_ context.Context, _ orderInput) (string, error) {
			return `{"status":"refunded","amount":"$89.99"}`, nil
		},
	)
}
