// Example: Typed graph for a simple research → summarise → score pipeline.
//
// TypedGraph wraps the untyped Graph with generics so nodes work directly
// with a concrete state struct — no map[string]any, no type assertions.
//
// Embedding graph.GraphState enables automatic token usage accumulation via
// s.AddUsage(usage) — no manual token fields needed on the state struct.
//
// Pipeline:
//
//	research → summarise → score → gate ──(score < 9)──► refine → summarise
//	                                  └──(score >= 9)──► done
//
// Run:
//
//	go run ./graph-typed

package main

import (
	"context"
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/graph"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/anthropic"
	"github.com/joho/godotenv"
)

// State flows through every node as a plain struct.
// Embedding graph.GraphState enables automatic token tracking via AddUsage.
type State struct {
	graph.GraphState
	Topic    string `json:"topic"`
	Research string `json:"research"`
	Summary  string `json:"summary"`
	Score    int    `json:"score"`
	Feedback string `json:"feedback"`
}

func main() {
	godotenv.Load() //nolint

	ctx := context.Background()

	provider := anthropic.Must(anthropic.ClaudeHaiku4_5())

	researcher, err := agent.Worker(provider, prompt.Text(
		"You are a researcher. Given a topic, write 3-4 sentences of key facts. Return only the facts.",
	), nil)
	if err != nil {
		log.Fatal(err)
	}

	summariser, err := agent.Worker(provider, prompt.Text(
		"You are a writer. Summarise the provided research into one clear sentence. Return only the sentence.",
	), nil)
	if err != nil {
		log.Fatal(err)
	}

	type ScoreResult struct {
		Score    int    `json:"score"    description:"Quality score 1-10"`
		Feedback string `json:"feedback" description:"One sentence of improvement advice"`
	}

	scorer, err := agent.Worker(provider, prompt.Text(
		"You are a strict editor. Rate the summary quality from 1 to 10. "+
			"Score 6 or below if the summary is vague or lacks concrete details. "+
			"Score 9 or 10 only if it includes at least one specific example, metric, or concrete detail. "+
			"Score 7-8 if it is accurate but still somewhat general.",
	), nil)
	if err != nil {
		log.Fatal(err)
	}

	refiner, err := agent.Worker(provider, prompt.Text(
		"You are an editor. Rewrite the summary to address the feedback. "+
			"Make it more specific — include at least one concrete example, metric, or tool name. "+
			"Return only the improved sentence.",
	), nil)
	if err != nil {
		log.Fatal(err)
	}

	// Build the typed graph — nodes receive and return State directly.
	g, err := graph.NewTypedGraph[State](
		graph.WithGraphMaxIterations(20),
		graph.WithGraphLogger(log.Default()),
	)
	if err != nil {
		log.Fatal(err)
	}

	if err := g.AddNode("research", func(ctx context.Context, s State) (State, error) {
		facts, usage, err := researcher.Invoke(ctx, s.Topic)
		if err != nil {
			return s, err
		}
		s.Research = facts
		s.AddUsage(usage)
		return s, nil
	}); err != nil {
		log.Fatal(err)
	}

	if err := g.AddNode("summarise", func(ctx context.Context, s State) (State, error) {
		summary, usage, err := summariser.Invoke(ctx, s.Research)
		if err != nil {
			return s, err
		}
		s.Summary = summary
		s.AddUsage(usage)
		return s, nil
	}); err != nil {
		log.Fatal(err)
	}

	if err := g.AddNode("score", func(ctx context.Context, s State) (State, error) {
		result, usage, err := agent.InvokeStructured[ScoreResult](ctx, scorer, s.Summary)
		if err != nil {
			return s, err
		}
		s.Score = result.Score
		s.Feedback = result.Feedback
		s.AddUsage(usage)
		return s, nil
	}); err != nil {
		log.Fatal(err)
	}

	if err := g.AddNode("refine", func(ctx context.Context, s State) (State, error) {
		input := fmt.Sprintf("Summary: %s\nFeedback: %s", s.Summary, s.Feedback)
		refined, usage, err := refiner.Invoke(ctx, input)
		if err != nil {
			return s, err
		}
		s.Research = refined // feed refined text back into summarise
		s.AddUsage(usage)
		return s, nil
	}); err != nil {
		log.Fatal(err)
	}

	if err := g.AddNode("done", func(_ context.Context, s State) (State, error) {
		return s, nil
	}); err != nil {
		log.Fatal(err)
	}

	// Wire the graph.
	g.SetEntry("research")
	if err := g.AddEdge("research", "summarise"); err != nil {
		log.Fatal(err)
	}
	if err := g.AddEdge("summarise", "score"); err != nil {
		log.Fatal(err)
	}
	if err := g.AddConditionalEdge("score", func(_ context.Context, s State) (string, error) {
		if s.Score < 9 {
			log.Printf("[graph] score %d — refining: %s", s.Score, s.Feedback)
			return "refine", nil
		}
		return "done", nil
	}); err != nil {
		log.Fatal(err)
	}
	if err := g.AddEdge("refine", "summarise"); err != nil {
		log.Fatal(err)
	}

	// Run with an initial state — only Topic needs to be set.
	result, err := g.Run(ctx, State{Topic: "the impact of large language models on software engineering"})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("\nFinal summary (score %d/10):\n%s\n", result.State.Score, result.State.Summary)
	fmt.Printf("Feedback: %s\n", result.State.Feedback)
	fmt.Printf("Tokens: %d in / %d out\n", result.Usage.InputTokens, result.Usage.OutputTokens)
}
