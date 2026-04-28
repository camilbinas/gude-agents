package eval

import (
	"context"
	"math"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
)

// idExtractor is a helper that extracts the "id" key from document metadata.
func idExtractor(d agent.Document) string {
	return d.Metadata["id"]
}

// makeDoc creates an agent.Document with the given id in its metadata.
func makeDoc(id string) agent.Document {
	return agent.Document{
		Content:  "content for " + id,
		Metadata: map[string]string{"id": id},
	}
}

func TestRetrievalOrdering_PerfectOrdering(t *testing.T) {
	expected := []string{"a", "b", "c"}
	ro, err := NewRetrievalOrdering(expected, idExtractor)
	if err != nil {
		t.Fatalf("unexpected constructor error: %v", err)
	}

	result, err := ro.Evaluate(context.Background(), EvalCase{
		RetrievedContext: []agent.Document{
			makeDoc("a"),
			makeDoc("b"),
			makeDoc("c"),
		},
	})
	if err != nil {
		t.Fatalf("unexpected evaluate error: %v", err)
	}

	if result.Score != 1.0 {
		t.Errorf("expected score 1.0 for perfect ordering, got %f", result.Score)
	}
	if result.EvaluatorName != "retrieval_ordering" {
		t.Errorf("expected evaluator name %q, got %q", "retrieval_ordering", result.EvaluatorName)
	}
}

func TestRetrievalOrdering_ReversedOrdering(t *testing.T) {
	expected := []string{"a", "b", "c"}
	ro, err := NewRetrievalOrdering(expected, idExtractor)
	if err != nil {
		t.Fatalf("unexpected constructor error: %v", err)
	}

	result, err := ro.Evaluate(context.Background(), EvalCase{
		RetrievedContext: []agent.Document{
			makeDoc("c"),
			makeDoc("b"),
			makeDoc("a"),
		},
	})
	if err != nil {
		t.Fatalf("unexpected evaluate error: %v", err)
	}

	if result.Score >= 1.0 {
		t.Errorf("expected score < 1.0 for reversed ordering, got %f", result.Score)
	}
	if result.Score <= 0.0 {
		t.Errorf("expected score > 0.0 for reversed ordering (all docs present), got %f", result.Score)
	}
}

func TestRetrievalOrdering_NoExpectedDocumentsFound(t *testing.T) {
	expected := []string{"a", "b", "c"}
	ro, err := NewRetrievalOrdering(expected, idExtractor)
	if err != nil {
		t.Fatalf("unexpected constructor error: %v", err)
	}

	result, err := ro.Evaluate(context.Background(), EvalCase{
		RetrievedContext: []agent.Document{
			makeDoc("x"),
			makeDoc("y"),
			makeDoc("z"),
		},
	})
	if err != nil {
		t.Fatalf("unexpected evaluate error: %v", err)
	}

	if result.Score != 0.0 {
		t.Errorf("expected score 0.0 when no expected documents found, got %f", result.Score)
	}
}

func TestRetrievalOrdering_SingleDocument(t *testing.T) {
	expected := []string{"a"}
	ro, err := NewRetrievalOrdering(expected, idExtractor)
	if err != nil {
		t.Fatalf("unexpected constructor error: %v", err)
	}

	result, err := ro.Evaluate(context.Background(), EvalCase{
		RetrievedContext: []agent.Document{
			makeDoc("a"),
		},
	})
	if err != nil {
		t.Fatalf("unexpected evaluate error: %v", err)
	}

	if result.Score != 1.0 {
		t.Errorf("expected score 1.0 for single matching document, got %f", result.Score)
	}
}

func TestRetrievalOrdering_PartialOverlap(t *testing.T) {
	expected := []string{"a", "b", "c", "d"}
	ro, err := NewRetrievalOrdering(expected, idExtractor)
	if err != nil {
		t.Fatalf("unexpected constructor error: %v", err)
	}

	// Only "a" and "c" are present; "b" and "d" are missing, replaced by unknowns.
	result, err := ro.Evaluate(context.Background(), EvalCase{
		RetrievedContext: []agent.Document{
			makeDoc("a"),
			makeDoc("x"),
			makeDoc("c"),
			makeDoc("y"),
		},
	})
	if err != nil {
		t.Fatalf("unexpected evaluate error: %v", err)
	}

	// Score should be between 0 and 1 (partial match, not perfect).
	if result.Score <= 0.0 || result.Score >= 1.0 {
		t.Errorf("expected score in (0.0, 1.0) for partial overlap, got %f", result.Score)
	}
}

func TestRetrievalOrdering_EmptyExpectedIDs_ReturnsError(t *testing.T) {
	_, err := NewRetrievalOrdering([]string{}, idExtractor)
	if err == nil {
		t.Fatal("expected error for empty expectedIDs, got nil")
	}
}

func TestRetrievalOrdering_NilIDExtractor_ReturnsError(t *testing.T) {
	_, err := NewRetrievalOrdering([]string{"a"}, nil)
	if err == nil {
		t.Fatal("expected error for nil idExtractor, got nil")
	}
}

func TestRetrievalOrdering_EmptyRetrievedContext(t *testing.T) {
	expected := []string{"a", "b"}
	ro, err := NewRetrievalOrdering(expected, idExtractor)
	if err != nil {
		t.Fatalf("unexpected constructor error: %v", err)
	}

	result, err := ro.Evaluate(context.Background(), EvalCase{
		RetrievedContext: []agent.Document{},
	})
	if err != nil {
		t.Fatalf("unexpected evaluate error: %v", err)
	}

	if result.Score != 0.0 {
		t.Errorf("expected score 0.0 for empty retrieved context, got %f", result.Score)
	}
}

func TestRetrievalOrdering_WithCustomThreshold(t *testing.T) {
	expected := []string{"a", "b", "c"}
	ro, err := NewRetrievalOrdering(expected, idExtractor, WithThreshold(0.9))
	if err != nil {
		t.Fatalf("unexpected constructor error: %v", err)
	}

	// Perfect ordering should pass even a high threshold.
	result, err := ro.Evaluate(context.Background(), EvalCase{
		RetrievedContext: []agent.Document{
			makeDoc("a"),
			makeDoc("b"),
			makeDoc("c"),
		},
	})
	if err != nil {
		t.Fatalf("unexpected evaluate error: %v", err)
	}

	if !result.Pass {
		t.Error("expected perfect ordering to pass with threshold 0.9")
	}

	// Reversed ordering should fail with a high threshold.
	result, err = ro.Evaluate(context.Background(), EvalCase{
		RetrievedContext: []agent.Document{
			makeDoc("c"),
			makeDoc("b"),
			makeDoc("a"),
		},
	})
	if err != nil {
		t.Fatalf("unexpected evaluate error: %v", err)
	}

	if result.Pass {
		t.Errorf("expected reversed ordering to fail with threshold 0.9, score was %f", result.Score)
	}
}

func TestRetrievalOrdering_ScoreInRange(t *testing.T) {
	expected := []string{"a", "b", "c"}
	ro, err := NewRetrievalOrdering(expected, idExtractor)
	if err != nil {
		t.Fatalf("unexpected constructor error: %v", err)
	}

	cases := [][]agent.Document{
		{makeDoc("a"), makeDoc("b"), makeDoc("c")},
		{makeDoc("c"), makeDoc("b"), makeDoc("a")},
		{makeDoc("b"), makeDoc("a"), makeDoc("c")},
		{makeDoc("a"), makeDoc("c"), makeDoc("b")},
		{makeDoc("x"), makeDoc("y"), makeDoc("z")},
		{makeDoc("a")},
	}

	for i, docs := range cases {
		result, err := ro.Evaluate(context.Background(), EvalCase{
			RetrievedContext: docs,
		})
		if err != nil {
			t.Fatalf("case %d: unexpected evaluate error: %v", i, err)
		}
		if result.Score < 0.0 || result.Score > 1.0 {
			t.Errorf("case %d: expected score in [0.0, 1.0], got %f", i, result.Score)
		}
	}
}

func TestRetrievalOrdering_Name(t *testing.T) {
	ro, err := NewRetrievalOrdering([]string{"a"}, idExtractor)
	if err != nil {
		t.Fatalf("unexpected constructor error: %v", err)
	}
	if ro.Name() != "retrieval_ordering" {
		t.Errorf("expected name %q, got %q", "retrieval_ordering", ro.Name())
	}
}

func TestComputeDCG(t *testing.T) {
	// DCG for grades [3, 2, 1]:
	// 3/log2(2) + 2/log2(3) + 1/log2(4) = 3/1 + 2/1.585 + 1/2 = 3 + 1.2619 + 0.5 = 4.7619
	grades := []float64{3, 2, 1}
	got := computeDCG(grades)
	expected := 3.0/math.Log2(2) + 2.0/math.Log2(3) + 1.0/math.Log2(4)
	if math.Abs(got-expected) > 1e-9 {
		t.Errorf("expected DCG %f, got %f", expected, got)
	}
}

func TestComputeDCG_Empty(t *testing.T) {
	got := computeDCG([]float64{})
	if got != 0.0 {
		t.Errorf("expected DCG 0.0 for empty grades, got %f", got)
	}
}
