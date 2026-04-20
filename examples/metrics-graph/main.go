// Example: Prometheus metrics for a graph workflow with fork/join.
//
// Builds a content-review pipeline as a graph and records Prometheus metrics
// for every graph run and node execution.
// The graph forks into parallel enrich + summarise + sentiment, then joins
// into a final report.
//
//	fetch → [enrich, summarise, sentiment] → report
//
// Metrics are exposed at http://localhost:2112/metrics for scraping.
//
// Run:
//
//	go run ./metrics-graph

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/graph"
	"github.com/camilbinas/gude-agents/agent/memory"
	"github.com/camilbinas/gude-agents/agent/metrics/prometheus"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load() //nolint

	ctx := context.Background()

	// 1. Set up Prometheus metrics — shared registry via NewHandler.
	agentOpt, handler := prometheus.NewHandler()

	// 2. Create Claude Haiku via Bedrock.
	haiku := bedrock.Must(bedrock.Cheapest())

	// 3. Build agents for each pipeline step.

	// Enrich agent — has a word_count tool.
	wordCountTool := tool.NewRaw(
		"word_count",
		"Count the number of words in a text",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text": map[string]any{
					"type":        "string",
					"description": "The text to count words in",
				},
			},
			"required": []any{"text"},
		},
		func(_ context.Context, input json.RawMessage) (string, error) {
			var req struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(input, &req); err != nil {
				return "", err
			}
			count := len(strings.Fields(req.Text))
			return fmt.Sprintf(`{"word_count": %d}`, count), nil
		},
	)

	enricher, err := agent.Minimal(haiku, prompt.Text(
		"You enrich articles with metadata. Use the word_count tool to count "+
			"the words in the article, then return a short metadata line like: "+
			"\"Words: 42, Reading time: ~1 min\". Return only the metadata line.",
	), []tool.Tool{wordCountTool},
		agent.WithName("enricher"),
		agentOpt,
	)
	if err != nil {
		log.Fatal(err)
	}

	// Summarise agent — has memory.
	store := memory.NewStore()
	summariser, err := agent.Minimal(haiku, prompt.Text(
		"Summarise the provided article in 2-3 sentences. Return only the summary.",
	), nil,
		agent.WithName("summariser"),
		prometheus.WithMetrics(),
		agent.WithMemory(store, "summarise-session"),
	)
	if err != nil {
		log.Fatal(err)
	}

	// Sentiment agent — plain, no tools or memory.
	sentimentAnalyser, err := agent.Minimal(haiku, prompt.Text(
		"Analyse the sentiment of the provided article. "+
			"Return a single word: Positive, Negative, or Neutral.",
	), nil,
		agent.WithName("sentiment"),
		prometheus.WithMetrics(),
	)
	if err != nil {
		log.Fatal(err)
	}

	// 4. Build the graph with metrics.
	g, err := graph.NewGraph(
		graph.WithGraphMaxIterations(20),
		prometheus.WithGraphMetrics(),
	)
	if err != nil {
		log.Fatal(err)
	}

	// Node: fetch — simulates loading an article.
	if err := g.AddNode("fetch", func(_ context.Context, _ graph.State) (graph.State, error) {
		return graph.State{
			"article": "Scientists have discovered a new species of deep-sea fish that " +
				"produces its own bioluminescent light. The discovery, made 3,000 metres " +
				"below the Pacific Ocean, could shed light on how life adapts to extreme " +
				"environments. Researchers are optimistic about future findings.",
		}, nil
	}); err != nil {
		log.Fatal(err)
	}

	// Node: enrich — uses the word_count tool.
	if err := g.AddNode("enrich", graph.AgentNode(enricher, "article", "metadata")); err != nil {
		log.Fatal(err)
	}

	// Node: summarise — uses memory.
	if err := g.AddNode("summarise", graph.AgentNode(summariser, "article", "summary")); err != nil {
		log.Fatal(err)
	}

	// Node: sentiment — plain agent.
	if err := g.AddNode("sentiment", graph.AgentNode(sentimentAnalyser, "article", "sentiment")); err != nil {
		log.Fatal(err)
	}

	// Node: report — combines all results.
	if err := g.AddNode("report", func(_ context.Context, s graph.State) (graph.State, error) {
		summary, _ := s["summary"].(string)
		sentiment, _ := s["sentiment"].(string)
		metadata, _ := s["metadata"].(string)
		report := fmt.Sprintf("=== Content Report ===\n"+
			"Metadata  : %s\n"+
			"Sentiment : %s\n"+
			"Summary   : %s",
			strings.TrimSpace(metadata),
			strings.TrimSpace(sentiment),
			strings.TrimSpace(summary),
		)
		return graph.State{"report": report}, nil
	}); err != nil {
		log.Fatal(err)
	}

	// Wiring: fetch forks into enrich + summarise + sentiment, report joins them.
	g.SetEntry("fetch")
	if err := g.AddFork("fetch", []string{"enrich", "summarise", "sentiment"}); err != nil {
		log.Fatal(err)
	}
	if err := g.AddJoin("report", []string{"enrich", "summarise", "sentiment"}); err != nil {
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

	// 6. Run the graph.
	fmt.Println("Running metrics graph pipeline...")
	fmt.Println()

	result, err := g.Run(ctx, graph.State{})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(result.State["report"])
	fmt.Printf("\nTokens: %d in / %d out\n", result.Usage.InputTokens, result.Usage.OutputTokens)
	fmt.Println("\nMetrics available at http://localhost:2112/metrics")
	fmt.Println("Press Ctrl+C to exit.")

	// Keep alive so metrics endpoint stays up.
	select {}
}
