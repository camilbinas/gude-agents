package graph

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestConcurrency_DisjointKeys verifies that two concurrent nodes writing
// disjoint keys both have their values present in the final state.
func TestConcurrency_DisjointKeys(t *testing.T) {
	g := mustGraph(t)

	nodeA := func(_ context.Context, s State) (State, error) {
		out := CopyState(s)
		out["key_a"] = "from_a"
		return out, nil
	}
	nodeB := func(_ context.Context, s State) (State, error) {
		out := CopyState(s)
		out["key_b"] = "from_b"
		return out, nil
	}

	mustAddNodeWithKeys(t, g, "entry", noop, []string{"trigger"}, []string{})
	mustAddNodeWithKeys(t, g, "node_a", nodeA, []string{"key_a"}, []string{"trigger"})
	mustAddNodeWithKeys(t, g, "node_b", nodeB, []string{"key_b"}, []string{"trigger"})
	g.Start("entry")

	result, err := g.Run(context.Background(), State{"trigger": true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.State["key_a"] != "from_a" {
		t.Errorf("expected key_a='from_a', got %v", result.State["key_a"])
	}
	if result.State["key_b"] != "from_b" {
		t.Errorf("expected key_b='from_b', got %v", result.State["key_b"])
	}
}

// TestConcurrency_SameKey_AlphabeticallyLastWins verifies that when two
// concurrent nodes write the same key, the alphabetically-last node wins.
func TestConcurrency_SameKey_AlphabeticallyLastWins(t *testing.T) {
	g := mustGraph(t)

	// "node_a" writes "conflict" = "a_value"
	nodeA := func(_ context.Context, s State) (State, error) {
		out := CopyState(s)
		out["conflict"] = "a_value"
		out["a_out"] = "done"
		return out, nil
	}
	// "node_b" writes "conflict" = "b_value"
	// Since "node_b" > "node_a" alphabetically, node_b's value should win.
	nodeB := func(_ context.Context, s State) (State, error) {
		out := CopyState(s)
		out["conflict"] = "b_value"
		out["b_out"] = "done"
		return out, nil
	}

	mustAddNodeWithKeys(t, g, "entry", noop, []string{"trigger"}, []string{})
	mustAddNodeWithKeys(t, g, "node_a", nodeA, []string{"a_out"}, []string{"trigger"})
	mustAddNodeWithKeys(t, g, "node_b", nodeB, []string{"b_out"}, []string{"trigger"})
	g.Start("entry")

	result, err := g.Run(context.Background(), State{"trigger": true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// node_b is alphabetically last, so its value should win.
	if result.State["conflict"] != "b_value" {
		t.Errorf("expected conflict='b_value' (alphabetically-last wins), got %v", result.State["conflict"])
	}
}

// TestConcurrency_Determinism verifies that running the same graph 10 times
// produces identical final state each time.
func TestConcurrency_Determinism(t *testing.T) {
	buildGraph := func() *Graph[State] {
		g := mustGraph(t)

		nodeA := func(_ context.Context, s State) (State, error) {
			out := CopyState(s)
			out["a_result"] = "hello_from_a"
			out["a_out"] = "done"
			return out, nil
		}
		nodeB := func(_ context.Context, s State) (State, error) {
			out := CopyState(s)
			out["b_result"] = "hello_from_b"
			out["b_out"] = "done"
			return out, nil
		}
		nodeC := func(_ context.Context, s State) (State, error) {
			out := CopyState(s)
			out["c_result"] = "hello_from_c"
			out["c_out"] = "done"
			return out, nil
		}

		mustAddNodeWithKeys(t, g, "entry", noop, []string{"trigger"}, []string{})
		mustAddNodeWithKeys(t, g, "node_a", nodeA, []string{"a_out"}, []string{"trigger"})
		mustAddNodeWithKeys(t, g, "node_b", nodeB, []string{"b_out"}, []string{"trigger"})
		mustAddNodeWithKeys(t, g, "node_c", nodeC, []string{"c_out"}, []string{"trigger"})
		g.Start("entry")
		return g
	}

	initial := State{"trigger": true}

	// Run 10 times and collect results.
	var firstResult State
	for i := 0; i < 10; i++ {
		g := buildGraph()
		result, err := g.Run(context.Background(), initial)
		if err != nil {
			t.Fatalf("run %d: unexpected error: %v", i, err)
		}

		if i == 0 {
			firstResult = result.State
		} else {
			// Compare to first result.
			for k, v := range firstResult {
				if result.State[k] != v {
					t.Fatalf("run %d: key %q differs: first=%v, this=%v", i, k, v, result.State[k])
				}
			}
			for k, v := range result.State {
				if firstResult[k] != v {
					t.Fatalf("run %d: extra key %q=%v not in first result", i, k, v)
				}
			}
		}
	}
}

// TestConcurrency_IsolatedCopies verifies that concurrent nodes receive
// isolated state copies and cannot observe each other's writes during execution.
func TestConcurrency_IsolatedCopies(t *testing.T) {
	g := mustGraph(t)

	var mu sync.Mutex
	var nodeASeenBWrite bool

	// node_b writes "b_wrote" to its state copy.
	nodeB := func(_ context.Context, s State) (State, error) {
		out := CopyState(s)
		out["b_wrote"] = "yes"
		out["b_out"] = "done"
		// Add a small delay to increase chance of overlap.
		time.Sleep(5 * time.Millisecond)
		return out, nil
	}

	// node_a checks if it can see node_b's write (it should NOT).
	nodeA := func(_ context.Context, s State) (State, error) {
		// Small delay to let node_b potentially write first.
		time.Sleep(10 * time.Millisecond)
		mu.Lock()
		if _, exists := s["b_wrote"]; exists {
			nodeASeenBWrite = true
		}
		mu.Unlock()
		out := CopyState(s)
		out["a_out"] = "done"
		return out, nil
	}

	mustAddNodeWithKeys(t, g, "entry", noop, []string{"trigger"}, []string{})
	mustAddNodeWithKeys(t, g, "node_a", nodeA, []string{"a_out"}, []string{"trigger"})
	mustAddNodeWithKeys(t, g, "node_b", nodeB, []string{"b_out"}, []string{"trigger"})
	g.Start("entry")

	result, err := g.Run(context.Background(), State{"trigger": true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// node_a should NOT have seen node_b's write since they get isolated copies.
	if nodeASeenBWrite {
		t.Error("node_a should NOT see node_b's writes — isolation violated")
	}

	// But after merge, both results should be present in final state.
	if result.State["b_wrote"] != "yes" {
		t.Errorf("expected b_wrote='yes' in final state, got %v", result.State["b_wrote"])
	}
	if result.State["a_out"] != "done" {
		t.Errorf("expected a_out='done' in final state, got %v", result.State["a_out"])
	}
}
