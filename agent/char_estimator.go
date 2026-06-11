package agent

import (
	"context"
	"encoding/json"
)

// CharEstimator is a zero-dependency token estimator that approximates
// token count as ceil(totalChars / 4). Suitable for rough budget
// enforcement without adding external libraries.
type CharEstimator struct{}

// compile-time check: CharEstimator satisfies TokenEstimator.
var _ TokenEstimator = CharEstimator{}

// EstimateTokens computes the approximate token count by dividing the total
// character count of all text content in params by 4, rounding up.
// It never returns an error (pure computation).
func (CharEstimator) EstimateTokens(_ context.Context, params ConverseParams) (int, error) {
	total := 0

	// Count system prompt characters.
	total += len(params.System)

	// Count all TextBlock.Text characters from messages.
	for _, msg := range params.Messages {
		for _, block := range msg.Content {
			if tb, ok := block.(TextBlock); ok {
				total += len(tb.Text)
			}
		}
	}

	// Count JSON-serialized ToolConfig spec characters.
	for _, spec := range params.ToolConfig {
		data, err := json.Marshal(spec)
		if err != nil {
			// Should never happen with well-formed specs, but if it does,
			// skip this spec rather than returning an error.
			continue
		}
		total += len(data)
	}

	if total == 0 {
		return 0, nil
	}

	// ceil(total / 4)
	return (total + 3) / 4, nil
}
