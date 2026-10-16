// Run: go run ./human-in-the-loop
//
// One support workflow demonstrates both interruption types: approval before
// a destructive refund, and human input when an order ID is missing.
//
// A paused run is persisted execution state that references the transcript, so
// pausing needs both a ConversationStore and an ExecutionStore. This example
// selects the in-process memory implementations explicitly: the transcript and
// any paused execution are lost when the process exits. Use a durable
// ConversationStore and a durable execution store (agent/execution/postgres,
// redis, or dynamodb) when a pause must survive a restart.
package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
	"github.com/camilbinas/gude-agents/agent/execution"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
)

type orderInput struct {
	OrderID string `json:"order_id" description:"Order ID" required:"true"`
}

func main() {
	conversations := conversation.NewInMemory()
	executions := execution.NewMemory()

	a, err := agent.New(
		bedrock.Must(bedrock.Standard()),
		"You are a support agent. Look up orders normally. Before a refund, call refund_order. If an order ID is missing, call request_order_id.",
		agent.WithTools(
			tool.New("lookup_order", "Look up an order", func(_ context.Context, in orderInput) (string, error) {
				return fmt.Sprintf(`{"order_id":%q,"status":"delivered"}`, in.OrderID), nil
			}),
			tool.New("refund_order", "Issue a refund", func(_ context.Context, in orderInput) (string, error) {
				return fmt.Sprintf(`{"order_id":%q,"refunded":true}`, in.OrderID), nil
			}, tool.RequiresApproval()),
			agent.NewHumanInputTool("request_order_id", "Ask when the customer has not supplied an order ID."),
		),
		agent.WithConversationStore(conversations),
		agent.WithExecutionStore(executions),
	)
	if err != nil {
		log.Fatal(err)
	}

	reader := bufio.NewScanner(os.Stdin)
	for i, request := range []string{"Please refund order 5678.", "I need a refund, but I do not know my order ID."} {
		// Every invocation names its conversation because the agent has a
		// ConversationStore.
		ctx := agent.Background().WithConversationID(fmt.Sprintf("support-%d", i+1))
		result, err := a.Invoke(ctx, request)
		if err != nil {
			log.Fatal(err)
		}
		for result.StopReason == agent.StopInterrupt {
			if result.Interrupt == nil {
				log.Fatal("missing interrupt")
			}
			switch result.Interrupt.Type {
			case agent.InterruptApproval:
				fmt.Printf("Approval requested for: %s\nApprove? [y/N]: ", result.Interrupt.Approval.Calls[0].Name)
				answer := readAnswer(reader, "")
				if strings.EqualFold(answer, "y") {
					result, err = a.Resume(ctx, result.Interrupt, agent.Approve())
				} else {
					result, err = a.Resume(ctx, result.Interrupt, agent.Deny("operator declined"))
				}
			case agent.InterruptHumanInput:
				fmt.Printf("%s\n> ", result.Interrupt.Input.Question)
				answer := readAnswer(reader, "Order ID is 5678.")
				result, err = a.Resume(ctx, result.Interrupt, agent.Respond(answer))
			}
			if err != nil {
				log.Fatal(err)
			}
		}
		fmt.Printf("Agent: %s\n\n", result.Text)
	}
}

// readAnswer makes this example safe to run with stdin closed (for example in
// an IDE task runner): a blank line or EOF uses the documented fallback rather
// than passing an empty human response to Resume.
func readAnswer(scanner *bufio.Scanner, fallback string) string {
	if scanner.Scan() {
		if answer := strings.TrimSpace(scanner.Text()); answer != "" {
			return answer
		}
	}
	return fallback
}
