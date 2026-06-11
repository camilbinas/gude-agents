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

// EstimateTokens extracts all text content from params (system prompt,
// message text blocks, and JSON-serialized tool specs), encodes it using
// the BPE tokenizer, and returns the token count.
// This operation is fully offline — no network calls are made.
func (e *Estimator) EstimateTokens(_ context.Context, params agent.ConverseParams) (int, error) {
	text := extractText(params)
	tokens := e.enc.Encode(text, nil, nil)
	return len(tokens), nil
}

// extractText concatenates all text sources from ConverseParams in the same
// order as CharEstimator: system prompt, message text blocks, tool config specs.
func extractText(params agent.ConverseParams) string {
	var buf []byte

	// System prompt.
	buf = append(buf, params.System...)

	// All TextBlock.Text from messages.
	for _, msg := range params.Messages {
		for _, block := range msg.Content {
			if tb, ok := block.(agent.TextBlock); ok {
				buf = append(buf, tb.Text...)
			}
		}
	}

	// JSON-serialized ToolConfig specs.
	for _, spec := range params.ToolConfig {
		data, err := json.Marshal(spec)
		if err != nil {
			continue
		}
		buf = append(buf, data...)
	}

	return string(buf)
}
