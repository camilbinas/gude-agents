// Example: OpenTelemetry tracing for a multi-agent swarm.
//
// Demonstrates swarm-level tracing where every handoff, agent turn, and tool
// execution produces OTEL spans under a single swarm.run root span.
//
// The swarm has three agents:
//   - triage: routes requests to the right specialist
//   - billing: handles payment and invoice questions
//   - technical: handles bugs and how-to questions
//
// Spans are printed to stderr as a formatted tree after each invocation.
//
// Run:
//
//	go run ./tracing-swarm
//
// Sample trace tree:
//
//   swarm.run  (3.08s) ✓  total_in=1909 total_out=222  ⚡swarm.handoff
//    ├─ swarm.agent.triage  (1.12s) ·
//    │  └─ agent.iteration  (1.12s) ·  iteration=1 tools=1
//    │     ├─ agent.provider.call  (1.12s) ·  in=564 out=68 tool_calls=1
//    │     └─ agent.tool.transfer_to_billing  (18.0µs) ·
//    └─ swarm.agent.billing  (1.96s) ·
//       ├─ agent.iteration  (841.3ms) ·  iteration=1 tools=1
//       │  ├─ agent.provider.call  (841.1ms) ·  in=609 out=56 tool_calls=1
//       │  └─ agent.tool.check_balance  (63.0µs) ·
//       └─ agent.iteration  (1.12s) ·  iteration=2 final
//          └─ agent.provider.call  (1.12s) ·  in=736 out=98

package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/camilbinas/gude-agents/agent/tracing"
	"github.com/camilbinas/gude-agents/examples/utils"
)

func main() {
	ctx := context.Background()

	// 1. Set up tracing — console tree formatter.
	treeExp := utils.NewTreeExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(treeExp))
	otel.SetTracerProvider(tp)
	defer func() {
		if err := tp.Shutdown(ctx); err != nil {
			log.Printf("tracing shutdown: %v", err)
		}
	}()

	// 2. Create a provider.
	provider := bedrock.Must(bedrock.Standard())

	// 3. Build agents with tracing enabled on each one.
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
		tracing.WithTracing(nil),
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
		tracing.WithTracing(nil),
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
		tracing.WithTracing(nil),
	)
	if err != nil {
		log.Fatal(err)
	}

	// 4. Build the swarm with tracing + memory.
	sw, err := agent.NewSwarm([]agent.SwarmMember{
		{Name: "triage", Description: "Routes requests to the right specialist", Agent: triageAgent},
		{Name: "billing", Description: "Handles invoices, payments, refunds, and subscriptions", Agent: billingAgent},
		{Name: "technical", Description: "Handles bugs, errors, and technical how-to questions", Agent: techAgent},
	},
		tracing.WithSwarmTracing(nil),
		agent.WithSwarmMaxHandoffs(5),
		agent.WithSwarmConversation(conversation.NewInMemory(), "support-session"),
	)
	if err != nil {
		log.Fatal(err)
	}

	// 5. Interactive loop.
	fmt.Println("Traced swarm ready. Type 'quit' to exit.")
	fmt.Println("Try: What's my account balance for ACC-123?")
	fmt.Println("Try: How do I fix a config file error?")
	fmt.Println()

	utils.SwarmChat(ctx, sw)
}
