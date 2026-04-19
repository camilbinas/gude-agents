package tracing

import (
	"context"
	"fmt"
	"testing"

	"go.opentelemetry.io/otel/codes"

	"github.com/camilbinas/gude-agents/agent/graph"
)

// ===========================================================================
// Task 9.1: Graph Tracing Tests
// ===========================================================================

func TestGraphTracing_RunSpanCreated(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	g, err := graph.NewGraph(WithGraphTracing(tp))
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}

	if err := g.AddNode("start", func(_ context.Context, s graph.State) (graph.State, error) {
		out := graph.CopyState(s)
		out["visited"] = true
		return out, nil
	}); err != nil {
		t.Fatal(err)
	}
	g.SetEntry("start")

	_, err = g.Run(context.Background(), graph.State{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	spans := exp.GetSpans()
	runSpan := findSpan(spans, "graph.run")
	if runSpan == nil {
		t.Fatal("expected graph.run span")
	}
}

func TestGraphTracing_NodeChildSpans(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	g, err := graph.NewGraph(WithGraphTracing(tp))
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}

	setter := func(key, val string) graph.NodeFunc {
		return func(_ context.Context, s graph.State) (graph.State, error) {
			out := graph.CopyState(s)
			out[key] = val
			return out, nil
		}
	}

	if err := g.AddNode("alpha", setter("a", "done")); err != nil {
		t.Fatal(err)
	}
	if err := g.AddNode("beta", setter("b", "done")); err != nil {
		t.Fatal(err)
	}
	if err := g.AddNode("gamma", setter("c", "done")); err != nil {
		t.Fatal(err)
	}
	g.SetEntry("alpha")
	if err := g.AddEdge("alpha", "beta"); err != nil {
		t.Fatal(err)
	}
	if err := g.AddEdge("beta", "gamma"); err != nil {
		t.Fatal(err)
	}

	_, err = g.Run(context.Background(), graph.State{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	spans := exp.GetSpans()
	runSpan := findSpan(spans, "graph.run")
	if runSpan == nil {
		t.Fatal("expected graph.run span")
	}

	// Verify child node spans exist.
	for _, name := range []string{"graph.node.alpha", "graph.node.beta", "graph.node.gamma"} {
		nodeSpan := findSpan(spans, name)
		if nodeSpan == nil {
			t.Fatalf("expected %s span", name)
		}
	}

	// In a linear chain A→B→C, the hierarchy is:
	//   graph.run → graph.node.alpha → graph.node.beta → graph.node.gamma
	// because step() is called recursively with the node's context.
	alphaSpan := findSpan(spans, "graph.node.alpha")
	betaSpan := findSpan(spans, "graph.node.beta")
	gammaSpan := findSpan(spans, "graph.node.gamma")

	if alphaSpan.Parent.SpanID() != runSpan.SpanContext.SpanID() {
		t.Errorf("graph.node.alpha parent should be graph.run span")
	}
	if betaSpan.Parent.SpanID() != alphaSpan.SpanContext.SpanID() {
		t.Errorf("graph.node.beta parent should be graph.node.alpha span")
	}
	if gammaSpan.Parent.SpanID() != betaSpan.SpanContext.SpanID() {
		t.Errorf("graph.node.gamma parent should be graph.node.beta span")
	}
}

func TestGraphTracing_IterationsAttribute(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	g, err := graph.NewGraph(WithGraphTracing(tp))
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}

	noop := func(_ context.Context, s graph.State) (graph.State, error) { return s, nil }
	if err := g.AddNode("a", noop); err != nil {
		t.Fatal(err)
	}
	if err := g.AddNode("b", noop); err != nil {
		t.Fatal(err)
	}
	if err := g.AddNode("c", noop); err != nil {
		t.Fatal(err)
	}
	g.SetEntry("a")
	if err := g.AddEdge("a", "b"); err != nil {
		t.Fatal(err)
	}
	if err := g.AddEdge("b", "c"); err != nil {
		t.Fatal(err)
	}

	_, err = g.Run(context.Background(), graph.State{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	spans := exp.GetSpans()
	runSpan := findSpan(spans, "graph.run")
	if runSpan == nil {
		t.Fatal("expected graph.run span")
	}

	v := getAttr(*runSpan, AttrGraphIterations)
	if v.AsInt64() != 3 {
		t.Errorf("expected graph.iterations=3, got %d", v.AsInt64())
	}
}

func TestGraphTracing_ErrorStatusOnNodeFailure(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	g, err := graph.NewGraph(WithGraphTracing(tp))
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}

	if err := g.AddNode("ok_node", func(_ context.Context, s graph.State) (graph.State, error) {
		return s, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := g.AddNode("bad_node", func(_ context.Context, _ graph.State) (graph.State, error) {
		return nil, fmt.Errorf("node exploded")
	}); err != nil {
		t.Fatal(err)
	}
	g.SetEntry("ok_node")
	if err := g.AddEdge("ok_node", "bad_node"); err != nil {
		t.Fatal(err)
	}

	_, err = g.Run(context.Background(), graph.State{})
	if err == nil {
		t.Fatal("expected error from bad_node")
	}

	spans := exp.GetSpans()

	// The bad_node span should have Error status.
	badSpan := findSpan(spans, "graph.node.bad_node")
	if badSpan == nil {
		t.Fatal("expected graph.node.bad_node span")
	}
	if badSpan.Status.Code != codes.Error {
		t.Errorf("expected Error status on bad_node span, got %v", badSpan.Status.Code)
	}

	// The graph.run span should also have Error status.
	runSpan := findSpan(spans, "graph.run")
	if runSpan == nil {
		t.Fatal("expected graph.run span")
	}
	if runSpan.Status.Code != codes.Error {
		t.Errorf("expected Error status on graph.run span, got %v", runSpan.Status.Code)
	}
}

// ===========================================================================
// Task 9.2: Graph Fork Tracing
// ===========================================================================

func TestGraphTracing_ForkNodeSpansShareParent(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	g, err := graph.NewGraph(WithGraphTracing(tp))
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}

	noop := func(_ context.Context, s graph.State) (graph.State, error) { return s, nil }
	setter := func(key, val string) graph.NodeFunc {
		return func(_ context.Context, s graph.State) (graph.State, error) {
			out := graph.CopyState(s)
			out[key] = val
			return out, nil
		}
	}

	if err := g.AddNode("start", noop); err != nil {
		t.Fatal(err)
	}
	if err := g.AddNode("branch_a", setter("a", "done_a")); err != nil {
		t.Fatal(err)
	}
	if err := g.AddNode("branch_b", setter("b", "done_b")); err != nil {
		t.Fatal(err)
	}
	if err := g.AddNode("join_node", noop); err != nil {
		t.Fatal(err)
	}
	g.SetEntry("start")
	if err := g.AddFork("start", []string{"branch_a", "branch_b"}); err != nil {
		t.Fatal(err)
	}
	if err := g.AddJoin("join_node", []string{"branch_a", "branch_b"}); err != nil {
		t.Fatal(err)
	}

	_, err = g.Run(context.Background(), graph.State{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	spans := exp.GetSpans()
	runSpan := findSpan(spans, "graph.run")
	if runSpan == nil {
		t.Fatal("expected graph.run span")
	}

	// Both fork branch spans should share the same graph.run parent.
	branchASpan := findSpan(spans, "graph.node.branch_a")
	branchBSpan := findSpan(spans, "graph.node.branch_b")
	if branchASpan == nil {
		t.Fatal("expected graph.node.branch_a span")
	}
	if branchBSpan == nil {
		t.Fatal("expected graph.node.branch_b span")
	}

	// Both should share the same trace ID.
	if branchASpan.SpanContext.TraceID() != branchBSpan.SpanContext.TraceID() {
		t.Error("fork branch spans should share the same trace ID")
	}

	// Fork branches are children of the "start" node span (the node that triggered the fork),
	// because forkStep passes the start node's context to each branch.
	startSpan := findSpan(spans, "graph.node.start")
	if startSpan == nil {
		t.Fatal("expected graph.node.start span")
	}
	startSpanID := startSpan.SpanContext.SpanID()
	if branchASpan.Parent.SpanID() != startSpanID {
		t.Errorf("branch_a parent should be graph.node.start span, got parent span ID %s", branchASpan.Parent.SpanID())
	}
	if branchBSpan.Parent.SpanID() != startSpanID {
		t.Errorf("branch_b parent should be graph.node.start span, got parent span ID %s", branchBSpan.Parent.SpanID())
	}
}
