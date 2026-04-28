package eval

import (
	"context"
	"fmt"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"pgregory.net/rapid"
)

// Feature: llm-evaluation, Property 3: NDCG score is in [0, 1] and perfect ranking yields 1.0
// Validates: Requirements 6.1, 6.2
func TestProperty_NDCGRangeAndPerfect(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate 1-10 distinct expected IDs using a prefix pattern to guarantee uniqueness.
		numExpected := rapid.IntRange(1, 10).Draw(t, "numExpected")
		expectedIDs := make([]string, numExpected)
		for i := 0; i < numExpected; i++ {
			suffix := rapid.StringMatching(`[a-z]{3,8}`).Draw(t, fmt.Sprintf("suffix_%d", i))
			expectedIDs[i] = fmt.Sprintf("doc_%d_%s", i, suffix)
		}

		extractID := func(d agent.Document) string {
			return d.Metadata["id"]
		}

		// --- Part 1: Random ordering, score must be in [0.0, 1.0] ---

		// Build retrieved context: start with expected IDs, optionally add extra non-expected docs.
		numExtra := rapid.IntRange(0, 5).Draw(t, "numExtra")
		retrievedIDs := make([]string, 0, numExpected+numExtra)

		// Include a random subset of expected IDs (at least 1 to make it interesting,
		// but also test the case where none are included).
		for i := 0; i < numExpected; i++ {
			if rapid.Bool().Draw(t, fmt.Sprintf("includeExpected_%d", i)) {
				retrievedIDs = append(retrievedIDs, expectedIDs[i])
			}
		}

		// Add extra non-expected documents.
		for i := 0; i < numExtra; i++ {
			extraSuffix := rapid.StringMatching(`[a-z]{3,8}`).Draw(t, fmt.Sprintf("extraSuffix_%d", i))
			retrievedIDs = append(retrievedIDs, fmt.Sprintf("extra_%d_%s", i, extraSuffix))
		}

		// Shuffle the retrieved IDs into a random order using Fisher-Yates.
		shuffled := make([]string, len(retrievedIDs))
		copy(shuffled, retrievedIDs)
		for i := len(shuffled) - 1; i > 0; i-- {
			j := rapid.IntRange(0, i).Draw(t, fmt.Sprintf("swap_%d", i))
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		}

		// Build retrieved context documents.
		retrievedDocs := make([]agent.Document, len(shuffled))
		for i, id := range shuffled {
			retrievedDocs[i] = agent.Document{
				Content:  "content for " + id,
				Metadata: map[string]string{"id": id},
			}
		}

		ro, err := NewRetrievalOrdering(expectedIDs, extractID)
		if err != nil {
			t.Fatalf("unexpected constructor error: %v", err)
		}

		result, err := ro.Evaluate(context.Background(), EvalCase{
			RetrievedContext: retrievedDocs,
		})
		if err != nil {
			t.Fatalf("unexpected evaluate error: %v", err)
		}

		// Assert score is in [0.0, 1.0].
		if result.Score < 0.0 || result.Score > 1.0 {
			t.Fatalf("score out of range [0, 1]: got %f (expectedIDs=%v, retrievedIDs=%v)",
				result.Score, expectedIDs, shuffled)
		}

		// --- Part 2: Perfect ordering yields score == 1.0 ---

		// Build retrieved context in the exact expected order.
		perfectDocs := make([]agent.Document, numExpected)
		for i, id := range expectedIDs {
			perfectDocs[i] = agent.Document{
				Content:  "content for " + id,
				Metadata: map[string]string{"id": id},
			}
		}

		perfectResult, err := ro.Evaluate(context.Background(), EvalCase{
			RetrievedContext: perfectDocs,
		})
		if err != nil {
			t.Fatalf("unexpected evaluate error for perfect ordering: %v", err)
		}

		if perfectResult.Score != 1.0 {
			t.Fatalf("expected score 1.0 for perfect ordering, got %f (expectedIDs=%v)",
				perfectResult.Score, expectedIDs)
		}
	})
}
