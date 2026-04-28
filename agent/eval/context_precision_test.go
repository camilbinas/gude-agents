package eval

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
)

func TestContextPrecision_AllDocumentsRelevant(t *testing.T) {
	provider := newScriptedProvider(
		[]*agent.ProviderResponse{
			{Text: `{"judgments": [{"document_index": 0, "relevant": true}, {"document_index": 1, "relevant": true}, {"document_index": 2, "relevant": true}]}`},
		},
		[]error{nil},
	)

	cp := NewContextPrecision(provider)
	result, err := cp.Evaluate(context.Background(), EvalCase{
		Query:           "What is Go?",
		ReferenceAnswer: "Go is a programming language created at Google.",
		RetrievedContext: []agent.Document{
			{Content: "Go is a statically typed language."},
			{Content: "Go was created at Google in 2009."},
			{Content: "Go has built-in concurrency support."},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Score != 1.0 {
		t.Errorf("expected score 1.0 when all documents relevant, got %f", result.Score)
	}
	if result.EvaluatorName != "context_precision" {
		t.Errorf("expected evaluator name %q, got %q", "context_precision", result.EvaluatorName)
	}
}

func TestContextPrecision_NoDocumentsRelevant(t *testing.T) {
	provider := newScriptedProvider(
		[]*agent.ProviderResponse{
			{Text: `{"judgments": [{"document_index": 0, "relevant": false}, {"document_index": 1, "relevant": false}]}`},
		},
		[]error{nil},
	)

	cp := NewContextPrecision(provider)
	result, err := cp.Evaluate(context.Background(), EvalCase{
		Query:           "What is Go?",
		ReferenceAnswer: "Go is a programming language.",
		RetrievedContext: []agent.Document{
			{Content: "Python is a dynamic language."},
			{Content: "Java runs on the JVM."},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Score != 0.0 {
		t.Errorf("expected score 0.0 when no documents relevant, got %f", result.Score)
	}
}

func TestContextPrecision_MixedRelevance(t *testing.T) {
	// Relevance vector: [true, false, true]
	// k=0: relevant, relevantSoFar=1, P@0 = 1/1 = 1.0
	// k=1: not relevant, skip
	// k=2: relevant, relevantSoFar=2, P@2 = 2/3 ≈ 0.6667
	// R=2, AP = (1.0 + 0.6667) / 2 ≈ 0.8333
	provider := newScriptedProvider(
		[]*agent.ProviderResponse{
			{Text: `{"judgments": [{"document_index": 0, "relevant": true}, {"document_index": 1, "relevant": false}, {"document_index": 2, "relevant": true}]}`},
		},
		[]error{nil},
	)

	cp := NewContextPrecision(provider)
	result, err := cp.Evaluate(context.Background(), EvalCase{
		Query:           "What is Go?",
		ReferenceAnswer: "Go is a programming language created at Google.",
		RetrievedContext: []agent.Document{
			{Content: "Go is a statically typed language."},
			{Content: "Python is interpreted."},
			{Content: "Go was created at Google."},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// AP = (1.0 + 2.0/3.0) / 2.0 = (1.0 + 0.6667) / 2.0 ≈ 0.8333
	expected := (1.0 + 2.0/3.0) / 2.0
	if math.Abs(result.Score-expected) > 1e-9 {
		t.Errorf("expected score %f, got %f", expected, result.Score)
	}
}

func TestContextPrecision_PerDocumentJudgmentsInExplanation(t *testing.T) {
	provider := newScriptedProvider(
		[]*agent.ProviderResponse{
			{Text: `{"judgments": [{"document_index": 0, "relevant": true}, {"document_index": 1, "relevant": false}, {"document_index": 2, "relevant": true}]}`},
		},
		[]error{nil},
	)

	cp := NewContextPrecision(provider)
	result, err := cp.Evaluate(context.Background(), EvalCase{
		Query:           "What is Go?",
		ReferenceAnswer: "Go is a programming language.",
		RetrievedContext: []agent.Document{
			{Content: "Go is statically typed."},
			{Content: "Python is dynamic."},
			{Content: "Go was created at Google."},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result.Explanation, "doc 0: relevant") {
		t.Errorf("expected explanation to contain 'doc 0: relevant', got %q", result.Explanation)
	}
	if !strings.Contains(result.Explanation, "doc 1: not relevant") {
		t.Errorf("expected explanation to contain 'doc 1: not relevant', got %q", result.Explanation)
	}
	if !strings.Contains(result.Explanation, "doc 2: relevant") {
		t.Errorf("expected explanation to contain 'doc 2: relevant', got %q", result.Explanation)
	}
}

func TestContextPrecision_ProviderError(t *testing.T) {
	providerErr := errors.New("service unavailable")
	provider := newScriptedProvider(
		[]*agent.ProviderResponse{nil},
		[]error{providerErr},
	)

	cp := NewContextPrecision(provider)
	_, err := cp.Evaluate(context.Background(), EvalCase{
		Query:           "What is Go?",
		ReferenceAnswer: "Go is a programming language.",
		RetrievedContext: []agent.Document{
			{Content: "Go is a language."},
		},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, providerErr) {
		t.Errorf("expected wrapped provider error, got %v", err)
	}
}
