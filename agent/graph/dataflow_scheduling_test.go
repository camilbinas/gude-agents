package graph

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// trackNode creates a NodeFunc that records the node name in a shared order slice
// and writes the specified output key to state.
func trackNode(name, outputKey string, mu *sync.Mutex, order *[]string) NodeFunc[State] {
	return func(_ context.Context, s State) (State, error) {
		mu.Lock()
		*order = append(*order, name)
		mu.Unlock()
		out := CopyState(s)
		out[outputKey] = "done"
		return out, nil
	}
}

func TestDataFlowScheduling_LinearChain(t *testing.T) {
	// A→B→C: each node depends on the previous node's output key.
	// Execution order must be A, B, C.
	var mu sync.Mutex
	var order []string

	g := mustGraph(t)
	mustAddNodeWithKeys(t, g, "A", trackNode("A", "a_out", &mu, &order), []string{"a_out"}, []string{})
	mustAddNodeWithKeys(t, g, "B", trackNode("B", "b_out", &mu, &order), []string{"b_out"}, []string{"a_out"})
	mustAddNodeWithKeys(t, g, "C", trackNode("C", "c_out", &mu, &order), []string{"c_out"}, []string{"b_out"})
	g.Start("A")

	_, err := g.Run(context.Background(), State{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(order) != 3 {
		t.Fatalf("expected 3 nodes executed, got %d: %v", len(order), order)
	}
	if order[0] != "A" || order[1] != "B" || order[2] != "C" {
		t.Fatalf("expected execution order [A, B, C], got %v", order)
	}
}

func TestDataFlowScheduling_Diamond(t *testing.T) {
	// A→(B,C)→D: B and C both depend on A's output, D depends on both B and C outputs.
	// B and C should execute concurrently (before D), but their relative order may vary.
	var mu sync.Mutex
	var order []string

	g := mustGraph(t)
	mustAddNodeWithKeys(t, g, "A", trackNode("A", "a_out", &mu, &order), []string{"a_out"}, []string{})
	mustAddNodeWithKeys(t, g, "B", trackNode("B", "b_out", &mu, &order), []string{"b_out"}, []string{"a_out"})
	mustAddNodeWithKeys(t, g, "C", trackNode("C", "c_out", &mu, &order), []string{"c_out"}, []string{"a_out"})
	mustAddNodeWithKeys(t, g, "D", trackNode("D", "d_out", &mu, &order), []string{"d_out"}, []string{"b_out", "c_out"})
	g.Start("A")

	_, err := g.Run(context.Background(), State{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(order) != 4 {
		t.Fatalf("expected 4 nodes executed, got %d: %v", len(order), order)
	}

	// A must be first
	if order[0] != "A" {
		t.Fatalf("expected A first, got %v", order)
	}

	// D must be last
	if order[3] != "D" {
		t.Fatalf("expected D last, got %v", order)
	}

	// B and C must both appear before D (positions 1 and 2)
	bIdx, cIdx := -1, -1
	for i, name := range order {
		if name == "B" {
			bIdx = i
		}
		if name == "C" {
			cIdx = i
		}
	}
	if bIdx == -1 || cIdx == -1 {
		t.Fatalf("expected both B and C to execute, got %v", order)
	}
	if bIdx >= 3 || cIdx >= 3 {
		t.Fatalf("expected B and C before D, got %v", order)
	}
}

func TestDataFlowScheduling_EntryNodeExecutesFirst(t *testing.T) {
	// Entry node executes first regardless of input declarations.
	// Even if entry declares input keys, it still runs first.
	var mu sync.Mutex
	var order []string

	g := mustGraph(t)
	// Entry has input keys declared but should still execute first
	mustAddNodeWithKeys(t, g, "entry", trackNode("entry", "e_out", &mu, &order), []string{"e_out"}, []string{"nonexistent_key"})
	mustAddNodeWithKeys(t, g, "second", trackNode("second", "s_out", &mu, &order), []string{"s_out"}, []string{"e_out"})
	g.Start("entry")

	_, err := g.Run(context.Background(), State{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(order) < 1 {
		t.Fatal("expected at least 1 node executed")
	}
	if order[0] != "entry" {
		t.Fatalf("expected entry node first, got %v", order)
	}
	if len(order) != 2 {
		t.Fatalf("expected 2 nodes executed, got %d: %v", len(order), order)
	}
	if order[1] != "second" {
		t.Fatalf("expected second node after entry, got %v", order)
	}
}

func TestDataFlowScheduling_ConditionalGating(t *testing.T) {
	// A node conditionally writes an output key. Downstream nodes only execute
	// when the key is written. When the key is NOT written, the graph terminates
	// because no more nodes can become ready.
	var mu sync.Mutex
	var order []string

	g := mustGraph(t)

	// Entry node that does NOT write "gate_key" — simulates conditional gating
	entryFn := func(_ context.Context, s State) (State, error) {
		mu.Lock()
		order = append(order, "entry")
		mu.Unlock()
		out := CopyState(s)
		out["entry_out"] = "done"
		// Intentionally NOT writing "gate_key"
		return out, nil
	}
	mustAddNodeWithKeys(t, g, "entry", entryFn, []string{"entry_out", "gate_key"}, []string{})

	// This node depends on "gate_key" which is never written
	mustAddNodeWithKeys(t, g, "gated", trackNode("gated", "gated_out", &mu, &order), []string{"gated_out"}, []string{"gate_key"})
	g.Start("entry")

	_, err := g.Run(context.Background(), State{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only entry should have executed; gated should NOT execute
	if len(order) != 1 {
		t.Fatalf("expected only 1 node executed (entry), got %d: %v", len(order), order)
	}
	if order[0] != "entry" {
		t.Fatalf("expected entry executed, got %v", order)
	}
}

func TestDataFlowScheduling_SingleNodeFromInitialState(t *testing.T) {
	// A node with inputs from initial state should execute immediately after entry.
	var mu sync.Mutex
	var order []string

	g := mustGraph(t)
	mustAddNodeWithKeys(t, g, "entry", trackNode("entry", "e_out", &mu, &order), []string{"e_out"}, []string{})
	// This node depends on "init_key" which is in the initial state
	mustAddNodeWithKeys(t, g, "consumer", trackNode("consumer", "c_out", &mu, &order), []string{"c_out"}, []string{"init_key"})
	g.Start("entry")

	_, err := g.Run(context.Background(), State{"init_key": "provided"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(order) != 2 {
		t.Fatalf("expected 2 nodes executed, got %d: %v", len(order), order)
	}
	if order[0] != "entry" {
		t.Fatalf("expected entry first, got %v", order)
	}
	if order[1] != "consumer" {
		t.Fatalf("expected consumer second, got %v", order)
	}
}

func TestDataFlowScheduling_MaxIterationsRespected(t *testing.T) {
	// Create a graph with more nodes than MaxIterations allows.
	// The engine should return a GraphIterationError.
	var mu sync.Mutex
	var order []string

	g := mustGraph(t, WithMaxIterations(2))
	mustAddNodeWithKeys(t, g, "A", trackNode("A", "a_out", &mu, &order), []string{"a_out"}, []string{})
	mustAddNodeWithKeys(t, g, "B", trackNode("B", "b_out", &mu, &order), []string{"b_out"}, []string{"a_out"})
	mustAddNodeWithKeys(t, g, "C", trackNode("C", "c_out", &mu, &order), []string{"c_out"}, []string{"b_out"})
	g.Start("A")

	_, err := g.Run(context.Background(), State{})
	if err == nil {
		t.Fatal("expected MaxIterations error, got nil")
	}

	var iterErr *GraphIterationError
	if !errors.As(err, &iterErr) {
		t.Fatalf("expected GraphIterationError, got: %T: %v", err, err)
	}
	if iterErr.Limit != 2 {
		t.Fatalf("expected limit 2, got %d", iterErr.Limit)
	}
}

// ── Struct State Scheduling ──────────────────────────────────────────────────

// scheduleTestState is a struct-based state for testing data-flow scheduling with Graph[S].
type scheduleTestState struct {
	Input  string `json:"input"`
	Middle string `json:"middle"`
	Output string `json:"output"`
}

func TestStructState_DataFlowSchedulingWithStructType(t *testing.T) {
	g, err := New[scheduleTestState]()
	if err != nil {
		t.Fatalf("New[scheduleTestState]: %v", err)
	}

	_, err = g.Node("entry", func(_ context.Context, s scheduleTestState) (scheduleTestState, error) {
		s.Input = "hello"
		return s, nil
	}, In(), Out("input"))
	if err != nil {
		t.Fatalf("Node(entry): %v", err)
	}

	_, err = g.Node("process", func(_ context.Context, s scheduleTestState) (scheduleTestState, error) {
		s.Middle = s.Input + "_processed"
		return s, nil
	}, In("input"), Out("middle"))
	if err != nil {
		t.Fatalf("Node(process): %v", err)
	}

	_, err = g.Node("finish", func(_ context.Context, s scheduleTestState) (scheduleTestState, error) {
		s.Output = s.Middle + "_done"
		return s, nil
	}, In("middle"), Out("output"))
	if err != nil {
		t.Fatalf("Node(finish): %v", err)
	}

	g.Start("entry")

	result, err := g.Run(context.Background(), scheduleTestState{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.State.Input != "hello" {
		t.Errorf("expected Input='hello', got %q", result.State.Input)
	}
	if result.State.Middle != "hello_processed" {
		t.Errorf("expected Middle='hello_processed', got %q", result.State.Middle)
	}
	if result.State.Output != "hello_processed_done" {
		t.Errorf("expected Output='hello_processed_done', got %q", result.State.Output)
	}
}

func TestStructState_ReadinessDeterminedByNonZeroFields(t *testing.T) {
	type ConditionalState struct {
		Input   string `json:"input"`
		RouteA  string `json:"route_a"`
		RouteB  string `json:"route_b"`
		ResultA string `json:"result_a"`
		ResultB string `json:"result_b"`
	}

	g, err := New[ConditionalState]()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = g.Node("entry", func(_ context.Context, s ConditionalState) (ConditionalState, error) {
		s.Input = "data"
		return s, nil
	}, In(), Out("input"))
	if err != nil {
		t.Fatalf("Node(entry): %v", err)
	}

	_, err = g.Node("classifier", func(_ context.Context, s ConditionalState) (ConditionalState, error) {
		s.RouteA = "go_a"
		return s, nil
	}, In("input"), Out("route_a", "route_b"))
	if err != nil {
		t.Fatalf("Node(classifier): %v", err)
	}

	handlerAExecuted := false
	_, err = g.Node("handler_a", func(_ context.Context, s ConditionalState) (ConditionalState, error) {
		handlerAExecuted = true
		s.ResultA = "done_a"
		return s, nil
	}, In("route_a"), Out("result_a"))
	if err != nil {
		t.Fatalf("Node(handler_a): %v", err)
	}

	handlerBExecuted := false
	_, err = g.Node("handler_b", func(_ context.Context, s ConditionalState) (ConditionalState, error) {
		handlerBExecuted = true
		s.ResultB = "done_b"
		return s, nil
	}, In("route_b"), Out("result_b"))
	if err != nil {
		t.Fatalf("Node(handler_b): %v", err)
	}

	g.Start("entry")

	result, err := g.Run(context.Background(), ConditionalState{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !handlerAExecuted {
		t.Error("expected handler_a to execute (route_a is non-zero)")
	}
	if result.State.ResultA != "done_a" {
		t.Errorf("expected ResultA='done_a', got %q", result.State.ResultA)
	}

	if handlerBExecuted {
		t.Error("expected handler_b NOT to execute (route_b is zero value)")
	}
	if result.State.ResultB != "" {
		t.Errorf("expected ResultB='', got %q", result.State.ResultB)
	}
}

func TestStructState_NodeWithKeysWorksWithStructGraph(t *testing.T) {
	type SimpleState struct {
		A string `json:"a"`
		B string `json:"b"`
	}

	g, err := New[SimpleState]()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = g.Node("entry", func(_ context.Context, s SimpleState) (SimpleState, error) {
		s.A = "value_a"
		return s, nil
	}, In(), Out("a"))
	if err != nil {
		t.Fatalf("Node(entry): %v", err)
	}

	_, err = g.Node("next", func(_ context.Context, s SimpleState) (SimpleState, error) {
		s.B = s.A + "_extended"
		return s, nil
	}, In("a"), Out("b"))
	if err != nil {
		t.Fatalf("Node(next): %v", err)
	}

	g.Start("entry")

	result, err := g.Run(context.Background(), SimpleState{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.State.A != "value_a" {
		t.Errorf("expected A='value_a', got %q", result.State.A)
	}
	if result.State.B != "value_a_extended" {
		t.Errorf("expected B='value_a_extended', got %q", result.State.B)
	}
}

func TestStructState_AgentWithStructAccessorRequiresExplicitKeyMetadata(t *testing.T) {
	type AgentState struct {
		Question string `json:"question"`
		Answer   string `json:"answer"`
	}

	g, err := New[AgentState]()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = g.Agent("answer", nil, AgentNodeAccessor[AgentState]{
		GetInput:  func(s AgentState) string { return s.Question },
		SetOutput: func(s *AgentState, out string) { s.Answer = out },
	})
	if err == nil {
		t.Fatal("expected error when registering agent without key metadata")
	}

	if !isValidationError(err) {
		t.Fatalf("expected GraphValidationError, got %T: %v", err, err)
	}
}

func TestStructState_NonZeroBoolField(t *testing.T) {
	type BoolState struct {
		Input  string `json:"input"`
		Flag   bool   `json:"flag"`
		Result string `json:"result"`
	}

	g, err := New[BoolState]()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = g.Node("entry", func(_ context.Context, s BoolState) (BoolState, error) {
		s.Input = "data"
		s.Flag = true
		return s, nil
	}, In(), Out("input", "flag"))
	if err != nil {
		t.Fatalf("Node(entry): %v", err)
	}

	resultExecuted := false
	_, err = g.Node("consumer", func(_ context.Context, s BoolState) (BoolState, error) {
		resultExecuted = true
		s.Result = "consumed"
		return s, nil
	}, In("flag"), Out("result"))
	if err != nil {
		t.Fatalf("Node(consumer): %v", err)
	}

	g.Start("entry")

	result, err := g.Run(context.Background(), BoolState{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !resultExecuted {
		t.Error("expected consumer to execute (flag is true)")
	}
	if result.State.Result != "consumed" {
		t.Errorf("expected Result='consumed', got %q", result.State.Result)
	}
}

func TestStructState_NonZeroNumericField(t *testing.T) {
	type NumericState struct {
		Input  string  `json:"input"`
		Count  float64 `json:"count"`
		Result string  `json:"result"`
	}

	g, err := New[NumericState]()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = g.Node("entry", func(_ context.Context, s NumericState) (NumericState, error) {
		s.Input = "data"
		s.Count = 42
		return s, nil
	}, In(), Out("input", "count"))
	if err != nil {
		t.Fatalf("Node(entry): %v", err)
	}

	resultExecuted := false
	_, err = g.Node("consumer", func(_ context.Context, s NumericState) (NumericState, error) {
		resultExecuted = true
		s.Result = "consumed"
		return s, nil
	}, In("count"), Out("result"))
	if err != nil {
		t.Fatalf("Node(consumer): %v", err)
	}

	g.Start("entry")

	result, err := g.Run(context.Background(), NumericState{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !resultExecuted {
		t.Error("expected consumer to execute (count is 42)")
	}
	if result.State.Result != "consumed" {
		t.Errorf("expected Result='consumed', got %q", result.State.Result)
	}
}

func TestStructState_ZeroNumericFieldBlocksReadiness(t *testing.T) {
	type NumericState struct {
		Input  string  `json:"input"`
		Count  float64 `json:"count"`
		Result string  `json:"result"`
	}

	g, err := New[NumericState]()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = g.Node("entry", func(_ context.Context, s NumericState) (NumericState, error) {
		s.Input = "data"
		return s, nil
	}, In(), Out("input", "count"))
	if err != nil {
		t.Fatalf("Node(entry): %v", err)
	}

	consumerExecuted := false
	_, err = g.Node("consumer", func(_ context.Context, s NumericState) (NumericState, error) {
		consumerExecuted = true
		s.Result = "consumed"
		return s, nil
	}, In("count"), Out("result"))
	if err != nil {
		t.Fatalf("Node(consumer): %v", err)
	}

	g.Start("entry")

	_, err = g.Run(context.Background(), NumericState{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if consumerExecuted {
		t.Error("expected consumer NOT to execute (count is 0)")
	}
}
