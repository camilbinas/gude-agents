package tiktoken

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	agent "github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/tool"
	tiktokenlib "github.com/pkoukk/tiktoken-go"
	"pgregory.net/rapid"
)

// Feature: token-estimation, Property 3: TiktokenEstimator encoding consistency

// **Validates: Requirements 3.3**

// TestProperty_TiktokenEncodingConsistency verifies that for any ConverseParams
// containing arbitrary text, the TiktokenEstimator produces a token count equal
// to directly encoding the same concatenated text with the tiktoken library.
func TestProperty_TiktokenEncodingConsistency(t *testing.T) {
	estimator, err := New("cl100k_base")
	if err != nil {
		t.Fatalf("failed to create TiktokenEstimator: %v", err)
	}

	// Create a direct encoding for oracle comparison.
	oracle, err := tiktokenlib.GetEncoding("cl100k_base")
	if err != nil {
		t.Fatalf("failed to get oracle encoding: %v", err)
	}

	rapid.Check(t, func(t *rapid.T) {
		params := drawConverseParams(t)

		// --- Estimator under test ---
		got, err := estimator.EstimateTokens(context.Background(), params)
		if err != nil {
			t.Fatalf("EstimateTokens returned unexpected error: %v", err)
		}

		// --- Oracle: directly encode the concatenated text ---
		text := oracleExtractText(params)
		oracleTokens := oracle.Encode(text, nil, nil)
		expected := len(oracleTokens)

		if got != expected {
			t.Fatalf("TiktokenEstimator returned %d tokens, oracle expected %d (text len=%d)", got, expected, len(text))
		}
	})
}

// oracleExtractText independently extracts and concatenates text from
// ConverseParams using the same ordering as the estimator: system prompt,
// message text blocks, JSON-serialized tool config specs.
func oracleExtractText(params agent.ConverseParams) string {
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

// drawConverseParams generates a random ConverseParams with ASCII/Unicode text
// for property testing the TiktokenEstimator.
func drawConverseParams(t *rapid.T) agent.ConverseParams {
	// Generate system prompt with mixed ASCII/Unicode.
	system := rapid.String().Draw(t, "system")

	// Generate messages (0–10, kept smaller to avoid excessive test time).
	numMessages := rapid.IntRange(0, 10).Draw(t, "numMessages")
	messages := make([]agent.Message, numMessages)
	for i := range messages {
		messages[i] = drawMessage(t, i)
	}

	// Generate tool specs (0–5).
	numTools := rapid.IntRange(0, 5).Draw(t, "numTools")
	tools := make([]tool.Spec, numTools)
	for i := range tools {
		tools[i] = drawToolSpec(t, i)
	}

	return agent.ConverseParams{
		Messages:   messages,
		System:     system,
		ToolConfig: tools,
	}
}

// drawMessage generates a random Message with text content blocks.
func drawMessage(t *rapid.T, idx int) agent.Message {
	roles := []agent.Role{agent.RoleUser, agent.RoleAssistant}
	role := rapid.SampledFrom(roles).Draw(t, fmt.Sprintf("role_%d", idx))

	// Generate 1–3 content blocks per message.
	numBlocks := rapid.IntRange(1, 3).Draw(t, fmt.Sprintf("numBlocks_%d", idx))
	content := make([]agent.ContentBlock, numBlocks)
	for i := range content {
		// Use TextBlock exclusively to ensure text coverage for encoding.
		text := rapid.String().Draw(t, fmt.Sprintf("text_%d_%d", idx, i))
		content[i] = agent.TextBlock{Text: text}
	}

	return agent.Message{
		Role:    role,
		Content: content,
	}
}

// drawToolSpec generates a random tool.Spec with simple schema.
func drawToolSpec(t *rapid.T, idx int) tool.Spec {
	name := rapid.StringMatching(`[a-z_]{3,15}`).Draw(t, fmt.Sprintf("toolName_%d", idx))
	desc := rapid.String().Draw(t, fmt.Sprintf("toolDesc_%d", idx))

	// Generate a simple JSON schema with 0–3 properties.
	numProps := rapid.IntRange(0, 3).Draw(t, fmt.Sprintf("numProps_%d", idx))
	props := make(map[string]any, numProps)
	for i := 0; i < numProps; i++ {
		propName := rapid.StringMatching(`[a-z]{3,8}`).Draw(t, fmt.Sprintf("propName_%d_%d", idx, i))
		props[propName] = map[string]any{
			"type":        "string",
			"description": rapid.StringMatching(`[a-zA-Z0-9 ]{0,30}`).Draw(t, fmt.Sprintf("propDesc_%d_%d", idx, i)),
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
