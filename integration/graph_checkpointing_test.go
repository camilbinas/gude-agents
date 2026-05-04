package integration_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/graph"
	"github.com/camilbinas/gude-agents/agent/graph/checkpointer/memory"
	"github.com/camilbinas/gude-agents/agent/prompt"
)

// Graph checkpointing integration tests that exercise interrupt, resume, rewind,
// and step-by-step execution with real LLM agents.
//
// Run with:
//   go test -v -timeout=180s -run TestIntegration_Graph_Checkpoint ./...

func TestIntegration_Graph_Checkpoint_InterruptAndResume(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)
	cp := memory.New()

	a, err := agent.Worker(p, prompt.Text(
		"You are a helpful assistant. Answer in one short sentence.",
	), nil)
	if err != nil {
		t.Fatal(err)
	}

	g, err := graph.New[graph.State](graph.WithCheckpointer(cp))
	if err != nil {
		t.Fatal(err)
	}

	// Node: ask — uses the LLM to answer a question.
	g.AddNode("ask", graph.AgentNode(a, "question", "answer"))

	// Node: format — formats the answer.
	g.AddNode("format", func(_ context.Context, state graph.State) (graph.State, error) {
		answer, _ := state["answer"].(string)
		state["formatted"] = "Result: " + answer
		return state, nil
	})

	g.SetEntry("ask")
	g.AddEdge("ask", "format")
	g.InterruptAfter("ask")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	threadID := "int-test-interrupt-resume"

	// Run until interrupt after "ask".
	_, err = g.Run(ctx, graph.State{"question": "What is 2+2?"}, graph.WithThreadID(threadID))

	var intErr *graph.GraphInterruptError
	if !errors.As(err, &intErr) {
		t.Fatalf("expected GraphInterruptError, got: %v", err)
	}

	if intErr.Result.NodeName != "ask" {
		t.Errorf("expected interrupt at 'ask', got %q", intErr.Result.NodeName)
	}
	if intErr.Result.Type != graph.InterruptTypeAfter {
		t.Errorf("expected InterruptTypeAfter, got %q", intErr.Result.Type)
	}

	// Verify the answer is in the checkpoint state.
	answer, _ := intErr.Result.Checkpoint.State["answer"].(string)
	if answer == "" {
		t.Fatal("expected non-empty answer in checkpoint state")
	}
	t.Logf("LLM answer: %s", answer)

	// Resume execution — should run "format" node.
	result, err := g.Resume(ctx, threadID, nil)
	if err != nil {
		t.Fatalf("Resume error: %v", err)
	}

	formatted, _ := result.State["formatted"].(string)
	if formatted == "" {
		t.Fatal("expected non-empty formatted result")
	}
	t.Logf("Formatted: %s", formatted)
}

func TestIntegration_Graph_Checkpoint_StepByStep(t *testing.T) {
	t.Parallel()
	cp := memory.New()

	g, err := graph.New[graph.State](graph.WithCheckpointer(cp))
	if err != nil {
		t.Fatal(err)
	}

	// Simple 3-node pipeline with no LLM (pure logic).
	g.AddNode("a", func(_ context.Context, s graph.State) (graph.State, error) {
		s["a"] = "done"
		return s, nil
	})
	g.AddNode("b", func(_ context.Context, s graph.State) (graph.State, error) {
		s["b"] = "done"
		return s, nil
	})
	g.AddNode("c", func(_ context.Context, s graph.State) (graph.State, error) {
		s["c"] = "done"
		return s, nil
	})
	g.SetEntry("a")
	g.AddEdge("a", "b")
	g.AddEdge("b", "c")

	ctx := context.Background()
	threadID := "int-test-step"

	// Step through all nodes.
	var versions []int
	var nodes []string
	for {
		res, err := g.Step(ctx, graph.State{}, threadID)
		if err != nil {
			t.Fatalf("Step error: %v", err)
		}
		versions = append(versions, res.Version)
		nodes = append(nodes, res.NodeName)
		if res.Done {
			break
		}
	}

	if len(nodes) != 3 {
		t.Fatalf("expected 3 steps, got %d: %v", len(nodes), nodes)
	}
	expectedNodes := []string{"a", "b", "c"}
	for i, name := range expectedNodes {
		if nodes[i] != name {
			t.Errorf("step %d: expected %q, got %q", i, name, nodes[i])
		}
	}
	// Versions should be sequential.
	for i, v := range versions {
		if v != i+1 {
			t.Errorf("step %d: expected version %d, got %d", i, i+1, v)
		}
	}
}

func TestIntegration_Graph_Checkpoint_RewindAndReplay(t *testing.T) {
	t.Parallel()
	cp := memory.New()

	g, err := graph.New[graph.State](graph.WithCheckpointer(cp))
	if err != nil {
		t.Fatal(err)
	}

	var callCount int
	g.AddNode("a", func(_ context.Context, s graph.State) (graph.State, error) {
		callCount++
		s["a"] = callCount
		return s, nil
	})
	g.AddNode("b", func(_ context.Context, s graph.State) (graph.State, error) {
		s["b"] = "done"
		return s, nil
	})
	g.SetEntry("a")
	g.AddEdge("a", "b")

	ctx := context.Background()
	threadID := "int-test-rewind"

	// Run the full graph.
	result, err := g.Run(ctx, graph.State{}, graph.WithThreadID(threadID))
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if result.State["a"] != 1 {
		t.Errorf("expected a=1, got %v", result.State["a"])
	}

	// Rewind to version 1 (after "a").
	if err := g.RewindTo(ctx, threadID, 1); err != nil {
		t.Fatalf("RewindTo error: %v", err)
	}

	// Resume — "b" should run again.
	result, err = g.Resume(ctx, threadID, nil)
	if err != nil {
		t.Fatalf("Resume after rewind error: %v", err)
	}
	if result.State["b"] != "done" {
		t.Errorf("expected b=done after rewind+resume, got %v", result.State["b"])
	}

	// Verify history has more than the original 2 checkpoints.
	history, err := cp.History(ctx, threadID)
	if err != nil {
		t.Fatalf("History error: %v", err)
	}
	if len(history) < 4 {
		t.Errorf("expected at least 4 history entries (2 original + rewind + resumed), got %d", len(history))
	}
}

func TestIntegration_Graph_Checkpoint_InterruptBeforeWithLLMRouter(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)
	cp := memory.New()

	a, err := agent.Worker(p, prompt.Text(
		"You classify questions. If the question is about math, respond with exactly 'math'. "+
			"If about geography, respond with exactly 'geography'. Nothing else.",
	), nil)
	if err != nil {
		t.Fatal(err)
	}

	g, err := graph.New[graph.State](graph.WithCheckpointer(cp))
	if err != nil {
		t.Fatal(err)
	}

	g.AddNode("classify", graph.AgentNode(a, "question", "category"))
	g.AddNode("math", func(_ context.Context, s graph.State) (graph.State, error) {
		s["result"] = "math handler"
		return s, nil
	})
	g.AddNode("geography", func(_ context.Context, s graph.State) (graph.State, error) {
		s["result"] = "geography handler"
		return s, nil
	})

	g.SetEntry("classify")
	g.AddConditionalEdge("classify", func(_ context.Context, s graph.State) (string, error) {
		cat, _ := s["category"].(string)
		cat = strings.TrimSpace(strings.ToLower(cat))
		if strings.Contains(cat, "math") {
			return "math", nil
		}
		return "geography", nil
	})

	// Interrupt before the handler nodes so we can inspect the classification.
	g.InterruptBefore("math")
	g.InterruptBefore("geography")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	threadID := "int-test-llm-router"

	_, err = g.Run(ctx, graph.State{"question": "What is 7 * 8?"}, graph.WithThreadID(threadID))

	var intErr *graph.GraphInterruptError
	if !errors.As(err, &intErr) {
		t.Fatalf("expected interrupt, got: %v", err)
	}

	t.Logf("Interrupted before node: %s", intErr.Result.NodeName)

	// The LLM should have classified this as math.
	if intErr.Result.NodeName != "math" {
		t.Errorf("expected interrupt at 'math', got %q (category=%v)",
			intErr.Result.NodeName, intErr.Result.Checkpoint.State["category"])
	}

	// Resume to complete.
	result, err := g.Resume(ctx, threadID, nil)
	if err != nil {
		t.Fatalf("Resume error: %v", err)
	}
	if result.State["result"] != "math handler" {
		t.Errorf("expected result='math handler', got %v", result.State["result"])
	}
}
