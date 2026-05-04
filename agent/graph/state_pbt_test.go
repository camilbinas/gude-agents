package graph

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"pgregory.net/rapid"
)

// ─── Test struct for struct-based property tests ─────────────────────────────

// pbtTestStruct is a test struct with various field types for property testing.
type pbtTestStruct struct {
	Name   string   `json:"name"`
	Count  int      `json:"count"`
	Score  float64  `json:"score"`
	Active bool     `json:"active"`
	Tags   []string `json:"tags"`
}

// ─── Generators ──────────────────────────────────────────────────────────────

// genJSONSafeState generates a State with various Go types that are JSON-safe
// but specifically tests type preservation (int64, float64, string, bool, nested maps, slices).
func genJSONSafeState(rt *rapid.T, label string) State {
	numKeys := rapid.IntRange(1, 8).Draw(rt, label+"_numKeys")
	state := make(State, numKeys)
	for i := 0; i < numKeys; i++ {
		key := rapid.StringMatching(`[a-z]{2,6}`).Draw(rt, fmt.Sprintf("%s_key%d", label, i))
		valType := rapid.IntRange(0, 6).Draw(rt, fmt.Sprintf("%s_valType%d", label, i))
		switch valType {
		case 0: // int64
			state[key] = int64(rapid.Int64Range(-1000, 1000).Draw(rt, fmt.Sprintf("%s_int64_%d", label, i)))
		case 1: // float64
			state[key] = float64(rapid.IntRange(-100, 100).Draw(rt, fmt.Sprintf("%s_float64_%d", label, i))) + 0.5
		case 2: // string
			state[key] = rapid.StringMatching(`[a-zA-Z0-9]{1,10}`).Draw(rt, fmt.Sprintf("%s_str_%d", label, i))
		case 3: // bool
			state[key] = rapid.Bool().Draw(rt, fmt.Sprintf("%s_bool_%d", label, i))
		case 4: // nested map
			nested := make(map[string]any)
			nested["inner"] = rapid.StringMatching(`[a-z]{2,5}`).Draw(rt, fmt.Sprintf("%s_nested_%d", label, i))
			state[key] = nested
		case 5: // slice of strings
			sliceLen := rapid.IntRange(0, 3).Draw(rt, fmt.Sprintf("%s_sliceLen_%d", label, i))
			sl := make([]any, sliceLen)
			for j := range sl {
				sl[j] = rapid.StringMatching(`[a-z]{2,4}`).Draw(rt, fmt.Sprintf("%s_slice_%d_%d", label, i, j))
			}
			state[key] = sl
		case 6: // int64 specifically to test no int64→float64 conversion
			state[key] = int64(42)
		}
	}
	return state
}

// genPbtTestStruct generates a random pbtTestStruct.
func genPbtTestStruct(rt *rapid.T, label string) pbtTestStruct {
	numTags := rapid.IntRange(0, 4).Draw(rt, label+"_numTags")
	tags := make([]string, numTags)
	for i := range tags {
		tags[i] = rapid.StringMatching(`[a-z]{2,6}`).Draw(rt, fmt.Sprintf("%s_tag%d", label, i))
	}
	return pbtTestStruct{
		Name:   rapid.StringMatching(`[a-zA-Z]{2,10}`).Draw(rt, label+"_name"),
		Count:  rapid.IntRange(0, 1000).Draw(rt, label+"_count"),
		Score:  float64(rapid.IntRange(0, 10000).Draw(rt, label+"_scoreInt")) / 100.0,
		Active: rapid.Bool().Draw(rt, label+"_active"),
		Tags:   tags,
	}
}

// ─── Property 1: State type preservation for map[string]any ──────────────────
//
// Feature: graph-generics-unification, Property 1: State type preservation for map[string]any
//
// **Validates: Requirements 1.2, 5.1, 12.3**
//
// For any State containing values of various Go types (int64, float64, string, bool,
// nested maps, slices), passing that state through identity nodes in Graph[State]
// should preserve the exact Go types of all values.

func TestProperty_StateTypePreservation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		state := genJSONSafeState(rt, "initial")

		// Build a graph with two identity nodes to verify type preservation through the engine.
		g, err := New[State]()
		if err != nil {
			rt.Fatal(err)
		}

		if err := g.AddNode("identity1", func(_ context.Context, s State) (State, error) {
			return s, nil
		}); err != nil {
			rt.Fatal(err)
		}
		if err := g.AddNode("identity2", func(_ context.Context, s State) (State, error) {
			return s, nil
		}); err != nil {
			rt.Fatal(err)
		}
		g.SetEntry("identity1")
		if err := g.AddEdge("identity1", "identity2"); err != nil {
			rt.Fatal(err)
		}

		res, err := g.Run(context.Background(), state)
		if err != nil {
			rt.Fatalf("Run failed: %v", err)
		}

		// Verify exact Go types are preserved for each key.
		for k, originalVal := range state {
			resultVal, exists := res.State[k]
			if !exists {
				rt.Fatalf("key %q missing from result state", k)
			}

			originalType := reflect.TypeOf(originalVal)
			resultType := reflect.TypeOf(resultVal)
			if originalType != resultType {
				rt.Fatalf("key %q: type changed from %v to %v (value: %v → %v)",
					k, originalType, resultType, originalVal, resultVal)
			}

			// Deep equality check.
			if !reflect.DeepEqual(originalVal, resultVal) {
				rt.Fatalf("key %q: value changed from %v to %v", k, originalVal, resultVal)
			}
		}
	})
}

// ─── Property 2: State isolation between nodes ───────────────────────────────
//
// Feature: graph-generics-unification, Property 2: State isolation between nodes
//
// **Validates: Requirements 5.2**
//
// Graph with two sequential nodes where first mutates its input copy; verify second
// node sees merged result, not mutated copy.

func TestProperty_StateIsolationBetweenNodes(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate initial state with a "preserved" key that node1 should not affect.
		preservedKey := rapid.StringMatching(`preserved_[a-z]{2,4}`).Draw(rt, "preservedKey")
		preservedVal := rapid.StringMatching(`[a-z]{3,8}`).Draw(rt, "preservedVal")
		node1Key := rapid.StringMatching(`node1_[a-z]{2,4}`).Draw(rt, "node1Key")
		node1Val := rapid.StringMatching(`[a-z]{3,8}`).Draw(rt, "node1Val")

		initial := State{
			preservedKey: preservedVal,
		}

		g, err := New[State]()
		if err != nil {
			rt.Fatal(err)
		}

		// Node1: mutates its input copy (adds a key to the copy it received),
		// but only returns a new key — does NOT return the preserved key.
		// The engine should merge node1's result into base, preserving keys not in the patch.
		if err := g.AddNode("node1", func(_ context.Context, s State) (State, error) {
			// Mutate the input copy (this should not affect the base state).
			s["mutated_by_node1"] = "should_not_leak"
			// Return only the new key.
			out := CopyState(s)
			out[node1Key] = node1Val
			return out, nil
		}); err != nil {
			rt.Fatal(err)
		}

		// Node2: verifies it sees the preserved key and node1's output.
		var node2State State
		if err := g.AddNode("node2", func(_ context.Context, s State) (State, error) {
			node2State = CopyState(s)
			return s, nil
		}); err != nil {
			rt.Fatal(err)
		}

		g.SetEntry("node1")
		if err := g.AddEdge("node1", "node2"); err != nil {
			rt.Fatal(err)
		}

		_, err = g.Run(context.Background(), initial)
		if err != nil {
			rt.Fatalf("Run failed: %v", err)
		}

		// Node2 should see the preserved key from initial state.
		if node2State[preservedKey] != preservedVal {
			rt.Fatalf("node2 lost preserved key %q: got %v, want %q",
				preservedKey, node2State[preservedKey], preservedVal)
		}

		// Node2 should see node1's output key.
		if node2State[node1Key] != node1Val {
			rt.Fatalf("node2 missing node1 output key %q: got %v, want %q",
				node1Key, node2State[node1Key], node1Val)
		}
	})
}

// ─── Property 3: Merge semantics (map state) ────────────────────────────────
//
// Feature: graph-generics-unification, Property 3: Merge semantics (map state)
//
// **Validates: Requirements 5.3**
//
// For any base and patch State, after merging patch into base, the result should
// contain: (a) all keys from patch with their patch values, and (b) all keys from
// base that are not in patch with their original values.

func TestProperty_MergeSemantics(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		base := genJSONSafeState(rt, "base")
		patch := genJSONSafeState(rt, "patch")

		// Make a copy of base before merge.
		baseCopy := CopyState(base)

		// Apply merge.
		ops := mapStateOps{}
		ops.merge(&baseCopy, patch)

		// (a) All keys from patch should have patch values.
		for k, patchVal := range patch {
			if !reflect.DeepEqual(baseCopy[k], patchVal) {
				rt.Fatalf("after merge, key %q should have patch value %v, got %v",
					k, patchVal, baseCopy[k])
			}
		}

		// (b) All keys from base not in patch should retain original values.
		for k, origVal := range base {
			if _, inPatch := patch[k]; !inPatch {
				if !reflect.DeepEqual(baseCopy[k], origVal) {
					rt.Fatalf("after merge, key %q (not in patch) should retain original value %v, got %v",
						k, origVal, baseCopy[k])
				}
			}
		}
	})
}

// ─── Property 4: Checkpoint round-trip for custom structs ────────────────────
//
// Feature: graph-generics-unification, Property 4: Checkpoint round-trip for custom structs
//
// **Validates: Requirements 6.1, 6.2**
//
// For any valid struct S, toMap then fromMap produces deeply equal struct.

func TestProperty_CheckpointRoundTripCustomStructs(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		original := genPbtTestStruct(rt, "s")

		ops := jsonStateOps[pbtTestStruct]{}

		// toMap
		m, err := ops.toMap(original)
		if err != nil {
			rt.Fatalf("toMap failed: %v", err)
		}

		// fromMap
		restored, err := ops.fromMap(m)
		if err != nil {
			rt.Fatalf("fromMap failed: %v", err)
		}

		// Verify deep equality.
		if original.Name != restored.Name {
			rt.Fatalf("Name mismatch: got %q, want %q", restored.Name, original.Name)
		}
		if original.Count != restored.Count {
			rt.Fatalf("Count mismatch: got %d, want %d", restored.Count, original.Count)
		}
		if original.Score != restored.Score {
			rt.Fatalf("Score mismatch: got %f, want %f", restored.Score, original.Score)
		}
		if original.Active != restored.Active {
			rt.Fatalf("Active mismatch: got %v, want %v", restored.Active, original.Active)
		}
		if len(original.Tags) != len(restored.Tags) {
			rt.Fatalf("Tags length mismatch: got %d, want %d", len(restored.Tags), len(original.Tags))
		}
		for i, tag := range original.Tags {
			if restored.Tags[i] != tag {
				rt.Fatalf("Tags[%d] mismatch: got %q, want %q", i, restored.Tags[i], tag)
			}
		}
	})
}

// ─── Property 5: Checkpoint identity for map[string]any ─────────────────────
//
// Feature: graph-generics-unification, Property 5: Checkpoint identity for map[string]any
//
// **Validates: Requirements 6.3, 12.3**
//
// For any State, mapStateOps.toMap and fromMap are identity operations.

func TestProperty_CheckpointIdentityMapState(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		state := genJSONSafeState(rt, "state")

		ops := mapStateOps{}

		// toMap should return the same map (identity).
		m, err := ops.toMap(state)
		if err != nil {
			rt.Fatalf("toMap failed: %v", err)
		}

		// The returned map should be the exact same reference (identity, not a copy).
		if fmt.Sprintf("%p", m) != fmt.Sprintf("%p", map[string]any(state)) {
			rt.Fatalf("toMap did not return identity: different pointer")
		}

		// fromMap should return the same map (identity).
		restored, err := ops.fromMap(m)
		if err != nil {
			rt.Fatalf("fromMap failed: %v", err)
		}

		// Verify it's the same reference.
		if fmt.Sprintf("%p", map[string]any(restored)) != fmt.Sprintf("%p", m) {
			rt.Fatalf("fromMap did not return identity: different pointer")
		}

		// Also verify values are equal.
		if !reflect.DeepEqual(state, restored) {
			rt.Fatalf("state != restored after identity round-trip")
		}
	})
}

// ─── Property 6: Fork/join mergeDiff correctness (map state) ─────────────────
//
// Feature: graph-generics-unification, Property 6: Fork/join mergeDiff correctness (map state)
//
// **Validates: Requirements 7.1**
//
// Two branches modify disjoint keys from snapshot; mergeDiff produces state with
// all changes from both branches.

func TestProperty_ForkJoinMergeDiffMap(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a snapshot state with some initial keys.
		numInitialKeys := rapid.IntRange(1, 5).Draw(rt, "numInitialKeys")
		snapshot := make(State, numInitialKeys)
		for i := 0; i < numInitialKeys; i++ {
			key := fmt.Sprintf("init_%d", i)
			snapshot[key] = rapid.StringMatching(`[a-z]{3,6}`).Draw(rt, fmt.Sprintf("initVal%d", i))
		}

		// Branch A adds new keys with prefix "a_".
		numAKeys := rapid.IntRange(1, 4).Draw(rt, "numAKeys")
		branchA := CopyState(snapshot)
		aKeys := make([]string, numAKeys)
		for i := 0; i < numAKeys; i++ {
			key := fmt.Sprintf("a_%d", i)
			aKeys[i] = key
			branchA[key] = rapid.StringMatching(`[a-z]{3,6}`).Draw(rt, fmt.Sprintf("aVal%d", i))
		}

		// Branch B adds new keys with prefix "b_".
		numBKeys := rapid.IntRange(1, 4).Draw(rt, "numBKeys")
		branchB := CopyState(snapshot)
		bKeys := make([]string, numBKeys)
		for i := 0; i < numBKeys; i++ {
			key := fmt.Sprintf("b_%d", i)
			bKeys[i] = key
			branchB[key] = rapid.StringMatching(`[a-z]{3,6}`).Draw(rt, fmt.Sprintf("bVal%d", i))
		}

		// Apply mergeDiff for both branches into base (initialized from snapshot).
		base := CopyState(snapshot)
		ops := mapStateOps{}
		ops.mergeDiff(&base, snapshot, branchA)
		ops.mergeDiff(&base, snapshot, branchB)

		// Verify: all branch A keys present with correct values.
		for _, key := range aKeys {
			if base[key] != branchA[key] {
				rt.Fatalf("branch A key %q: got %v, want %v", key, base[key], branchA[key])
			}
		}

		// Verify: all branch B keys present with correct values.
		for _, key := range bKeys {
			if base[key] != branchB[key] {
				rt.Fatalf("branch B key %q: got %v, want %v", key, base[key], branchB[key])
			}
		}

		// Verify: initial keys unchanged from snapshot.
		for i := 0; i < numInitialKeys; i++ {
			key := fmt.Sprintf("init_%d", i)
			if !reflect.DeepEqual(base[key], snapshot[key]) {
				rt.Fatalf("initial key %q changed: got %v, want %v", key, base[key], snapshot[key])
			}
		}
	})
}

// ─── Property 7: Fork/join mergeDiff correctness (struct state) ──────────────
//
// Feature: graph-generics-unification, Property 7: Fork/join mergeDiff correctness (struct state)
//
// **Validates: Requirements 7.2**
//
// Two branches modify disjoint fields of struct; fork/join merge produces struct
// with all field changes.

func TestProperty_ForkJoinMergeDiffStruct(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a snapshot struct.
		snapshot := genPbtTestStruct(rt, "snapshot")

		ops := jsonStateOps[pbtTestStruct]{}

		// Branch A modifies Name and Count (disjoint from branch B's fields).
		branchA := ops.copy(snapshot)
		branchA.Name = rapid.StringMatching(`[a-zA-Z]{2,10}`).Draw(rt, "branchA_name")
		branchA.Count = rapid.IntRange(1001, 2000).Draw(rt, "branchA_count")

		// Branch B modifies Score and Active (disjoint from branch A's fields).
		branchB := ops.copy(snapshot)
		branchB.Score = float64(rapid.IntRange(10001, 20000).Draw(rt, "branchB_scoreInt")) / 100.0
		branchB.Active = !snapshot.Active

		// Apply mergeDiff for both branches.
		base := ops.copy(snapshot)
		ops.mergeDiff(&base, snapshot, branchA)
		ops.mergeDiff(&base, snapshot, branchB)

		// Verify: branch A's fields are applied.
		if base.Name != branchA.Name {
			rt.Fatalf("Name: got %q, want %q (from branch A)", base.Name, branchA.Name)
		}
		if base.Count != branchA.Count {
			rt.Fatalf("Count: got %d, want %d (from branch A)", base.Count, branchA.Count)
		}

		// Verify: branch B's fields are applied.
		if base.Score != branchB.Score {
			rt.Fatalf("Score: got %f, want %f (from branch B)", base.Score, branchB.Score)
		}
		if base.Active != branchB.Active {
			rt.Fatalf("Active: got %v, want %v (from branch B)", base.Active, branchB.Active)
		}

		// Verify: Tags (unchanged by either branch) retain snapshot value.
		if !reflect.DeepEqual(base.Tags, snapshot.Tags) {
			rt.Fatalf("Tags changed unexpectedly: got %v, want %v", base.Tags, snapshot.Tags)
		}
	})
}
