package graph

import (
	"context"
	"fmt"
	"testing"
)

// noopNode is a minimal node function for benchmarking engine overhead.
func noopNode(name string) NodeFunc[State] {
	return func(_ context.Context, s State) (State, error) {
		out := CopyState(s)
		out[name] = true
		out[name+"_out"] = true
		return out, nil
	}
}

// buildLinearBenchGraph creates a linear chain of n nodes using data-flow keys.
func buildLinearBenchGraph(b *testing.B, n int) *Graph[State] {
	b.Helper()
	g, err := New[State](WithMaxIterations(n + 10))
	if err != nil {
		b.Fatal(err)
	}

	for i := 0; i < n; i++ {
		name := fmt.Sprintf("n%d", i)
		var in []string
		if i > 0 {
			in = []string{fmt.Sprintf("n%d_out", i-1)}
		}
		out := []string{name + "_out"}
		if _, err := g.Node(name, noopNode(name), In(in...), Out(out...)); err != nil {
			b.Fatal(err)
		}
	}
	return g
}

// buildLinearBenchGraphThen creates a linear chain using Then() for sequencing.
func buildLinearBenchGraphThen(b *testing.B, n int) *Graph[State] {
	b.Helper()
	g, err := New[State](WithMaxIterations(n + 10))
	if err != nil {
		b.Fatal(err)
	}

	var prev *Node[State]
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("n%d", i)
		node, err := g.Node(name, noopNode(name))
		if err != nil {
			b.Fatal(err)
		}
		if prev != nil {
			if err := prev.Then(node); err != nil {
				b.Fatal(err)
			}
		}
		prev = node
	}
	return g
}

// buildDiamondBenchGraph creates: entry → n parallel branches → join.
func buildDiamondBenchGraph(b *testing.B, branches int) *Graph[State] {
	b.Helper()
	g, err := New[State](WithMaxIterations(branches + 10))
	if err != nil {
		b.Fatal(err)
	}

	// Entry node produces "trigger".
	if _, err := g.Node("entry", func(_ context.Context, s State) (State, error) {
		out := CopyState(s)
		out["trigger"] = true
		return out, nil
	}, Out("trigger")); err != nil {
		b.Fatal(err)
	}

	// N branches all read "trigger" and write their own key.
	branchOuts := make([]string, branches)
	for i := 0; i < branches; i++ {
		name := fmt.Sprintf("branch%d", i)
		outKey := name + "_out"
		branchOuts[i] = outKey
		if _, err := g.Node(name, noopNode(name), In("trigger"), Out(outKey)); err != nil {
			b.Fatal(err)
		}
	}

	// Join node reads all branch outputs.
	if _, err := g.Node("join", func(_ context.Context, s State) (State, error) {
		out := CopyState(s)
		out["joined"] = true
		return out, nil
	}, In(branchOuts...), Out("joined")); err != nil {
		b.Fatal(err)
	}

	return g
}

// ─── Benchmarks: Linear chain (data-flow keys) ──────────────────────────────

func BenchmarkLinear_100_DataFlow(b *testing.B) {
	g := buildLinearBenchGraph(b, 100)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := g.Run(ctx, State{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLinear_500_DataFlow(b *testing.B) {
	g := buildLinearBenchGraph(b, 500)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := g.Run(ctx, State{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLinear_1000_DataFlow(b *testing.B) {
	g := buildLinearBenchGraph(b, 1000)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := g.Run(ctx, State{}); err != nil {
			b.Fatal(err)
		}
	}
}

// ─── Benchmarks: Linear chain (Then-based sequencing) ────────────────────────

func BenchmarkLinear_100_Then(b *testing.B) {
	g := buildLinearBenchGraphThen(b, 100)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := g.Run(ctx, State{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLinear_500_Then(b *testing.B) {
	g := buildLinearBenchGraphThen(b, 500)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := g.Run(ctx, State{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLinear_1000_Then(b *testing.B) {
	g := buildLinearBenchGraphThen(b, 1000)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := g.Run(ctx, State{}); err != nil {
			b.Fatal(err)
		}
	}
}

// ─── Benchmarks: Diamond (fan-out/fan-in concurrency) ────────────────────────

func BenchmarkDiamond_100_Branches(b *testing.B) {
	g := buildDiamondBenchGraph(b, 100)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := g.Run(ctx, State{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDiamond_500_Branches(b *testing.B) {
	g := buildDiamondBenchGraph(b, 500)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := g.Run(ctx, State{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDiamond_1000_Branches(b *testing.B) {
	g := buildDiamondBenchGraph(b, 1000)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := g.Run(ctx, State{}); err != nil {
			b.Fatal(err)
		}
	}
}
