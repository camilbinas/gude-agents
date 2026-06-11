package tiktoken

import (
	"context"
	"testing"

	agent "github.com/camilbinas/gude-agents/agent"
)

// Requirements: 3.3, 3.5

func TestNew_ValidEncoding(t *testing.T) {
	est, err := New("cl100k_base")
	if err != nil {
		t.Fatalf("expected no error for cl100k_base, got: %v", err)
	}
	if est == nil {
		t.Fatal("expected non-nil Estimator")
	}
}

func TestNew_InvalidEncoding(t *testing.T) {
	est, err := New("invalid_encoding")
	if err == nil {
		t.Fatal("expected error for invalid encoding name, got nil")
	}
	if est != nil {
		t.Fatal("expected nil Estimator on error")
	}
}

func TestEstimateTokens_Hello(t *testing.T) {
	est, err := New("cl100k_base")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	// "hello" is 1 token in cl100k_base.
	got, err := est.EstimateTokens(context.Background(), agent.ConverseParams{
		System: "hello",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 1 {
		t.Fatalf("expected 1 token for 'hello', got %d", got)
	}
}

func TestEstimateTokens_HelloWorld(t *testing.T) {
	est, err := New("cl100k_base")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	// "hello world" is 2 tokens in cl100k_base.
	got, err := est.EstimateTokens(context.Background(), agent.ConverseParams{
		System: "hello world",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 2 {
		t.Fatalf("expected 2 tokens for 'hello world', got %d", got)
	}
}

func TestEstimateTokens_EmptyParams(t *testing.T) {
	est, err := New("cl100k_base")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	got, err := est.EstimateTokens(context.Background(), agent.ConverseParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 0 {
		t.Fatalf("expected 0 tokens for empty params, got %d", got)
	}
}

func TestEstimateTokens_MessageContent(t *testing.T) {
	est, err := New("cl100k_base")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	// "hello" in a message text block should also produce 1 token.
	got, err := est.EstimateTokens(context.Background(), agent.ConverseParams{
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hello"}}},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 1 {
		t.Fatalf("expected 1 token for 'hello' in message, got %d", got)
	}
}

func TestEstimateTokens_CombinedSources(t *testing.T) {
	est, err := New("cl100k_base")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	// System "hello" + message "world" concatenated = "helloworld".
	// "helloworld" is 1 token in cl100k_base (single BPE token).
	// This verifies concatenation order: system first, then messages.
	params := agent.ConverseParams{
		System: "hello",
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: " world"}}},
		},
	}

	got, err := est.EstimateTokens(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// "hello world" as concatenated is 2 tokens (hello + space world).
	if got != 2 {
		t.Fatalf("expected 2 tokens for 'hello' + ' world', got %d", got)
	}
}

func TestEstimateTokens_NonNegative(t *testing.T) {
	est, err := New("cl100k_base")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	got, err := est.EstimateTokens(context.Background(), agent.ConverseParams{
		System: "This is a longer sentence to tokenize with multiple tokens.",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got < 0 {
		t.Fatalf("expected non-negative token count, got %d", got)
	}
	if got == 0 {
		t.Fatal("expected non-zero token count for non-empty text")
	}
}
