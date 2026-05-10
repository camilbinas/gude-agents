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
	if _, err := g.Agent("ask", a, graph.Keys("answer", "question")); err != nil {
		t.Fatal(err)
	}

	// Node: format — formats the answer.
	g.Node("format", func(_ context.Context, state graph.State) (graph.State, error) {
		answer, _ := state["answer"].(string)
		state["formatted"] = "Result: " + answer
		return state, nil
	}, graph.In("answer"), graph.Out("formatted"))

	g.Start("ask")
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
	if _, err := g.Node("a", func(_ context.Context, s graph.State) (graph.State, error) {
		s["a_out"] = "done"
		return s, nil
	}, graph.In(), graph.Out("a_out")); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Node("b", func(_ context.Context, s graph.State) (graph.State, error) {
		s["b_out"] = "done"
		return s, nil
	}, graph.In("a_out"), graph.Out("b_out")); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Node("c", func(_ context.Context, s graph.State) (graph.State, error) {
		s["c_out"] = "done"
		return s, nil
	}, graph.In("b_out"), graph.Out("c_out")); err != nil {
		t.Fatal(err)
	}
	g.Start("a")

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
	if _, err := g.Node("a", func(_ context.Context, s graph.State) (graph.State, error) {
		callCount++
		s["a_out"] = callCount
		return s, nil
	}, graph.In(), graph.Out("a_out")); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Node("b", func(_ context.Context, s graph.State) (graph.State, error) {
		s["b_out"] = "done"
		return s, nil
	}, graph.In("a_out"), graph.Out("b_out")); err != nil {
		t.Fatal(err)
	}
	g.Start("a")

	ctx := context.Background()
	threadID := "int-test-rewind"

	// Run the full graph.
	result, err := g.Run(ctx, graph.State{}, graph.WithThreadID(threadID))
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if result.State["a_out"] != 1 {
		t.Errorf("expected a_out=1, got %v", result.State["a_out"])
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
	if result.State["b_out"] != "done" {
		t.Errorf("expected b_out=done after rewind+resume, got %v", result.State["b_out"])
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

	// classify node: uses LLM to classify, then conditionally writes route keys
	g.Node("classify", func(ctx context.Context, s graph.State) (graph.State, error) {
		c := agent.NewContext(ctx)
		question, _ := s["question"].(string)
		category, err := a.Invoke(c, question)
		if err != nil {
			return s, err
		}
		out := graph.CopyState(s)
		out["category"] = category
		cat := strings.TrimSpace(strings.ToLower(category))
		if strings.Contains(cat, "math") {
			out["route_math"] = "go"
		} else {
			out["route_geography"] = "go"
		}
		return out, nil
	}, graph.In(), graph.Out("route_math", "route_geography", "category"))

	g.Node("math", func(_ context.Context, s graph.State) (graph.State, error) {
		s["result"] = "math handler"
		return s, nil
	}, graph.In("route_math"), graph.Out("result_math"))
	g.Node("geography", func(_ context.Context, s graph.State) (graph.State, error) {
		s["result"] = "geography handler"
		return s, nil
	}, graph.In("route_geography"), graph.Out("result_geography"))

	g.Start("classify")

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
