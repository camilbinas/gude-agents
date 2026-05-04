package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"pgregory.net/rapid"
)

// ─── Test struct for event snapshot property tests ───────────────────────────

// snapshotTestState is a struct used for testing event snapshot deserialization.
type snapshotTestState struct {
	Name   string   `json:"name"`
	Value  int      `json:"value"`
	Active bool     `json:"active"`
	Items  []string `json:"items"`
}

// snapshotRecordingHook records all events emitted during graph execution.
type snapshotRecordingHook struct {
	events []GraphEvent
}

func (h *snapshotRecordingHook) OnEvent(event GraphEvent) {
	h.events = append(h.events, event)
}

// ─── Property 13: Event snapshots are valid map representations ──────────────
//
// Feature: graph-generics-unification, Property 13: Event snapshots are valid map representations
//
// **Validates: Requirements 11.2, 11.3**
//
// For struct S graph with event hook, every StateSnapshot deserializes back to S
// equal to state at that point.
// Create a typed graph with an event hook that records all events. After execution,
// verify each StateSnapshot can be deserialized back to the struct type.

func TestProperty_EventSnapshotsAreValidMapRepresentations(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate initial state.
		numItems := rapid.IntRange(0, 4).Draw(rt, "numItems")
		items := make([]string, numItems)
		for i := range numItems {
			items[i] = rapid.StringMatching(`[a-z]{2,6}`).Draw(rt, fmt.Sprintf("item_%d", i))
		}

		initial := snapshotTestState{
			Name:   rapid.StringMatching(`[a-zA-Z]{2,10}`).Draw(rt, "name"),
			Value:  rapid.IntRange(0, 1000).Draw(rt, "value"),
			Active: rapid.Bool().Draw(rt, "active"),
			Items:  items,
		}

		// Generate modifications for nodes.
		newName := rapid.StringMatching(`[a-zA-Z]{2,10}`).Draw(rt, "newName")
		newValue := rapid.IntRange(1001, 9999).Draw(rt, "newValue")

		hook := &snapshotRecordingHook{}

		g, err := New[snapshotTestState](WithEventHook(hook))
		if err != nil {
			rt.Fatal(err)
		}

		// Node A: modifies Name
		if err := g.AddNode("nodeA", func(_ context.Context, s snapshotTestState) (snapshotTestState, error) {
			s.Name = newName
			return s, nil
		}); err != nil {
			rt.Fatal(err)
		}

		// Node B: modifies Value
		if err := g.AddNode("nodeB", func(_ context.Context, s snapshotTestState) (snapshotTestState, error) {
			s.Value = newValue
			return s, nil
		}); err != nil {
			rt.Fatal(err)
		}

		g.SetEntry("nodeA")
		if err := g.AddEdge("nodeA", "nodeB"); err != nil {
			rt.Fatal(err)
		}

		// Run the graph.
		_, err = g.Run(context.Background(), initial)
		if err != nil {
			rt.Fatalf("Run failed: %v", err)
		}

		// Verify each event with a non-nil StateSnapshot can be deserialized back.
		if len(hook.events) == 0 {
			rt.Fatal("expected at least one event, got none")
		}

		for i, ev := range hook.events {
			if ev.StateSnapshot == nil {
				continue
			}

			// Deserialize the StateSnapshot back to snapshotTestState via JSON.
			b, err := json.Marshal(ev.StateSnapshot)
			if err != nil {
				rt.Fatalf("event %d (%s): failed to marshal StateSnapshot: %v", i, ev.Type, err)
			}

			var deserialized snapshotTestState
			if err := json.Unmarshal(b, &deserialized); err != nil {
				rt.Fatalf("event %d (%s): failed to unmarshal StateSnapshot to struct: %v", i, ev.Type, err)
			}

			// Verify the deserialized struct has valid field types (non-zero-value check
			// is not appropriate since fields can legitimately be zero). Instead, verify
			// that the round-trip produces a consistent result: re-serializing the
			// deserialized struct should produce the same map.
			reMap, err := typedToState(deserialized)
			if err != nil {
				rt.Fatalf("event %d (%s): failed to re-serialize deserialized struct: %v", i, ev.Type, err)
			}

			// Compare the re-serialized map with the original snapshot.
			// We need to handle JSON number normalization (json.Unmarshal produces float64 for numbers).
			originalJSON, _ := json.Marshal(ev.StateSnapshot)
			reJSON, _ := json.Marshal(reMap)
			if string(originalJSON) != string(reJSON) {
				rt.Fatalf("event %d (%s): snapshot round-trip mismatch:\n  original: %s\n  re-serialized: %s",
					i, ev.Type, string(originalJSON), string(reJSON))
			}
		}

		// Additionally verify specific events have correct state at that point.
		// Find NodeCompleted for nodeA — state should have newName but original Value.
		for _, ev := range hook.events {
			if ev.Type == EventNodeCompleted && ev.NodeName == "nodeA" {
				b, _ := json.Marshal(ev.StateSnapshot)
				var s snapshotTestState
				if err := json.Unmarshal(b, &s); err != nil {
					rt.Fatalf("failed to unmarshal nodeA completed snapshot: %v", err)
				}
				if s.Name != newName {
					rt.Fatalf("nodeA completed: expected Name=%q, got %q", newName, s.Name)
				}
				if s.Value != initial.Value {
					rt.Fatalf("nodeA completed: expected Value=%d (unchanged), got %d", initial.Value, s.Value)
				}
			}
		}

		// Find NodeCompleted for nodeB — state should have newName AND newValue.
		for _, ev := range hook.events {
			if ev.Type == EventNodeCompleted && ev.NodeName == "nodeB" {
				b, _ := json.Marshal(ev.StateSnapshot)
				var s snapshotTestState
				if err := json.Unmarshal(b, &s); err != nil {
					rt.Fatalf("failed to unmarshal nodeB completed snapshot: %v", err)
				}
				if s.Name != newName {
					rt.Fatalf("nodeB completed: expected Name=%q, got %q", newName, s.Name)
				}
				if s.Value != newValue {
					rt.Fatalf("nodeB completed: expected Value=%d, got %d", newValue, s.Value)
				}
				if s.Active != initial.Active {
					rt.Fatalf("nodeB completed: expected Active=%v (unchanged), got %v", initial.Active, s.Active)
				}
				if !reflect.DeepEqual(s.Items, initial.Items) {
					rt.Fatalf("nodeB completed: expected Items=%v (unchanged), got %v", initial.Items, s.Items)
				}
			}
		}
	})
}
