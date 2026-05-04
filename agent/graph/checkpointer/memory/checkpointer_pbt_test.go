package memory

import (
	"context"
	"fmt"
	"testing"

	"github.com/camilbinas/gude-agents/agent/graph"
	"pgregory.net/rapid"
)

// Feature: graph-checkpointing, Property 19: Deep Copy Isolation on Save/Load
//
// **Validates: Requirements 10.5**
//
// For any checkpoint saved to the InMemory checkpointer, mutating the original
// State map after save SHALL NOT affect the stored checkpoint, and mutating the
// loaded State map SHALL NOT affect the stored checkpoint.

func TestProperty_DeepCopyIsolationOnSaveLoad(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		c := New()
		threadID := rapid.StringMatching(`thread-[a-z0-9]{3,8}`).Draw(rt, "threadID")

		// Generate state with multiple keys.
		numKeys := rapid.IntRange(1, 8).Draw(rt, "numKeys")
		state := make(graph.State, numKeys)
		for i := 0; i < numKeys; i++ {
			key := fmt.Sprintf("key%d", i)
			state[key] = rapid.StringMatching(`[a-z]{3,10}`).Draw(rt, fmt.Sprintf("val%d", i))
		}

		// Save checkpoint.
		cp := graph.Checkpoint{
			State:    state,
			NodeName: "testnode",
		}
		saved, err := c.Save(context.Background(), threadID, cp)
		if err != nil {
			rt.Fatalf("Save failed: %v", err)
		}

		// Mutate the original state map after save.
		mutationKey := rapid.StringMatching(`mutated_[a-z]{2,6}`).Draw(rt, "mutationKey")
		state[mutationKey] = "mutated_value"

		// Also mutate an existing key.
		if numKeys > 0 {
			state["key0"] = "MUTATED"
		}

		// Load the checkpoint and verify stored data is unchanged.
		loaded, err := c.LoadAt(context.Background(), threadID, saved.Version)
		if err != nil {
			rt.Fatalf("LoadAt failed: %v", err)
		}

		// The mutation key should NOT be in the loaded state.
		if _, exists := loaded.State[mutationKey]; exists {
			rt.Fatalf("mutation of original state affected stored checkpoint: found key %q", mutationKey)
		}

		// The original key0 value should be preserved.
		if numKeys > 0 {
			originalVal := cp.State["key0"]
			// cp.State was mutated, so we can't use it. But we know the loaded value
			// should NOT be "MUTATED" since we saved before the mutation.
			if loaded.State["key0"] == "MUTATED" {
				rt.Fatalf("mutation of original state affected stored checkpoint: key0=%v, should not be 'MUTATED'", originalVal)
			}
		}

		// Now mutate the loaded state and verify it doesn't affect the stored checkpoint.
		loadedMutationKey := rapid.StringMatching(`loaded_mut_[a-z]{2,4}`).Draw(rt, "loadedMutKey")
		loaded.State[loadedMutationKey] = "loaded_mutation"

		// Load again and verify the loaded mutation didn't affect storage.
		loaded2, err := c.LoadAt(context.Background(), threadID, saved.Version)
		if err != nil {
			rt.Fatalf("second LoadAt failed: %v", err)
		}

		if _, exists := loaded2.State[loadedMutationKey]; exists {
			rt.Fatalf("mutation of loaded state affected stored checkpoint: found key %q", loadedMutationKey)
		}
	})
}
