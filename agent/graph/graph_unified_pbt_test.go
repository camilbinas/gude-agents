package graph

import (
	"context"
	"reflect"
	"testing"

	"pgregory.net/rapid"
)

// ─── Property 8: Fork/join deterministic merge order ─────────────────────────

func TestProperty_ForkJoinDeterministicMergeOrder(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		valA := rapid.StringMatching(`[a-z]{3,8}`).Draw(rt, "valA")
		valB := rapid.StringMatching(`[a-z]{3,8}`).Draw(rt, "valB")
		valC := rapid.StringMatching(`[a-z]{3,8}`).Draw(rt, "valC")
		initVal := rapid.StringMatching(`[a-z]{3,8}`).Draw(rt, "initVal")

		buildGraph := func() *Graph[State] {
			g, err := New[State]()
			if err != nil {
				rt.Fatal(err)
			}

			// Start node: writes trigger key
			if _, err := g.Node("start", func(_ context.Context, s State) (State, error) {
				out := CopyState(s)
				out["trigger"] = "done"
				return out, nil
			}, In(), Out("trigger")); err != nil {
				rt.Fatal(err)
			}

			// Branch nodes: each depends on trigger and writes a unique key
			if _, err := g.Node("branchA", func(_ context.Context, s State) (State, error) {
				out := CopyState(s)
				out["key_a"] = valA
				out["a_out"] = "done"
				return out, nil
			}, In("trigger"), Out("a_out")); err != nil {
				rt.Fatal(err)
			}
			if _, err := g.Node("branchB", func(_ context.Context, s State) (State, error) {
				out := CopyState(s)
				out["key_b"] = valB
				out["b_out"] = "done"
				return out, nil
			}, In("trigger"), Out("b_out")); err != nil {
				rt.Fatal(err)
			}
			if _, err := g.Node("branchC", func(_ context.Context, s State) (State, error) {
				out := CopyState(s)
				out["key_c"] = valC
				out["c_out"] = "done"
				return out, nil
			}, In("trigger"), Out("c_out")); err != nil {
				rt.Fatal(err)
			}

			// Join node: depends on all branch outputs
			if _, err := g.Node("join", func(_ context.Context, s State) (State, error) {
				return s, nil
			}, In("a_out", "b_out", "c_out"), Out("join_out")); err != nil {
				rt.Fatal(err)
			}

			g.Start("start")
			return g
		}

		initial := State{"init": initVal}

		// Run the graph 5 times and collect results.
		var results []State
		for range 5 {
			g := buildGraph()
			res, err := g.Run(context.Background(), initial)
			if err != nil {
				rt.Fatalf("Run failed: %v", err)
			}
			results = append(results, res.State)
		}

		// Verify all results are identical.
		for i := 1; i < len(results); i++ {
			if !reflect.DeepEqual(results[0], results[i]) {
				rt.Fatalf("run 0 and run %d produced different results:\n  run 0: %v\n  run %d: %v",
					i, results[0], i, results[i])
			}
		}

		// Also verify the merged state contains all branch keys.
		if results[0]["key_a"] != valA {
			rt.Fatalf("expected key_a=%q, got %v", valA, results[0]["key_a"])
		}
		if results[0]["key_b"] != valB {
			rt.Fatalf("expected key_b=%q, got %v", valB, results[0]["key_b"])
		}
		if results[0]["key_c"] != valC {
			rt.Fatalf("expected key_c=%q, got %v", valC, results[0]["key_c"])
		}
	})
}
