// Run: go run ./multi-agent
//
// An orchestrator delegates to two focused in-process specialists through
// AgentAsTool. The specialists do not need a network protocol or a second
// deployment when they live in the same service.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
)

type destinationInput struct {
	City string `json:"city" description:"Destination city" required:"true"`
}

func main() {
	provider := bedrock.Must(bedrock.Standard())
	researcher, err := agent.New(provider, "You research a destination using the supplied facts.", agent.WithTools(
		tool.New("destination_facts", "Return destination facts", func(_ context.Context, in destinationInput) (string, error) {
			return fmt.Sprintf(`{"city":%q,"highlights":["historic temples","walkable neighborhoods"]}`, in.City), nil
		}),
	))
	if err != nil {
		log.Fatal(err)
	}
	planner, err := agent.New(provider, "You make compact travel plans using the supplied facts.", agent.WithTools(
		tool.New("trip_constraints", "Return planning constraints", func(_ context.Context, in destinationInput) (string, error) {
			return fmt.Sprintf(`{"city":%q,"duration_days":3,"style":"quiet and food-focused"}`, in.City), nil
		}),
	))
	if err != nil {
		log.Fatal(err)
	}

	orchestrator, err := agent.New(
		provider,
		"You coordinate specialists. Delegate research and planning when useful, then combine their answers into one itinerary.",
		agent.WithTools(
			agent.AgentAsTool("research_destination", "Research a travel destination.", researcher),
			agent.AgentAsTool("plan_trip", "Plan a trip using constraints.", planner),
		),
	)
	if err != nil {
		log.Fatal(err)
	}
	result, err := orchestrator.Invoke(agent.Background(), "Plan a quiet three-day trip to Kyoto with good food.")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result.Text)
}
