package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"pgregory.net/rapid"
)

// Feature: llm-evaluation, Property 2: JSON structure evaluator returns binary score with explanation on failure
// Validates: Requirements 4.1, 4.2
func TestProperty_JSONStructureBinaryScore(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate a random set of key-value pairs for a JSON object.
		numKeys := rapid.IntRange(1, 10).Draw(t, "numKeys")
		allKeys := make([]string, numKeys)
		obj := make(map[string]any, numKeys)
		for i := 0; i < numKeys; i++ {
			key := fmt.Sprintf("key_%d_%s", i, rapid.StringMatching(`[a-z]{2,6}`).Draw(t, fmt.Sprintf("keySuffix_%d", i)))
			allKeys[i] = key
			obj[key] = rapid.StringMatching(`[a-zA-Z0-9 ]{1,20}`).Draw(t, fmt.Sprintf("val_%d", i))
		}

		// Randomly select a subset as required keys.
		var requiredKeys []string
		for i := 0; i < numKeys; i++ {
			if rapid.Bool().Draw(t, fmt.Sprintf("required_%d", i)) {
				requiredKeys = append(requiredKeys, allKeys[i])
			}
		}

		// --- Scenario 1: Valid JSON containing ALL required keys → score 1.0 ---
		validJSON, err := json.Marshal(obj)
		if err != nil {
			t.Fatalf("failed to marshal valid JSON: %v", err)
		}

		js := NewJSONStructure(requiredKeys)
		result, err := js.Evaluate(context.Background(), EvalCase{
			ActualOutput: string(validJSON),
		})
		if err != nil {
			t.Fatalf("unexpected evaluate error for valid JSON: %v", err)
		}
		if result.Score != 1.0 {
			t.Fatalf("expected score 1.0 for valid JSON with all required keys, got %f (requiredKeys=%v, json=%s)",
				result.Score, requiredKeys, validJSON)
		}

		// --- Scenario 2: Valid JSON missing some required keys → score 0.0, non-empty Explanation ---
		if len(requiredKeys) > 0 {
			// Remove a random number of required keys from the object.
			numToRemove := rapid.IntRange(1, len(requiredKeys)).Draw(t, "numToRemove")
			partialObj := make(map[string]any, len(obj))
			for k, v := range obj {
				partialObj[k] = v
			}
			for i := 0; i < numToRemove; i++ {
				delete(partialObj, requiredKeys[i])
			}

			partialJSON, err := json.Marshal(partialObj)
			if err != nil {
				t.Fatalf("failed to marshal partial JSON: %v", err)
			}

			result2, err := js.Evaluate(context.Background(), EvalCase{
				ActualOutput: string(partialJSON),
			})
			if err != nil {
				t.Fatalf("unexpected evaluate error for partial JSON: %v", err)
			}
			if result2.Score != 0.0 {
				t.Fatalf("expected score 0.0 for JSON missing required keys, got %f", result2.Score)
			}
			if result2.Explanation == "" {
				t.Fatalf("expected non-empty Explanation for JSON missing required keys")
			}
		}

		// --- Scenario 3: Invalid JSON string → score 0.0, non-empty Explanation ---
		invalidStr := rapid.StringMatching(`[a-zA-Z]{3,20}`).Draw(t, "invalidJSON")
		// Ensure it's actually not valid JSON by prepending a character that breaks parsing.
		invalidStr = "{broken:" + invalidStr

		result3, err := js.Evaluate(context.Background(), EvalCase{
			ActualOutput: invalidStr,
		})
		if err != nil {
			t.Fatalf("unexpected evaluate error for invalid JSON: %v", err)
		}
		if result3.Score != 0.0 {
			t.Fatalf("expected score 0.0 for invalid JSON, got %f", result3.Score)
		}
		if result3.Explanation == "" {
			t.Fatalf("expected non-empty Explanation for invalid JSON")
		}
	})
}
