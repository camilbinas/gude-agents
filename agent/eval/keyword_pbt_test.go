package eval

import (
	"context"
	"fmt"
	"math"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// Feature: llm-evaluation, Property 1: Keyword grounding score equals fraction of keywords found (case-insensitive)
// Validates: Requirements 5.1, 5.2
func TestProperty_KeywordGroundingFraction(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate 1-10 distinct keywords using a prefix pattern to avoid substring collisions.
		numKeywords := rapid.IntRange(1, 10).Draw(t, "numKeywords")
		keywords := make([]string, numKeywords)
		for i := 0; i < numKeywords; i++ {
			suffix := rapid.StringMatching(`[a-z]{3,8}`).Draw(t, fmt.Sprintf("suffix_%d", i))
			keywords[i] = fmt.Sprintf("kw_%d_%s", i, suffix)
		}

		// For each keyword, randomly decide whether to include it in the output.
		included := make([]bool, numKeywords)
		expectedFound := 0
		for i := 0; i < numKeywords; i++ {
			included[i] = rapid.Bool().Draw(t, fmt.Sprintf("include_%d", i))
			if included[i] {
				expectedFound++
			}
		}

		// Build the output string by inserting included keywords with random case variations.
		var parts []string
		// Add some filler text that won't collide with our kw_ prefixed keywords.
		parts = append(parts, "some filler text here")
		for i := 0; i < numKeywords; i++ {
			if included[i] {
				// Apply a random case variation to the keyword.
				variation := rapid.SampledFrom([]string{"lower", "upper", "title"}).Draw(t, fmt.Sprintf("case_%d", i))
				kw := keywords[i]
				switch variation {
				case "lower":
					kw = strings.ToLower(kw)
				case "upper":
					kw = strings.ToUpper(kw)
				case "title":
					kw = strings.ToUpper(kw[:1]) + strings.ToLower(kw[1:])
				}
				parts = append(parts, kw)
			}
		}
		output := strings.Join(parts, " ")

		// Compute expected score.
		expectedScore := float64(expectedFound) / float64(numKeywords)

		// Run the evaluator.
		kg, err := NewKeywordGrounding(keywords)
		if err != nil {
			t.Fatalf("unexpected constructor error: %v", err)
		}

		result, err := kg.Evaluate(context.Background(), EvalCase{
			ActualOutput: output,
		})
		if err != nil {
			t.Fatalf("unexpected evaluate error: %v", err)
		}

		// Assert score equals expected fraction.
		if math.Abs(result.Score-expectedScore) > 1e-9 {
			t.Fatalf("score mismatch: got %f, want %f (keywords=%v, included=%v, output=%q)",
				result.Score, expectedScore, keywords, included, output)
		}
	})
}
