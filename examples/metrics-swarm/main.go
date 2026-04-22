// Example: Prometheus metrics for a multi-agent swarm.
//
// Demonstrates swarm-level metrics where every handoff, agent turn, and tool
// execution produces Prometheus counters and histograms.
//
// The swarm has three agents:
//   - triage: routes requests to the right specialist
//   - billing: handles payment and invoice questions
//   - technical: handles bugs and how-to questions
//
// Metrics are exposed at http://localhost:2112/metrics for scraping.
//
// Run:
//
//	go run ./metrics-swarm

package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/memory"
	"github.com/camilbinas/gude-agents/agent/metrics/prometheus"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/camilbinas/gude-agents/examples/utils"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load() //nolint

	ctx := context.Background()

	// 1. Set up Prometheus metrics — shared registry via NewHandler.
	agentOpt, handler := prometheus.NewHandler()

	// 2. Create a provider.
	provider := bedrock.Must(bedrock.Standard())

	// 3. Build agents with metrics enabled on each one.
	triageAgent, err := agent.SwarmAgent(
		provider,
		prompt.Text(strings.Join([]string{
			"You are a customer support triage agent.",
			"Determine what the user needs and hand off to the right specialist.",
			"Use transfer_to_billing for payment/invoice questions.",
			"Use transfer_to_technical for bugs/errors/how-to questions.",
			"Never answer questions yourself — always hand off.",
		}, " ")),
		nil,
		agent.WithName("triage"),
		agentOpt,
	)
	if err != nil {
		log.Fatal(err)
	}

	billingAgent, err := agent.SwarmAgent(
		provider,
		prompt.Text(strings.Join([]string{
			"You are a billing support specialist.",
			"Help users with invoices, payments, refunds, and subscription questions.",
			"Use the check_balance tool to look up account info.",
			"If the question is not about billing, transfer back to triage.",
		}, " ")),
		[]tool.Tool{utils.CheckBalanceTool()},
		agent.WithName("billing"),
		prometheus.WithMetrics(),
	)
	if err != nil {
		log.Fatal(err)
	}

	techAgent, err := agent.SwarmAgent(
		provider,
		prompt.Text(strings.Join([]string{
			"You are a technical support specialist.",
			"Help users with bugs, errors, configuration, and how-to questions.",
			"Use the search_docs tool to find relevant documentation.",
			"If the question is not technical, transfer back to triage.",
		}, " ")),
		[]tool.Tool{utils.SearchDocsTool()},
		agent.WithName("technical"),
		prometheus.WithMetrics(),
	)
	if err != nil {
		log.Fatal(err)
	}

	// 4. Build the swarm with metrics + memory.
	sw, err := agent.NewSwarm([]agent.SwarmMember{
		{Name: "triage", Description: "Routes requests to the right specialist", Agent: triageAgent},
		{Name: "billing", Description: "Handles invoices, payments, refunds, and subscriptions", Agent: billingAgent},
		{Name: "technical", Description: "Handles bugs, errors, and technical how-to questions", Agent: techAgent},
	},
		prometheus.WithSwarmMetrics(),
		agent.WithSwarmMaxHandoffs(5),
		agent.WithSwarmMemory(memory.NewStore(), "support-session"),
	)
	if err != nil {
		log.Fatal(err)
	}

	// 5. Start Prometheus HTTP endpoint.
	go func() {
		http.Handle("/metrics", handler)
		fmt.Println("Prometheus metrics at http://localhost:2112/metrics")
		if err := http.ListenAndServe(":2112", nil); err != nil {
			log.Printf("metrics server: %v", err)
		}
	}()

	// 6. Interactive loop.
	fmt.Println("Metrics swarm ready. Type 'quit' to exit.")
	fmt.Println("Try: What's my account balance for ACC-123?")
	fmt.Println("Try: How do I fix a config file error?")
	fmt.Println()

	utils.SwarmChat(ctx, sw)
}
