package agent

// ThinkingBlock carries extended-thinking output from a model so it can be
// replayed to the provider on the next request.
//
// Anthropic and Bedrock (Claude) require the signed thinking block to precede
// the tool_use blocks of the same assistant turn when extended thinking is
// combined with tool use. Providers that have no equivalent concept ignore
// ThinkingBlock when converting messages.
//
// Exactly one of two shapes is used:
//   - A regular block: Text holds the reasoning and Signature the opaque
//     signature issued by the provider.
//   - A redacted block: RedactedData holds the opaque encrypted payload and
//     Text and Signature are empty.
type ThinkingBlock struct {
	Text         string // reasoning text (regular blocks)
	Signature    string // opaque provider signature (regular blocks)
	RedactedData string // opaque redacted payload (redacted blocks); Bedrock bytes are stored base64-encoded
}

func (ThinkingBlock) contentBlock() {}

// Redacted reports whether the block is a redacted thinking block.
func (b ThinkingBlock) Redacted() bool { return b.RedactedData != "" }

// Replayable reports whether a provider can send the block back to the API.
// Regular blocks without a signature are rejected by the APIs, so they are
// kept in memory but never replayed.
func (b ThinkingBlock) Replayable() bool {
	return b.RedactedData != "" || (b.Text != "" && b.Signature != "")
}

// ThinkingContent returns the replayable thinking blocks of a response as
// content blocks, in the order the model produced them. Callers that build the
// assistant message for a turn must put these first, before text and tool use.
func (r *ModelResponse) ThinkingContent() []ContentBlock {
	if r == nil {
		return nil
	}
	var out []ContentBlock
	for _, b := range r.Thinking {
		if b.Replayable() {
			out = append(out, b)
		}
	}
	return out
}
