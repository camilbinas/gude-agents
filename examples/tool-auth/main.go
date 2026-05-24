// Example: Per-tool guards with tool.WithGuard (interactive chat).
//
// Demonstrates a typed guard that inspects the deserialized input to enforce
// a spending limit: process_payment calls above $100 are denied at execution
// time. The LLM receives a structured denial and explains the limit to the user.
//
// Try asking:
//   - "Pay $50 for office supplies"       → goes through
//   - "Pay $250 for new monitors"         → denied, LLM explains the limit
//   - "Refund $500 for order ORD-9876"    → goes through (no guard on refunds)
//
// Run:
//
//	go run ./tool-auth

package main

import (
	"context"
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
	"github.com/camilbinas/gude-agents/agent/logging/auto"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/camilbinas/gude-agents/examples/utils"
)

type PaymentInput struct {
	Amount      float64 `json:"amount" description:"Payment amount in USD" required:"true"`
	Description string  `json:"description" description:"What the payment is for" required:"true"`
}

type RefundInput struct {
	OrderID string  `json:"order_id" description:"The order to refund" required:"true"`
	Amount  float64 `json:"amount" description:"Refund amount in USD" required:"true"`
}

func processPayment(_ context.Context, in PaymentInput) (string, error) {
	return fmt.Sprintf("Payment of $%.2f processed for: %s. Confirmation #PAY-%d.",
		in.Amount, in.Description, int(in.Amount*100)), nil
}

func processRefund(_ context.Context, in RefundInput) (string, error) {
	return fmt.Sprintf("Refund of $%.2f issued for order %s.", in.Amount, in.OrderID), nil
}

func main() {
	provider := bedrock.Must(bedrock.Standard())

	tools := []tool.Tool{
		tool.New("process_payment", "Process a payment", processPayment,
			tool.WithGuard(func(_ context.Context, in PaymentInput) (tool.Decision, error) {
				if in.Amount > 100 {
					return tool.Denyf("amount $%.2f exceeds the $100 limit — requires manager approval", in.Amount), nil
				}
				return tool.Allow(), nil
			}),
		),
		tool.New("process_refund", "Process a refund for an existing order", processRefund),
	}

	store := conversation.NewInMemory()

	a, err := agent.New(provider,
		prompt.Text(`You are a payment processing assistant. You can process payments and refunds.
If a payment is denied, explain the limit to the user and suggest they contact a manager for approval.
Be concise.`),
		tools,
		agent.WithConversation(store, "payment-chat"),
		auto.WithLogging(),
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Payment assistant (type 'quit' to exit)")
	fmt.Println("Try: 'Pay $50 for lunch' or 'Pay $250 for a laptop'")

	utils.Chat(agent.Background(), a)
}
