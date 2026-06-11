package openai

import (
	"context"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/tokencount/tiktoken"
	"github.com/camilbinas/gude-agents/agent/tool"
)

func TestEstimator_DelegatesToTiktoken(t *testing.T) {
	tik, err := tiktoken.New("o200k_base")
	if err != nil {
		t.Fatalf("failed to create tiktoken estimator: %v", err)
	}

	est := NewEstimator(tik)
	params := agent.ConverseParams{
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "Hello, world!"}}},
		},
		System: "You are helpful.",
	}

	// Get result from OpenAI estimator.
	openaiCount, err := est.EstimateTokens(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error from OpenAI estimator: %v", err)
	}

	// Get result directly from tiktoken estimator.
	tikCount, err := tik.EstimateTokens(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error from tiktoken estimator: %v", err)
	}

	if openaiCount != tikCount {
		t.Errorf("OpenAI estimator (%d) != tiktoken estimator (%d)", openaiCount, tikCount)
	}
}

func TestEstimator_DelegatesWithToolConfig(t *testing.T) {
	tik, err := tiktoken.New("o200k_base")
	if err != nil {
		t.Fatalf("failed to create tiktoken estimator: %v", err)
	}

	est := NewEstimator(tik)
	params := agent.ConverseParams{
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "Use a tool"}}},
		},
		ToolConfig: []tool.Spec{
			{
				Name:        "search",
				Description: "Search the web",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{"type": "string"},
					},
				},
			},
		},
	}

	openaiCount, err := est.EstimateTokens(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error from OpenAI estimator: %v", err)
	}

	tikCount, err := tik.EstimateTokens(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error from tiktoken estimator: %v", err)
	}

	if openaiCount != tikCount {
		t.Errorf("OpenAI estimator (%d) != tiktoken estimator (%d)", openaiCount, tikCount)
	}
}

func TestEstimator_DelegatesEmptyParams(t *testing.T) {
	tik, err := tiktoken.New("o200k_base")
	if err != nil {
		t.Fatalf("failed to create tiktoken estimator: %v", err)
	}

	est := NewEstimator(tik)
	params := agent.ConverseParams{}

	openaiCount, err := est.EstimateTokens(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error from OpenAI estimator: %v", err)
	}

	tikCount, err := tik.EstimateTokens(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error from tiktoken estimator: %v", err)
	}

	if openaiCount != tikCount {
		t.Errorf("OpenAI estimator (%d) != tiktoken estimator (%d)", openaiCount, tikCount)
	}

	// With empty params, both should return 0.
	if openaiCount != 0 {
		t.Errorf("expected 0 tokens for empty params, got %d", openaiCount)
	}
}

func TestEstimator_NonNegativeResult(t *testing.T) {
	tik, err := tiktoken.New("cl100k_base")
	if err != nil {
		t.Fatalf("failed to create tiktoken estimator: %v", err)
	}

	est := NewEstimator(tik)
	params := agent.ConverseParams{
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "A longer message with multiple words to test token counting."}}},
		},
		System: "System prompt with some content.",
	}

	count, err := est.EstimateTokens(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count < 0 {
		t.Errorf("expected non-negative token count, got %d", count)
	}
	if count == 0 {
		t.Error("expected non-zero token count for non-empty input")
	}
}
