package tiktoken

import (
	"context"
	"fmt"
	"testing"

	agent "github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/tool"
	"pgregory.net/rapid"
)

// TestProperty_NonNegativeEstimation verifies that every valid generated
// request, including tool-use and tool-result blocks, estimates without error
// to a non-negative count.
func TestProperty_NonNegativeEstimation(t *testing.T) {
	estimator, err := New("cl100k_base")
	if err != nil {
		t.Fatalf("failed to create Estimator: %v", err)
	}

	rapid.Check(t, func(t *rapid.T) {
		req := drawModelRequest(t)

		got, err := estimator.EstimateTokens(context.Background(), req)
		if err != nil {
			t.Fatalf("EstimateTokens returned unexpected error: %v", err)
		}
		if got < 0 {
			t.Fatalf("EstimateTokens returned negative value: %d", got)
		}
	})
}

// drawModelRequest generates a random ModelRequest with text, tool-use,
// tool-result, and tool spec content.
func drawModelRequest(t *rapid.T) agent.ModelRequest {
	system := rapid.String().Draw(t, "system")

	numMessages := rapid.IntRange(0, 10).Draw(t, "numMessages")
	messages := make([]agent.Message, numMessages)
	for i := range messages {
		messages[i] = drawMessage(t, i)
	}

	numTools := rapid.IntRange(0, 5).Draw(t, "numTools")
	tools := make([]tool.Spec, numTools)
	for i := range tools {
		tools[i] = drawToolSpec(t, i)
	}

	return agent.ModelRequest{
		Messages: messages,
		System:   system,
		Tools:    tools,
	}
}

// drawMessage generates a random Message with mixed content blocks.
func drawMessage(t *rapid.T, idx int) agent.Message {
	roles := []agent.Role{agent.RoleUser, agent.RoleAssistant}
	role := rapid.SampledFrom(roles).Draw(t, fmt.Sprintf("role_%d", idx))

	numBlocks := rapid.IntRange(1, 3).Draw(t, fmt.Sprintf("numBlocks_%d", idx))
	content := make([]agent.ContentBlock, numBlocks)
	for i := range content {
		switch rapid.IntRange(0, 2).Draw(t, fmt.Sprintf("blockType_%d_%d", idx, i)) {
		case 0:
			content[i] = agent.TextBlock{Text: rapid.String().Draw(t, fmt.Sprintf("text_%d_%d", idx, i))}
		case 1:
			content[i] = agent.ToolUseBlock{
				ToolUseID: rapid.StringMatching(`tu-[a-z0-9]{4}`).Draw(t, fmt.Sprintf("toolUseID_%d_%d", idx, i)),
				Name:      rapid.StringMatching(`[a-z_]{2,12}`).Draw(t, fmt.Sprintf("toolUseName_%d_%d", idx, i)),
				Input:     []byte(rapid.String().Draw(t, fmt.Sprintf("toolInput_%d_%d", idx, i))),
			}
		default:
			content[i] = agent.ToolResultBlock{
				ToolUseID: rapid.StringMatching(`tu-[a-z0-9]{4}`).Draw(t, fmt.Sprintf("resultID_%d_%d", idx, i)),
				Content:   rapid.String().Draw(t, fmt.Sprintf("resultContent_%d_%d", idx, i)),
			}
		}
	}

	return agent.Message{Role: role, Content: content}
}

// drawToolSpec generates a random tool.Spec with a simple schema.
func drawToolSpec(t *rapid.T, idx int) tool.Spec {
	name := rapid.StringMatching(`[a-z_]{3,15}`).Draw(t, fmt.Sprintf("toolName_%d", idx))
	desc := rapid.String().Draw(t, fmt.Sprintf("toolDesc_%d", idx))

	numProps := rapid.IntRange(0, 3).Draw(t, fmt.Sprintf("numProps_%d", idx))
	props := make(map[string]any, numProps)
	for i := 0; i < numProps; i++ {
		propName := rapid.StringMatching(`[a-z]{3,8}`).Draw(t, fmt.Sprintf("propName_%d_%d", idx, i))
		props[propName] = map[string]any{
			"type":        "string",
			"description": rapid.StringMatching(`[a-zA-Z0-9 ]{0,30}`).Draw(t, fmt.Sprintf("propDesc_%d_%d", idx, i)),
		}
	}

	return tool.Spec{
		Name:        name,
		Description: desc,
		InputSchema: map[string]any{
			"type":       "object",
			"properties": props,
		},
	}
}
