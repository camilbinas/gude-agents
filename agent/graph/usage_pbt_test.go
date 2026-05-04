package graph

import (
	"context"
	"fmt"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"pgregory.net/rapid"
)

// ─── Test struct for usage accumulation via GraphState embedding ──────────────

// usageTestState embeds GraphState for automatic token usage accumulation.
type usageTestState struct {
	GraphState
	Counter int    `json:"counter"`
	Label   string `json:"label"`
}

// ─── Property 11: Usage accumulation via GraphState embedding ────────────────
//
// Feature: graph-generics-unification, Property 11: Usage accumulation via GraphState embedding
//
// **Validates: Requirements 10.1**
//
// Struct S embedding GraphState, multiple AddUsage calls across nodes;
// final Result.Usage equals sum of all.
// Create a typed graph where each node calls AddUsage with random token counts.
// Verify final Result.Usage equals the sum.

func TestProperty_UsageAccumulationGraphStateEmbedding(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate random usage values for 3 nodes.
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

		// Build a linear graph with numNodes nodes.
		g, err := New[usageTestState]()
		if err != nil {
			rt.Fatal(err)
		}

		for i := range numNodes {
			idx := i // capture for closure
			nodeName := fmt.Sprintf("node_%d", i)
			if err := g.AddNode(nodeName, func(_ context.Context, s usageTestState) (usageTestState, error) {
				s.AddUsage(agent.TokenUsage{
					InputTokens:  usages[idx].input,
					OutputTokens: usages[idx].output,
				})
				s.Counter++
				return s, nil
			}); err != nil {
				rt.Fatal(err)
			}
		}

		// Set entry and chain edges.
		g.SetEntry("node_0")
		for i := 0; i < numNodes-1; i++ {
			from := fmt.Sprintf("node_%d", i)
			to := fmt.Sprintf("node_%d", i+1)
			if err := g.AddEdge(from, to); err != nil {
				rt.Fatal(err)
			}
		}

		// Run the graph.
		initial := usageTestState{Label: "test"}
		res, err := g.Run(context.Background(), initial)
		if err != nil {
			rt.Fatalf("Run failed: %v", err)
		}

		// Verify usage accumulation.
		if res.Usage.InputTokens != expectedInput {
			rt.Fatalf("InputTokens: got %d, want %d", res.Usage.InputTokens, expectedInput)
		}
		if res.Usage.OutputTokens != expectedOutput {
			rt.Fatalf("OutputTokens: got %d, want %d", res.Usage.OutputTokens, expectedOutput)
		}

		// Verify state was also updated correctly.
		if res.State.Counter != numNodes {
			rt.Fatalf("Counter: got %d, want %d", res.State.Counter, numNodes)
		}
	})
}

// ─── Property 12: Usage accumulation via __usage__ key ───────────────────────
//
// Feature: graph-generics-unification, Property 12: Usage accumulation via __usage__ key
//
// **Validates: Requirements 10.2**
//
// State graph where nodes write TokenUsage to `__usage__`; final Result.Usage
// equals sum, key absent from final state.
// Create a map state graph where each node writes agent.TokenUsage to "__usage__".
// Verify final Result.Usage equals sum and key is absent from final state.

func TestProperty_UsageAccumulationUsageKey(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate random usage values for multiple nodes.
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

		// Build a linear graph with map state.
		g, err := New[State]()
		if err != nil {
			rt.Fatal(err)
		}

		for i := range numNodes {
			idx := i // capture for closure
			nodeName := fmt.Sprintf("node_%d", i)
			if err := g.AddNode(nodeName, func(_ context.Context, s State) (State, error) {
				out := CopyState(s)
				out["__usage__"] = agent.TokenUsage{
					InputTokens:  usages[idx].input,
					OutputTokens: usages[idx].output,
				}
				out[fmt.Sprintf("visited_%d", idx)] = true
				return out, nil
			}); err != nil {
				rt.Fatal(err)
			}
		}

		// Set entry and chain edges.
		g.SetEntry("node_0")
		for i := 0; i < numNodes-1; i++ {
			from := fmt.Sprintf("node_%d", i)
			to := fmt.Sprintf("node_%d", i+1)
			if err := g.AddEdge(from, to); err != nil {
				rt.Fatal(err)
			}
		}

		// Run the graph.
		initial := State{"start": true}
		res, err := g.Run(context.Background(), initial)
		if err != nil {
			rt.Fatalf("Run failed: %v", err)
		}

		// Verify usage accumulation.
		if res.Usage.InputTokens != expectedInput {
			rt.Fatalf("InputTokens: got %d, want %d", res.Usage.InputTokens, expectedInput)
		}
		if res.Usage.OutputTokens != expectedOutput {
			rt.Fatalf("OutputTokens: got %d, want %d", res.Usage.OutputTokens, expectedOutput)
		}

		// Verify __usage__ key is absent from final state.
		if _, exists := res.State["__usage__"]; exists {
			rt.Fatalf("__usage__ key should be absent from final state, but it exists: %v", res.State["__usage__"])
		}

		// Verify other state keys are present.
		for i := range numNodes {
			key := fmt.Sprintf("visited_%d", i)
			if res.State[key] != true {
				rt.Fatalf("expected %s=true in final state, got %v", key, res.State[key])
			}
		}
	})
}
