package tracing

import (
	"context"
	"fmt"
	"testing"

	"go.opentelemetry.io/otel/codes"

	"github.com/camilbinas/gude-agents/agent/graph"
)

// ===========================================================================
// ===========================================================================

func TestGraphTracing_RunSpanCreated(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	g, err := graph.New[graph.State](WithGraphTracing(tp))
	if err != nil {
		t.Fatalf("New[graph.State]: %v", err)
	}

	if _, err := g.Node("start", func(_ context.Context, s graph.State) (graph.State, error) {
		out := graph.CopyState(s)
		out["visited"] = true
		return out, nil
	}, graph.In(), graph.Out("visited")); err != nil {
		t.Fatal(err)
	}
	g.Start("start")

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

	g, err := graph.New[graph.State](WithGraphTracing(tp))
	if err != nil {
		t.Fatalf("New[graph.State]: %v", err)
	}

	setter := func(key, val string) graph.NodeFunc[graph.State] {
		return func(_ context.Context, s graph.State) (graph.State, error) {
			out := graph.CopyState(s)
			out[key] = val
			return out, nil
		}
	}

	if _, err := g.Node("alpha", setter("a", "done"), graph.In(), graph.Out("a")); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Node("beta", setter("b", "done"), graph.In("a"), graph.Out("b")); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Node("gamma", setter("c", "done"), graph.In("b"), graph.Out("c")); err != nil {
		t.Fatal(err)
	}
	g.Start("alpha")

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

	// In data-flow scheduling, all nodes are children of graph.run.
	alphaSpan := findSpan(spans, "graph.node.alpha")
	betaSpan := findSpan(spans, "graph.node.beta")
	gammaSpan := findSpan(spans, "graph.node.gamma")

	if alphaSpan.Parent.SpanID() != runSpan.SpanContext.SpanID() {
		t.Errorf("graph.node.alpha parent should be graph.run span")
	}
	if betaSpan.Parent.SpanID() != runSpan.SpanContext.SpanID() {
		t.Errorf("graph.node.beta parent should be graph.run span")
	}
	if gammaSpan.Parent.SpanID() != runSpan.SpanContext.SpanID() {
		t.Errorf("graph.node.gamma parent should be graph.run span")
	}
}

func TestGraphTracing_IterationsAttribute(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	g, err := graph.New[graph.State](WithGraphTracing(tp))
	if err != nil {
		t.Fatalf("New[graph.State]: %v", err)
	}

	noop := func(key string) graph.NodeFunc[graph.State] {
		return func(_ context.Context, s graph.State) (graph.State, error) {
			out := graph.CopyState(s)
			out[key] = true
			return out, nil
		}
	}
	if _, err := g.Node("a", noop("out_a"), graph.In(), graph.Out("out_a")); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Node("b", noop("out_b"), graph.In("out_a"), graph.Out("out_b")); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Node("c", noop("out_c"), graph.In("out_b"), graph.Out("out_c")); err != nil {
		t.Fatal(err)
	}
	g.Start("a")

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

	g, err := graph.New[graph.State](WithGraphTracing(tp))
	if err != nil {
		t.Fatalf("New[graph.State]: %v", err)
	}

	if _, err := g.Node("ok_node", func(_ context.Context, s graph.State) (graph.State, error) {
		out := graph.CopyState(s)
		out["ok"] = true
		return out, nil
	}, graph.In(), graph.Out("ok")); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Node("bad_node", func(_ context.Context, _ graph.State) (graph.State, error) {
		return nil, fmt.Errorf("node exploded")
	}, graph.In("ok"), graph.Out("bad_out")); err != nil {
		t.Fatal(err)
	}
	g.Start("ok_node")

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
// ===========================================================================

func TestGraphTracing_ConcurrentNodeSpansShareParent(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	g, err := graph.New[graph.State](WithGraphTracing(tp))
	if err != nil {
		t.Fatalf("New[graph.State]: %v", err)
	}

	noop := func(_ context.Context, s graph.State) (graph.State, error) { return s, nil }
	setter := func(key, val string) graph.NodeFunc[graph.State] {
		return func(_ context.Context, s graph.State) (graph.State, error) {
			out := graph.CopyState(s)
			out[key] = val
			return out, nil
		}
	}
	_ = noop

	// start produces "ready", branch_a and branch_b both read "ready" (concurrent),
	// join_node reads both outputs.
	if _, err := g.Node("start", setter("ready", "yes"), graph.In(), graph.Out("ready")); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Node("branch_a", setter("a", "done_a"), graph.In("ready"), graph.Out("a")); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Node("branch_b", setter("b", "done_b"), graph.In("ready"), graph.Out("b")); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Node("join_node", setter("joined", "yes"), graph.In("a", "b"), graph.Out("joined")); err != nil {
		t.Fatal(err)
	}
	g.Start("start")

	_, err = g.Run(context.Background(), graph.State{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	spans := exp.GetSpans()
	runSpan := findSpan(spans, "graph.run")
	if runSpan == nil {
		t.Fatal("expected graph.run span")
	}

	// Both concurrent branch spans should exist.
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
		t.Error("concurrent branch spans should share the same trace ID")
	}
}
