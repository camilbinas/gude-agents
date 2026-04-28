package integration_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/graph"
	"github.com/camilbinas/gude-agents/agent/prompt"
)

// Graph routing integration tests that call real LLM APIs.
//
// Run with:
//   go test -v -timeout=180s -run TestIntegration_Graph ./...

func TestIntegration_Graph_ConditionalRouting(t *testing.T) {
	// Pure logic graph: route based on state value, no LLM needed.
	g, err := graph.NewGraph()
	if err != nil {
		t.Fatal(err)
	}

	// classify sets a "category" based on the input.
	err = g.AddNode("classify", func(_ context.Context, state graph.State) (graph.State, error) {
		input, _ := state["input"].(string)
		if strings.Contains(strings.ToLower(input), "code") {
			state["category"] = "technical"
		} else {
			state["category"] = "general"
		}
		return state, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	err = g.AddNode("technical", func(_ context.Context, state graph.State) (graph.State, error) {
		state["result"] = "handled by technical"
		return state, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	err = g.AddNode("general", func(_ context.Context, state graph.State) (graph.State, error) {
		state["result"] = "handled by general"
		return state, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	g.SetEntry("classify")
	err = g.AddConditionalEdge("classify", func(_ context.Context, state graph.State) (string, error) {
		cat, _ := state["category"].(string)
		if cat == "technical" {
			return "technical", nil
		}
		return "general", nil
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Technical input → technical node.
	r1, err := g.Run(ctx, graph.State{"input": "Help me write code in Go"})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if r1.State["result"] != "handled by technical" {
		t.Errorf("expected technical handler, got %v", r1.State["result"])
	}

	// General input → general node.
	r2, err := g.Run(ctx, graph.State{"input": "What is the weather today?"})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if r2.State["result"] != "handled by general" {
		t.Errorf("expected general handler, got %v", r2.State["result"])
	}
}

func TestIntegration_Graph_ConditionalEndSignal(t *testing.T) {
	g, err := graph.NewGraph()
	if err != nil {
		t.Fatal(err)
	}

	err = g.AddNode("check", func(_ context.Context, state graph.State) (graph.State, error) {
		count, _ := state["count"].(int)
		state["count"] = count + 1
		return state, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	err = g.AddNode("process", func(_ context.Context, state graph.State) (graph.State, error) {
		state["processed"] = true
		return state, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	g.SetEntry("check")
	// Route to "process" only if count < 3, otherwise end.
	err = g.AddConditionalEdge("check", func(_ context.Context, state graph.State) (string, error) {
		count, _ := state["count"].(int)
		if count < 3 {
			return "process", nil
		}
		return "", nil // end signal
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// count starts at 0, check increments to 1, routes to process.
	r1, err := g.Run(ctx, graph.State{"count": 0})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if r1.State["processed"] != true {
		t.Error("expected processed=true when count < 3")
	}

	// count starts at 5, check increments to 6, routes to end.
	r2, err := g.Run(ctx, graph.State{"count": 5})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if r2.State["processed"] != nil {
		t.Errorf("expected processed to be nil when count >= 3, got %v", r2.State["processed"])
	}
}

func TestIntegration_Graph_ForkAndJoin(t *testing.T) {
	g, err := graph.NewGraph()
	if err != nil {
		t.Fatal(err)
	}

	err = g.AddNode("start", func(_ context.Context, state graph.State) (graph.State, error) {
		state["started"] = true
		return state, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	err = g.AddNode("branch_a", func(_ context.Context, state graph.State) (graph.State, error) {
		state["a_done"] = true
		return state, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	err = g.AddNode("branch_b", func(_ context.Context, state graph.State) (graph.State, error) {
		state["b_done"] = true
		return state, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	err = g.AddNode("merge", func(_ context.Context, state graph.State) (graph.State, error) {
		aDone, _ := state["a_done"].(bool)
		bDone, _ := state["b_done"].(bool)
		state["both_done"] = aDone && bDone
		return state, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	g.SetEntry("start")
	if err := g.AddFork("start", []string{"branch_a", "branch_b"}); err != nil {
		t.Fatal(err)
	}
	if err := g.AddJoin("merge", []string{"branch_a", "branch_b"}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := g.Run(ctx, graph.State{})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}

	if result.State["started"] != true {
		t.Error("expected started=true")
	}
	if result.State["a_done"] != true {
		t.Error("expected a_done=true")
	}
	if result.State["b_done"] != true {
		t.Error("expected b_done=true")
	}
	if result.State["both_done"] != true {
		t.Error("expected both_done=true after join")
	}
}

func TestIntegration_Graph_AgentNode(t *testing.T) {
	p := newTestProvider(t)

	a, err := agent.New(p,
		prompt.Text("You are a helpful assistant. Be very brief — one sentence max."),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	g, err := graph.NewGraph()
	if err != nil {
		t.Fatal(err)
	}

	err = g.AddNode("ask", graph.AgentNode(a, "question", "answer"))
	if err != nil {
		t.Fatal(err)
	}

	err = g.AddNode("format", func(_ context.Context, state graph.State) (graph.State, error) {
		answer, _ := state["answer"].(string)
		state["formatted"] = "Answer: " + answer
		return state, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	g.SetEntry("ask")
	if err := g.AddEdge("ask", "format"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result, err := g.Run(ctx, graph.State{"question": "What is the capital of France?"})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}

	formatted, _ := result.State["formatted"].(string)
	t.Logf("Formatted: %s", formatted)

	if !strings.HasPrefix(formatted, "Answer: ") {
		t.Errorf("expected formatted to start with 'Answer: ', got: %s", formatted)
	}
	if !strings.Contains(strings.ToLower(formatted), "paris") {
		t.Errorf("expected answer to mention Paris, got: %s", formatted)
	}

	if result.Usage.InputTokens <= 0 {
		t.Errorf("expected InputTokens > 0 from agent node, got %d", result.Usage.InputTokens)
	}
}

func TestIntegration_Graph_TypedGraph(t *testing.T) {
	type PipelineState struct {
		graph.GraphState
		Input  string `json:"input"`
		Upper  string `json:"upper"`
		Length int    `json:"length"`
	}

	g, err := graph.NewTypedGraph[PipelineState]()
	if err != nil {
		t.Fatal(err)
	}

	err = g.AddNode("uppercase", func(_ context.Context, s PipelineState) (PipelineState, error) {
		s.Upper = strings.ToUpper(s.Input)
		return s, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	err = g.AddNode("count", func(_ context.Context, s PipelineState) (PipelineState, error) {
		s.Length = len(s.Upper)
		return s, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	g.SetEntry("uppercase")
	if err := g.AddEdge("uppercase", "count"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := g.Run(ctx, PipelineState{Input: "hello world"})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}

	if result.State.Upper != "HELLO WORLD" {
		t.Errorf("expected Upper='HELLO WORLD', got %q", result.State.Upper)
	}
	if result.State.Length != 11 {
		t.Errorf("expected Length=11, got %d", result.State.Length)
	}
}

func TestIntegration_Graph_LLMRouter(t *testing.T) {
	p := newTestProvider(t)

	routerAgent, err := agent.New(p,
		prompt.Text("You are a router. You will be given an input and a list of valid next nodes. Respond with ONLY the node name, nothing else."),
		nil,
		agent.WithTemperature(0.0),
	)
	if err != nil {
		t.Fatal(err)
	}

	g, err := graph.NewGraph()
	if err != nil {
		t.Fatal(err)
	}

	err = g.AddNode("classify", func(_ context.Context, state graph.State) (graph.State, error) {
		return state, nil // pass-through, routing happens via conditional edge
	})
	if err != nil {
		t.Fatal(err)
	}

	err = g.AddNode("math_expert", func(_ context.Context, state graph.State) (graph.State, error) {
		state["handler"] = "math_expert"
		return state, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	err = g.AddNode("language_expert", func(_ context.Context, state graph.State) (graph.State, error) {
		state["handler"] = "language_expert"
		return state, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	g.SetEntry("classify")
	err = g.AddConditionalEdge("classify",
		graph.LLMRouter(routerAgent, []string{"math_expert", "language_expert"}),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result, err := g.Run(ctx, graph.State{"input": "What is 2+2?"})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}

	handler, _ := result.State["handler"].(string)
	t.Logf("LLM routed to: %s", handler)

	if handler != "math_expert" {
		t.Errorf("expected LLM to route math question to math_expert, got %q", handler)
	}
}

func TestIntegration_Graph_MaxIterationsExceeded(t *testing.T) {
	g, err := graph.NewGraph(graph.WithMaxIterations(3))
	if err != nil {
		t.Fatal(err)
	}

	// Create a cycle: a → b → a → b → ... until max iterations.
	err = g.AddNode("a", func(_ context.Context, state graph.State) (graph.State, error) {
		count, _ := state["count"].(int)
		state["count"] = count + 1
		return state, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	err = g.AddNode("b", func(_ context.Context, state graph.State) (graph.State, error) {
		return state, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	g.SetEntry("a")
	if err := g.AddEdge("a", "b"); err != nil {
		t.Fatal(err)
	}
	if err := g.AddEdge("b", "a"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err = g.Run(ctx, graph.State{})
	if err == nil {
		t.Fatal("expected max iterations error, got nil")
	}

	var iterErr *graph.GraphIterationError
	if !isGraphIterationError(err) {
		t.Errorf("expected GraphIterationError, got %T: %v", err, err)
	}
	_ = iterErr
	t.Logf("Correctly hit max iterations: %v", err)
}

// isGraphIterationError checks if the error message indicates a max iterations error.
func isGraphIterationError(err error) bool {
	return strings.Contains(err.Error(), "max iterations")
}
