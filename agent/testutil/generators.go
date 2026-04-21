// Package testutil provides shared property-based test generators for
// agent.Message and agent.ContentBlock types. It is intended for use by
// memory backend test suites to avoid duplicating generator code.
package testutil

import (
	"encoding/json"
	"fmt"

	"github.com/camilbinas/gude-agents/agent"
	"pgregory.net/rapid"
)

// GenTextBlock generates a random TextBlock with 0–50 alphanumeric characters.
func GenTextBlock(t *rapid.T) agent.TextBlock {
	return agent.TextBlock{
		Text: rapid.StringMatching(`[a-zA-Z0-9 ]{0,50}`).Draw(t, "text"),
	}
}

// GenToolUseBlock generates a random ToolUseBlock with a tool use ID, name, and JSON input.
func GenToolUseBlock(t *rapid.T) agent.ToolUseBlock {
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

// GenToolResultBlock generates a random ToolResultBlock with a tool use ID, content, and error flag.
func GenToolResultBlock(t *rapid.T) agent.ToolResultBlock {
	return agent.ToolResultBlock{
		ToolUseID: rapid.StringMatching(`tu_[a-zA-Z0-9]{4,12}`).Draw(t, "toolResultID"),
		Content:   rapid.StringMatching(`[a-zA-Z0-9 ]{0,50}`).Draw(t, "resultContent"),
		IsError:   rapid.Bool().Draw(t, "isError"),
	}
}

// GenContentBlock generates a random ContentBlock: TextBlock, ToolUseBlock, or ToolResultBlock.
func GenContentBlock(t *rapid.T) agent.ContentBlock {
	kind := rapid.IntRange(0, 2).Draw(t, "blockKind")
	switch kind {
	case 0:
		return GenTextBlock(t)
	case 1:
		return GenToolUseBlock(t)
	default:
		return GenToolResultBlock(t)
	}
}

// GenMessage generates a random Message with a random role and 1–5 content blocks.
func GenMessage(t *rapid.T) agent.Message {
	roles := []agent.Role{agent.RoleUser, agent.RoleAssistant}
	numBlocks := rapid.IntRange(1, 5).Draw(t, "numBlocks")
	blocks := make([]agent.ContentBlock, numBlocks)
	for j := range numBlocks {
		blocks[j] = GenContentBlock(t)
	}
	return agent.Message{
		Role:    rapid.SampledFrom(roles).Draw(t, "role"),
		Content: blocks,
	}
}

// GenMessages generates a random slice of 0–maxMessages messages, each with 1–5 content blocks.
func GenMessages(t *rapid.T, maxMessages int) []agent.Message {
	numMsgs := rapid.IntRange(0, maxMessages).Draw(t, "numMessages")
	msgs := make([]agent.Message, numMsgs)
	roles := []agent.Role{agent.RoleUser, agent.RoleAssistant}
	for i := range numMsgs {
		numBlocks := rapid.IntRange(1, 5).Draw(t, fmt.Sprintf("numBlocks_%d", i))
		blocks := make([]agent.ContentBlock, numBlocks)
		for j := range numBlocks {
			blocks[j] = GenContentBlock(t)
		}
		msgs[i] = agent.Message{
			Role:    rapid.SampledFrom(roles).Draw(t, fmt.Sprintf("role_%d", i)),
			Content: blocks,
		}
	}
	return msgs
}

// GenMessagesWithText generates a random slice of 0–maxMessages messages where each
// message has at least one TextBlock, ensuring Filter tests always have text content.
func GenMessagesWithText(t *rapid.T, maxMessages int) []agent.Message {
	numMsgs := rapid.IntRange(0, maxMessages).Draw(t, "numMessages")
	msgs := make([]agent.Message, numMsgs)
	roles := []agent.Role{agent.RoleUser, agent.RoleAssistant}
	for i := range numMsgs {
		blocks := []agent.ContentBlock{GenTextBlock(t)}
		extra := rapid.IntRange(0, 4).Draw(t, fmt.Sprintf("extraBlocks_%d", i))
		for range extra {
			blocks = append(blocks, GenContentBlock(t))
		}
		msgs[i] = agent.Message{
			Role:    rapid.SampledFrom(roles).Draw(t, fmt.Sprintf("role_%d", i)),
			Content: blocks,
		}
	}
	return msgs
}
