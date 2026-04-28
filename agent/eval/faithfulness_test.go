package eval

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
)

// scriptedProvider is a mock agent.Provider that returns pre-defined responses
// in sequence. Each call to Converse returns the next response from the list.
type scriptedProvider struct {
	responses []*agent.ProviderResponse
	errors    []error
	callCount int
}

func newScriptedProvider(responses []*agent.ProviderResponse, errs []error) *scriptedProvider {
	return &scriptedProvider{
		responses: responses,
		errors:    errs,
	}
}

func (sp *scriptedProvider) Converse(_ context.Context, _ agent.ConverseParams) (*agent.ProviderResponse, error) {
	idx := sp.callCount
	sp.callCount++
	if idx < len(sp.errors) && sp.errors[idx] != nil {
		return nil, sp.errors[idx]
	}
	if idx < len(sp.responses) {
		return sp.responses[idx], nil
	}
	return nil, errors.New("scriptedProvider: no more responses")
}

func (sp *scriptedProvider) ConverseStream(_ context.Context, _ agent.ConverseParams, _ agent.StreamCallback) (*agent.ProviderResponse, error) {
	return nil, errors.New("scriptedProvider: ConverseStream not implemented")
}

func TestFaithfulness_AllClaimsSupported(t *testing.T) {
	provider := newScriptedProvider(
		[]*agent.ProviderResponse{
			// Step 1: claims extraction
			{Text: `{"claims": ["Go is a language", "Go was created at Google"]}`},
			// Step 2: verdict judgment
			{Text: `{"verdicts": [{"claim": "Go is a language", "verdict": "supported"}, {"claim": "Go was created at Google", "verdict": "supported"}]}`},
		},
		[]error{nil, nil},
	)

	f := NewFaithfulness(provider)
	result, err := f.Evaluate(context.Background(), EvalCase{
		ActualOutput:     "Go is a language created at Google.",
		RetrievedContext: []agent.Document{{Content: "Go is a programming language created at Google."}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Score != 1.0 {
		t.Errorf("expected score 1.0, got %f", result.Score)
	}
	if result.EvaluatorName != "faithfulness" {
		t.Errorf("expected evaluator name %q, got %q", "faithfulness", result.EvaluatorName)
	}
}

func TestFaithfulness_NoClaimsSupported(t *testing.T) {
	provider := newScriptedProvider(
		[]*agent.ProviderResponse{
			// Step 1: claims extraction
			{Text: `{"claims": ["Go is slow", "Go has no generics"]}`},
			// Step 2: verdict judgment — all unsupported
			{Text: `{"verdicts": [{"claim": "Go is slow", "verdict": "unsupported"}, {"claim": "Go has no generics", "verdict": "unsupported"}]}`},
		},
		[]error{nil, nil},
	)

	f := NewFaithfulness(provider)
	result, err := f.Evaluate(context.Background(), EvalCase{
		ActualOutput:     "Go is slow and has no generics.",
		RetrievedContext: []agent.Document{{Content: "Go is fast and supports generics since 1.18."}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Score != 0.0 {
		t.Errorf("expected score 0.0, got %f", result.Score)
	}
	if !strings.Contains(result.Explanation, "Go is slow") {
		t.Errorf("expected explanation to mention unsupported claim, got %q", result.Explanation)
	}
	if !strings.Contains(result.Explanation, "Go has no generics") {
		t.Errorf("expected explanation to mention unsupported claim, got %q", result.Explanation)
	}
}

func TestFaithfulness_MixedClaims(t *testing.T) {
	provider := newScriptedProvider(
		[]*agent.ProviderResponse{
			// Step 1: claims extraction — 3 claims
			{Text: `{"claims": ["Go is compiled", "Go is dynamically typed", "Go has garbage collection"]}`},
			// Step 2: verdict judgment — 2 supported, 1 unsupported
			{Text: `{"verdicts": [{"claim": "Go is compiled", "verdict": "supported"}, {"claim": "Go is dynamically typed", "verdict": "unsupported"}, {"claim": "Go has garbage collection", "verdict": "supported"}]}`},
		},
		[]error{nil, nil},
	)

	f := NewFaithfulness(provider)
	result, err := f.Evaluate(context.Background(), EvalCase{
		ActualOutput:     "Go is compiled, dynamically typed, and has garbage collection.",
		RetrievedContext: []agent.Document{{Content: "Go is a statically typed, compiled language with garbage collection."}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := 2.0 / 3.0
	if math.Abs(result.Score-expected) > 1e-9 {
		t.Errorf("expected score %f, got %f", expected, result.Score)
	}
	if !strings.Contains(result.Explanation, "Go is dynamically typed") {
		t.Errorf("expected explanation to mention unsupported claim, got %q", result.Explanation)
	}
}

func TestFaithfulness_NoClaimsFound(t *testing.T) {
	provider := newScriptedProvider(
		[]*agent.ProviderResponse{
			// Step 1: claims extraction — empty claims
			{Text: `{"claims": []}`},
		},
		[]error{nil},
	)

	f := NewFaithfulness(provider)
	result, err := f.Evaluate(context.Background(), EvalCase{
		ActualOutput:     "Hello!",
		RetrievedContext: []agent.Document{{Content: "Some context."}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Score != 1.0 {
		t.Errorf("expected score 1.0 when no claims found, got %f", result.Score)
	}
	if result.EvaluatorName != "faithfulness" {
		t.Errorf("expected evaluator name %q, got %q", "faithfulness", result.EvaluatorName)
	}
	if !strings.Contains(result.Explanation, "no claims") {
		t.Errorf("expected explanation about no claims, got %q", result.Explanation)
	}
}

func TestFaithfulness_ProviderErrorOnClaimsExtraction(t *testing.T) {
	providerErr := errors.New("network timeout")
	provider := newScriptedProvider(
		[]*agent.ProviderResponse{nil},
		[]error{providerErr},
	)

	f := NewFaithfulness(provider)
	_, err := f.Evaluate(context.Background(), EvalCase{
		ActualOutput:     "Some output.",
		RetrievedContext: []agent.Document{{Content: "Some context."}},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, providerErr) {
		t.Errorf("expected wrapped provider error, got %v", err)
	}
}

func TestFaithfulness_ProviderErrorOnVerdictJudgment(t *testing.T) {
	providerErr := errors.New("rate limit exceeded")
	provider := newScriptedProvider(
		[]*agent.ProviderResponse{
			// Step 1 succeeds
			{Text: `{"claims": ["Go is fast"]}`},
			nil, // Step 2 fails
		},
		[]error{nil, providerErr},
	)

	f := NewFaithfulness(provider)
	_, err := f.Evaluate(context.Background(), EvalCase{
		ActualOutput:     "Go is fast.",
		RetrievedContext: []agent.Document{{Content: "Go is a fast language."}},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, providerErr) {
		t.Errorf("expected wrapped provider error, got %v", err)
	}
}

func TestFaithfulness_MalformedClaimsResponse(t *testing.T) {
	provider := newScriptedProvider(
		[]*agent.ProviderResponse{
			// Step 1: malformed JSON
			{Text: `not valid json at all`},
		},
		[]error{nil},
	)

	f := NewFaithfulness(provider)
	_, err := f.Evaluate(context.Background(), EvalCase{
		ActualOutput:     "Some output.",
		RetrievedContext: []agent.Document{{Content: "Some context."}},
	})
	if err == nil {
		t.Fatal("expected error for malformed claims response, got nil")
	}
	if !strings.Contains(err.Error(), "failed to parse claims response") {
		t.Errorf("expected parse error message, got %q", err.Error())
	}
}

func TestFaithfulness_MalformedVerdictsResponse(t *testing.T) {
	provider := newScriptedProvider(
		[]*agent.ProviderResponse{
			// Step 1: valid claims
			{Text: `{"claims": ["Go is fast"]}`},
			// Step 2: malformed JSON
			{Text: `{broken json`},
		},
		[]error{nil, nil},
	)

	f := NewFaithfulness(provider)
	_, err := f.Evaluate(context.Background(), EvalCase{
		ActualOutput:     "Go is fast.",
		RetrievedContext: []agent.Document{{Content: "Go is a fast language."}},
	})
	if err == nil {
		t.Fatal("expected error for malformed verdicts response, got nil")
	}
	if !strings.Contains(err.Error(), "failed to parse verdicts response") {
		t.Errorf("expected parse error message, got %q", err.Error())
	}
}
