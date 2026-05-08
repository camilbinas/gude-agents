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

func TestProperty_UsageAccumulationGraphStateEmbedding(t *testing.T) {
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
			if _, err := g.Node(nodeName, func(_ context.Context, s usageTestState) (usageTestState, error) {
				s.AddUsage(agent.TokenUsage{
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

// ─── Property 12: Usage accumulation via __usage__ key ───────────────────────

func TestProperty_UsageAccumulationUsageKey(t *testing.T) {
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
			if _, err := g.Node(nodeName, func(_ context.Context, s State) (State, error) {
				out := CopyState(s)
				out["__usage__"] = agent.TokenUsage{
					InputTokens:  usages[idx].input,
					OutputTokens: usages[idx].output,
				}
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
			rt.Fatalf("__usage__ key should be absent from final state, but it exists: %v", res.State["__usage__"])
		}

		for i := range numNodes {
			key := fmt.Sprintf("visited_%d", i)
			if res.State[key] != true {
				rt.Fatalf("expected %s=true in final state, got %v", key, res.State[key])
			}
		}
	})
}
