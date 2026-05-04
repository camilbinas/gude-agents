package graph

import (
	"context"
	"fmt"
	"testing"

	"pgregory.net/rapid"
)

// Feature: graph-checkpointing, Property 21: Typed State Round-Trip Through Checkpoint
//
// **Validates: Requirements 13.5, 13.6**
//
// For any valid typed state S, saving a checkpoint (S → JSON → State →
// checkpoint) and loading it back (checkpoint → State → JSON → S) SHALL produce
// an equivalent typed state.

// testTypedState is a typed state struct for property testing.
type testTypedState struct {
	Name   string   `json:"name"`
	Count  int      `json:"count"`
	Score  float64  `json:"score"`
	Active bool     `json:"active"`
	Tags   []string `json:"tags"`
}

func TestProperty_TypedStateRoundTripThroughCheckpoint(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		cp := &mockCheckpointer{}

		tg, err := New[testTypedState](WithCheckpointer(cp))
		if err != nil {
			rt.Fatal(err)
		}

		// Generate arbitrary typed state.
		numTags := rapid.IntRange(0, 5).Draw(rt, "numTags")
		tags := make([]string, numTags)
		for i := range tags {
			tags[i] = rapid.StringMatching(`[a-z]{2,8}`).Draw(rt, fmt.Sprintf("tag%d", i))
		}

		initialState := testTypedState{
			Name:   rapid.StringMatching(`[a-zA-Z]{2,12}`).Draw(rt, "name"),
			Count:  rapid.IntRange(0, 1000).Draw(rt, "count"),
			Score:  float64(rapid.IntRange(0, 10000).Draw(rt, "scoreInt")) / 100.0,
			Active: rapid.Bool().Draw(rt, "active"),
			Tags:   tags,
		}

		// Add a simple node that passes state through.
		if err := tg.AddNode("passthrough", func(_ context.Context, s testTypedState) (testTypedState, error) {
			return s, nil
		}); err != nil {
			rt.Fatal(err)
		}
		tg.SetEntry("passthrough")

		threadID := rapid.StringMatching(`thread-[a-z0-9]{3,8}`).Draw(rt, "threadID")

		// Step to create a checkpoint with the typed state.
		result, err := tg.Step(context.Background(), initialState, threadID)
		if err != nil {
			rt.Fatalf("Step failed: %v", err)
		}

		// Verify the round-tripped state matches the original.
		if result.State.Name != initialState.Name {
			rt.Fatalf("Name mismatch: got %q, want %q", result.State.Name, initialState.Name)
		}
		if result.State.Count != initialState.Count {
			rt.Fatalf("Count mismatch: got %d, want %d", result.State.Count, initialState.Count)
		}
		if result.State.Score != initialState.Score {
			rt.Fatalf("Score mismatch: got %f, want %f", result.State.Score, initialState.Score)
		}
		if result.State.Active != initialState.Active {
			rt.Fatalf("Active mismatch: got %v, want %v", result.State.Active, initialState.Active)
		}
		if len(result.State.Tags) != len(initialState.Tags) {
			rt.Fatalf("Tags length mismatch: got %d, want %d", len(result.State.Tags), len(initialState.Tags))
		}
		for i, tag := range initialState.Tags {
			if result.State.Tags[i] != tag {
				rt.Fatalf("Tags[%d] mismatch: got %q, want %q", i, result.State.Tags[i], tag)
			}
		}

		// Also verify by loading the checkpoint and converting back.
		loaded, err := cp.LoadAt(context.Background(), threadID, result.Version)
		if err != nil {
			rt.Fatalf("LoadAt failed: %v", err)
		}

		// Convert loaded state back to typed.
		restored, err := stateToTyped[testTypedState](loaded.State)
		if err != nil {
			rt.Fatalf("stateToTyped failed: %v", err)
		}

		if restored.Name != initialState.Name {
			rt.Fatalf("restored Name mismatch: got %q, want %q", restored.Name, initialState.Name)
		}
		if restored.Count != initialState.Count {
			rt.Fatalf("restored Count mismatch: got %d, want %d", restored.Count, initialState.Count)
		}
		if restored.Active != initialState.Active {
			rt.Fatalf("restored Active mismatch: got %v, want %v", restored.Active, initialState.Active)
		}
	})
}
