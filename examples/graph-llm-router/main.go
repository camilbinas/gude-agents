// Example: LLM-powered router graph.
//
// An LLM classifies the user's question and routes it to the right specialist
// agent using graph.LLMRouter. Unlike a swarm (where agents hand off to each
// other), the graph controls the flow — the router picks the next node, and
// the specialist runs once and produces the final answer.
//
// Graph:
//
//	classify ──(LLM decides)──► code_expert
//	                          ├► devops_expert
//	                          └► general_expert
//
// Run:
//
//	go run ./graph-llm-router

package main

import (
	"context"
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/graph"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load() //nolint

	ctx := context.Background()

	provider := bedrock.Must(bedrock.Standard())

	// A cheap model for classification — it only picks a node name.
	classifier := bedrock.Must(bedrock.Cheapest())

	routerAgent, err := agent.Minimal(classifier, prompt.Text(
		"You are a question classifier. Based on the question, pick the best expert.\n"+
			"- code_expert: programming, algorithms, code review, debugging\n"+
			"- devops_expert: infrastructure, CI/CD, Docker, Kubernetes, cloud\n"+
			"- general_expert: anything else\n"+
			"Respond with ONLY the expert name, nothing else.",
	), nil)
	if err != nil {
		log.Fatal(err)
	}

	codeExpert, err := agent.Worker(provider, prompt.Text(
		"You are a senior software engineer. Answer programming questions with clear, "+
			"concise explanations and code examples when helpful. Be brief.",
	), nil)
	if err != nil {
		log.Fatal(err)
	}

	devopsExpert, err := agent.Worker(provider, prompt.Text(
		"You are a DevOps engineer. Answer infrastructure, deployment, and operations "+
			"questions with practical advice. Be brief.",
	), nil)
	if err != nil {
		log.Fatal(err)
	}

	generalExpert, err := agent.Worker(provider, prompt.Text(
		"You are a helpful assistant. Answer general questions clearly and concisely.",
	), nil)
	if err != nil {
		log.Fatal(err)
	}

	// Build the graph.
	g, err := graph.NewGraph()
	if err != nil {
		log.Fatal(err)
	}

	// Classify node — pass-through, routing happens on the conditional edge.
	g.AddNode("classify", func(_ context.Context, state graph.State) (graph.State, error) {
		return state, nil
	})

	// Specialist nodes using AgentNode — reads "question", writes "answer".
	g.AddNode("code_expert", graph.AgentNode(codeExpert, "question", "answer"))
	g.AddNode("devops_expert", graph.AgentNode(devopsExpert, "question", "answer"))
	g.AddNode("general_expert", graph.AgentNode(generalExpert, "question", "answer"))

	// Wire: classify → LLM picks one of the three experts.
	g.SetEntry("classify")
	g.AddConditionalEdge("classify", graph.LLMRouter(
		routerAgent,
		[]string{"code_expert", "devops_expert", "general_expert"},
	))

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
