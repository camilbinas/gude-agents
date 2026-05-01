// Run:
//
//	go run ./swarm

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
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load() //nolint

	haiku := bedrock.Must(bedrock.Cheapest())

	// --- Triage agent: routes to the right specialist ---
	triageAgent, err := agent.SwarmAgent(
		haiku,
		prompt.RISEN{
			Role:         "You are a customer support triage agent.",
			Instructions: "Determine what the user needs and hand off to the appropriate specialist. Use transfer_to_billing for payment/invoice questions, transfer_to_technical for bugs/errors/how-to questions.",
			Steps:        []string{"Read the user message", "Decide which specialist handles it", "Transfer with a brief summary"},
			EndGoal:      "Route every request to the right specialist quickly.",
			Narrowing:    "Never try to answer questions yourself — always hand off.",
		},
		nil,
		auto.WithLogging(),
		agent.WithName("triage"),
	)
	if err != nil {
		log.Fatal(err)
	}

	// --- Billing agent ---
	billingAgent, err := agent.SwarmAgent(
		haiku,
		prompt.RISEN{
			Role:         "You are a billing support specialist.",
			Instructions: "Help users with invoices, payments, refunds, and subscription questions. Use the check_balance tool to look up account info.",
			Steps:        []string{"Understand the billing question", "Look up relevant data", "Provide a clear answer"},
			EndGoal:      "Resolve billing questions accurately and helpfully.",
			Narrowing:    "If the question is not about billing, use transfer_to_triage to route it back.",
		},
		[]tool.Tool{utils.CheckBalanceTool()},
		auto.WithLogging(),
		agent.WithName("billing"),
	)
	if err != nil {
		log.Fatal(err)
	}

	// --- Technical support agent ---
	techAgent, err := agent.SwarmAgent(
		haiku,
		prompt.RISEN{
			Role:         "You are a technical support specialist.",
			Instructions: "Help users with bugs, errors, configuration, and how-to questions. Use the search_docs tool to find relevant documentation.",
			Steps:        []string{"Understand the technical issue", "Search docs if needed", "Provide a solution"},
			EndGoal:      "Resolve technical issues with clear, actionable guidance.",
			Narrowing:    "If the question is not technical, use transfer_to_triage to route it back.",
		},
		[]tool.Tool{utils.SearchDocsTool()},
		auto.WithLogging(),
		agent.WithName("technical"),
	)
	if err != nil {
		log.Fatal(err)
	}

	// --- Build the swarm with conversation memory ---
	sw, err := agent.NewSwarm([]agent.SwarmMember{
		{Name: "triage", Description: "Routes requests to the right specialist", Agent: triageAgent},
		{Name: "billing", Description: "Handles invoices, payments, refunds, and subscriptions", Agent: billingAgent},
		{Name: "technical", Description: "Handles bugs, errors, and technical how-to questions", Agent: techAgent},
	},
		agent.WithSwarmMaxHandoffs(5),
		agent.WithSwarmConversation(conversation.NewInMemory(), "support-session"),
		auto.WithSwarmLogging(),
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Customer support swarm ready. Type a message (or 'quit' to exit):")
	utils.SwarmChat(context.Background(), sw)
}
