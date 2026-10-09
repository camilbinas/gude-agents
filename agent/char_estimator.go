package agent

import (
	"context"
	"encoding/json"
)

// CharEstimator is a zero-dependency token estimator that approximates
// token count as ceil(totalChars / 4). Suitable for rough budget
// enforcement without adding external libraries.
//
// The character total covers, in this order:
//   - ModelRequest.System
//   - TextBlock.Text
//   - ToolUseBlock.ToolUseID, Name, and raw Input bytes
//   - ToolResultBlock.ToolUseID and Content
//   - JSON-serialized tool specs
//
// Tool input is counted as raw bytes without parsing, so malformed payloads
// are still charged. Image and document payloads (including images attached
// to a ToolResultBlock) are not estimated; requests that carry large binary
// content will be under-counted.
type CharEstimator struct{}

// compile-time check: CharEstimator satisfies TokenEstimator.
var _ TokenEstimator = CharEstimator{}

// EstimateTokens computes the approximate token count by dividing the total
// character count of the request content (see CharEstimator) by 4, rounding
// up. It never returns an error (pure computation).
func (CharEstimator) EstimateTokens(_ context.Context, req ModelRequest) (int, error) {
	total := len(req.System)

	for _, msg := range req.Messages {
		for _, block := range msg.Content {
			switch b := block.(type) {
			case TextBlock:
				total += len(b.Text)
			case ToolUseBlock:
				total += len(b.ToolUseID) + len(b.Name) + len(b.Input)
			case ToolResultBlock:
				total += len(b.ToolUseID) + len(b.Content)
			}
		}
	}

	// Count JSON-serialized tool spec characters.
	for _, spec := range req.Tools {
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
