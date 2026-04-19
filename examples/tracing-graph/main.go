// Example: Traced graph workflow with fork/join, tools, and memory.
//
// Builds a content-review pipeline as a graph and traces every node execution.
// The graph forks into parallel enrich + summarise + sentiment, then joins
// into a final report.
//
//	fetch → [enrich, summarise, sentiment] → report
//
// The trace tree shows:
//   - graph.run / graph.node.* spans for the graph workflow
//   - agent.invoke / agent.iteration / agent.provider.call for each agent
//   - agent.tool.word_count for the enrich agent's tool call
//   - agent.memory.load / agent.memory.save for the summarise agent's memory
//
// Run:
//
//	go run ./tracing-graph

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/graph"
	"github.com/camilbinas/gude-agents/agent/memory"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/camilbinas/gude-agents/agent/tracing"
	"github.com/camilbinas/gude-agents/examples/utils"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load() //nolint

	ctx := context.Background()

	// 1. Set up tracing with the shared tree exporter.
	exp := utils.NewTreeExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	defer tp.Shutdown(ctx)
	otel.SetTracerProvider(tp)

	// 2. Create Claude Haiku via Bedrock.
	haiku := bedrock.Must(bedrock.ClaudeHaiku4_5())

	// 3. Build agents for each pipeline step.

	// Enrich agent — has a word_count tool. Produces agent.tool.word_count spans.
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
	), []tool.Tool{wordCountTool}, tracing.WithTracing(nil))
	if err != nil {
		log.Fatal(err)
	}

	// Summarise agent — has memory. Produces agent.memory.load / agent.memory.save spans.
	store := memory.NewStore()
	summariser, err := agent.Minimal(haiku, prompt.Text(
		"Summarise the provided article in 2-3 sentences. Return only the summary.",
	), nil,
		tracing.WithTracing(nil),
		agent.WithMemory(store, "summarise-session"),
	)
	if err != nil {
		log.Fatal(err)
	}

	// Sentiment agent — plain, no tools or memory.
	sentimentAnalyser, err := agent.Minimal(haiku, prompt.Text(
		"Analyse the sentiment of the provided article. "+
			"Return a single word: Positive, Negative, or Neutral.",
	), nil, tracing.WithTracing(nil))
	if err != nil {
		log.Fatal(err)
	}

	// 4. Build the graph with tracing.
	g, err := graph.NewGraph(
		graph.WithGraphMaxIterations(20),
		tracing.WithGraphTracing(nil),
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

	// 5. Run the graph.
	fmt.Println("Running traced graph pipeline...")
	fmt.Println()

	result, err := g.Run(ctx, graph.State{})
	if err != nil {
		log.Fatal(err)
	}

	// Flush the tree exporter so the trace prints before the results.
	exp.Flush()

	fmt.Println(result.State["report"])
	fmt.Printf("\nTokens: %d in / %d out\n", result.Usage.InputTokens, result.Usage.OutputTokens)
}
