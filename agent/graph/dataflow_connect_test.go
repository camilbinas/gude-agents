package graph

import (
	"context"
	"testing"
)

func TestConnect_BasicSequencing(t *testing.T) {
	g := mustGraph(t)

	// Register two nodes with no data dependency — just ordering.
	mustAddNodeWithKeys(t, g, "a", setter("a", "done_a"), []string{}, []string{})
	mustAddNodeWithKeys(t, g, "b", setter("b", "done_b"), []string{}, []string{})
	g.Start("a")

	// Without Connect, both "a" and "b" would be entry nodes (empty input keys).
	// Connect forces "b" to wait for "a".
	if err := g.Connect("a", "b"); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	res, err := g.Run(context.Background(), State{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.State["a"] != "done_a" {
		t.Errorf("expected a=done_a, got %v", res.State["a"])
	}
	if res.State["b"] != "done_b" {
		t.Errorf("expected b=done_b, got %v", res.State["b"])
	}
}

func TestConnect_ThreeNodeChain(t *testing.T) {
	g := mustGraph(t)

	var order []string
	trackNode := func(name string) NodeFunc[State] {
		return func(_ context.Context, s State) (State, error) {
			out := CopyState(s)
			order = append(order, name)
			out[name] = "done"
			return out, nil
		}
	}

	mustAddNodeWithKeys(t, g, "first", trackNode("first"), []string{}, []string{})
	mustAddNodeWithKeys(t, g, "second", trackNode("second"), []string{}, []string{})
	mustAddNodeWithKeys(t, g, "third", trackNode("third"), []string{}, []string{})
	g.Start("first")

	if err := g.Connect("first", "second"); err != nil {
		t.Fatal(err)
	}
	if err := g.Connect("second", "third"); err != nil {
		t.Fatal(err)
	}

	_, err := g.Run(context.Background(), State{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(order) != 3 {
		t.Fatalf("expected 3 nodes executed, got %d: %v", len(order), order)
	}
	if order[0] != "first" || order[1] != "second" || order[2] != "third" {
		t.Errorf("expected [first, second, third], got %v", order)
	}
}

func TestConnect_MixedWithDataKeys(t *testing.T) {
	// Node "a" writes a real data key "a_data". Node "b" reads it (data dependency).
	// Node "c" has no data dependency on "b" but must run after it (Connect).
	g := mustGraph(t)

	var order []string
	trackNode := func(name, outKey string) NodeFunc[State] {
		return func(_ context.Context, s State) (State, error) {
			out := CopyState(s)
			order = append(order, name)
			out[name] = "done"
			if outKey != "" {
				out[outKey] = "done"
			}
			return out, nil
		}
	}

	mustAddNodeWithKeys(t, g, "a", trackNode("a", "a_data"), []string{"a_data"}, []string{})
	mustAddNodeWithKeys(t, g, "b", trackNode("b", ""), []string{}, []string{"a_data"})
	mustAddNodeWithKeys(t, g, "c", trackNode("c", ""), []string{}, []string{})
	g.Start("a")

	// c must wait for b (ordering only, no data).
	if err := g.Connect("b", "c"); err != nil {
		t.Fatal(err)
	}

	_, err := g.Run(context.Background(), State{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(order) != 3 {
		t.Fatalf("expected 3 nodes, got %d: %v", len(order), order)
	}
	// a must be first, b must be before c.
	if order[0] != "a" {
		t.Errorf("expected a first, got %v", order)
	}
	bIdx, cIdx := -1, -1
	for i, n := range order {
		if n == "b" {
			bIdx = i
		}
		if n == "c" {
			cIdx = i
		}
	}
	if bIdx >= cIdx {
		t.Errorf("expected b before c, got order %v", order)
	}
}

func TestConnect_UnregisteredNodeReturnsError(t *testing.T) {
	g := mustGraph(t)
	mustAddNodeWithKeys(t, g, "a", noop, []string{}, []string{})

	err := g.Connect("a", "ghost")
	if err == nil {
		t.Fatal("expected error for unregistered target, got nil")
	}
	if !isValidationError(err) {
		t.Fatalf("expected GraphValidationError, got %T: %v", err, err)
	}

	err = g.Connect("ghost", "a")
	if err == nil {
		t.Fatal("expected error for unregistered source, got nil")
	}
	if !isValidationError(err) {
		t.Fatalf("expected GraphValidationError, got %T: %v", err, err)
	}
}

func TestConnect_WithCheckpointing(t *testing.T) {
	cp := &mockCheckpointer{}
	g := mustGraph(t, WithCheckpointer(cp))

	mustAddNodeWithKeys(t, g, "a", setter("a", "done_a"), []string{}, []string{})
	mustAddNodeWithKeys(t, g, "b", setter("b", "done_b"), []string{}, []string{})
	g.Start("a")

	if err := g.Connect("a", "b"); err != nil {
		t.Fatal(err)
	}

	res, err := g.Run(context.Background(), State{}, WithThreadID("connect-cp-1"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.State["a"] != "done_a" || res.State["b"] != "done_b" {
		t.Errorf("unexpected state: %v", res.State)
	}

	// Verify checkpoints were saved for both nodes.
	if len(cp.saved) < 2 {
		t.Errorf("expected at least 2 checkpoints, got %d", len(cp.saved))
	}
}
