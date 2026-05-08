// Example: LLM-powered conditional routing via data-flow gating.
//
// Demonstrates:
//   - Conditional execution: classifier writes one route key, only the matching expert runs
//   - Agent nodes with In/Out
//   - Data-flow gating (no explicit if/else routing — the engine handles it)
//
// Pipeline: classify → (code_expert | devops_expert | general_expert)
//
// Run:
//
//	go run ./graph-llm-router

package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/graph"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load() //nolint

	ctx := agent.Background()

	provider := bedrock.Must(bedrock.Standard())
	classifier := bedrock.Must(bedrock.Cheapest())

	router, err := agent.Worker(classifier, prompt.Text(
		"You are a question classifier. Reply with EXACTLY one word from this list: "+
			"code, devops, general.",
	), nil)
	if err != nil {
		log.Fatal(err)
	}

	codeExpert, err := agent.Worker(provider, prompt.Text(
		"You are a senior software engineer. Answer programming questions concisely.",
	), nil)
	if err != nil {
		log.Fatal(err)
	}

	devopsExpert, err := agent.Worker(provider, prompt.Text(
		"You are a DevOps engineer. Answer infrastructure questions concisely.",
	), nil)
	if err != nil {
		log.Fatal(err)
	}

	generalExpert, err := agent.Worker(provider, prompt.Text(
		"You are a helpful assistant. Answer general questions concisely.",
	), nil)
	if err != nil {
		log.Fatal(err)
	}

	g, err := graph.New[graph.State]()
	if err != nil {
		log.Fatal(err)
	}

	// Classifier: reads "question", conditionally writes ONE route key.
	// Only the expert whose route key is written becomes ready.
	g.Node("classify", func(ctx context.Context, s graph.State) (graph.State, error) {
		c := agent.NewContext(ctx)
		choice, err := router.Invoke(c, s["question"].(string))
		if err != nil {
			return s, err
		}
		out := graph.CopyState(s)
		switch strings.ToLower(strings.TrimSpace(choice)) {
		case "code":
			out["route_code"] = true
		case "devops":
			out["route_devops"] = true
		default:
			out["route_general"] = true
		}
		return out, nil
	}, graph.In("question"), graph.Out("route_code", "route_devops", "route_general"))

	// Each expert gates on its own route key — only one runs per execution.
	g.Agent("code_expert", codeExpert, graph.In("question", "route_code"), graph.Out("answer"))
	g.Agent("devops_expert", devopsExpert, graph.In("question", "route_devops"), graph.Out("answer"))
	g.Agent("general_expert", generalExpert, graph.In("question", "route_general"), graph.Out("answer"))

	// Run with different questions.
	questions := []string{
		"How do I reverse a linked list in Go?",
		"What's the best way to set up a CI/CD pipeline with GitHub Actions?",
		"What's a good recipe for banana bread?",
	}

	for _, q := range questions {
		fmt.Printf("Q: %s\n", q)
		result, err := g.Run(ctx, graph.State{"question": q})
		if err != nil {
			log.Fatalf("Run: %v", err)
		}
		fmt.Printf("A: %s\n", result.State["answer"])
		fmt.Printf("--- tokens: %d in / %d out ---\n\n", result.Usage.InputTokens, result.Usage.OutputTokens)
	}
}
