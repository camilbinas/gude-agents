package agent

import (
	"context"
	"encoding/json"
	"testing"

	"pgregory.net/rapid"
)

// Feature: token-estimation, Property 2: CharEstimator formula correctness
// **Validates: Requirements 2.2, 2.3**

// TestProperty_CharEstimatorFormula verifies that for any ConverseParams the
// CharEstimator returns ceil(totalCharCount / 4) where totalCharCount is
// the sum of: all TextBlock.Text characters, System string characters, and
// JSON-serialized ToolConfig characters.
func TestProperty_CharEstimatorFormula(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		params := drawConverseParams(t)

		// --- Oracle: independently compute the expected result ---
		totalChars := 0

		// System prompt characters.
		totalChars += len(params.System)

		// All TextBlock.Text characters from messages.
		for _, msg := range params.Messages {
			for _, block := range msg.Content {
				if tb, ok := block.(TextBlock); ok {
					totalChars += len(tb.Text)
				}
			}
		}

		// JSON-serialized ToolConfig spec characters.
		for _, spec := range params.ToolConfig {
			data, err := json.Marshal(spec)
			if err != nil {
				continue
			}
			totalChars += len(data)
		}

		var expected int
		if totalChars == 0 {
			expected = 0
		} else {
			expected = (totalChars + 3) / 4
		}

		// --- Run the estimator ---
		estimator := CharEstimator{}
		got, err := estimator.EstimateTokens(context.Background(), params)
		if err != nil {
			t.Fatalf("EstimateTokens returned unexpected error: %v", err)
		}

		if got != expected {
			t.Fatalf("CharEstimator returned %d, oracle expected %d (totalChars=%d)", got, expected, totalChars)
		}
	})
}
