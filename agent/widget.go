package agent

import (
	"encoding/json"
	"fmt"
)

// ─── WidgetBlock ─────────────────────────────────────────────────────────────

// WidgetBlock is a ContentBlock that carries structured, domain-agnostic widget
// data produced by a tool handler via EmitWidget. Type is a caller-defined discriminator string
// (e.g. "chart", "table", "progress"). Payload is an opaque json.RawMessage
// whose schema is entirely defined by the caller.
type WidgetBlock struct {
	Type    string          // non-empty discriminator; required
	Payload json.RawMessage // opaque caller-defined JSON; may be nil
}

func (WidgetBlock) contentBlock() {}

// Validate returns a non-nil error if the WidgetBlock is malformed.
// Currently the only hard requirement is a non-empty Type.
func (w WidgetBlock) Validate() error {
	if w.Type == "" {
		return fmt.Errorf("widget block: Type must not be empty")
	}
	return nil
}

// ─── stripWidgets ────────────────────────────────────────────────────────────

// stripWidgets returns a new []Message slice in which every WidgetBlock has
// been removed from each Message.Content. Messages whose Content becomes empty
// after stripping are omitted entirely. The input slice and its Message values
// are never mutated.
func stripWidgets(msgs []Message) []Message {
	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		filtered := make([]ContentBlock, 0, len(m.Content))
		for _, b := range m.Content {
			if _, isWidget := b.(WidgetBlock); !isWidget {
				filtered = append(filtered, b)
			}
		}
		if len(filtered) == 0 {
			continue
		}
		mc := m
		mc.Content = filtered
		out = append(out, mc)
	}
	return out
}
