// Example: Pre-call tool approval in a CLI (stdin/stdout) environment.
//
// The agent processes a request to delete an order. Before the destructive
// tool runs, the loop pauses and returns an approval interrupt so the operator
// can review every pending call. The operator types "y" to approve all calls
// or anything else to deny them. The invocation is preserved across resume.
//
// Key difference from human input: the agent chose the tool itself — approval
// intercepts it before execution, deterministically, regardless of what the
// model says.
//
// Run:
//
//	go run ./approval-cli
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load() //nolint

	provider := bedrock.Must(bedrock.Standard())

	a, err := agent.New(
		provider,
		"You are an order management assistant. When asked to delete an order, use the delete_order tool. When asked to look up an order, use lookup_order.",
		agent.WithTools(lookupOrderTool(), deleteOrderTool()),
	)
	if err != nil {
		log.Fatal(err)
	}

	c := agent.Background()
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("Agent: Processing your request...")
	result, err := streamResult(a, c, "Please delete order #5678")
	if err != nil {
		log.Fatal(err)
	}

	if result.StopReason != agent.StopInterrupt || result.Interrupt == nil {
		return
	}
	if result.Interrupt.Type != agent.InterruptApproval || result.Interrupt.Approval == nil {
		log.Fatalf("unexpected interrupt type: %s", result.Interrupt.Type)
	}

	fmt.Printf("\n\n--- APPROVAL REQUIRED ---\n")
	for _, call := range result.Interrupt.Approval.Calls {
		fmt.Printf("Tool:  %s\n", call.Name)
		fmt.Printf("Input: %s\n", string(call.Input))
	}
	fmt.Print("\nApprove all calls? [y/N]: ")

	scanner.Scan()
	answer := strings.TrimSpace(strings.ToLower(scanner.Text()))

	var response agent.ResumeResponse
	if answer == "y" {
		response = agent.Approve()
		fmt.Println("\nApproved. Resuming...")
	} else {
		response = agent.Deny("operator rejected the request")
		fmt.Println("\nDenied. Resuming...")
	}

	resumed, err := a.Resume(c, result.Interrupt, response)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("\nAgent:", resumed.Text)
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

func lookupOrderTool() tool.Tool {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"order_id": map[string]any{"type": "string", "description": "The order ID"},
		},
		"required": []string{"order_id"},
	}
	return tool.NewRaw(
		"lookup_order",
		"Look up order details by ID",
		func(_ context.Context, input json.RawMessage) (string, error) {
			var p struct {
				OrderID string `json:"order_id"`
			}
			if err := json.Unmarshal(input, &p); err != nil {
				return "", fmt.Errorf("decode order lookup: %w", err)
			}
			return fmt.Sprintf(`{"order_id":%q,"status":"active","total":"$249.99","items":["Laptop Stand","USB Hub"]}`, p.OrderID), nil
		},
		tool.WithSchema(schema),
	)
}

func deleteOrderTool() tool.Tool {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"order_id": map[string]any{"type": "string", "description": "The order ID to delete"},
		},
		"required": []string{"order_id"},
	}
	return tool.NewRaw(
		"delete_order",
		"Permanently cancel and delete an order",
		func(_ context.Context, input json.RawMessage) (string, error) {
			var p struct {
				OrderID string `json:"order_id"`
			}
			if err := json.Unmarshal(input, &p); err != nil {
				return "", fmt.Errorf("decode order deletion: %w", err)
			}
			return fmt.Sprintf(`{"deleted":true,"order_id":%q}`, p.OrderID), nil
		},
		tool.WithSchema(schema),
		tool.RequiresApproval(),
	)
}
