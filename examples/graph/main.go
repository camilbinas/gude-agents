// Example: Content review pipeline with streaming agent nodes.
//
// Demonstrates:
//   - g.Node() and g.Agent() with In/Out for data-flow scheduling
//   - Automatic concurrency (summarise + sentiment run in parallel)
//   - Agent streaming with event hook integration
//   - SetEventHook for per-run hook swapping
//
// Pipeline: fetch → [summarise, sentiment] → report
//
// Run:
//
//	go run ./graph

package main

import (
	"context"
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/graph"
	"github.com/camilbinas/gude-agents/agent/logging/auto"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/examples/utils"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load() //nolint

	haiku := bedrock.Must(bedrock.Standard())

	summariser, err := agent.Worker(haiku, prompt.Text(
		"Summarise the provided article in 2-3 sentences. Return only the summary.",
	), nil, auto.WithLogging(), agent.WithName("summariser"))
	if err != nil {
		log.Fatal(err)
	}

	sentimentAnalyser, err := agent.Worker(haiku, prompt.Text(
		"Analyse the sentiment of the provided article. "+
			"Return a single word: Positive, Negative, or Neutral.",
	), nil, auto.WithLogging(), agent.WithName("sentiment"))
	if err != nil {
		log.Fatal(err)
	}

	// Build the graph once — topology is inferred from In/Out keys.
	g, err := graph.New[graph.State](
		graph.WithMaxIterations(20),
		auto.WithGraphLogging(),
	)
	if err != nil {
		log.Fatal(err)
	}

	// fetch produces "article" — entry node (no In keys).
	g.Node("fetch", func(_ context.Context, _ graph.State) (graph.State, error) {
		return graph.State{
			"article": "Scientists have discovered a new species of deep-sea fish that " +
				"produces its own bioluminescent light. The discovery, made 3,000 metres " +
				"below the Pacific Ocean, could shed light on how life adapts to extreme " +
				"environments. Researchers are optimistic about future findings.",
		}, nil
	}, graph.Out("article"))

	// summarise and sentiment both read "article" → run concurrently after fetch.
	g.Agent("summarise", summariser, graph.In("article"), graph.Out("summary"))
	g.Agent("sentiment", sentimentAnalyser, graph.In("article"), graph.Out("sentiment"))

	// report reads both outputs → waits for both to complete.
	g.Node("report", func(_ context.Context, s graph.State) (graph.State, error) {
		summary, _ := s["summary"].(string)
		sentiment, _ := s["sentiment"].(string)
		return graph.State{
			"report": fmt.Sprintf("=== Content Report ===\nSentiment : %s\nSummary   : %s", sentiment, summary),
		}, nil
	}, graph.In("summary", "sentiment"), graph.Out("report"))

	// Serve with devtools — swap event hook per run.
	dt := utils.NewDevTools(utils.DevToolsConfig{
		Port:      4040,
		Structure: g.Structure(),
		RunFunc: func(ctx context.Context, hook *utils.DevToolsHook) error {
			g.SetEventHook(hook)
			defer g.SetEventHook(nil)

			result, err := g.Run(ctx, graph.State{})
			if err != nil {
				return err
			}
			fmt.Println(result.State["report"])
			return nil
		},
	})

	log.Fatal(dt.ListenAndServe())
}
