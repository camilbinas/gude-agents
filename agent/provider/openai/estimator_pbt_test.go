package openai

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/tool"
	"pgregory.net/rapid"
)

// TestProperty_OpenAIDelegationEquivalence verifies that every ModelRequest is
// forwarded unchanged and that the delegate's exact result is returned.
func TestProperty_OpenAIDelegationEquivalence(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		req := drawModelRequest(t)
		want := rapid.IntRange(0, 1_000_000).Draw(t, "tokenCount")
		delegate := &stubTokenEstimator{count: want}

		got, err := NewEstimator(delegate).EstimateTokens(context.Background(), req)
		if err != nil {
			t.Fatalf("EstimateTokens() error = %v", err)
		}
		if got != want {
			t.Fatalf("EstimateTokens() = %d, want %d", got, want)
		}
		if !reflect.DeepEqual(delegate.got, req) {
			t.Fatalf("delegated request = %#v, want %#v", delegate.got, req)
		}
	})
}

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
		Messages:       messages,
		System:         system,
		Tools:          tools,
		CachingEnabled: rapid.Bool().Draw(t, "cachingEnabled"),
	}
}

func drawMessage(t *rapid.T, idx int) agent.Message {
	role := rapid.SampledFrom([]agent.Role{agent.RoleUser, agent.RoleAssistant}).Draw(t, fmt.Sprintf("role_%d", idx))
	numBlocks := rapid.IntRange(1, 3).Draw(t, fmt.Sprintf("numBlocks_%d", idx))
	content := make([]agent.ContentBlock, numBlocks)
	for i := range content {
		content[i] = agent.TextBlock{Text: rapid.String().Draw(t, fmt.Sprintf("text_%d_%d", idx, i))}
	}
	return agent.Message{Role: role, Content: content}
}

func drawToolSpec(t *rapid.T, idx int) tool.Spec {
	return tool.Spec{
		Name:        rapid.StringMatching(`[a-z_]{3,15}`).Draw(t, fmt.Sprintf("toolName_%d", idx)),
		Description: rapid.String().Draw(t, fmt.Sprintf("toolDesc_%d", idx)),
		InputSchema: map[string]any{"type": "object"},
	}
}
