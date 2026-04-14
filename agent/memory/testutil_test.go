package memory

import (
	"encoding/json"
	"fmt"

	"github.com/camilbinas/gude-agents/agent"
	"pgregory.net/rapid"
)

// genTextBlock generates a random TextBlock with 0–50 alphanumeric characters.
func genTextBlock(t *rapid.T) agent.TextBlock {
	return agent.TextBlock{
		Text: rapid.StringMatching(`[a-zA-Z0-9 ]{0,50}`).Draw(t, "text"),
	}
}

// genToolUseBlock generates a random ToolUseBlock with a tool use ID, name, and JSON input.
func genToolUseBlock(t *rapid.T) agent.ToolUseBlock {
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
}

// genToolResultBlock generates a random ToolResultBlock with a tool use ID, content, and error flag.
func genToolResultBlock(t *rapid.T) agent.ToolResultBlock {
	return agent.ToolResultBlock{
		ToolUseID: rapid.StringMatching(`tu_[a-zA-Z0-9]{4,12}`).Draw(t, "toolResultID"),
		Content:   rapid.StringMatching(`[a-zA-Z0-9 ]{0,50}`).Draw(t, "resultContent"),
		IsError:   rapid.Bool().Draw(t, "isError"),
	}
}

// genContentBlock generates a random ContentBlock: TextBlock, ToolUseBlock, or ToolResultBlock.
func genContentBlock(t *rapid.T) agent.ContentBlock {
	kind := rapid.IntRange(0, 2).Draw(t, "blockKind")
	switch kind {
	case 0:
		return genTextBlock(t)
	case 1:
		return genToolUseBlock(t)
	default:
		return genToolResultBlock(t)
	}
}

// genMessage generates a random Message with a random role and 1–5 content blocks.
func genMessage(t *rapid.T) agent.Message {
	roles := []agent.Role{agent.RoleUser, agent.RoleAssistant}
	numBlocks := rapid.IntRange(1, 5).Draw(t, "numBlocks")
	blocks := make([]agent.ContentBlock, numBlocks)
	for j := range numBlocks {
		blocks[j] = genContentBlock(t)
	}
	return agent.Message{
		Role:    rapid.SampledFrom(roles).Draw(t, "role"),
		Content: blocks,
	}
}

// genMessages generates a random slice of 0–100 messages.
func genMessages(t *rapid.T) []agent.Message {
	numMsgs := rapid.IntRange(0, 100).Draw(t, "numMessages")
	msgs := make([]agent.Message, numMsgs)
	for i := range numMsgs {
		numBlocks := rapid.IntRange(1, 5).Draw(t, fmt.Sprintf("numBlocks_%d", i))
		blocks := make([]agent.ContentBlock, numBlocks)
		for j := range numBlocks {
			blocks[j] = genContentBlock(t)
		}
		roles := []agent.Role{agent.RoleUser, agent.RoleAssistant}
		msgs[i] = agent.Message{
			Role:    rapid.SampledFrom(roles).Draw(t, fmt.Sprintf("role_%d", i)),
			Content: blocks,
		}
	}
	return msgs
}

// genMessagesWithText generates a random slice of 0–100 messages where each message
// has at least one TextBlock, ensuring Filter tests always have text content to work with.
func genMessagesWithText(t *rapid.T) []agent.Message {
	numMsgs := rapid.IntRange(0, 100).Draw(t, "numMessages")
	msgs := make([]agent.Message, numMsgs)
	for i := range numMsgs {
		// Start with a guaranteed TextBlock.
		blocks := []agent.ContentBlock{genTextBlock(t)}
		// Add 0–4 additional random content blocks.
		extra := rapid.IntRange(0, 4).Draw(t, fmt.Sprintf("extraBlocks_%d", i))
		for j := range extra {
			_ = j
			blocks = append(blocks, genContentBlock(t))
		}
		roles := []agent.Role{agent.RoleUser, agent.RoleAssistant}
		msgs[i] = agent.Message{
			Role:    rapid.SampledFrom(roles).Draw(t, fmt.Sprintf("role_%d", i)),
			Content: blocks,
		}
	}
	return msgs
}
