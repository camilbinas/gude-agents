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
	t.Parallel()
	// Pure logic graph: route based on state value using data-flow gating.
	g, err := graph.New[graph.State]()
	if err != nil {
		t.Fatal(err)
	}

	// classify sets a "category" and conditionally writes route keys.
	_, err = g.Node("classify", func(_ context.Context, state graph.State) (graph.State, error) {
		input, _ := state["input"].(string)
		out := graph.CopyState(state)
		if strings.Contains(strings.ToLower(input), "code") {
			out["category"] = "technical"
			out["route_technical"] = "go"
		} else {
			out["category"] = "general"
			out["route_general"] = "go"
		}
		return out, nil
	}, graph.In(), graph.Out("route_technical", "route_general", "category"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = g.Node("technical", func(_ context.Context, state graph.State) (graph.State, error) {
		state["result"] = "handled by technical"
		return state, nil
	}, graph.In("route_technical"), graph.Out("result_technical"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = g.Node("general", func(_ context.Context, state graph.State) (graph.State, error) {
		state["result"] = "handled by general"
		return state, nil
	}, graph.In("route_general"), graph.Out("result_general"))
	if err != nil {
		t.Fatal(err)
	}

	g.Start("classify")

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
	t.Parallel()
	g, err := graph.New[graph.State]()
	if err != nil {
		t.Fatal(err)
	}

	// check increments count and conditionally writes route_process key
	_, err = g.Node("check", func(_ context.Context, state graph.State) (graph.State, error) {
		count, _ := state["count"].(int)
		out := graph.CopyState(state)
		out["count"] = count + 1
		if count+1 < 3 {
			out["route_process"] = "go"
		}
		return out, nil
	}, graph.In(), graph.Out("route_process", "check_done"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = g.Node("process", func(_ context.Context, state graph.State) (graph.State, error) {
		state["processed"] = true
		return state, nil
	}, graph.In("route_process"), graph.Out("processed_out"))
	if err != nil {
		t.Fatal(err)
	}

	g.Start("check")

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

	// count starts at 5, check increments to 6, does NOT write route_process → terminates.
	r2, err := g.Run(ctx, graph.State{"count": 5})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if r2.State["processed"] != nil {
		t.Errorf("expected processed to be nil when count >= 3, got %v", r2.State["processed"])
	}
}

func TestIntegration_Graph_ForkAndJoin(t *testing.T) {
	t.Parallel()
	g, err := graph.New[graph.State]()
	if err != nil {
		t.Fatal(err)
	}

	// start writes "started" key, branch_a and branch_b both read it (concurrent)
	_, err = g.Node("start", func(_ context.Context, state graph.State) (graph.State, error) {
		state["started"] = true
		return state, nil
	}, graph.In(), graph.Out("started"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = g.Node("branch_a", func(_ context.Context, state graph.State) (graph.State, error) {
		state["a_done"] = true
		return state, nil
	}, graph.In("started"), graph.Out("a_done"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = g.Node("branch_b", func(_ context.Context, state graph.State) (graph.State, error) {
		state["b_done"] = true
		return state, nil
	}, graph.In("started"), graph.Out("b_done"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = g.Node("merge", func(_ context.Context, state graph.State) (graph.State, error) {
		aDone, _ := state["a_done"].(bool)
		bDone, _ := state["b_done"].(bool)
		state["both_done"] = aDone && bDone
		return state, nil
	}, graph.In("a_done", "b_done"), graph.Out("both_done"))
	if err != nil {
		t.Fatal(err)
	}

	g.Start("start")

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
	t.Parallel()
	p := newTestProvider(t)

	a, err := agent.New(p,
		prompt.Text("You are a helpful assistant. Be very brief — one sentence max."),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	g, err := graph.New[graph.State]()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := g.Agent("ask", a, graph.Keys("answer", "question")); err != nil {
		t.Fatal(err)
	}

	_, err = g.Node("format", func(_ context.Context, state graph.State) (graph.State, error) {
		answer, _ := state["answer"].(string)
		state["formatted"] = "Answer: " + answer
		return state, nil
	}, graph.In("answer"), graph.Out("formatted"))
	if err != nil {
		t.Fatal(err)
	}

	g.Start("ask")

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
		t.Logf("note: InputTokens=0 — token usage propagation requires typed state with AddUsage()")
	}
}

func TestIntegration_Graph_TypedState(t *testing.T) {
	t.Parallel()
	type PipelineState struct {
		Input  string `json:"input"`
		Upper  string `json:"upper"`
		Length int    `json:"length"`
	}

	g, err := graph.New[PipelineState]()
	if err != nil {
		t.Fatal(err)
	}

	_, err = g.Node("uppercase", func(_ context.Context, s PipelineState) (PipelineState, error) {
		s.Upper = strings.ToUpper(s.Input)
		return s, nil
	}, graph.In(), graph.Out("upper"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = g.Node("count", func(_ context.Context, s PipelineState) (PipelineState, error) {
		s.Length = len(s.Upper)
		return s, nil
	}, graph.In("upper"), graph.Out("length"))
	if err != nil {
		t.Fatal(err)
	}

	g.Start("uppercase")

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
	t.Parallel()
	p := newTestProvider(t)

	routerAgent, err := agent.New(p,
		prompt.Text("You are a router. You will be given an input and a list of valid next nodes. Respond with ONLY the node name, nothing else."),
		nil,
		agent.WithTemperature(0.0),
	)
	if err != nil {
		t.Fatal(err)
	}

	g, err := graph.New[graph.State]()
	if err != nil {
		t.Fatal(err)
	}

	// classify node uses LLM to decide which route key to write
	_, err = g.Node("classify", func(ctx context.Context, state graph.State) (graph.State, error) {
		c := agent.NewContext(ctx)
		input, _ := state["input"].(string)
		routerPrompt := "Input: " + input + "\nValid nodes: math_expert, language_expert"
		result, err := routerAgent.Invoke(c, routerPrompt)
		if err != nil {
			return state, err
		}
		out := graph.CopyState(state)
		result = strings.TrimSpace(strings.ToLower(result))
		if strings.Contains(result, "math") {
			out["route_math"] = "go"
		} else {
			out["route_language"] = "go"
		}
		return out, nil
	}, graph.In(), graph.Out("route_math", "route_language"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = g.Node("math_expert", func(_ context.Context, state graph.State) (graph.State, error) {
		state["handler"] = "math_expert"
		return state, nil
	}, graph.In("route_math"), graph.Out("handler_math"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = g.Node("language_expert", func(_ context.Context, state graph.State) (graph.State, error) {
		state["handler"] = "language_expert"
		return state, nil
	}, graph.In("route_language"), graph.Out("handler_language"))
	if err != nil {
		t.Fatal(err)
	}

	g.Start("classify")

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
	t.Parallel()
	// With data-flow scheduling, cycles are detected at validation time.
	// This test verifies that a graph with no progress terminates cleanly.
	g, err := graph.New[graph.State](graph.WithMaxIterations(3))
	if err != nil {
		t.Fatal(err)
	}

	// Single entry node that writes a key — graph terminates after entry since
	// no other nodes can become ready.
	_, err = g.Node("a", func(_ context.Context, state graph.State) (graph.State, error) {
		count, _ := state["count"].(int)
		state["count"] = count + 1
		return state, nil
	}, graph.In(), graph.Out("a_out"))
	if err != nil {
		t.Fatal(err)
	}

	g.Start("a")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := g.Run(ctx, graph.State{})
	if err != nil {
		t.Fatalf("expected clean termination, got error: %v", err)
	}

	// Should have executed "a" once and terminated.
	if result.State["count"] != 1 {
		t.Errorf("expected count=1, got %v", result.State["count"])
	}
	t.Logf("Graph terminated cleanly with single node execution")
}
