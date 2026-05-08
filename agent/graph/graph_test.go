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

func setter(key, val string) NodeFunc[State] {
	return func(_ context.Context, s State) (State, error) {
		out := CopyState(s)
		out[key] = val
		return out, nil
	}
}

func mustGraph(t *testing.T, opts ...GraphOption) *Graph[State] {
	t.Helper()
	g, err := New[State](opts...)
	if err != nil {
		t.Fatalf("New[State]: %v", err)
	}
	return g
}

func isValidationError(err error) bool {
	var ve *GraphValidationError
	return errors.As(err, &ve)
}

func mustAddNodeWithKeys(t *testing.T, g *Graph[State], name string, fn NodeFunc[State], outputKeys []string, inputKeys []string) {
	t.Helper()
	if _, err := g.Node(name, fn, In(inputKeys...), Out(outputKeys...)); err != nil {
		t.Fatalf("Node(%s): %v", name, err)
	}
}

func TestGraphBuilder(t *testing.T) {
	t.Run("6.1 Node rejects empty name", func(t *testing.T) {
		g := mustGraph(t)
		if _, err := g.Node("", noop, In(), Out("out")); !isValidationError(err) {
			t.Fatalf("expected GraphValidationError, got %v", err)
		}
	})

	t.Run("6.1 Node rejects nil fn", func(t *testing.T) {
		g := mustGraph(t)
		if _, err := g.Node("a", nil, In(), Out("out")); !isValidationError(err) {
			t.Fatalf("expected GraphValidationError, got %v", err)
		}
	})

	t.Run("6.1 Node rejects duplicate name", func(t *testing.T) {
		g := mustGraph(t)
		if _, err := g.Node("a", noop, In(), Out("a_out")); err != nil {
			t.Fatalf("first Node: %v", err)
		}
		if _, err := g.Node("a", noop, In(), Out("a_out2")); !isValidationError(err) {
			t.Fatalf("expected GraphValidationError on duplicate, got %v", err)
		}
	})

	t.Run("6.5 WithMaxIterations rejects value < 1", func(t *testing.T) {
		_, err := New[State](WithMaxIterations(0))
		if !isValidationError(err) {
			t.Fatalf("expected GraphValidationError for 0, got %v", err)
		}
		_, err = New[State](WithMaxIterations(-5))
		if !isValidationError(err) {
			t.Fatalf("expected GraphValidationError for -5, got %v", err)
		}
	})
}

func TestGraphValidation(t *testing.T) {
	t.Run("6.6 validate: entry node missing", func(t *testing.T) {
		g := mustGraph(t)
		mustAddNodeWithKeys(t, g, "a", noop, []string{"a_out"}, []string{})
		g.Start("missing")
		_, err := g.Run(context.Background(), State{})
		if !isValidationError(err) {
			t.Fatalf("expected GraphValidationError, got %v", err)
		}
	})
}

func TestGraphExecution(t *testing.T) {
	t.Run("7.1 linear chain A→B→C", func(t *testing.T) {
		g := mustGraph(t)
		mustAddNodeWithKeys(t, g, "a", setter("a", "done_a"), []string{"a"}, []string{})
		mustAddNodeWithKeys(t, g, "b", setter("b", "done_b"), []string{"b"}, []string{"a"})
		mustAddNodeWithKeys(t, g, "c", setter("c", "done_c"), []string{"c"}, []string{"b"})
		g.Start("a")

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

	t.Run("7.2 conditional gating: node only runs when key is written", func(t *testing.T) {
		// Entry conditionally writes "gate_key". branch_yes depends on "gate_key".
		// When entry writes it, branch_yes runs. When it doesn't, branch_yes is gated.
		g := mustGraph(t)
		mustAddNodeWithKeys(t, g, "start", func(_ context.Context, s State) (State, error) {
			out := CopyState(s)
			out["trigger"] = "done"
			// Conditionally write gate_key based on flag
			if s["flag"] == true {
				out["gate_key"] = "open"
			}
			return out, nil
		}, []string{"trigger", "gate_key"}, []string{})
		mustAddNodeWithKeys(t, g, "branch_yes", setter("result", "yes"), []string{"result"}, []string{"gate_key"})
		g.Start("start")

		// With flag=true, gate_key is written, so branch_yes runs
		res, err := g.Run(context.Background(), State{"flag": true})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.State["result"] != "yes" {
			t.Errorf("expected result=yes, got %v", res.State["result"])
		}

		// With flag=false, gate_key is NOT written, so branch_yes is gated
		res2, err := g.Run(context.Background(), State{"flag": false})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, exists := res2.State["result"]; exists {
			t.Errorf("branch_yes should not have run when gate_key not written, but result exists: %v", res2.State["result"])
		}
	})

	t.Run("7.3 terminal node ends execution cleanly", func(t *testing.T) {
		g := mustGraph(t)
		mustAddNodeWithKeys(t, g, "start", setter("visited", "yes"), []string{"visited"}, []string{})
		g.Start("start")

		res, err := g.Run(context.Background(), State{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.State["visited"] != "yes" {
			t.Errorf("expected visited=yes, got %v", res.State["visited"])
		}
	})

	t.Run("7.4 diamond: concurrent branches merge", func(t *testing.T) {
		g := mustGraph(t)
		mustAddNodeWithKeys(t, g, "start", func(_ context.Context, s State) (State, error) {
			out := CopyState(s)
			out["trigger"] = "done"
			return out, nil
		}, []string{"trigger"}, []string{})
		mustAddNodeWithKeys(t, g, "branch_a", setter("a", "done_a"), []string{"a"}, []string{"trigger"})
		mustAddNodeWithKeys(t, g, "branch_b", setter("b", "done_b"), []string{"b"}, []string{"trigger"})
		mustAddNodeWithKeys(t, g, "join", func(_ context.Context, s State) (State, error) {
			out := CopyState(s)
			out["merged"] = "yes"
			return out, nil
		}, []string{"merged"}, []string{"a", "b"})
		g.Start("start")

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

	t.Run("7.5 concurrent branch error cancels others", func(t *testing.T) {
		branchErr := fmt.Errorf("branch_bad exploded")
		g := mustGraph(t)
		mustAddNodeWithKeys(t, g, "start", func(_ context.Context, s State) (State, error) {
			out := CopyState(s)
			out["trigger"] = "done"
			return out, nil
		}, []string{"trigger"}, []string{})
		mustAddNodeWithKeys(t, g, "branch_ok", func(ctx context.Context, s State) (State, error) {
			// Slow branch — should be cancelled.
			<-ctx.Done()
			return nil, ctx.Err()
		}, []string{"ok_out"}, []string{"trigger"})
		mustAddNodeWithKeys(t, g, "branch_bad", func(_ context.Context, _ State) (State, error) {
			return nil, branchErr
		}, []string{"bad_out"}, []string{"trigger"})
		g.Start("start")

		_, err := g.Run(context.Background(), State{})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, branchErr) && err.Error() != branchErr.Error() {
			t.Errorf("expected branchErr, got %v", err)
		}
	})

	t.Run("7.6 graph hits MaxIterations returns GraphIterationError", func(t *testing.T) {
		// MaxIterations limits total node executions. With 3 nodes and limit 2,
		// the third node triggers the error.
		g := mustGraph(t, WithMaxIterations(2))
		mustAddNodeWithKeys(t, g, "a", func(_ context.Context, s State) (State, error) {
			out := CopyState(s)
			out["a_out"] = "done"
			return out, nil
		}, []string{"a_out"}, []string{})
		mustAddNodeWithKeys(t, g, "b", func(_ context.Context, s State) (State, error) {
			out := CopyState(s)
			out["b_out"] = "done"
			return out, nil
		}, []string{"b_out"}, []string{"a_out"})
		mustAddNodeWithKeys(t, g, "c", func(_ context.Context, s State) (State, error) {
			out := CopyState(s)
			out["c_out"] = "done"
			return out, nil
		}, []string{"c_out"}, []string{"b_out"})
		g.Start("a")

		_, err := g.Run(context.Background(), State{})
		var iterErr *GraphIterationError
		if !errors.As(err, &iterErr) {
			t.Fatalf("expected GraphIterationError, got %v", err)
		}
		if iterErr.Limit != 2 {
			t.Errorf("expected Limit=2, got %d", iterErr.Limit)
		}
	})

	t.Run("7.7 context cancellation stops execution", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		g := mustGraph(t)
		mustAddNodeWithKeys(t, g, "a", func(ctx context.Context, s State) (State, error) {
			cancel() // cancel before returning
			out := CopyState(s)
			out["a_out"] = "done"
			return out, nil
		}, []string{"a_out"}, []string{})
		mustAddNodeWithKeys(t, g, "b", noop, []string{"b_out"}, []string{"a_out"})
		g.Start("a")

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
		mustAddNodeWithKeys(t, g, "a", func(_ context.Context, s State) (State, error) {
			out := CopyState(s)
			out["echo"] = s["id"]
			return out, nil
		}, []string{"echo"}, []string{})
		g.Start("a")

		const n = 10
		results := make([]Result[State], n)
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
		mustAddNodeWithKeys(t, g, "a", setter("from_a", "a"), []string{"from_a"}, []string{})
		mustAddNodeWithKeys(t, g, "b", func(_ context.Context, s State) (State, error) {
			// Return only the new key — the engine must merge, not replace.
			return State{"from_b": "b"}, nil
		}, []string{"from_b"}, []string{"from_a"})
		g.Start("a")

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
		mustAddNodeWithKeys(t, g, "only", setter("done", "yes"), []string{"done"}, []string{})
		g.Start("only")
		// No downstream nodes — "only" is a terminal node.

		res, err := g.Run(context.Background(), State{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.State["done"] != "yes" {
			t.Errorf("expected done=yes, got %v", res.State["done"])
		}
	})
}
