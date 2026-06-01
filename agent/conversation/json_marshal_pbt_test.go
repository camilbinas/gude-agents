package conversation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"pgregory.net/rapid"
)

// genJSONContentBlock generates a random ContentBlock: TextBlock, ToolUseBlock, or ToolResultBlock.
func genJSONContentBlock(t *rapid.T) agent.ContentBlock {
	kind := rapid.IntRange(0, 2).Draw(t, "blockKind")
	switch kind {
	case 0:
		return agent.TextBlock{
			Text: rapid.StringMatching(`[a-zA-Z0-9 ]{0,50}`).Draw(t, "text"),
		}
	case 1:
		jsonOptions := []json.RawMessage{
			json.RawMessage(`{}`),
			json.RawMessage(`{"key":"value"}`),
			json.RawMessage(`{"a":1,"b":true}`),
		}
		return agent.ToolUseBlock{
			ToolUseID: rapid.StringMatching(`tu_[a-zA-Z0-9]{4,12}`).Draw(t, "toolUseID"),
			Name:      rapid.StringMatching(`[a-z_]{1,20}`).Draw(t, "toolName"),
			Input:     rapid.SampledFrom(jsonOptions).Draw(t, "input"),
		}
	default:
		return agent.ToolResultBlock{
			ToolUseID: rapid.StringMatching(`tu_[a-zA-Z0-9]{4,12}`).Draw(t, "toolResultID"),
			Content:   rapid.StringMatching(`[a-zA-Z0-9 ]{0,50}`).Draw(t, "resultContent"),
			IsError:   rapid.Bool().Draw(t, "isError"),
		}
	}
}

// genJSONMessages generates a random slice of 0–10 agent.Message, each with 1–5 ContentBlocks.
func genJSONMessages(t *rapid.T) []agent.Message {
	numMsgs := rapid.IntRange(0, 10).Draw(t, "numMessages")
	msgs := make([]agent.Message, numMsgs)
	roles := []agent.Role{agent.RoleUser, agent.RoleAssistant}
	for i := 0; i < numMsgs; i++ {
		numBlocks := rapid.IntRange(1, 5).Draw(t, fmt.Sprintf("numBlocks_%d", i))
		blocks := make([]agent.ContentBlock, numBlocks)
		for j := 0; j < numBlocks; j++ {
			blocks[j] = genJSONContentBlock(t)
		}
		msgs[i] = agent.Message{
			Role:    rapid.SampledFrom(roles).Draw(t, fmt.Sprintf("role_%d", i)),
			Content: blocks,
		}
	}
	return msgs
}

func TestProperty_MarshalUnmarshalRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		original := genJSONMessages(t)

		data, err := MarshalMessages(original)
		if err != nil {
			t.Fatalf("MarshalMessages failed: %v", err)
		}

		result, err := UnmarshalMessages(data)
		if err != nil {
			t.Fatalf("UnmarshalMessages failed: %v", err)
		}

		if !reflect.DeepEqual(original, result) {
			t.Fatalf("round-trip mismatch:\n  original: %+v\n  result:   %+v", original, result)
		}
	})
}

// **Validates: Requirements 3.3, 3.4**

// TestProperty_WidgetBlockRoundTrip verifies Property 6: for any WidgetBlock with a
// non-empty Type and any Payload (including nil), passing it through MarshalMessages
// then UnmarshalMessages produces a WidgetBlock with equal Type and Payload.
func TestProperty_WidgetBlockRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate a WidgetBlock with a non-empty Type.
		widgetType := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9_-]{0,30}`).Draw(t, "widgetType")
		useNilPayload := rapid.Bool().Draw(t, "useNilPayload")

		var payload json.RawMessage
		if !useNilPayload {
			// Generate valid JSON bytes so the payload survives a JSON round-trip intact.
			jsonOptions := []json.RawMessage{
				json.RawMessage(`null`),
				json.RawMessage(`{}`),
				json.RawMessage(`{"key":"value"}`),
				json.RawMessage(`{"a":1,"b":true}`),
				json.RawMessage(`[1,2,3]`),
				json.RawMessage(`"hello"`),
				json.RawMessage(`42`),
			}
			payload = rapid.SampledFrom(jsonOptions).Draw(t, "payload")
		}

		original := agent.WidgetBlock{Type: widgetType, Payload: payload}
		msg := agent.Message{
			Role:    agent.RoleAssistant,
			Content: []agent.ContentBlock{original},
		}

		data, err := MarshalMessages([]agent.Message{msg})
		if err != nil {
			t.Fatalf("MarshalMessages failed: %v", err)
		}

		restored, err := UnmarshalMessages(data)
		if err != nil {
			t.Fatalf("UnmarshalMessages failed: %v", err)
		}

		if len(restored) != 1 {
			t.Fatalf("expected 1 message, got %d", len(restored))
		}
		if len(restored[0].Content) != 1 {
			t.Fatalf("expected 1 content block, got %d", len(restored[0].Content))
		}

		wb, ok := restored[0].Content[0].(agent.WidgetBlock)
		if !ok {
			t.Fatalf("expected WidgetBlock, got %T", restored[0].Content[0])
		}
		if wb.Type != original.Type {
			t.Fatalf("Type mismatch: got %q, expected %q", wb.Type, original.Type)
		}
		if !bytes.Equal(wb.Payload, original.Payload) {
			t.Fatalf("Payload mismatch: got %v, expected %v", wb.Payload, original.Payload)
		}
	})
}

// **Validates: Requirements 3.5**

// TestProperty_WidgetBlockOrderPreserved verifies Property 7: for any ordered sequence
// of WidgetBlock values in a Message.Content slice, the round-trip preserves their
// relative order.
func TestProperty_WidgetBlockOrderPreserved(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate between 1 and 10 WidgetBlocks with distinct types.
		n := rapid.IntRange(1, 10).Draw(t, "n")
		blocks := make([]agent.WidgetBlock, n)
		content := make([]agent.ContentBlock, n)
		for i := 0; i < n; i++ {
			// Use index-based types to guarantee distinctness and stable ordering.
			blocks[i] = agent.WidgetBlock{Type: fmt.Sprintf("type-%d", i)}
			content[i] = blocks[i]
		}

		msg := agent.Message{Role: agent.RoleAssistant, Content: content}
		data, err := MarshalMessages([]agent.Message{msg})
		if err != nil {
			t.Fatalf("MarshalMessages failed: %v", err)
		}

		restored, err := UnmarshalMessages(data)
		if err != nil {
			t.Fatalf("UnmarshalMessages failed: %v", err)
		}

		if len(restored) != 1 {
			t.Fatalf("expected 1 message, got %d", len(restored))
		}
		if len(restored[0].Content) != n {
			t.Fatalf("expected %d content blocks, got %d", n, len(restored[0].Content))
		}

		for i, cb := range restored[0].Content {
			wb, ok := cb.(agent.WidgetBlock)
			if !ok {
				t.Fatalf("block[%d]: expected WidgetBlock, got %T", i, cb)
			}
			if wb.Type != blocks[i].Type {
				t.Fatalf("block[%d]: Type mismatch: got %q, expected %q", i, wb.Type, blocks[i].Type)
			}
		}
	})
}
