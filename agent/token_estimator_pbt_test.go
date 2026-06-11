package agent

import (
	"context"
	"fmt"
	"testing"

	"github.com/camilbinas/gude-agents/agent/tool"
	"pgregory.net/rapid"
)

// Feature: token-estimation, Property 1: Non-negative estimation invariant

// **Validates: Requirements 1.3**

// TestProperty_NonNegativeEstimation verifies that for any valid ConverseParams
// (with any combination of messages, system prompt, and tool config), calling
// EstimateTokens on CharEstimator returns a non-negative integer.
func TestProperty_NonNegativeEstimation(t *testing.T) {
	estimator := CharEstimator{}

	rapid.Check(t, func(t *rapid.T) {
		params := drawConverseParams(t)

		result, err := estimator.EstimateTokens(context.Background(), params)
		if err != nil {
			t.Fatalf("EstimateTokens returned unexpected error: %v", err)
		}
		if result < 0 {
			t.Fatalf("EstimateTokens returned negative value: %d", result)
		}
	})
}

// drawConverseParams generates a random ConverseParams with varying message counts,
// text lengths, and tool specs.
func drawConverseParams(t *rapid.T) ConverseParams {
	// Generate system prompt.
	system := rapid.String().Draw(t, "system")

	// Generate messages (0-50).
	numMessages := rapid.IntRange(0, 50).Draw(t, "numMessages")
	messages := make([]Message, numMessages)
	for i := range messages {
		messages[i] = drawMessage(t, i)
	}

	// Generate tool specs (0-20).
	numTools := rapid.IntRange(0, 20).Draw(t, "numTools")
	tools := make([]tool.Spec, numTools)
	for i := range tools {
		tools[i] = drawToolSpec(t, i)
	}

	return ConverseParams{
		Messages:   messages,
		System:     system,
		ToolConfig: tools,
	}
}

// drawMessage generates a random Message with mixed content blocks.
func drawMessage(t *rapid.T, idx int) Message {
	roles := []Role{RoleUser, RoleAssistant}
	role := rapid.SampledFrom(roles).Draw(t, fmt.Sprintf("role_%d", idx))

	// Generate 1-5 content blocks per message (mix of TextBlock and others).
	numBlocks := rapid.IntRange(1, 5).Draw(t, fmt.Sprintf("numBlocks_%d", idx))
	content := make([]ContentBlock, numBlocks)
	for i := range content {
		blockType := rapid.IntRange(0, 2).Draw(t, fmt.Sprintf("blockType_%d_%d", idx, i))
		switch blockType {
		case 0:
			text := rapid.String().Draw(t, fmt.Sprintf("text_%d_%d", idx, i))
			content[i] = TextBlock{Text: text}
		case 1:
			content[i] = ToolUseBlock{
				ToolUseID: rapid.StringMatching(`tu-[a-z0-9]{4}`).Draw(t, fmt.Sprintf("toolUseID_%d_%d", idx, i)),
				Name:      rapid.StringMatching(`[a-z_]{2,12}`).Draw(t, fmt.Sprintf("toolUseName_%d_%d", idx, i)),
				Input:     []byte(`{}`),
			}
		default:
			content[i] = ToolResultBlock{
				ToolUseID: rapid.StringMatching(`tu-[a-z0-9]{4}`).Draw(t, fmt.Sprintf("resultID_%d_%d", idx, i)),
				Content:   rapid.String().Draw(t, fmt.Sprintf("resultContent_%d_%d", idx, i)),
			}
		}
	}

	return Message{
		Role:    role,
		Content: content,
	}
}

// drawToolSpec generates a random tool.Spec.
func drawToolSpec(t *rapid.T, idx int) tool.Spec {
	name := rapid.StringMatching(`[a-z_]{3,20}`).Draw(t, fmt.Sprintf("toolName_%d", idx))
	desc := rapid.StringMatching(`[a-zA-Z0-9 ]{0,100}`).Draw(t, fmt.Sprintf("toolDesc_%d", idx))

	// Generate a simple JSON schema with 0-5 properties.
	numProps := rapid.IntRange(0, 5).Draw(t, fmt.Sprintf("numProps_%d", idx))
	props := make(map[string]any, numProps)
	for i := 0; i < numProps; i++ {
		propName := rapid.StringMatching(`[a-z]{3,10}`).Draw(t, fmt.Sprintf("propName_%d_%d", idx, i))
		props[propName] = map[string]any{
			"type":        "string",
			"description": rapid.StringMatching(`[a-zA-Z0-9 ]{0,50}`).Draw(t, fmt.Sprintf("propDesc_%d_%d", idx, i)),
		}
	}

	schema := map[string]any{
		"type":       "object",
		"properties": props,
	}

	return tool.Spec{
		Name:        name,
		Description: desc,
		InputSchema: schema,
	}
}
