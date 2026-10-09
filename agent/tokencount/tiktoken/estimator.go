package tiktoken

import (
	"context"
	"encoding/json"

	agent "github.com/camilbinas/gude-agents/agent"
	tiktoken "github.com/pkoukk/tiktoken-go"
)

// Estimator uses tiktoken-go BPE encoding for accurate offline token counting.
// It satisfies the agent.TokenEstimator interface.
type Estimator struct {
	enc *tiktoken.Tiktoken
}

// compile-time check: *Estimator satisfies agent.TokenEstimator.
var _ agent.TokenEstimator = (*Estimator)(nil)

// New creates an Estimator for the given encoding name.
// Common encodings: "cl100k_base" (GPT-4), "o200k_base" (GPT-4o).
// Returns an error if the encoding name is not recognized.
func New(encodingName string) (*Estimator, error) {
	enc, err := tiktoken.GetEncoding(encodingName)
	if err != nil {
		return nil, err
	}
	return &Estimator{enc: enc}, nil
}

// EstimateTokens extracts the text content of req, encodes it using the BPE
// tokenizer, and returns the token count. The content covers the system
// prompt, text blocks, tool-use blocks (ID, name, raw input), tool-result
// blocks (ID, content), and JSON-serialized tool specs, in the same order as
// agent.CharEstimator.
//
// Image and document payloads are not estimated. This operation is fully
// offline; no network calls are made.
func (e *Estimator) EstimateTokens(_ context.Context, req agent.ModelRequest) (int, error) {
	text := extractText(req)
	tokens := e.enc.Encode(text, nil, nil)
	return len(tokens), nil
}

// extractText concatenates the estimated content of req in the same field
// order as agent.CharEstimator. Tool input is appended as raw bytes without
// parsing so malformed payloads are still counted.
func extractText(req agent.ModelRequest) string {
	var buf []byte

	// System prompt.
	buf = append(buf, req.System...)

	// Message content blocks.
	for _, msg := range req.Messages {
		for _, block := range msg.Content {
			switch b := block.(type) {
			case agent.TextBlock:
				buf = append(buf, b.Text...)
			case agent.ToolUseBlock:
				buf = append(buf, b.ToolUseID...)
				buf = append(buf, b.Name...)
				buf = append(buf, b.Input...)
			case agent.ToolResultBlock:
				buf = append(buf, b.ToolUseID...)
				buf = append(buf, b.Content...)
			}
		}
	}

	// JSON-serialized tool specs.
	for _, spec := range req.Tools {
		data, err := json.Marshal(spec)
		if err != nil {
			continue
		}
		buf = append(buf, data...)
	}

	return string(buf)
}
