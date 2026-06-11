package openai

import (
	"context"
	"fmt"
	"testing"

	agent "github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/tokencount/tiktoken"
	"github.com/camilbinas/gude-agents/agent/tool"
	"pgregory.net/rapid"
)

// Feature: token-estimation, Property 4: OpenAI estimator delegation equivalence

// **Validates: Requirements 4.4**

// TestProperty_OpenAIDelegationEquivalence verifies that for any ConverseParams,
// the OpenAI provider's EstimateTokens returns exactly the same value as calling
// TiktokenEstimator.EstimateTokens with the same params.
func TestProperty_OpenAIDelegationEquivalence(t *testing.T) {
	tik, err := tiktoken.New("cl100k_base")
	if err != nil {
		t.Fatalf("failed to create TiktokenEstimator: %v", err)
	}

	oaiEstimator := NewEstimator(tik)

	rapid.Check(t, func(t *rapid.T) {
		params := drawConverseParams(t)

		// Call the OpenAI estimator (delegates to tiktoken).
		oaiResult, oaiErr := oaiEstimator.EstimateTokens(context.Background(), params)
		if oaiErr != nil {
			t.Fatalf("OpenAI EstimateTokens returned unexpected error: %v", oaiErr)
		}

		// Call the tiktoken estimator directly.
		tikResult, tikErr := tik.EstimateTokens(context.Background(), params)
		if tikErr != nil {
			t.Fatalf("Tiktoken EstimateTokens returned unexpected error: %v", tikErr)
		}

		// Both must produce the exact same result.
		if oaiResult != tikResult {
			t.Fatalf("OpenAI estimator returned %d, tiktoken returned %d — delegation mismatch", oaiResult, tikResult)
		}
	})
}

// drawConverseParams generates a random ConverseParams with ASCII/Unicode text
// for property testing.
func drawConverseParams(t *rapid.T) agent.ConverseParams {
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

	numBlocks := rapid.IntRange(1, 3).Draw(t, fmt.Sprintf("numBlocks_%d", idx))
	content := make([]agent.ContentBlock, numBlocks)
	for i := range content {
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
