package graph

import (
	"context"
	"fmt"
	"testing"

	"pgregory.net/rapid"
)

// Feature: graph-checkpointing, Property 18: Iterative Engine Behavioral Equivalence

// trackerNode creates a NodeFunc that appends the node name to a "visited" slice
// in state and sets a key with the node's name. It also writes the output key
// (name + "_out") to satisfy data-flow readiness.
func trackerNode(name string) NodeFunc[State] {
	return func(_ context.Context, s State) (State, error) {
		out := CopyState(s)
		out[name] = "executed"
		out[name+"_out"] = "done"

		// Append to visited list for ordering verification.
		visited, _ := out["__visited__"].([]string)
		newVisited := make([]string, len(visited)+1)
		copy(newVisited, visited)
		newVisited[len(visited)] = name
		out["__visited__"] = newVisited

		return out, nil
	}
}

// buildLinearGraph creates a linear chain: node0 → node1 → ... → nodeN-1
// using data-flow keys for sequencing.
func buildLinearGraph(t *rapid.T, numNodes int) (*Graph[State], []string) {
	g, err := New[State]()
	if err != nil {
		t.Fatal(err)
	}

	names := make([]string, numNodes)
	for i := range names {
		names[i] = fmt.Sprintf("node%d", i)
		var inputKeys []string
		if i > 0 {
			inputKeys = []string{fmt.Sprintf("node%d_out", i-1)}
		}
		if _, err := g.Node(names[i], trackerNode(names[i]), In(inputKeys...), Out(names[i]+"_out")); err != nil {
			t.Fatal(err)
		}
	}

	g.Start(names[0])
	return g, names
}

// TestProperty_IterativeEngineBehavioralEquivalence verifies that the iterative
// execution engine produces correct results for randomly generated graph topologies.
func TestProperty_IterativeEngineBehavioralEquivalence(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Test linear and diamond topologies (conditional/fork-join/cyclic are gone).
		topoType := rapid.IntRange(0, 2).Draw(rt, "topology")

		switch topoType {
		case 0: // Linear
			numNodes := rapid.IntRange(2, 8).Draw(rt, "numNodes")
			g, expectedNodes := buildLinearGraph(rt, numNodes)

			res, err := g.Run(context.Background(), State{})
			if err != nil {
				rt.Fatalf("linear graph Run failed: %v", err)
			}

			for _, name := range expectedNodes {
				if res.State[name] != "executed" {
					rt.Fatalf("linear: node %q not executed, state=%v", name, res.State)
				}
			}

			visited, ok := res.State["__visited__"].([]string)
			if !ok {
				rt.Fatalf("linear: __visited__ not found in state")
			}
			if len(visited) != len(expectedNodes) {
				rt.Fatalf("linear: visited length %d != expected %d", len(visited), len(expectedNodes))
			}
			for i, name := range expectedNodes {
				if visited[i] != name {
					rt.Fatalf("linear: visited[%d]=%q, expected %q", i, visited[i], name)
				}
			}

		case 1: // Diamond (concurrent branches)
			g, err := New[State]()
			if err != nil {
				rt.Fatal(err)
			}

			if _, err := g.Node("start", trackerNode("start"), In(), Out("start_out")); err != nil {
				rt.Fatal(err)
			}
			if _, err := g.Node("branchA", trackerNode("branchA"), In("start_out"), Out("branchA_out")); err != nil {
				rt.Fatal(err)
			}
			if _, err := g.Node("branchB", trackerNode("branchB"), In("start_out"), Out("branchB_out")); err != nil {
				rt.Fatal(err)
			}
			if _, err := g.Node("join", trackerNode("join"), In("branchA_out", "branchB_out"), Out("join_out")); err != nil {
				rt.Fatal(err)
			}
			g.Start("start")

			res, err := g.Run(context.Background(), State{})
			if err != nil {
				rt.Fatalf("diamond graph Run failed: %v", err)
			}

			for _, name := range []string{"start", "branchA", "branchB", "join"} {
				if res.State[name] != "executed" {
					rt.Fatalf("diamond: node %q not executed, state=%v", name, res.State)
				}
			}

		case 2: // MaxIterations respected
			maxIter := rapid.IntRange(1, 5).Draw(rt, "maxIter")
			numNodes := maxIter + 2 // more nodes than iterations allowed

			g, err := New[State](WithMaxIterations(maxIter))
			if err != nil {
				rt.Fatal(err)
			}

			names := make([]string, numNodes)
			for i := range names {
				names[i] = fmt.Sprintf("node%d", i)
				var inputKeys []string
				if i > 0 {
					inputKeys = []string{fmt.Sprintf("node%d_out", i-1)}
				}
				if _, err := g.Node(names[i], trackerNode(names[i]), In(inputKeys...), Out(names[i]+"_out")); err != nil {
					rt.Fatal(err)
				}
			}
			g.Start(names[0])

			_, err = g.Run(context.Background(), State{})
			if err == nil {
				rt.Fatalf("expected GraphIterationError for maxIter=%d, got nil", maxIter)
			}

			iterErr, ok := err.(*GraphIterationError)
			if !ok {
				rt.Fatalf("expected GraphIterationError, got %T: %v", err, err)
			}
			if iterErr.Limit != maxIter {
				rt.Fatalf("iteration error limit=%d, expected %d", iterErr.Limit, maxIter)
			}
		}
	})
}

// TestProperty_IterativeEngineLinearStateAccumulation verifies that for linear chains
// of arbitrary length, state accumulates correctly with each node adding its name.
func TestProperty_IterativeEngineLinearStateAccumulation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		numNodes := rapid.IntRange(1, 15).Draw(rt, "numNodes")
		g, expectedNodes := buildLinearGraph(rt, numNodes)

		initialKey := rapid.StringMatching(`[a-z]{3,8}`).Draw(rt, "initialKey")
		initialVal := rapid.StringMatching(`[a-z]{3,8}`).Draw(rt, "initialVal")
		initial := State{initialKey: initialVal}

		res, err := g.Run(context.Background(), initial)
		if err != nil {
			rt.Fatalf("Run failed: %v", err)
		}

		if res.State[initialKey] != initialVal {
			rt.Fatalf("initial state key %q lost: got %v, want %q", initialKey, res.State[initialKey], initialVal)
		}

		for _, name := range expectedNodes {
			if res.State[name] != "executed" {
				rt.Fatalf("node %q not in final state", name)
			}
		}

		visited, ok := res.State["__visited__"].([]string)
		if !ok {
			rt.Fatalf("__visited__ not found")
		}
		if len(visited) != numNodes {
			rt.Fatalf("visited length %d != %d", len(visited), numNodes)
		}
		for i, name := range expectedNodes {
			if visited[i] != name {
				rt.Fatalf("visited[%d]=%q, want %q", i, visited[i], name)
			}
		}
	})
}

// TestProperty_IterativeEngineForkJoinDeterminism verifies that fork/join produces
// deterministic merged state regardless of branch execution order.
func TestProperty_IterativeEngineForkJoinDeterminism(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		numBranches := rapid.IntRange(2, 5).Draw(rt, "numBranches")

		g, err := New[State]()
		if err != nil {
			rt.Fatal(err)
		}

		branchNames := make([]string, numBranches)
		branchOutKeys := make([]string, numBranches)
		for i := range branchNames {
			branchNames[i] = fmt.Sprintf("branch%d", i)
			branchOutKeys[i] = fmt.Sprintf("branch%d_out", i)
			name := branchNames[i]
			outKey := branchOutKeys[i]
			if _, err := g.Node(name, func(_ context.Context, s State) (State, error) {
				out := CopyState(s)
				out[name] = "done"
				out[outKey] = "done"
				return out, nil
			}, In("start_out"), Out(outKey)); err != nil {
				rt.Fatal(err)
			}
		}

		if _, err := g.Node("start", trackerNode("start"), In(), Out("start_out")); err != nil {
			rt.Fatal(err)
		}
		if _, err := g.Node("join", trackerNode("join"), In(branchOutKeys...), Out("join_out")); err != nil {
			rt.Fatal(err)
		}

		g.Start("start")

		// Run multiple times to check determinism.
		var firstState State
		for run := 0; run < 3; run++ {
			res, err := g.Run(context.Background(), State{})
			if err != nil {
				rt.Fatalf("run %d failed: %v", run, err)
			}

			for _, name := range branchNames {
				if res.State[name] != "done" {
					rt.Fatalf("run %d: branch %q not executed", run, name)
				}
			}

			if res.State["join"] != "executed" {
				rt.Fatalf("run %d: join not executed", run)
			}

			if run == 0 {
				firstState = res.State
			} else {
				for _, name := range branchNames {
					if res.State[name] != firstState[name] {
						rt.Fatalf("run %d: non-deterministic state for %q: %v vs %v",
							run, name, res.State[name], firstState[name])
					}
				}
			}
		}
	})
}

// TestProperty_IterativeEngineMaxIterationsRespected verifies that for graphs exceeding
// MaxIterations, the correct error is returned.
func TestProperty_IterativeEngineMaxIterationsRespected(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		maxIter := rapid.IntRange(1, 20).Draw(rt, "maxIter")
		numNodes := maxIter + 2 // ensure we exceed the limit

		g, err := New[State](WithMaxIterations(maxIter))
		if err != nil {
			rt.Fatal(err)
		}

		names := make([]string, numNodes)
		for i := range names {
			names[i] = fmt.Sprintf("node%d", i)
			var inputKeys []string
			if i > 0 {
				inputKeys = []string{fmt.Sprintf("node%d_out", i-1)}
			}
			if _, err := g.Node(names[i], trackerNode(names[i]), In(inputKeys...), Out(names[i]+"_out")); err != nil {
				rt.Fatal(err)
			}
		}
		g.Start(names[0])

		_, err = g.Run(context.Background(), State{})
		if err == nil {
			rt.Fatalf("expected GraphIterationError for maxIter=%d, got nil", maxIter)
		}

		iterErr, ok := err.(*GraphIterationError)
		if !ok {
			rt.Fatalf("expected GraphIterationError, got %T: %v", err, err)
		}
		if iterErr.Limit != maxIter {
			rt.Fatalf("iteration error limit=%d, expected %d", iterErr.Limit, maxIter)
		}
	})
}
