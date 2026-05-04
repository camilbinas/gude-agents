package graph

import (
	"context"
	"reflect"
	"testing"

	"pgregory.net/rapid"
)

// ─── Property 8: Fork/join deterministic merge order ─────────────────────────
//
// Feature: graph-generics-unification, Property 8: Fork/join deterministic merge order
//
// **Validates: Requirements 7.3**
//
// Run same fork/join graph N times, verify identical merged state each time.
// Create a graph with start → fork → [branchA, branchB, branchC] → join,
// where each branch sets a unique key. Run 5 times and verify all results are identical.

func TestProperty_ForkJoinDeterministicMergeOrder(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate unique values for each branch to write.
		valA := rapid.StringMatching(`[a-z]{3,8}`).Draw(rt, "valA")
		valB := rapid.StringMatching(`[a-z]{3,8}`).Draw(rt, "valB")
		valC := rapid.StringMatching(`[a-z]{3,8}`).Draw(rt, "valC")
		initVal := rapid.StringMatching(`[a-z]{3,8}`).Draw(rt, "initVal")

		buildGraph := func() *Graph[State] {
			g, err := New[State]()
			if err != nil {
				rt.Fatal(err)
			}

			// Start node: identity
			if err := g.AddNode("start", func(_ context.Context, s State) (State, error) {
				return s, nil
			}); err != nil {
				rt.Fatal(err)
			}

			// Branch nodes: each sets a unique key
			if err := g.AddNode("branchA", func(_ context.Context, s State) (State, error) {
				out := CopyState(s)
				out["key_a"] = valA
				return out, nil
			}); err != nil {
				rt.Fatal(err)
			}
			if err := g.AddNode("branchB", func(_ context.Context, s State) (State, error) {
				out := CopyState(s)
				out["key_b"] = valB
				return out, nil
			}); err != nil {
				rt.Fatal(err)
			}
			if err := g.AddNode("branchC", func(_ context.Context, s State) (State, error) {
				out := CopyState(s)
				out["key_c"] = valC
				return out, nil
			}); err != nil {
				rt.Fatal(err)
			}

			// Join node: identity
			if err := g.AddNode("join", func(_ context.Context, s State) (State, error) {
				return s, nil
			}); err != nil {
				rt.Fatal(err)
			}

			g.SetEntry("start")
			if err := g.AddFork("start", []string{"branchA", "branchB", "branchC"}); err != nil {
				rt.Fatal(err)
			}
			if err := g.AddJoin("join", []string{"branchA", "branchB", "branchC"}); err != nil {
				rt.Fatal(err)
			}

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
