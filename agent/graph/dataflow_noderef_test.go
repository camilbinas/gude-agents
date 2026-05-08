package graph

import (
	"context"
	"testing"
)

func TestAdd_ReturnsNodeHandle(t *testing.T) {
	g := mustGraph(t)

	n, err := g.Add("mynode", noop, In(), Out("out"))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if n == nil {
		t.Fatal("expected non-nil node handle")
	}
	if n.Name() != "mynode" {
		t.Errorf("Name() = %q, want 'mynode'", n.Name())
	}
	if n.String() != "mynode" {
		t.Errorf("String() = %q, want 'mynode'", n.String())
	}
}

func TestAdd_PropagatesError(t *testing.T) {
	g := mustGraph(t)

	n, err := g.Add("", noop, In(), Out("out"))
	if !isValidationError(err) {
		t.Fatalf("expected validation error for empty name, got %v", err)
	}
	if n != nil {
		t.Errorf("expected nil node on error, got %v", n)
	}

	g.Add("a", noop, In(), Out("a_out"))
	n, err = g.Add("a", noop, In(), Out("a_out2"))
	if !isValidationError(err) {
		t.Fatalf("expected validation error for duplicate, got %v", err)
	}
	if n != nil {
		t.Errorf("expected nil node on error, got %v", n)
	}
}

func TestNode_Then_EnforcesOrdering(t *testing.T) {
	g := mustGraph(t)

	var order []string
	track := func(name string) NodeFunc[State] {
		return func(_ context.Context, s State) (State, error) {
			out := CopyState(s)
			order = append(order, name)
			out[name] = "done"
			return out, nil
		}
	}

	a, _ := g.Add("a", track("a"), In(), Out())
	b, _ := g.Add("b", track("b"), In(), Out())
	c, _ := g.Add("c", track("c"), In(), Out())
	g.Start("a")

	if err := a.Then(b); err != nil {
		t.Fatal(err)
	}
	if err := b.Then(c); err != nil {
		t.Fatal(err)
	}

	_, err := g.Run(context.Background(), State{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(order) != 3 {
		t.Fatalf("expected 3 nodes, got %d: %v", len(order), order)
	}
	if order[0] != "a" || order[1] != "b" || order[2] != "c" {
		t.Errorf("expected [a, b, c], got %v", order)
	}
}

func TestNode_Then_NilTargetReturnsError(t *testing.T) {
	g := mustGraph(t)
	a, _ := g.Add("a", noop, In(), Out())
	if err := a.Then(nil); !isValidationError(err) {
		t.Fatalf("expected validation error for nil target, got %v", err)
	}
}

func TestNode_InputOutputKeysAccessors(t *testing.T) {
	g := mustGraph(t)

	n, _ := g.Add("compute", noop, In("a", "b"), Out("c"))

	gotIn := n.InputKeys()
	if len(gotIn) != 2 || gotIn[0] != "a" || gotIn[1] != "b" {
		t.Errorf("InputKeys() = %v, want [a b]", gotIn)
	}

	gotOut := n.OutputKeys()
	if len(gotOut) != 1 || gotOut[0] != "c" {
		t.Errorf("OutputKeys() = %v, want [c]", gotOut)
	}

	// Accessors should return copies — mutating the returned slice must not
	// affect the node's metadata.
	gotIn[0] = "mutated"
	gotIn2 := n.InputKeys()
	if gotIn2[0] != "a" {
		t.Errorf("InputKeys() returned mutable slice: got %q after mutation, want 'a'", gotIn2[0])
	}
}

func TestNode_InputKeysIncludesSyntheticKeys(t *testing.T) {
	g := mustGraph(t)

	a, _ := g.Add("a", noop, In(), Out("a_data"))
	b, _ := g.Add("b", noop, In("a_data"), Out())

	if err := a.Then(b); err != nil {
		t.Fatal(err)
	}

	// b's input keys should now include both "a_data" and the synthetic connect key.
	keys := b.InputKeys()
	if len(keys) != 2 {
		t.Fatalf("expected 2 input keys, got %d: %v", len(keys), keys)
	}
	if keys[0] != "a_data" {
		t.Errorf("first input key = %q, want 'a_data'", keys[0])
	}
}

func TestNode_InterruptBeforeAfter(t *testing.T) {
	cp := &mockCheckpointer{}
	g := mustGraph(t, WithCheckpointer(cp))

	a, _ := g.Add("a", setter("a", "done"), In(), Out("a_out"))
	if err := a.InterruptBefore(); err != nil {
		t.Fatalf("InterruptBefore: %v", err)
	}
	if !g.interruptBefore["a"] {
		t.Error("expected interruptBefore['a'] = true")
	}

	b, _ := g.Add("b", setter("b", "done"), In("a_out"), Out("b_out"))
	if err := b.InterruptAfter(); err != nil {
		t.Fatalf("InterruptAfter: %v", err)
	}
	if !g.interruptAfter["b"] {
		t.Error("expected interruptAfter['b'] = true")
	}
}

func TestNode_SetMeta(t *testing.T) {
	g := mustGraph(t)
	n, _ := g.Add("worker", noop, In(), Out("out"))

	n.SetMeta(NodeMeta{Label: "Worker", Provider: "openai", Model: "gpt-4"})

	got := g.nodeMeta["worker"]
	if got.Label != "Worker" || got.Provider != "openai" || got.Model != "gpt-4" {
		t.Errorf("SetMeta did not persist: %+v", got)
	}
}
