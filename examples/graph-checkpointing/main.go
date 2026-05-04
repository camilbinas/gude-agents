// Example: Graph checkpointing with interrupt, resume, and rewind.
//
// This demonstrates a content-approval pipeline where a human reviews
// the draft before it's published. The graph pauses at the "review" node,
// allowing the caller to inspect state, inject feedback, and resume.
//
// Pipeline:
//
//	draft → review (interrupt) → publish
//
// The example shows:
//  1. Run until interrupt (pauses before "review")
//  2. Resume with injected state (human approves)
//  3. Rewind to an earlier checkpoint and replay with different input
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

	// In-memory checkpointer for this example. Use dynamodb or postgres
	// backends for production persistence.
	cp := memory.New()

	// Build the graph.
	g, err := graph.New[graph.State](graph.WithCheckpointer(cp))
	if err != nil {
		log.Fatal(err)
	}

	g.AddNode("draft", func(_ context.Context, s graph.State) (graph.State, error) {
		topic, _ := s["topic"].(string)
		out := graph.CopyState(s)
		out["document"] = fmt.Sprintf("Draft about %q: This is a well-researched article.", topic)
		out["status"] = "drafted"
		return out, nil
	})
	g.AddNode("review", func(_ context.Context, s graph.State) (graph.State, error) {
		out := graph.CopyState(s)
		out["status"] = "reviewed"
		return out, nil
	})
	g.AddNode("publish", func(_ context.Context, s graph.State) (graph.State, error) {
		out := graph.CopyState(s)
		if s["approved"] == true {
			out["status"] = "published"
		} else {
			out["status"] = "rejected"
		}
		return out, nil
	})

	g.SetEntry("draft")
	g.AddEdge("draft", "review")
	g.AddEdge("review", "publish")
	g.InterruptBefore("review")

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

	// Resume hits the interrupt again — it fires every time execution reaches that node.
	rejectUpdates := graph.State{"approved": false}
	_, err = g.Resume(ctx, threadID, &rejectUpdates)
	if !errors.As(err, &intErr) {
		log.Fatalf("expected interrupt after rewind, got: %v", err)
	}
	fmt.Printf("Paused again before: %s (as expected)\n", intErr.Result.NodeName)

	// Resume past the interrupt with rejection.
	rejectUpdates2 := graph.State{"approved": false}
	result, err = g.Resume(ctx, threadID, &rejectUpdates2)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Final status after rejection: %s\n\n", result.State["status"])

	// ─── Bonus: Step-by-step execution ──────────────────────────────────

	fmt.Println("=== Bonus: Step-by-step execution ===")

	g2, _ := graph.New[graph.State](graph.WithCheckpointer(cp))
	g2.AddNode("a", func(_ context.Context, s graph.State) (graph.State, error) {
		s["a"] = "done"
		return s, nil
	})
	g2.AddNode("b", func(_ context.Context, s graph.State) (graph.State, error) {
		s["b"] = "done"
		return s, nil
	})
	g2.AddNode("c", func(_ context.Context, s graph.State) (graph.State, error) {
		s["c"] = "done"
		return s, nil
	})
	g2.SetEntry("a")
	g2.AddEdge("a", "b")
	g2.AddEdge("b", "c")

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
