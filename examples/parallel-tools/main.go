// Example: Fire-and-forget tools for async side effects.
//
// Demonstrates NewFireAndForget — a tool constructor where the handler
// runs in a background goroutine and the LLM gets an instant acknowledgment.
// The agent loop never blocks on the side effect.
//
// This example simulates a support agent that can:
//
//   - look up a customer (synchronous — the LLM needs the data)
//   - log an interaction to the CRM (fire-and-forget — just a side effect)
//   - send a follow-up email (fire-and-forget — just a side effect)
//
// Sample session:
//
//	You: Look up customer C-42 and log that I spoke with them about a billing issue
//	Agent: Customer C-42 is Acme Corp (active, enterprise tier). I've queued the
//	       CRM note about your billing conversation.
//
//	You: Send them a follow-up email summarizing our chat
//	Agent: Follow-up email queued for customer C-42.
//
// Run:
//
//	go run ./parallel-tools

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
	provider := bedrock.Must(bedrock.Standard())

	// Synchronous tool — the LLM needs the result to continue.
	lookupTool := tool.NewString(
		"lookup_customer", "Look up a customer by ID",
		"customer_id", "The customer ID to look up",
		func(_ context.Context, id string) (string, error) {
			log.Printf("[sync] Looking up customer %s", id)
			return fmt.Sprintf(`{"id": %q, "name": "Acme Corp", "status": "active", "tier": "enterprise"}`, id), nil
		},
	)

	// Fire-and-forget — CRM update runs in the background.
	type CRMNote struct {
		CustomerID string `json:"customer_id" description:"The customer ID" required:"true"`
		Note       string `json:"note"        description:"Note to log in the CRM" required:"true"`
	}

	logCRMTool := tool.NewAsync(
		"log_crm_interaction", "Log an interaction note to the CRM system",
		"CRM note queued.",
		func(_ context.Context, in CRMNote) {
			// Simulate a slow CRM API call.
			log.Printf("[async] Writing CRM note for %s...", in.CustomerID)
			time.Sleep(2 * time.Second)
			log.Printf("[async] CRM note saved: %s → %q", in.CustomerID, in.Note)
		},
		log.Printf,
	)

	// Fire-and-forget — email runs in the background.
	type Email struct {
		CustomerID string `json:"customer_id" description:"The customer to email" required:"true"`
		Subject    string `json:"subject"     description:"Email subject line" required:"true"`
		Body       string `json:"body"        description:"Email body text" required:"true"`
	}

	emailTool := tool.NewAsync(
		"send_followup_email", "Send a follow-up email to a customer",
		"Follow-up email queued.",
		func(_ context.Context, in Email) {
			log.Printf("[async] Sending email to %s: %q", in.CustomerID, in.Subject)
			time.Sleep(3 * time.Second)
			log.Printf("[async] Email sent to %s", in.CustomerID)
		},
		log.Printf,
	)

	instructions := prompt.Text(strings.Join([]string{
		"You are a customer support assistant.",
		"You can look up customers, log CRM notes, and send follow-up emails.",
		"Always look up the customer first before logging notes or sending emails.",
		"The CRM and email tools are fire-and-forget — just confirm they were queued.",
	}, " "))

	store := conversation.NewInMemory()

	a, err := agent.Default(
		provider,
		instructions,
		[]tool.Tool{lookupTool, logCRMTool, emailTool},
		agent.WithConversation(store, "fire-and-forget-session"),
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Support agent ready. Type 'quit' to exit.")
	fmt.Println("Try: Look up customer C-42 and log that I spoke with them about billing")
	fmt.Println()

	utils.Chat(context.Background(), a)

	// Give background goroutines a moment to finish logging.
	fmt.Println("Waiting for background tasks...")
	time.Sleep(4 * time.Second)
}
