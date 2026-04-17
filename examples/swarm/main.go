// Run:
//
//	go run ./swarm

package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/memory"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load() //nolint

	haiku, err := bedrock.ClaudeHaiku4_5()
	if err != nil {
		log.Fatal(err)
	}

	// --- Triage agent: routes to the right specialist ---
	triageAgent, err := agent.SwarmAgent(
		haiku,
		prompt.RISEN{
			Role:         "You are a customer support triage agent.",
			Instructions: "Determine what the user needs and hand off to the appropriate specialist. Use transfer_to_billing for payment/invoice questions, transfer_to_technical for bugs/errors/how-to questions.",
			Steps:        "1) Read the user message. 2) Decide which specialist handles it. 3) Transfer with a brief summary.",
			EndGoal:      "Route every request to the right specialist quickly.",
			Narrowing:    "Never try to answer questions yourself — always hand off.",
		},
		nil, // no domain tools — handoff tools are injected by the swarm
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
			Steps:        "1) Understand the billing question. 2) Look up relevant data. 3) Provide a clear answer.",
			EndGoal:      "Resolve billing questions accurately and helpfully.",
			Narrowing:    "If the question is not about billing, use transfer_to_triage to route it back.",
		},
		[]tool.Tool{checkBalanceTool()},
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
			Steps:        "1) Understand the technical issue. 2) Search docs if needed. 3) Provide a solution.",
			EndGoal:      "Resolve technical issues with clear, actionable guidance.",
			Narrowing:    "If the question is not technical, use transfer_to_triage to route it back.",
		},
		[]tool.Tool{searchDocsTool()},
	)
	if err != nil {
		log.Fatal(err)
	}

	// --- Build the swarm with conversation memory ---
	swarm, err := agent.NewSwarm([]agent.SwarmMember{
		{Name: "triage", Description: "Routes requests to the right specialist", Agent: triageAgent},
		{Name: "billing", Description: "Handles invoices, payments, refunds, and subscriptions", Agent: billingAgent},
		{Name: "technical", Description: "Handles bugs, errors, and technical how-to questions", Agent: techAgent},
	},
		agent.WithSwarmLogger(log.Default()),
		agent.WithSwarmMaxHandoffs(5),
		agent.WithSwarmMemory(memory.NewStore(), "support-session"),
	)
	if err != nil {
		log.Fatal(err)
	}

	// --- Interactive loop ---
	fmt.Println("Customer support swarm ready. Type a message (or 'quit' to exit):")
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("\n> ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if input == "quit" {
			break
		}

		result, err := swarm.Run(context.Background(), input, func(chunk string) {
			fmt.Print(chunk)
		})
		if err != nil {
			fmt.Printf("\nError: %v\n", err)
			continue
		}

		fmt.Printf("\n\n--- Handled by: %s | Tokens: %d in / %d out | Handoffs: %d ---\n",
			result.FinalAgent,
			result.Usage.InputTokens,
			result.Usage.OutputTokens,
			len(result.HandoffHistory),
		)
	}
}

// --- Mock tools for the example ---

func checkBalanceTool() tool.Tool {
	type input struct {
		AccountID string `json:"account_id" description:"The customer account ID" required:"true"`
	}
	return tool.New("check_balance", "Look up account balance and billing info", func(_ context.Context, in input) (string, error) {
		return fmt.Sprintf(`{"account_id": "%s", "balance": "$42.50", "plan": "Pro", "next_billing": "2026-05-01"}`, in.AccountID), nil
	})
}

func searchDocsTool() tool.Tool {
	type input struct {
		Query string `json:"query" description:"Search query for documentation" required:"true"`
	}
	return tool.New("search_docs", "Search technical documentation", func(_ context.Context, in input) (string, error) {
		return fmt.Sprintf(`{"results": [{"title": "Troubleshooting Guide", "snippet": "For '%s': Check your config file at ~/.app/config.yaml and ensure all required fields are set."}]}`, in.Query), nil
	})
}
