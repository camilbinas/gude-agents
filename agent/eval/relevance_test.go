package eval

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
)

func TestRelevance_HighRelevance(t *testing.T) {
	provider := newScriptedProvider(
		[]*agent.ProviderResponse{
			{Text: `{"score": 0.95, "justification": "The answer directly and thoroughly addresses the question about Go concurrency."}`},
		},
		[]error{nil},
	)

	r := NewRelevance(provider)
	result, err := r.Evaluate(context.Background(), EvalCase{
		Query:        "How does Go handle concurrency?",
		ActualOutput: "Go uses goroutines and channels for concurrency. Goroutines are lightweight threads managed by the Go runtime.",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Score != 0.95 {
		t.Errorf("expected score 0.95, got %f", result.Score)
	}
	if !result.Pass {
		t.Errorf("expected pass to be true with default threshold 0.5, got false")
	}
	if result.EvaluatorName != "relevance" {
		t.Errorf("expected evaluator name %q, got %q", "relevance", result.EvaluatorName)
	}
}

func TestRelevance_LowRelevance(t *testing.T) {
	provider := newScriptedProvider(
		[]*agent.ProviderResponse{
			{Text: `{"score": 0.05, "justification": "The answer discusses Python, not Go, and is completely off-topic."}`},
		},
		[]error{nil},
	)

	r := NewRelevance(provider)
	result, err := r.Evaluate(context.Background(), EvalCase{
		Query:        "How does Go handle concurrency?",
		ActualOutput: "Python uses the GIL for thread safety and asyncio for async programming.",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Score != 0.05 {
		t.Errorf("expected score 0.05, got %f", result.Score)
	}
	if result.Pass {
		t.Errorf("expected pass to be false with default threshold 0.5, got true")
	}
}

func TestRelevance_JustificationInExplanation(t *testing.T) {
	justification := "The answer partially addresses the question but misses key details about channels."
	provider := newScriptedProvider(
		[]*agent.ProviderResponse{
			{Text: `{"score": 0.6, "justification": "` + justification + `"}`},
		},
		[]error{nil},
	)

	r := NewRelevance(provider)
	result, err := r.Evaluate(context.Background(), EvalCase{
		Query:        "How does Go handle concurrency?",
		ActualOutput: "Go uses goroutines for concurrency.",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Explanation != justification {
		t.Errorf("expected explanation to be the justification %q, got %q", justification, result.Explanation)
	}
}

func TestRelevance_ProviderError(t *testing.T) {
	providerErr := errors.New("service unavailable")
	provider := newScriptedProvider(
		[]*agent.ProviderResponse{nil},
		[]error{providerErr},
	)

	r := NewRelevance(provider)
	_, err := r.Evaluate(context.Background(), EvalCase{
		Query:        "What is Go?",
		ActualOutput: "Go is a programming language.",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, providerErr) {
		t.Errorf("expected wrapped provider error, got %v", err)
	}
}

func TestRelevance_MalformedResponse(t *testing.T) {
	provider := newScriptedProvider(
		[]*agent.ProviderResponse{
			{Text: `not valid json`},
		},
		[]error{nil},
	)

	r := NewRelevance(provider)
	_, err := r.Evaluate(context.Background(), EvalCase{
		Query:        "What is Go?",
		ActualOutput: "Go is a programming language.",
	})
	if err == nil {
		t.Fatal("expected error for malformed response, got nil")
	}
	if !strings.Contains(err.Error(), "failed to parse response") {
		t.Errorf("expected parse error message, got %q", err.Error())
	}
}
