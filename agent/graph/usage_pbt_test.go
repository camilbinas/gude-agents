package graph

import (
	"context"
	"fmt"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"pgregory.net/rapid"
)

// ─── Test struct for usage accumulation via a custom state type ──────────────

// usageTestState is a plain custom state type — no embedding required. Usage is
// reported via graph.AddUsage(ctx, ...) rather than a state method.
type usageTestState struct {
	Counter int    `json:"counter"`
	Label   string `json:"label"`
}

// ─── Property 11: Usage accumulation via AddUsage in a typed-state graph ─────

func TestProperty_UsageAccumulationTypedState(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		numNodes := rapid.IntRange(2, 5).Draw(rt, "numNodes")

		type nodeUsage struct {
			input  int
			output int
		}
		usages := make([]nodeUsage, numNodes)
		var expectedInput, expectedOutput int
		for i := range numNodes {
			usages[i] = nodeUsage{
				input:  rapid.IntRange(1, 500).Draw(rt, fmt.Sprintf("input_%d", i)),
				output: rapid.IntRange(1, 500).Draw(rt, fmt.Sprintf("output_%d", i)),
			}
			expectedInput += usages[i].input
			expectedOutput += usages[i].output
		}

		g, err := New[usageTestState]()
		if err != nil {
			rt.Fatal(err)
		}

		for i := range numNodes {
			idx := i
			nodeName := fmt.Sprintf("node_%d", i)
			var inputKeys []string
			if i > 0 {
				inputKeys = []string{fmt.Sprintf("node_%d_out", i-1)}
			}
			if _, err := g.Node(nodeName, func(ctx context.Context, s usageTestState) (usageTestState, error) {
				AddUsage(ctx, agent.TokenUsage{
					InputTokens:  usages[idx].input,
					OutputTokens: usages[idx].output,
				})
				s.Counter++
				return s, nil
			}, In(inputKeys...), Out(nodeName+"_out")); err != nil {
				rt.Fatal(err)
			}
		}

		g.Start("node_0")

		initial := usageTestState{Label: "test"}
		res, err := g.Run(context.Background(), initial)
		if err != nil {
			rt.Fatalf("Run failed: %v", err)
		}

		if res.Usage.InputTokens != expectedInput {
			rt.Fatalf("InputTokens: got %d, want %d", res.Usage.InputTokens, expectedInput)
		}
		if res.Usage.OutputTokens != expectedOutput {
			rt.Fatalf("OutputTokens: got %d, want %d", res.Usage.OutputTokens, expectedOutput)
		}

		if res.State.Counter != numNodes {
			rt.Fatalf("Counter: got %d, want %d", res.State.Counter, numNodes)
		}
	})
}

// ─── Property 12: Usage accumulation via AddUsage in a map-state graph ───────

func TestProperty_UsageAccumulationMapState(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		numNodes := rapid.IntRange(2, 5).Draw(rt, "numNodes")

		type nodeUsage struct {
			input  int
			output int
		}
		usages := make([]nodeUsage, numNodes)
		var expectedInput, expectedOutput int
		for i := range numNodes {
			usages[i] = nodeUsage{
				input:  rapid.IntRange(1, 500).Draw(rt, fmt.Sprintf("input_%d", i)),
				output: rapid.IntRange(1, 500).Draw(rt, fmt.Sprintf("output_%d", i)),
			}
			expectedInput += usages[i].input
			expectedOutput += usages[i].output
		}

		g, err := New[State]()
		if err != nil {
			rt.Fatal(err)
		}

		for i := range numNodes {
			idx := i
			nodeName := fmt.Sprintf("node_%d", i)
			var inputKeys []string
			if i > 0 {
				inputKeys = []string{fmt.Sprintf("node_%d_out", i-1)}
			}
			if _, err := g.Node(nodeName, func(ctx context.Context, s State) (State, error) {
				out := CopyState(s)
				AddUsage(ctx, agent.TokenUsage{
					InputTokens:  usages[idx].input,
					OutputTokens: usages[idx].output,
				})
				out[fmt.Sprintf("visited_%d", idx)] = true
				out[nodeName+"_out"] = "done"
				return out, nil
			}, In(inputKeys...), Out(nodeName+"_out")); err != nil {
				rt.Fatal(err)
			}
		}

		g.Start("node_0")

		initial := State{"start": true}
		res, err := g.Run(context.Background(), initial)
		if err != nil {
			rt.Fatalf("Run failed: %v", err)
		}

		if res.Usage.InputTokens != expectedInput {
			rt.Fatalf("InputTokens: got %d, want %d", res.Usage.InputTokens, expectedInput)
		}
		if res.Usage.OutputTokens != expectedOutput {
			rt.Fatalf("OutputTokens: got %d, want %d", res.Usage.OutputTokens, expectedOutput)
		}

		if _, exists := res.State["__usage__"]; exists {
			rt.Fatalf("__usage__ key should never appear in state (usage is context-based now): %v", res.State["__usage__"])
		}

		for i := range numNodes {
			key := fmt.Sprintf("visited_%d", i)
			if res.State[key] != true {
				rt.Fatalf("expected %s=true in final state, got %v", key, res.State[key])
			}
		}
	})
}

// ─── AddUsage threading guarantees ───────────────────────────────────────────

// TestAddUsage_AccumulatesAcrossMultipleCallsInOneNode verifies that multiple
// AddUsage calls within a single node sum correctly into the graph total.
func TestAddUsage_AccumulatesAcrossMultipleCallsInOneNode(t *testing.T) {
	g, err := New[State]()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := g.Node("n", func(ctx context.Context, s State) (State, error) {
		AddUsage(ctx, agent.TokenUsage{InputTokens: 5, OutputTokens: 3})
		AddUsage(ctx, agent.TokenUsage{InputTokens: 2, OutputTokens: 1, CacheReadTokens: 4})
		out := CopyState(s)
		out["done"] = true
		return out, nil
	}, In(), Out("done")); err != nil {
		t.Fatal(err)
	}
	g.Start("n")

	res, err := g.Run(context.Background(), State{})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if res.Usage.InputTokens != 7 || res.Usage.OutputTokens != 4 || res.Usage.CacheReadTokens != 4 {
		t.Fatalf("expected 7in/4out/4cacheRead, got %din/%dout/%dcacheRead",
			res.Usage.InputTokens, res.Usage.OutputTokens, res.Usage.CacheReadTokens)
	}
}

// TestAddUsage_OutsideNodeIsNoop verifies AddUsage on a context with no
// collector (i.e. not inside a graph node) is a safe no-op and does not panic.
func TestAddUsage_OutsideNodeIsNoop(t *testing.T) {
	// Must not panic.
	AddUsage(context.Background(), agent.TokenUsage{InputTokens: 100})
}

// TestAddUsage_IsolatedPerNode verifies that the collector is per-node: usage
// reported in one node does not bleed into another node's collector, while the
// graph total still sums both.
func TestAddUsage_IsolatedPerNode(t *testing.T) {
	g, err := New[State]()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := g.Node("a", func(ctx context.Context, s State) (State, error) {
		AddUsage(ctx, agent.TokenUsage{InputTokens: 10})
		out := CopyState(s)
		out["a_out"] = true
		return out, nil
	}, In(), Out("a_out")); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Node("b", func(ctx context.Context, s State) (State, error) {
		AddUsage(ctx, agent.TokenUsage{InputTokens: 20})
		out := CopyState(s)
		out["b_out"] = true
		return out, nil
	}, In("a_out"), Out("b_out")); err != nil {
		t.Fatal(err)
	}
	g.Start("a")

	res, err := g.Run(context.Background(), State{})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if res.Usage.InputTokens != 30 {
		t.Fatalf("expected total 30 input tokens, got %d", res.Usage.InputTokens)
	}
}
