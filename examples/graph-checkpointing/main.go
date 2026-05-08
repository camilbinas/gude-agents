// Example: Checkpointing with interrupt, resume, rewind, and step-by-step execution.
//
// Demonstrates:
//   - Node handles for interrupt configuration (review.InterruptBefore())
//   - Human-in-the-loop: pause → inject state → resume
//   - RewindTo for replaying from a checkpoint
//   - Then() for pure sequencing in step-by-step mode
//
// Pipeline: draft → review (interrupt) → publish
//
// Run:
//
//	go run ./graph-checkpointing

package main

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent/graph"
	"github.com/camilbinas/gude-agents/agent/graph/checkpointer/memory"
)

func main() {
	ctx := context.Background()
	cp := memory.New()

	g, err := graph.New[graph.State](graph.WithCheckpointer(cp))
	if err != nil {
		log.Fatal(err)
	}

	// Data-flow wiring: draft → review → publish via keys.
	g.Node("draft", func(_ context.Context, s graph.State) (graph.State, error) {
		topic, _ := s["topic"].(string)
		out := graph.CopyState(s)
		out["document"] = fmt.Sprintf("Draft about %q: This is a well-researched article.", topic)
		return out, nil
	}, graph.Out("document"))

	review, _ := g.Node("review", func(_ context.Context, s graph.State) (graph.State, error) {
		out := graph.CopyState(s)
		out["reviewed"] = true
		return out, nil
	}, graph.In("document"), graph.Out("reviewed"))

	g.Node("publish", func(_ context.Context, s graph.State) (graph.State, error) {
		out := graph.CopyState(s)
		if s["approved"] == true {
			out["status"] = "published ✓"
		} else {
			out["status"] = "rejected ✗"
		}
		return out, nil
	}, graph.In("reviewed"), graph.Out("status"))

	// Interrupt before review — human must approve.
	review.InterruptBefore()

	threadID := "approval-thread-1"

	// ─── Step 1: Run until interrupt ─────────────────────────────────────

	fmt.Println("=== Step 1: Run until interrupt ===")
	_, err = g.Run(ctx, graph.State{"topic": "AI safety"}, graph.WithThreadID(threadID))

	var intErr *graph.GraphInterruptError
	if !errors.As(err, &intErr) {
		log.Fatalf("expected interrupt, got: %v", err)
	}

	fmt.Printf("Paused before node: %s\n", intErr.Result.NodeName)
	fmt.Printf("Draft: %s\n", intErr.Result.Checkpoint.State["document"])
	fmt.Printf("Checkpoint version: %d\n\n", intErr.Result.Checkpoint.Version)

	// ─── Step 2: Resume with human approval ──────────────────────────────

	fmt.Println("=== Step 2: Resume with approval ===")
	updates := graph.State{"approved": true}
	result, err := g.Resume(ctx, threadID, &updates)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Final status: %s\n\n", result.State["status"])

	// ─── Step 3: Rewind and replay with rejection ────────────────────────

	fmt.Println("=== Step 3: Rewind to checkpoint 1 and reject ===")

	if err := g.RewindTo(ctx, threadID, 1); err != nil {
		log.Fatal(err)
	}

	rejectUpdates := graph.State{"approved": false}
	_, err = g.Resume(ctx, threadID, &rejectUpdates)
	if !errors.As(err, &intErr) {
		log.Fatalf("expected interrupt after rewind, got: %v", err)
	}
	fmt.Printf("Paused again before: %s (as expected)\n", intErr.Result.NodeName)

	rejectUpdates2 := graph.State{"approved": false}
	result, err = g.Resume(ctx, threadID, &rejectUpdates2)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Final status after rejection: %s\n\n", result.State["status"])

	// ─── Bonus: Step-by-step with Then() ─────────────────────────────────

	fmt.Println("=== Bonus: Step-by-step execution with Then() ===")

	g2, _ := graph.New[graph.State](graph.WithCheckpointer(cp))

	// Pure sequencing — no data keys, just ordering via Then().
	a, _ := g2.Node("a", func(_ context.Context, s graph.State) (graph.State, error) {
		s["a"] = "done"
		return s, nil
	})
	b, _ := g2.Node("b", func(_ context.Context, s graph.State) (graph.State, error) {
		s["b"] = "done"
		return s, nil
	})
	c, _ := g2.Node("c", func(_ context.Context, s graph.State) (graph.State, error) {
		s["c"] = "done"
		return s, nil
	})

	a.Then(b)
	b.Then(c)

	stepThread := "step-thread-1"
	var res graph.StepResult[graph.State]
	for {
		res, err = g2.Step(ctx, graph.State{}, stepThread)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("  Executed: %s (version %d, done=%v)\n", res.NodeName, res.Version, res.Done)
		if res.Done {
			break
		}
	}
}
