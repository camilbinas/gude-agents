// Example: Typed graph for a research → summarise → score → (refine | accept) pipeline.
//
// Graph[S] uses generics so nodes work directly with a concrete state struct
// — no map[string]any, no type assertions.
//
// Token usage is accumulated via the package-level graph.AddUsage(ctx, usage)
// function — it threads usage through the node's context, so the state struct
// needs no embedded type or manual token fields.
//
// For typed state, readiness is determined by non-zero struct field values.
// We use two bool fields (NeedsRefine / Accepted) as conditional route keys —
// only one becomes non-zero, so only the corresponding downstream node runs.
//
// Pipeline:
//
//	research → summarise → score ─(score < 9 → NeedsRefine)─► refine
//	                            └─(score ≥ 9 → Accepted)──► (done)
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
// Token tracking is handled by graph.AddUsage(ctx, usage) — no embedded type needed.
type State struct {
	Topic       string `json:"topic"`
	Research    string `json:"research"`
	Summary     string `json:"summary"`
	Score       int    `json:"score"`
	Feedback    string `json:"feedback"`
	NeedsRefine bool   `json:"needs_refine,omitempty"`
	Accepted    bool   `json:"accepted,omitempty"`
	Refined     string `json:"refined,omitempty"`
}

// FinalSummary picks the refined version when present, otherwise the original.
func (s State) FinalSummary() string {
	if s.Refined != "" {
		return s.Refined
	}
	return s.Summary
}

func main() {
	godotenv.Load() //nolint

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

	g, err := graph.New[State](graph.WithMaxIterations(20))
	if err != nil {
		log.Fatal(err)
	}

	// research is the entry node — it reads s.Topic from the initial state directly
	// (no declared input key) and writes "research" for downstream nodes.
	if _, err := g.Node("research", func(ctx context.Context, s State) (State, error) {
		c := agent.NewContext(ctx)
		facts, err := researcher.Invoke(c, s.Topic)
		if err != nil {
			return s, err
		}
		s.Research = facts
		graph.AddUsage(ctx, c.Usage())
		return s, nil
	}, graph.In(), graph.Out("research")); err != nil {
		log.Fatal(err)
	}

	if _, err := g.Node("summarise", func(ctx context.Context, s State) (State, error) {
		c := agent.NewContext(ctx)
		summary, err := summariser.Invoke(c, s.Research)
		if err != nil {
			return s, err
		}
		s.Summary = summary
		graph.AddUsage(ctx, c.Usage())
		return s, nil
	}, graph.In("research"), graph.Out("summary")); err != nil {
		log.Fatal(err)
	}

	// score writes exactly one of needs_refine / accepted depending on the score value.
	// The other field stays at the zero value (false) and the corresponding downstream
	// node never becomes ready.
	if _, err := g.Node("score", func(ctx context.Context, s State) (State, error) {
		c := agent.NewContext(ctx)
		result, err := agent.InvokeStructured[ScoreResult](c, scorer, s.Summary)
		if err != nil {
			return s, err
		}
		s.Score = result.Score
		s.Feedback = result.Feedback
		if s.Score < 9 {
			s.NeedsRefine = true
		} else {
			s.Accepted = true
		}
		graph.AddUsage(ctx, c.Usage())
		return s, nil
	}, graph.In("summary"), graph.Out("needs_refine", "accepted")); err != nil {
		log.Fatal(err)
	}

	// refine only runs when NeedsRefine is true.
	if _, err := g.Node("refine", func(ctx context.Context, s State) (State, error) {
		c := agent.NewContext(ctx)
		input := fmt.Sprintf("Summary: %s\nFeedback: %s", s.Summary, s.Feedback)
		refined, err := refiner.Invoke(c, input)
		if err != nil {
			return s, err
		}
		s.Refined = refined
		graph.AddUsage(ctx, c.Usage())
		return s, nil
	}, graph.In("needs_refine"), graph.Out("refined")); err != nil {
		log.Fatal(err)
	}

	result, err := g.Run(context.Background(), State{Topic: "the impact of large language models on software engineering"})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("\nFinal summary (score %d/10):\n%s\n", result.State.Score, result.State.FinalSummary())
	if result.State.NeedsRefine {
		fmt.Printf("Feedback: %s\n", result.State.Feedback)
	}
	fmt.Printf("Tokens: %d in / %d out\n", result.Usage.InputTokens, result.Usage.OutputTokens)
}
