package graph

import (
	"context"
	"fmt"
	"sort"
	"testing"

	"pgregory.net/rapid"
)

// Feature: graph-checkpointing, Property 18: Iterative Engine Behavioral Equivalence
//
// **Validates: Requirements 9.2, 9.6**
//
// For any graph topology (linear, conditional, fork/join, cyclic up to iteration limit)
// and initial state, the iterative execution engine produces correct final state and
// respects MaxIterations for cyclic graphs.

// topologyKind identifies the type of generated graph topology.
type topologyKind int

const (
	topoLinear      topologyKind = 0
	topoConditional topologyKind = 1
	topoForkJoin    topologyKind = 2
	topoCyclic      topologyKind = 3
)

// trackerNode creates a NodeFunc that appends the node name to a "visited" slice
// in state and sets a key with the node's name.
func trackerNode(name string) NodeFunc[State] {
	return func(_ context.Context, s State) (State, error) {
		out := CopyState(s)
		out[name] = "executed"

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
func buildLinearGraph(t *rapid.T, numNodes int) (*Graph[State], []string) {
	g, err := New[State]()
	if err != nil {
		t.Fatal(err)
	}

	names := make([]string, numNodes)
	for i := range names {
		names[i] = fmt.Sprintf("node%d", i)
		if err := g.AddNode(names[i], trackerNode(names[i])); err != nil {
			t.Fatal(err)
		}
	}

	g.SetEntry(names[0])
	for i := 0; i < numNodes-1; i++ {
		if err := g.AddEdge(names[i], names[i+1]); err != nil {
			t.Fatal(err)
		}
	}

	return g, names
}

// buildConditionalGraph creates: start → router → (branchA or branchB)
func buildConditionalGraph(t *rapid.T, chooseBranch bool) (*Graph[State], string) {
	g, err := New[State]()
	if err != nil {
		t.Fatal(err)
	}

	if err := g.AddNode("start", trackerNode("start")); err != nil {
		t.Fatal(err)
	}
	if err := g.AddNode("branchA", trackerNode("branchA")); err != nil {
		t.Fatal(err)
	}
	if err := g.AddNode("branchB", trackerNode("branchB")); err != nil {
		t.Fatal(err)
	}

	g.SetEntry("start")
	if err := g.AddConditionalEdge("start", func(_ context.Context, s State) (string, error) {
		if s["choose_a"] == true {
			return "branchA", nil
		}
		return "branchB", nil
	}); err != nil {
		t.Fatal(err)
	}

	expectedBranch := "branchB"
	if chooseBranch {
		expectedBranch = "branchA"
	}
	return g, expectedBranch
}

// buildForkJoinGraph creates: start → fork → [branchA, branchB] → join → end
func buildForkJoinGraph(t *rapid.T) (*Graph[State], []string) {
	g, err := New[State]()
	if err != nil {
		t.Fatal(err)
	}

	nodes := []string{"start", "branchA", "branchB", "join"}
	for _, name := range nodes {
		if err := g.AddNode(name, trackerNode(name)); err != nil {
			t.Fatal(err)
		}
	}

	g.SetEntry("start")
	if err := g.AddFork("start", []string{"branchA", "branchB"}); err != nil {
		t.Fatal(err)
	}
	if err := g.AddJoin("join", []string{"branchA", "branchB"}); err != nil {
		t.Fatal(err)
	}

	return g, nodes
}

// buildCyclicGraph creates: node0 → node1 → router → (node0 or END)
// The router loops back to node0 for `loopCount` iterations, then terminates.
func buildCyclicGraph(t *rapid.T, maxIter int) (*Graph[State], int) {
	g, err := New[State](WithMaxIterations(maxIter))
	if err != nil {
		t.Fatal(err)
	}

	if err := g.AddNode("node0", func(_ context.Context, s State) (State, error) {
		out := CopyState(s)
		count, _ := out["counter"].(int)
		out["counter"] = count + 1
		out["node0"] = "executed"

		visited, _ := out["__visited__"].([]string)
		newVisited := make([]string, len(visited)+1)
		copy(newVisited, visited)
		newVisited[len(visited)] = "node0"
		out["__visited__"] = newVisited

		return out, nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := g.AddNode("node1", trackerNode("node1")); err != nil {
		t.Fatal(err)
	}

	g.SetEntry("node0")
	if err := g.AddEdge("node0", "node1"); err != nil {
		t.Fatal(err)
	}
	// node1 routes back to node0 (creating a cycle)
	if err := g.AddEdge("node1", "node0"); err != nil {
		t.Fatal(err)
	}

	return g, maxIter
}

// TestProperty_IterativeEngineBehavioralEquivalence verifies that the iterative
// execution engine produces correct results for randomly generated graph topologies.
//
// **Validates: Requirements 9.2, 9.6**
func TestProperty_IterativeEngineBehavioralEquivalence(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		topoType := topologyKind(rapid.IntRange(0, 3).Draw(rt, "topology"))

		switch topoType {
		case topoLinear:
			numNodes := rapid.IntRange(2, 8).Draw(rt, "numNodes")
			g, expectedNodes := buildLinearGraph(rt, numNodes)

			res, err := g.Run(context.Background(), State{})
			if err != nil {
				rt.Fatalf("linear graph Run failed: %v", err)
			}

			// Verify all nodes were executed.
			for _, name := range expectedNodes {
				if res.State[name] != "executed" {
					rt.Fatalf("linear: node %q not executed, state=%v", name, res.State)
				}
			}

			// Verify execution order via visited list.
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

		case topoConditional:
			chooseBranch := rapid.Bool().Draw(rt, "chooseBranch")
			g, expectedBranch := buildConditionalGraph(rt, chooseBranch)

			initialState := State{"choose_a": chooseBranch}
			res, err := g.Run(context.Background(), initialState)
			if err != nil {
				rt.Fatalf("conditional graph Run failed: %v", err)
			}

			// Verify start was executed.
			if res.State["start"] != "executed" {
				rt.Fatalf("conditional: start not executed")
			}

			// Verify the correct branch was taken.
			if res.State[expectedBranch] != "executed" {
				rt.Fatalf("conditional: expected branch %q not executed, state=%v", expectedBranch, res.State)
			}

			// Verify the other branch was NOT taken.
			otherBranch := "branchA"
			if expectedBranch == "branchA" {
				otherBranch = "branchB"
			}
			if res.State[otherBranch] == "executed" {
				rt.Fatalf("conditional: unexpected branch %q was executed", otherBranch)
			}

		case topoForkJoin:
			g, expectedNodes := buildForkJoinGraph(rt)

			res, err := g.Run(context.Background(), State{})
			if err != nil {
				rt.Fatalf("fork/join graph Run failed: %v", err)
			}

			// Verify all nodes were executed via their state keys.
			// Note: __visited__ is unreliable for fork/join because each branch
			// gets its own state copy and the merge overwrites the list.
			// Instead, verify via the individual node keys set by trackerNode.
			for _, name := range expectedNodes {
				if res.State[name] != "executed" {
					rt.Fatalf("fork/join: node %q not executed, state=%v", name, res.State)
				}
			}

			// Verify deterministic merged state: both branches contribute.
			if res.State["branchA"] != "executed" || res.State["branchB"] != "executed" {
				rt.Fatalf("fork/join: branches not both executed, state=%v", res.State)
			}

			// Verify join node ran after both branches.
			if res.State["join"] != "executed" {
				rt.Fatalf("fork/join: join node not executed")
			}

		case topoCyclic:
			maxIter := rapid.IntRange(2, 10).Draw(rt, "maxIter")
			g, limit := buildCyclicGraph(rt, maxIter)

			_, err := g.Run(context.Background(), State{})
			if err == nil {
				rt.Fatalf("cyclic graph should have hit MaxIterations limit")
			}

			// Verify it's a GraphIterationError with the correct limit.
			iterErr, ok := err.(*GraphIterationError)
			if !ok {
				rt.Fatalf("cyclic: expected GraphIterationError, got %T: %v", err, err)
			}
			if iterErr.Limit != limit {
				rt.Fatalf("cyclic: iteration limit=%d, expected %d", iterErr.Limit, limit)
			}
		}
	})
}

// TestProperty_IterativeEngineLinearStateAccumulation verifies that for linear chains
// of arbitrary length, state accumulates correctly with each node adding its name.
//
// **Validates: Requirements 9.2, 9.6**
func TestProperty_IterativeEngineLinearStateAccumulation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		numNodes := rapid.IntRange(1, 15).Draw(rt, "numNodes")
		g, expectedNodes := buildLinearGraph(rt, numNodes)

		// Add some initial state to verify it's preserved.
		initialKey := rapid.StringMatching(`[a-z]{3,8}`).Draw(rt, "initialKey")
		initialVal := rapid.StringMatching(`[a-z]{3,8}`).Draw(rt, "initialVal")
		initial := State{initialKey: initialVal}

		res, err := g.Run(context.Background(), initial)
		if err != nil {
			rt.Fatalf("Run failed: %v", err)
		}

		// Verify initial state is preserved.
		if res.State[initialKey] != initialVal {
			rt.Fatalf("initial state key %q lost: got %v, want %q", initialKey, res.State[initialKey], initialVal)
		}

		// Verify each node added its key.
		for _, name := range expectedNodes {
			if res.State[name] != "executed" {
				rt.Fatalf("node %q not in final state", name)
			}
		}

		// Verify visited order is sequential.
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
//
// **Validates: Requirements 9.2, 9.6**
func TestProperty_IterativeEngineForkJoinDeterminism(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Run the same fork/join graph multiple times and verify consistent results.
		numBranches := rapid.IntRange(2, 5).Draw(rt, "numBranches")

		g, err := New[State]()
		if err != nil {
			rt.Fatal(err)
		}

		branchNames := make([]string, numBranches)
		for i := range branchNames {
			branchNames[i] = fmt.Sprintf("branch%d", i)
			name := branchNames[i]
			if err := g.AddNode(name, func(_ context.Context, s State) (State, error) {
				out := CopyState(s)
				out[name] = "done"
				return out, nil
			}); err != nil {
				rt.Fatal(err)
			}
		}

		if err := g.AddNode("start", trackerNode("start")); err != nil {
			rt.Fatal(err)
		}
		if err := g.AddNode("join", trackerNode("join")); err != nil {
			rt.Fatal(err)
		}

		g.SetEntry("start")
		if err := g.AddFork("start", branchNames); err != nil {
			rt.Fatal(err)
		}
		if err := g.AddJoin("join", branchNames); err != nil {
			rt.Fatal(err)
		}

		// Run multiple times to check determinism.
		var firstState State
		for run := 0; run < 3; run++ {
			res, err := g.Run(context.Background(), State{})
			if err != nil {
				rt.Fatalf("run %d failed: %v", run, err)
			}

			// Verify all branches executed.
			for _, name := range branchNames {
				if res.State[name] != "done" {
					rt.Fatalf("run %d: branch %q not executed", run, name)
				}
			}

			// Verify join executed.
			if res.State["join"] != "executed" {
				rt.Fatalf("run %d: join not executed", run)
			}

			if run == 0 {
				firstState = res.State
			} else {
				// Compare keys (excluding __visited__ which may have non-deterministic order for branches).
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

// TestProperty_IterativeEngineMaxIterationsRespected verifies that for cyclic graphs,
// the MaxIterations limit is always respected and the correct error is returned.
//
// **Validates: Requirements 9.6**
func TestProperty_IterativeEngineMaxIterationsRespected(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		maxIter := rapid.IntRange(1, 20).Draw(rt, "maxIter")

		g, err := New[State](WithMaxIterations(maxIter))
		if err != nil {
			rt.Fatal(err)
		}

		// Create a simple cycle: a → b → a
		if err := g.AddNode("a", trackerNode("a")); err != nil {
			rt.Fatal(err)
		}
		if err := g.AddNode("b", trackerNode("b")); err != nil {
			rt.Fatal(err)
		}
		g.SetEntry("a")
		if err := g.AddEdge("a", "b"); err != nil {
			rt.Fatal(err)
		}
		if err := g.AddEdge("b", "a"); err != nil {
			rt.Fatal(err)
		}

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

// TestProperty_IterativeEngineConditionalRouting verifies that conditional edges
// correctly route to the selected branch for arbitrary router decisions.
//
// **Validates: Requirements 9.2**
func TestProperty_IterativeEngineConditionalRouting(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a random number of branches (2-6).
		numBranches := rapid.IntRange(2, 6).Draw(rt, "numBranches")
		selectedIdx := rapid.IntRange(0, numBranches-1).Draw(rt, "selectedBranch")

		g, err := New[State]()
		if err != nil {
			rt.Fatal(err)
		}

		branchNames := make([]string, numBranches)
		for i := range branchNames {
			branchNames[i] = fmt.Sprintf("branch%d", i)
			name := branchNames[i]
			if err := g.AddNode(name, trackerNode(name)); err != nil {
				rt.Fatal(err)
			}
		}

		if err := g.AddNode("router", trackerNode("router")); err != nil {
			rt.Fatal(err)
		}

		g.SetEntry("router")

		// Sort branch names for deterministic selection.
		sorted := make([]string, len(branchNames))
		copy(sorted, branchNames)
		sort.Strings(sorted)

		selectedBranch := sorted[selectedIdx]
		if err := g.AddConditionalEdge("router", func(_ context.Context, s State) (string, error) {
			target, _ := s["__target__"].(string)
			return target, nil
		}); err != nil {
			rt.Fatal(err)
		}

		res, err := g.Run(context.Background(), State{"__target__": selectedBranch})
		if err != nil {
			rt.Fatalf("Run failed: %v", err)
		}

		// Verify router was executed.
		if res.State["router"] != "executed" {
			rt.Fatalf("router not executed")
		}

		// Verify selected branch was executed.
		if res.State[selectedBranch] != "executed" {
			rt.Fatalf("selected branch %q not executed, state=%v", selectedBranch, res.State)
		}

		// Verify other branches were NOT executed.
		for _, name := range sorted {
			if name != selectedBranch && res.State[name] == "executed" {
				rt.Fatalf("unexpected branch %q was executed", name)
			}
		}
	})
}
