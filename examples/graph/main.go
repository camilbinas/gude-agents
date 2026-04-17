// Run:
//
//	go run ./graph

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

// This example builds a content-review pipeline using agent.Graph:
//
//   fetch → [summarise, sentiment] → report
//
// "fetch" simulates retrieving an article.
// "summarise" and "sentiment" run in parallel (fork).
// "report" waits for both (join) and combines their outputs.

func main() {
	godotenv.Load() //nolint

	haiku, err := bedrock.ClaudeHaiku4_5()
	if err != nil {
		log.Fatal(err)
	}

	// --- Agents for each LLM step ---

	summariser, err := agent.Worker(haiku, prompt.Text(
		"Summarise the provided article in 2-3 sentences. Return only the summary.",
	), nil)
	if err != nil {
		log.Fatal(err)
	}

	sentimentAnalyser, err := agent.Worker(haiku, prompt.Text(
		"Analyse the sentiment of the provided article. "+
			"Return a single word: Positive, Negative, or Neutral.",
	), nil)
	if err != nil {
		log.Fatal(err)
	}

	// --- Build the graph ---

	g, err := graph.NewGraph(
		graph.WithGraphMaxIterations(20),
		graph.WithGraphLogger(log.Default()),
	)
	if err != nil {
		log.Fatal(err)
	}

	// Node: fetch — simulates loading an article into state["article"]
	if err := g.AddNode("fetch", func(_ context.Context, s graph.State) (graph.State, error) {
		out := graph.State{
			"article": "Scientists have discovered a new species of deep-sea fish that " +
				"produces its own bioluminescent light. The discovery, made 3,000 metres " +
				"below the Pacific Ocean, could shed light on how life adapts to extreme " +
				"environments. Researchers are optimistic about future findings.",
		}
		return out, nil
	}); err != nil {
		log.Fatal(err)
	}

	// Node: summarise — wraps the summariser agent
	if err := g.AddNode("summarise", graph.AgentNode(summariser, "article", "summary")); err != nil {
		log.Fatal(err)
	}

	// Node: sentiment — wraps the sentiment agent
	if err := g.AddNode("sentiment", graph.AgentNode(sentimentAnalyser, "article", "sentiment")); err != nil {
		log.Fatal(err)
	}

	// Node: report — combines summary + sentiment into a final report
	if err := g.AddNode("report", func(_ context.Context, s graph.State) (graph.State, error) {
		summary, _ := s["summary"].(string)
		sentiment, _ := s["sentiment"].(string)
		report := fmt.Sprintf("=== Content Report ===\nSentiment : %s\nSummary   : %s",
			strings.TrimSpace(sentiment),
			strings.TrimSpace(summary),
		)
		return graph.State{"report": report}, nil
	}); err != nil {
		log.Fatal(err)
	}

	// Wiring
	g.SetEntry("fetch")
	if err := g.AddFork("fetch", []string{"summarise", "sentiment"}); err != nil {
		log.Fatal(err)
	}
	if err := g.AddJoin("report", []string{"summarise", "sentiment"}); err != nil {
		log.Fatal(err)
	}

	// --- Run ---

	result, err := g.Run(context.Background(), graph.State{})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(result.State["report"])
	fmt.Printf("\nTokens used: %d in / %d out\n",
		result.Usage.InputTokens,
		result.Usage.OutputTokens,
	)
}
