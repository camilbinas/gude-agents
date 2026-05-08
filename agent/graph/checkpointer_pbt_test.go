package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// Feature: graph-checkpointing, Property 1: Checkpoint Serialization Round-Trip
//
// **Validates: Requirements 2.1–2.8, 15.1, 15.2, 15.3**
//
// For any valid Checkpoint with JSON-serializable state, serializing to JSON then
// deserializing SHALL produce an equivalent checkpoint with all fields preserved.

func TestProperty_CheckpointSerializationRoundTrip(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate arbitrary JSON-serializable state.
		numKeys := rapid.IntRange(0, 10).Draw(rt, "numKeys")
		state := make(State, numKeys)
		for i := 0; i < numKeys; i++ {
			key := rapid.StringMatching(`[a-z]{2,8}`).Draw(rt, fmt.Sprintf("key%d", i))
			valType := rapid.IntRange(0, 3).Draw(rt, fmt.Sprintf("valType%d", i))
			switch valType {
			case 0:
				state[key] = rapid.StringMatching(`[a-zA-Z0-9 ]{0,20}`).Draw(rt, fmt.Sprintf("strVal%d", i))
			case 1:
				state[key] = rapid.Float64().Draw(rt, fmt.Sprintf("numVal%d", i))
			case 2:
				state[key] = rapid.Bool().Draw(rt, fmt.Sprintf("boolVal%d", i))
			case 3:
				state[key] = nil
			}
		}

		// Generate completed set.
		numCompleted := rapid.IntRange(0, 5).Draw(rt, "numCompleted")
		completed := make(map[string]bool, numCompleted)
		for i := 0; i < numCompleted; i++ {
			nodeName := rapid.StringMatching(`[a-z]{2,6}`).Draw(rt, fmt.Sprintf("completedNode%d", i))
			completed[nodeName] = true
		}

		cp := Checkpoint{
			ThreadID:   rapid.StringMatching(`thread-[a-z0-9]{3,10}`).Draw(rt, "threadID"),
			Version:    rapid.IntRange(1, 1000).Draw(rt, "version"),
			State:      state,
			Completed:  completed,
			Iterations: rapid.IntRange(0, 100).Draw(rt, "iterations"),
			NodeName:   rapid.StringMatching(`[a-z]{2,8}`).Draw(rt, "nodeName"),
			Timestamp:  time.Now().Truncate(time.Millisecond),
		}

		// Marshal to JSON.
		data, err := json.Marshal(cp)
		if err != nil {
			rt.Fatalf("marshal failed: %v", err)
		}

		// Unmarshal back.
		var restored Checkpoint
		if err := json.Unmarshal(data, &restored); err != nil {
			rt.Fatalf("unmarshal failed: %v", err)
		}

		// Verify equivalence.
		if restored.ThreadID != cp.ThreadID {
			rt.Fatalf("ThreadID mismatch: got %q, want %q", restored.ThreadID, cp.ThreadID)
		}
		if restored.Version != cp.Version {
			rt.Fatalf("Version mismatch: got %d, want %d", restored.Version, cp.Version)
		}
		if restored.Iterations != cp.Iterations {
			rt.Fatalf("Iterations mismatch: got %d, want %d", restored.Iterations, cp.Iterations)
		}
		if restored.NodeName != cp.NodeName {
			rt.Fatalf("NodeName mismatch: got %q, want %q", restored.NodeName, cp.NodeName)
		}
		if !restored.Timestamp.Equal(cp.Timestamp) {
			rt.Fatalf("Timestamp mismatch: got %v, want %v", restored.Timestamp, cp.Timestamp)
		}

		// Verify state round-trip via JSON comparison.
		origStateJSON, _ := json.Marshal(cp.State)
		restoredStateJSON, _ := json.Marshal(restored.State)
		if string(origStateJSON) != string(restoredStateJSON) {
			rt.Fatalf("State mismatch:\n  orig: %s\n  restored: %s", origStateJSON, restoredStateJSON)
		}

		// Verify completed set.
		if len(restored.Completed) != len(cp.Completed) {
			rt.Fatalf("Completed length mismatch: got %d, want %d", len(restored.Completed), len(cp.Completed))
		}
		for k, v := range cp.Completed {
			if restored.Completed[k] != v {
				rt.Fatalf("Completed[%q] mismatch: got %v, want %v", k, restored.Completed[k], v)
			}
		}
	})
}

// Feature: graph-checkpointing, Property 2: Save/Load Round-Trip
func TestProperty_SaveLoadRoundTrip(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		cp := &mockCheckpointer{}

		threadID := rapid.StringMatching(`thread-[a-z0-9]{3,8}`).Draw(rt, "threadID")

		numKeys := rapid.IntRange(1, 8).Draw(rt, "numKeys")
		state := make(State, numKeys)
		for i := 0; i < numKeys; i++ {
			key := rapid.StringMatching(`[a-z]{2,6}`).Draw(rt, fmt.Sprintf("key%d", i))
			state[key] = rapid.StringMatching(`[a-zA-Z0-9]{1,10}`).Draw(rt, fmt.Sprintf("val%d", i))
		}

		numCompleted := rapid.IntRange(1, 4).Draw(rt, "numCompleted")
		completed := make(map[string]bool, numCompleted)
		for i := 0; i < numCompleted; i++ {
			completed[fmt.Sprintf("node%d", i)] = true
		}

		original := Checkpoint{
			State:      state,
			Completed:  completed,
			Iterations: rapid.IntRange(1, 50).Draw(rt, "iterations"),
			NodeName:   rapid.StringMatching(`[a-z]{2,6}`).Draw(rt, "nodeName"),
			Timestamp:  time.Now(),
		}

		saved, err := cp.Save(context.Background(), threadID, original)
		if err != nil {
			rt.Fatalf("Save failed: %v", err)
		}

		loaded, err := cp.LoadAt(context.Background(), threadID, saved.Version)
		if err != nil {
			rt.Fatalf("LoadAt failed: %v", err)
		}

		if loaded.ThreadID != threadID {
			rt.Fatalf("ThreadID mismatch: got %q, want %q", loaded.ThreadID, threadID)
		}
		if loaded.Version != saved.Version {
			rt.Fatalf("Version mismatch: got %d, want %d", loaded.Version, saved.Version)
		}
		if loaded.NodeName != original.NodeName {
			rt.Fatalf("NodeName mismatch: got %q, want %q", loaded.NodeName, original.NodeName)
		}
		if loaded.Iterations != original.Iterations {
			rt.Fatalf("Iterations mismatch: got %d, want %d", loaded.Iterations, original.Iterations)
		}

		for k, v := range original.State {
			if loaded.State[k] != v {
				rt.Fatalf("State[%q] mismatch: got %v, want %v", k, loaded.State[k], v)
			}
		}

		for k, v := range original.Completed {
			if loaded.Completed[k] != v {
				rt.Fatalf("Completed[%q] mismatch: got %v, want %v", k, loaded.Completed[k], v)
			}
		}
	})
}

// Feature: graph-checkpointing, Property 3: Load Returns Latest Version
func TestProperty_LoadReturnsLatestVersion(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		cp := &mockCheckpointer{}
		threadID := rapid.StringMatching(`thread-[a-z0-9]{3,8}`).Draw(rt, "threadID")

		n := rapid.IntRange(1, 10).Draw(rt, "numCheckpoints")
		var lastSaved Checkpoint
		for i := 0; i < n; i++ {
			checkpoint := Checkpoint{
				State:    State{fmt.Sprintf("key%d", i): fmt.Sprintf("val%d", i)},
				NodeName: fmt.Sprintf("node%d", i),
			}
			saved, err := cp.Save(context.Background(), threadID, checkpoint)
			if err != nil {
				rt.Fatalf("Save %d failed: %v", i, err)
			}
			lastSaved = saved
		}

		loaded, err := cp.Load(context.Background(), threadID)
		if err != nil {
			rt.Fatalf("Load failed: %v", err)
		}

		if loaded.Version != lastSaved.Version {
			rt.Fatalf("Load returned version %d, expected latest %d", loaded.Version, lastSaved.Version)
		}
		if loaded.Version != n {
			rt.Fatalf("Load returned version %d, expected %d (total saved)", loaded.Version, n)
		}
	})
}

// Feature: graph-checkpointing, Property 4: History Ordering and Completeness
func TestProperty_HistoryOrderingAndCompleteness(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		cp := &mockCheckpointer{}
		threadID := rapid.StringMatching(`thread-[a-z0-9]{3,8}`).Draw(rt, "threadID")

		n := rapid.IntRange(1, 10).Draw(rt, "numCheckpoints")
		nodeNames := make([]string, n)
		for i := 0; i < n; i++ {
			nodeNames[i] = rapid.StringMatching(`[a-z]{2,6}`).Draw(rt, fmt.Sprintf("nodeName%d", i))
			checkpoint := Checkpoint{
				State:    State{"step": i},
				NodeName: nodeNames[i],
			}
			_, err := cp.Save(context.Background(), threadID, checkpoint)
			if err != nil {
				rt.Fatalf("Save %d failed: %v", i, err)
			}
		}

		history, err := cp.History(context.Background(), threadID)
		if err != nil {
			rt.Fatalf("History failed: %v", err)
		}

		if len(history) != n {
			rt.Fatalf("History returned %d entries, expected %d", len(history), n)
		}

		for i := 1; i < len(history); i++ {
			if history[i].Version <= history[i-1].Version {
				rt.Fatalf("History not ordered: version[%d]=%d <= version[%d]=%d",
					i, history[i].Version, i-1, history[i-1].Version)
			}
		}

		for i, meta := range history {
			if meta.NodeName != nodeNames[i] {
				rt.Fatalf("History[%d].NodeName=%q, expected %q", i, meta.NodeName, nodeNames[i])
			}
		}
	})
}

// Feature: graph-checkpointing, Property 5: List Completeness
func TestProperty_ListCompleteness(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		cp := &mockCheckpointer{}

		numThreads := rapid.IntRange(1, 8).Draw(rt, "numThreads")
		threadIDs := make(map[string]bool, numThreads)
		for i := 0; i < numThreads; i++ {
			id := fmt.Sprintf("thread-%s-%d",
				rapid.StringMatching(`[a-z]{3,6}`).Draw(rt, fmt.Sprintf("threadSuffix%d", i)), i)
			threadIDs[id] = true
			checkpoint := Checkpoint{
				State:    State{"thread": id},
				NodeName: "node",
			}
			_, err := cp.Save(context.Background(), id, checkpoint)
			if err != nil {
				rt.Fatalf("Save failed for thread %q: %v", id, err)
			}
		}

		listed, err := cp.List(context.Background())
		if err != nil {
			rt.Fatalf("List failed: %v", err)
		}

		listedSet := make(map[string]bool, len(listed))
		for _, id := range listed {
			listedSet[id] = true
		}

		for id := range threadIDs {
			if !listedSet[id] {
				rt.Fatalf("List missing thread ID %q", id)
			}
		}
	})
}

// Feature: graph-checkpointing, Property 6: Delete Removes All Thread Checkpoints
func TestProperty_DeleteRemovesAllThreadCheckpoints(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		cp := &mockCheckpointer{}
		threadID := rapid.StringMatching(`thread-[a-z0-9]{3,8}`).Draw(rt, "threadID")

		n := rapid.IntRange(1, 5).Draw(rt, "numCheckpoints")
		for i := 0; i < n; i++ {
			checkpoint := Checkpoint{
				State:    State{"step": i},
				NodeName: fmt.Sprintf("node%d", i),
			}
			_, err := cp.Save(context.Background(), threadID, checkpoint)
			if err != nil {
				rt.Fatalf("Save %d failed: %v", i, err)
			}
		}

		if err := cp.Delete(context.Background(), threadID); err != nil {
			rt.Fatalf("Delete failed: %v", err)
		}

		_, err := cp.Load(context.Background(), threadID)
		if !errors.Is(err, ErrCheckpointNotFound) {
			rt.Fatalf("Load after Delete: expected ErrCheckpointNotFound, got %v", err)
		}

		listed, err := cp.List(context.Background())
		if err != nil {
			rt.Fatalf("List failed: %v", err)
		}
		for _, id := range listed {
			if id == threadID {
				rt.Fatalf("List still contains deleted thread %q", threadID)
			}
		}
	})
}

// Feature: graph-checkpointing, Property 7: Serialization Validation Catches Non-Serializable State
func TestProperty_SerializationValidationCatchesNonSerializableState(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		numValidKeys := rapid.IntRange(0, 5).Draw(rt, "numValidKeys")
		state := make(State, numValidKeys+1)
		for i := 0; i < numValidKeys; i++ {
			key := rapid.StringMatching(`[a-z]{2,6}`).Draw(rt, fmt.Sprintf("validKey%d", i))
			state[key] = rapid.StringMatching(`[a-z]{1,10}`).Draw(rt, fmt.Sprintf("validVal%d", i))
		}

		badKey := rapid.StringMatching(`bad_[a-z]{2,6}`).Draw(rt, "badKey")
		badType := rapid.IntRange(0, 1).Draw(rt, "badType")
		switch badType {
		case 0:
			state[badKey] = make(chan int)
		case 1:
			state[badKey] = func() {}
		}

		err := validateStateSerializable(state)
		if err == nil {
			rt.Fatalf("expected serialization error for non-serializable state, got nil")
		}

		var serErr *StateSerializationError
		if !errors.As(err, &serErr) {
			rt.Fatalf("expected StateSerializationError, got %T: %v", err, err)
		}

		if serErr.Key == "" {
			rt.Fatalf("StateSerializationError.Key is empty, expected offending key name")
		}
		if serErr.Type == "" {
			rt.Fatalf("StateSerializationError.Type is empty, expected Go type")
		}
	})
}

// Feature: graph-checkpointing, Property 8: Monotonically Increasing Versions
func TestProperty_MonotonicallyIncreasingVersions(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		cp := &mockCheckpointer{}
		threadID := rapid.StringMatching(`thread-[a-z0-9]{3,8}`).Draw(rt, "threadID")

		n := rapid.IntRange(1, 15).Draw(rt, "numCheckpoints")
		versions := make([]int, 0, n)

		for i := 0; i < n; i++ {
			checkpoint := Checkpoint{
				State:    State{"step": i},
				NodeName: fmt.Sprintf("node%d", i),
			}
			saved, err := cp.Save(context.Background(), threadID, checkpoint)
			if err != nil {
				rt.Fatalf("Save %d failed: %v", i, err)
			}
			versions = append(versions, saved.Version)
		}

		if versions[0] != 1 {
			rt.Fatalf("first version=%d, expected 1", versions[0])
		}
		for i := 1; i < len(versions); i++ {
			if versions[i] != versions[i-1]+1 {
				rt.Fatalf("version[%d]=%d is not version[%d]+1=%d",
					i, versions[i], i-1, versions[i-1]+1)
			}
		}
	})
}

// Feature: graph-checkpointing, Property 9: Checkpoint Ordering Reflects Execution Order
func TestProperty_CheckpointOrderingReflectsExecutionOrder(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		cp := &mockCheckpointer{}

		numNodes := rapid.IntRange(2, 6).Draw(rt, "numNodes")
		g, err := New[State](WithCheckpointer(cp))
		if err != nil {
			rt.Fatal(err)
		}

		names := make([]string, numNodes)
		for i := range names {
			names[i] = fmt.Sprintf("node%d", i)
			name := names[i]
			var inputKeys []string
			if i > 0 {
				inputKeys = []string{fmt.Sprintf("node%d_out", i-1)}
			}
			if _, err := g.Node(name, func(_ context.Context, s State) (State, error) {
				out := CopyState(s)
				out[name] = "done"
				out[name+"_out"] = "done"
				return out, nil
			}, In(inputKeys...), Out(name+"_out")); err != nil {
				rt.Fatal(err)
			}
		}
		g.Start(names[0])

		threadID := rapid.StringMatching(`thread-[a-z0-9]{3,8}`).Draw(rt, "threadID")
		_, err = g.Run(context.Background(), State{}, WithThreadID(threadID))
		if err != nil {
			rt.Fatalf("Run failed: %v", err)
		}

		if len(cp.saved) < 2 {
			rt.Fatalf("expected at least 2 checkpoints, got %d", len(cp.saved))
		}

		for i := 0; i < len(cp.saved)-1; i++ {
			currentCompleted := cp.saved[i].Completed
			nextCompleted := cp.saved[i+1].Completed

			for k := range currentCompleted {
				if !nextCompleted[k] {
					rt.Fatalf("checkpoint %d completed key %q not in checkpoint %d completed set",
						i, k, i+1)
				}
			}
			if len(nextCompleted) <= len(currentCompleted) {
				rt.Fatalf("checkpoint %d completed size %d not > checkpoint %d completed size %d",
					i+1, len(nextCompleted), i, len(currentCompleted))
			}
		}
	})
}

// Feature: graph-checkpointing, Property 20: CheckpointOnInterruptOnly Limits Checkpoints
func TestProperty_CheckpointOnInterruptOnlyLimitsCheckpoints(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		cp := &mockCheckpointer{}

		numNodes := rapid.IntRange(3, 5).Draw(rt, "numNodes")
		g, err := New[State](WithCheckpointer(cp), WithCheckpointOnInterruptOnly())
		if err != nil {
			rt.Fatal(err)
		}

		names := make([]string, numNodes)
		for i := range names {
			names[i] = fmt.Sprintf("node%d", i)
			name := names[i]
			var inputKeys []string
			if i > 0 {
				inputKeys = []string{fmt.Sprintf("node%d_out", i-1)}
			}
			if _, err := g.Node(name, func(_ context.Context, s State) (State, error) {
				out := CopyState(s)
				out[name] = "done"
				out[name+"_out"] = "done"
				return out, nil
			}, In(inputKeys...), Out(name+"_out")); err != nil {
				rt.Fatal(err)
			}
		}
		g.Start(names[0])

		// Pick a random node (not the first) to interrupt before.
		interruptIdx := rapid.IntRange(1, numNodes-1).Draw(rt, "interruptIdx")
		if err := g.InterruptBefore(names[interruptIdx]); err != nil {
			rt.Fatal(err)
		}

		threadID := rapid.StringMatching(`thread-[a-z0-9]{3,8}`).Draw(rt, "threadID")
		_, err = g.Run(context.Background(), State{}, WithThreadID(threadID))

		var intErr *GraphInterruptError
		if !errors.As(err, &intErr) {
			rt.Fatalf("expected GraphInterruptError, got %T: %v", err, err)
		}

		if len(cp.saved) != 1 {
			rt.Fatalf("expected exactly 1 checkpoint (at interrupt), got %d", len(cp.saved))
		}

		if cp.saved[0].NodeName != names[interruptIdx] {
			rt.Fatalf("checkpoint node=%q, expected %q", cp.saved[0].NodeName, names[interruptIdx])
		}
	})
}

// ── Struct State Round-Trip Through Checkpoint ───────────────────────────────
//
// Feature: graph-checkpointing, Property 21: Struct State Round-Trip Through Checkpoint

// testTypedState is a struct state for property testing.
type testTypedState struct {
	Name   string   `json:"name"`
	Count  int      `json:"count"`
	Score  float64  `json:"score"`
	Active bool     `json:"active"`
	Tags   []string `json:"tags"`
}

func TestProperty_StructStateRoundTripThroughCheckpoint(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		cp := &mockCheckpointer{}

		tg, err := New[testTypedState](WithCheckpointer(cp))
		if err != nil {
			rt.Fatal(err)
		}

		// Generate arbitrary struct state.
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

		if _, err := tg.Node("passthrough", func(_ context.Context, s testTypedState) (testTypedState, error) {
			return s, nil
		}, In(), Out("pass_out")); err != nil {
			rt.Fatal(err)
		}
		tg.Start("passthrough")

		threadID := rapid.StringMatching(`thread-[a-z0-9]{3,8}`).Draw(rt, "threadID")

		result, err := tg.Step(context.Background(), initialState, threadID)
		if err != nil {
			rt.Fatalf("Step failed: %v", err)
		}

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

		loaded, err := cp.LoadAt(context.Background(), threadID, result.Version)
		if err != nil {
			rt.Fatalf("LoadAt failed: %v", err)
		}

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
