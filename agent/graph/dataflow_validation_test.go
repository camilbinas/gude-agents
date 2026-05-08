package graph

import (
	"context"
	"strings"
	"testing"
)

func TestValidateDataFlow_CycleDetection(t *testing.T) {
	t.Run("detects simple two-node cycle", func(t *testing.T) {
		g := mustGraph(t)
		mustAddNodeWithKeys(t, g, "a", noop, []string{"x"}, []string{"y"})
		mustAddNodeWithKeys(t, g, "b", noop, []string{"y"}, []string{"x"})
		g.Start("a")

		_, err := g.Run(context.Background(), State{})
		if err == nil {
			t.Fatal("expected error for cycle, got nil")
		}
		if !strings.Contains(err.Error(), "data-flow cycle detected") {
			t.Fatalf("expected cycle error, got: %v", err)
		}
		if !strings.Contains(err.Error(), "b") {
			t.Fatalf("expected error to mention node 'b', got: %v", err)
		}
	})

	t.Run("detects three-node cycle", func(t *testing.T) {
		g := mustGraph(t)
		mustAddNodeWithKeys(t, g, "entry", noop, []string{"e_out"}, []string{})
		mustAddNodeWithKeys(t, g, "a", noop, []string{"a_out"}, []string{"c_out"})
		mustAddNodeWithKeys(t, g, "b", noop, []string{"b_out"}, []string{"a_out"})
		mustAddNodeWithKeys(t, g, "c", noop, []string{"c_out"}, []string{"b_out"})
		g.Start("entry")

		_, err := g.Run(context.Background(), State{})
		if err == nil {
			t.Fatal("expected error for cycle, got nil")
		}
		if !strings.Contains(err.Error(), "data-flow cycle detected") {
			t.Fatalf("expected cycle error, got: %v", err)
		}
	})

	t.Run("valid DAG passes", func(t *testing.T) {
		g := mustGraph(t)
		mustAddNodeWithKeys(t, g, "entry", noop, []string{"a_out"}, []string{})
		mustAddNodeWithKeys(t, g, "b", noop, []string{"b_out"}, []string{"a_out"})
		mustAddNodeWithKeys(t, g, "c", noop, []string{"c_out"}, []string{"b_out"})
		g.Start("entry")

		_, err := g.Run(context.Background(), State{})
		// No validation error expected (execution may not complete all nodes
		// since the scheduler isn't implemented yet, but validation should pass)
		if err != nil {
			if isValidationError(err) {
				t.Fatalf("unexpected validation error: %v", err)
			}
		}
	})
}

func TestValidateDataFlow_InputKeySatisfiability(t *testing.T) {
	t.Run("unsatisfiable input key returns error", func(t *testing.T) {
		g := mustGraph(t)
		mustAddNodeWithKeys(t, g, "entry", noop, []string{"out"}, []string{})
		mustAddNodeWithKeys(t, g, "consumer", noop, []string{"result"}, []string{"missing_key"})
		g.Start("entry")

		_, err := g.Run(context.Background(), State{})
		if err == nil {
			t.Fatal("expected error for unsatisfiable input, got nil")
		}
		if !strings.Contains(err.Error(), "consumer") {
			t.Fatalf("expected error to mention node 'consumer', got: %v", err)
		}
		if !strings.Contains(err.Error(), "missing_key") {
			t.Fatalf("expected error to mention key 'missing_key', got: %v", err)
		}
	})

	t.Run("input key satisfied by initial state passes", func(t *testing.T) {
		g := mustGraph(t)
		mustAddNodeWithKeys(t, g, "entry", noop, []string{"a_out"}, []string{})
		mustAddNodeWithKeys(t, g, "b", noop, []string{"b_out"}, []string{"initial_key"})
		g.Start("entry")

		_, err := g.Run(context.Background(), State{"initial_key": "value"})
		if err != nil {
			if isValidationError(err) {
				t.Fatalf("unexpected validation error: %v", err)
			}
		}
	})

	t.Run("input key satisfied by another node's output passes", func(t *testing.T) {
		g := mustGraph(t)
		mustAddNodeWithKeys(t, g, "entry", noop, []string{"a_out"}, []string{})
		mustAddNodeWithKeys(t, g, "b", noop, []string{"b_out"}, []string{"a_out"})
		g.Start("entry")

		_, err := g.Run(context.Background(), State{})
		if err != nil {
			if isValidationError(err) {
				t.Fatalf("unexpected validation error: %v", err)
			}
		}
	})

	t.Run("entry node input keys are not validated", func(t *testing.T) {
		g := mustGraph(t)
		// Entry node has input keys that aren't satisfied — should still pass
		mustAddNodeWithKeys(t, g, "entry", noop, []string{"a_out"}, []string{"nonexistent"})
		g.Start("entry")

		_, err := g.Run(context.Background(), State{})
		if err != nil {
			if isValidationError(err) {
				t.Fatalf("entry node input keys should not be validated, got: %v", err)
			}
		}
	})
}

func TestValidateDataFlow_OutputKeyUniqueness(t *testing.T) {
	t.Run("duplicate output key returns error", func(t *testing.T) {
		g := mustGraph(t)
		mustAddNodeWithKeys(t, g, "entry", noop, []string{"shared"}, []string{})
		mustAddNodeWithKeys(t, g, "other", noop, []string{"shared"}, []string{})
		g.Start("entry")

		_, err := g.Run(context.Background(), State{})
		if err == nil {
			t.Fatal("expected error for duplicate output key, got nil")
		}
		if !strings.Contains(err.Error(), "both declare output key") {
			t.Fatalf("expected duplicate output error, got: %v", err)
		}
		if !strings.Contains(err.Error(), "shared") {
			t.Fatalf("expected error to mention key 'shared', got: %v", err)
		}
	})

	t.Run("distinct output keys pass", func(t *testing.T) {
		g := mustGraph(t)
		mustAddNodeWithKeys(t, g, "entry", noop, []string{"out_a"}, []string{})
		mustAddNodeWithKeys(t, g, "other", noop, []string{"out_b"}, []string{"out_a"})
		g.Start("entry")

		_, err := g.Run(context.Background(), State{})
		if err != nil {
			if isValidationError(err) {
				t.Fatalf("unexpected validation error: %v", err)
			}
		}
	})
}
