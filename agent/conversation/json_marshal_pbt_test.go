package conversation

import (
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

// Feature: memory-drivers, Property 1: JSON serialisation round-trip
// **Validates: Requirements 13.1, 13.2, 13.3, 13.4, 13.5**
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
