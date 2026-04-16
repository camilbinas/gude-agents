package graph

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func noop(_ context.Context, s State) (State, error) { return s, nil }

func setter(key, val string) NodeFunc {
	return func(_ context.Context, s State) (State, error) {
		out := CopyState(s)
		out[key] = val
		return out, nil
	}
}

func mustGraph(t *testing.T, opts ...GraphOption) *Graph {
	t.Helper()
	g, err := NewGraph(opts...)
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	return g
}

func isValidationError(err error) bool {
	var ve *GraphValidationError
	return errors.As(err, &ve)
}

// ── Task 6: builder and validation ───────────────────────────────────────────

func TestGraphBuilder(t *testing.T) {
	t.Run("6.1 AddNode rejects empty name", func(t *testing.T) {
		g := mustGraph(t)
		if err := g.AddNode("", noop); !isValidationError(err) {
			t.Fatalf("expected GraphValidationError, got %v", err)
		}
	})

	t.Run("6.1 AddNode rejects nil fn", func(t *testing.T) {
		g := mustGraph(t)
		if err := g.AddNode("a", nil); !isValidationError(err) {
			t.Fatalf("expected GraphValidationError, got %v", err)
		}
	})

	t.Run("6.1 AddNode rejects duplicate name", func(t *testing.T) {
		g := mustGraph(t)
		if err := g.AddNode("a", noop); err != nil {
			t.Fatalf("first AddNode: %v", err)
		}
		if err := g.AddNode("a", noop); !isValidationError(err) {
			t.Fatalf("expected GraphValidationError on duplicate, got %v", err)
		}
	})

	t.Run("6.2 AddEdge rejects empty from", func(t *testing.T) {
		g := mustGraph(t)
		if err := g.AddEdge("", "b"); !isValidationError(err) {
			t.Fatalf("expected GraphValidationError, got %v", err)
		}
	})

	t.Run("6.2 AddEdge rejects empty to", func(t *testing.T) {
		g := mustGraph(t)
		if err := g.AddEdge("a", ""); !isValidationError(err) {
			t.Fatalf("expected GraphValidationError, got %v", err)
		}
	})

	t.Run("6.3 AddFork rejects fewer than two targets", func(t *testing.T) {
		g := mustGraph(t)
		if err := g.AddFork("a", []string{"b"}); !isValidationError(err) {
			t.Fatalf("expected GraphValidationError, got %v", err)
		}
		if err := g.AddFork("a", nil); !isValidationError(err) {
			t.Fatalf("expected GraphValidationError for nil targets, got %v", err)
		}
	})

	t.Run("6.4 AddJoin rejects fewer than two predecessors", func(t *testing.T) {
		g := mustGraph(t)
		if err := g.AddJoin("j", []string{"a"}); !isValidationError(err) {
			t.Fatalf("expected GraphValidationError, got %v", err)
		}
		if err := g.AddJoin("j", nil); !isValidationError(err) {
			t.Fatalf("expected GraphValidationError for nil predecessors, got %v", err)
		}
	})

	t.Run("6.5 WithGraphMaxIterations rejects value < 1", func(t *testing.T) {
		_, err := NewGraph(WithGraphMaxIterations(0))
		if !isValidationError(err) {
			t.Fatalf("expected GraphValidationError for 0, got %v", err)
		}
		_, err = NewGraph(WithGraphMaxIterations(-5))
		if !isValidationError(err) {
			t.Fatalf("expected GraphValidationError for -5, got %v", err)
		}
	})
}

func TestGraphValidation(t *testing.T) {
	t.Run("6.6 validate: entry node missing", func(t *testing.T) {
		g := mustGraph(t)
		if err := g.AddNode("a", noop); err != nil {
			t.Fatal(err)
		}
		g.SetEntry("missing")
		_, err := g.Run(context.Background(), State{})
		if !isValidationError(err) {
			t.Fatalf("expected GraphValidationError, got %v", err)
		}
	})

	t.Run("6.7 validate: edge target unregistered", func(t *testing.T) {
		g := mustGraph(t)
		if err := g.AddNode("a", noop); err != nil {
			t.Fatal(err)
		}
		g.SetEntry("a")
		if err := g.AddEdge("a", "ghost"); err != nil {
			t.Fatal(err)
		}
		_, err := g.Run(context.Background(), State{})
		if !isValidationError(err) {
			t.Fatalf("expected GraphValidationError, got %v", err)
		}
	})

	t.Run("6.8 validate: fork target unregistered", func(t *testing.T) {
		g := mustGraph(t)
		if err := g.AddNode("a", noop); err != nil {
			t.Fatal(err)
		}
		if err := g.AddNode("b", noop); err != nil {
			t.Fatal(err)
		}
		g.SetEntry("a")
		if err := g.AddFork("a", []string{"b", "ghost"}); err != nil {
			t.Fatal(err)
		}
		_, err := g.Run(context.Background(), State{})
		if !isValidationError(err) {
			t.Fatalf("expected GraphValidationError, got %v", err)
		}
	})

	t.Run("6.9 validate: join predecessor unregistered", func(t *testing.T) {
		g := mustGraph(t)
		if err := g.AddNode("a", noop); err != nil {
			t.Fatal(err)
		}
		if err := g.AddNode("b", noop); err != nil {
			t.Fatal(err)
		}
		if err := g.AddNode("j", noop); err != nil {
			t.Fatal(err)
		}
		g.SetEntry("a")
		if err := g.AddJoin("j", []string{"b", "ghost"}); err != nil {
			t.Fatal(err)
		}
		_, err := g.Run(context.Background(), State{})
		if !isValidationError(err) {
			t.Fatalf("expected GraphValidationError, got %v", err)
		}
	})

	t.Run("6.10 validate: conflicting routing rules (static + fork)", func(t *testing.T) {
		g := mustGraph(t)
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
		// Directly set fork on the same route to create a conflict.
		g.routes["a"] = route{static: "b", fork: []string{"b", "c"}}
		_, err := g.Run(context.Background(), State{})
		if !isValidationError(err) {
			t.Fatalf("expected GraphValidationError for conflicting rules, got %v", err)
		}
	})
}

// ── Task 7: execution ─────────────────────────────────────────────────────────

func TestGraphExecution(t *testing.T) {
	t.Run("7.1 linear chain A→B→C", func(t *testing.T) {
		g := mustGraph(t)
		mustAddNode(t, g, "a", setter("a", "done_a"))
		mustAddNode(t, g, "b", setter("b", "done_b"))
		mustAddNode(t, g, "c", setter("c", "done_c"))
		g.SetEntry("a")
		mustAddEdge(t, g, "a", "b")
		mustAddEdge(t, g, "b", "c")

		res, err := g.Run(context.Background(), State{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, key := range []string{"a", "b", "c"} {
			if res.State[key] != "done_"+key {
				t.Errorf("state[%q] = %v, want %q", key, res.State[key], "done_"+key)
			}
		}
	})

	t.Run("7.2 conditional edge selects branch by state", func(t *testing.T) {
		g := mustGraph(t)
		mustAddNode(t, g, "start", noop)
		mustAddNode(t, g, "branch_yes", setter("result", "yes"))
		mustAddNode(t, g, "branch_no", setter("result", "no"))
		g.SetEntry("start")
		if err := g.AddConditionalEdge("start", func(_ context.Context, s State) (string, error) {
			if s["flag"] == true {
				return "branch_yes", nil
			}
			return "branch_no", nil
		}); err != nil {
			t.Fatal(err)
		}

		res, err := g.Run(context.Background(), State{"flag": true})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.State["result"] != "yes" {
			t.Errorf("expected result=yes, got %v", res.State["result"])
		}

		res2, err := g.Run(context.Background(), State{"flag": false})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res2.State["result"] != "no" {
			t.Errorf("expected result=no, got %v", res2.State["result"])
		}
	})

	t.Run("7.3 conditional edge returns empty string terminates", func(t *testing.T) {
		g := mustGraph(t)
		mustAddNode(t, g, "start", setter("visited", "yes"))
		g.SetEntry("start")
		if err := g.AddConditionalEdge("start", func(_ context.Context, _ State) (string, error) {
			return "", nil
		}); err != nil {
			t.Fatal(err)
		}

		res, err := g.Run(context.Background(), State{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.State["visited"] != "yes" {
			t.Errorf("expected visited=yes, got %v", res.State["visited"])
		}
	})

	t.Run("7.4 fork/join both branches run and states merged", func(t *testing.T) {
		g := mustGraph(t)
		mustAddNode(t, g, "start", noop)
		mustAddNode(t, g, "branch_a", setter("a", "done_a"))
		mustAddNode(t, g, "branch_b", setter("b", "done_b"))
		mustAddNode(t, g, "join", func(_ context.Context, s State) (State, error) {
			out := CopyState(s)
			out["merged"] = "yes"
			return out, nil
		})
		g.SetEntry("start")
		if err := g.AddFork("start", []string{"branch_a", "branch_b"}); err != nil {
			t.Fatal(err)
		}
		if err := g.AddJoin("join", []string{"branch_a", "branch_b"}); err != nil {
			t.Fatal(err)
		}

		res, err := g.Run(context.Background(), State{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.State["a"] != "done_a" {
			t.Errorf("expected a=done_a, got %v", res.State["a"])
		}
		if res.State["b"] != "done_b" {
			t.Errorf("expected b=done_b, got %v", res.State["b"])
		}
		if res.State["merged"] != "yes" {
			t.Errorf("expected merged=yes, got %v", res.State["merged"])
		}
	})

	t.Run("7.5 fork error cancels others and is returned", func(t *testing.T) {
		branchErr := fmt.Errorf("branch_bad exploded")
		g := mustGraph(t)
		mustAddNode(t, g, "start", noop)
		mustAddNode(t, g, "branch_ok", func(ctx context.Context, s State) (State, error) {
			// Slow branch — should be cancelled.
			<-ctx.Done()
			return nil, ctx.Err()
		})
		mustAddNode(t, g, "branch_bad", func(_ context.Context, _ State) (State, error) {
			return nil, branchErr
		})
		g.SetEntry("start")
		if err := g.AddFork("start", []string{"branch_ok", "branch_bad"}); err != nil {
			t.Fatal(err)
		}

		_, err := g.Run(context.Background(), State{})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, branchErr) && err.Error() != branchErr.Error() {
			t.Errorf("expected branchErr, got %v", err)
		}
	})

	t.Run("7.6 cyclic graph hits MaxIterations returns GraphIterationError", func(t *testing.T) {
		g := mustGraph(t, WithGraphMaxIterations(3))
		mustAddNode(t, g, "a", noop)
		mustAddNode(t, g, "b", noop)
		g.SetEntry("a")
		mustAddEdge(t, g, "a", "b")
		mustAddEdge(t, g, "b", "a")

		_, err := g.Run(context.Background(), State{})
		var iterErr *GraphIterationError
		if !errors.As(err, &iterErr) {
			t.Fatalf("expected GraphIterationError, got %v", err)
		}
		if iterErr.Limit != 3 {
			t.Errorf("expected Limit=3, got %d", iterErr.Limit)
		}
	})

	t.Run("7.7 context cancellation stops execution", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		g := mustGraph(t)
		mustAddNode(t, g, "a", func(ctx context.Context, s State) (State, error) {
			cancel() // cancel before returning
			return s, nil
		})
		mustAddNode(t, g, "b", noop)
		g.SetEntry("a")
		mustAddEdge(t, g, "a", "b")

		_, err := g.Run(ctx, State{})
		if err == nil {
			t.Fatal("expected context error, got nil")
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	})

	t.Run("7.8 concurrent Run calls do not share state", func(t *testing.T) {
		g := mustGraph(t)
		mustAddNode(t, g, "a", func(_ context.Context, s State) (State, error) {
			out := CopyState(s)
			out["echo"] = s["id"]
			return out, nil
		})
		g.SetEntry("a")

		const n = 10
		results := make([]GraphResult, n)
		errs := make([]error, n)
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				results[idx], errs[idx] = g.Run(context.Background(), State{"id": idx})
			}(i)
		}
		wg.Wait()

		for i := 0; i < n; i++ {
			if errs[i] != nil {
				t.Errorf("goroutine %d: unexpected error: %v", i, errs[i])
				continue
			}
			if results[i].State["echo"] != i {
				t.Errorf("goroutine %d: expected echo=%d, got %v", i, i, results[i].State["echo"])
			}
		}
	})

	t.Run("7.9 state merge does not drop keys from previous nodes", func(t *testing.T) {
		g := mustGraph(t)
		// Node "a" sets key "from_a"; node "b" only sets "from_b" (partial state).
		mustAddNode(t, g, "a", setter("from_a", "a"))
		mustAddNode(t, g, "b", func(_ context.Context, s State) (State, error) {
			// Return only the new key — the engine must merge, not replace.
			return State{"from_b": "b"}, nil
		})
		g.SetEntry("a")
		mustAddEdge(t, g, "a", "b")

		res, err := g.Run(context.Background(), State{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.State["from_a"] != "a" {
			t.Errorf("expected from_a=a, got %v", res.State["from_a"])
		}
		if res.State["from_b"] != "b" {
			t.Errorf("expected from_b=b, got %v", res.State["from_b"])
		}
	})

	t.Run("7.10 no-route terminal ends execution cleanly", func(t *testing.T) {
		g := mustGraph(t)
		mustAddNode(t, g, "only", setter("done", "yes"))
		g.SetEntry("only")
		// No edges added — "only" is a terminal node.

		res, err := g.Run(context.Background(), State{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.State["done"] != "yes" {
			t.Errorf("expected done=yes, got %v", res.State["done"])
		}
	})
}

// ── small helpers to reduce boilerplate ──────────────────────────────────────

func mustAddNode(t *testing.T, g *Graph, name string, fn NodeFunc) {
	t.Helper()
	if err := g.AddNode(name, fn); err != nil {
		t.Fatalf("AddNode(%q): %v", name, err)
	}
}

func mustAddEdge(t *testing.T, g *Graph, from, to string) {
	t.Helper()
	if err := g.AddEdge(from, to); err != nil {
		t.Fatalf("AddEdge(%q→%q): %v", from, to, err)
	}
}
