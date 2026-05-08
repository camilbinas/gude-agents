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
		if _, err := g.Node("nodeA", func(_ context.Context, s snapshotTestState) (snapshotTestState, error) {
			s.Name = newName
			return s, nil
		}, In(), Out("nodeA_out")); err != nil {
			rt.Fatal(err)
		}

		// Node B: modifies Value
		if _, err := g.Node("nodeB", func(_ context.Context, s snapshotTestState) (snapshotTestState, error) {
			s.Value = newValue
			return s, nil
		}, In("nodeA_out"), Out("nodeB_out")); err != nil {
			rt.Fatal(err)
		}

		g.Start("nodeA")

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

			reMap, err := typedToState(deserialized)
			if err != nil {
				rt.Fatalf("event %d (%s): failed to re-serialize deserialized struct: %v", i, ev.Type, err)
			}

			originalJSON, _ := json.Marshal(ev.StateSnapshot)
			reJSON, _ := json.Marshal(reMap)
			if string(originalJSON) != string(reJSON) {
				rt.Fatalf("event %d (%s): snapshot round-trip mismatch:\n  original: %s\n  re-serialized: %s",
					i, ev.Type, string(originalJSON), string(reJSON))
			}
		}

		// Verify specific events have correct state at that point.
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
