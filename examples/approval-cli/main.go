// Example: Pre-call tool approval in a CLI (stdin/stdout) environment.
//
// The agent processes a request to delete an order. Before the destructive
// tool runs, the loop pauses and surfaces an ApprovalRequest so the operator
// can review the exact tool name and input. The operator types "y" to approve
// or anything else to deny. The conversation context is fully preserved across
// the approval round-trip.
//
// Key difference from handoff: the agent chose the tool itself — approval
// intercepts it *before* execution, deterministically, regardless of what
// the LLM says.
//
// Run:
//
//	go run ./approval-cli
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load() //nolint

	provider := bedrock.Must(bedrock.Standard())

	a, err := agent.New(provider, prompt.Text(
		"You are an order management assistant. "+
			"When asked to delete an order, use the delete_order tool. "+
			"When asked to look up an order, use lookup_order.",
	), []tool.Tool{
		lookupOrderTool(),
		deleteOrderTool(), // marked RequiresApproval
	})
	if err != nil {
		log.Fatal(err)
	}

	c := agent.Background()
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("Agent: Processing your request...")
	err = a.InvokeStream(c, "Please delete order #5678", func(chunk string) {
		fmt.Print(chunk)
	})

	if errors.Is(err, agent.ErrToolApprovalRequired) {
		ar, _ := agent.GetApprovalRequest(c)

		fmt.Printf("\n\n--- APPROVAL REQUIRED ---\n")
		fmt.Printf("Tool:  %s\n", ar.ToolName)
		fmt.Printf("Input: %s\n", string(ar.ToolInput))
		fmt.Print("\nApprove? [y/N]: ")

		scanner.Scan()
		answer := strings.TrimSpace(strings.ToLower(scanner.Text()))

		var decision tool.Decision
		if answer == "y" {
			decision = tool.Allow()
			fmt.Println("\nApproved. Resuming...")
		} else {
			decision = tool.Deny("operator rejected the request")
			fmt.Println("\nDenied. Resuming...")
		}

		result, err := a.ResumeWithApprovalInvoke(c, ar, decision)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println("\nAgent:", result)
		return
	}

	if err != nil {
		log.Fatal(err)
	}
}

func lookupOrderTool() tool.Tool {
	return tool.NewRaw("lookup_order", "Look up order details by ID",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"order_id": map[string]any{"type": "string", "description": "The order ID"},
			},
			"required": []string{"order_id"},
		},
		func(_ context.Context, input json.RawMessage) (string, error) {
			var p struct {
				OrderID string `json:"order_id"`
			}
			json.Unmarshal(input, &p)
			return fmt.Sprintf(`{"order_id":%q,"status":"active","total":"$249.99","items":["Laptop Stand","USB Hub"]}`, p.OrderID), nil
		},
	)
}

func deleteOrderTool() tool.Tool {
	return tool.NewRaw("delete_order", "Permanently cancel and delete an order",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"order_id": map[string]any{"type": "string", "description": "The order ID to delete"},
			},
			"required": []string{"order_id"},
		},
		func(_ context.Context, input json.RawMessage) (string, error) {
			var p struct {
				OrderID string `json:"order_id"`
			}
			json.Unmarshal(input, &p)
			return fmt.Sprintf(`{"deleted":true,"order_id":%q}`, p.OrderID), nil
		},
		tool.RequiresApproval(), // pause the loop before this tool runs
	)
}
