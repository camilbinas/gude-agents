package eval

import (
	"encoding/json"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
)

func TestResultsForEvaluator_FiltersCorrectly(t *testing.T) {
	report := &EvalReport{
		Results: []CaseResults{
			{
				Results: []EvalResult{
					{EvaluatorName: "keyword_grounding", Score: 0.8},
					{EvaluatorName: "json_structure", Score: 1.0},
				},
			},
			{
				Results: []EvalResult{
					{EvaluatorName: "keyword_grounding", Score: 0.6},
					{EvaluatorName: "relevance", Score: 0.9},
				},
			},
		},
	}

	got := report.ResultsForEvaluator("keyword_grounding")
	if len(got) != 2 {
		t.Fatalf("expected 2 results, got %d", len(got))
	}
	if got[0].Score != 0.8 {
		t.Errorf("expected first score 0.8, got %f", got[0].Score)
	}
	if got[1].Score != 0.6 {
		t.Errorf("expected second score 0.6, got %f", got[1].Score)
	}
}

func TestResultsForEvaluator_NoMatch(t *testing.T) {
	report := &EvalReport{
		Results: []CaseResults{
			{
				Results: []EvalResult{
					{EvaluatorName: "keyword_grounding", Score: 0.8},
				},
			},
		},
	}

	got := report.ResultsForEvaluator("nonexistent")
	if len(got) != 0 {
		t.Fatalf("expected 0 results, got %d", len(got))
	}
}

func TestResultsForEvaluator_EmptyReport(t *testing.T) {
	report := &EvalReport{}
	got := report.ResultsForEvaluator("anything")
	if len(got) != 0 {
		t.Fatalf("expected 0 results, got %d", len(got))
	}
}

func TestApplyThreshold_DefaultThreshold(t *testing.T) {
	cfg := defaultEvaluatorConfig()
	if cfg.threshold != 0.5 {
		t.Fatalf("expected default threshold 0.5, got %f", cfg.threshold)
	}

	// Score exactly at threshold should pass.
	if !applyThreshold(0.5, cfg) {
		t.Error("expected score 0.5 to pass with default threshold 0.5")
	}
	// Score above threshold should pass.
	if !applyThreshold(0.8, cfg) {
		t.Error("expected score 0.8 to pass with default threshold 0.5")
	}
	// Score below threshold should fail.
	if applyThreshold(0.3, cfg) {
		t.Error("expected score 0.3 to fail with default threshold 0.5")
	}
}

func TestWithThreshold_OverridesDefault(t *testing.T) {
	cfg := applyOptions([]EvaluatorOption{WithThreshold(0.7)})
	if cfg.threshold != 0.7 {
		t.Fatalf("expected threshold 0.7, got %f", cfg.threshold)
	}

	if !applyThreshold(0.7, cfg) {
		t.Error("expected score 0.7 to pass with threshold 0.7")
	}
	if !applyThreshold(0.9, cfg) {
		t.Error("expected score 0.9 to pass with threshold 0.7")
	}
	if applyThreshold(0.6, cfg) {
		t.Error("expected score 0.6 to fail with threshold 0.7")
	}
}

func TestWithThreshold_ZeroThreshold(t *testing.T) {
	cfg := applyOptions([]EvaluatorOption{WithThreshold(0.0)})

	// Everything should pass with a zero threshold.
	if !applyThreshold(0.0, cfg) {
		t.Error("expected score 0.0 to pass with threshold 0.0")
	}
	if !applyThreshold(1.0, cfg) {
		t.Error("expected score 1.0 to pass with threshold 0.0")
	}
}

func TestEvalCase_JSONMarshal_OmitsOptionalFields(t *testing.T) {
	ec := EvalCase{
		Query:        "What is Go?",
		ActualOutput: "Go is a programming language.",
		RetrievedContext: []agent.Document{
			{Content: "Go was created at Google."},
		},
	}

	data, err := json.Marshal(ec)
	if err != nil {
		t.Fatalf("unexpected marshal error: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unexpected unmarshal error: %v", err)
	}

	// Required fields must be present.
	for _, key := range []string{"query", "actual_output", "retrieved_context"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("expected key %q in JSON output", key)
		}
	}

	// Optional fields with omitempty should be absent when zero-valued.
	for _, key := range []string{"reference_answer", "metadata"} {
		if _, ok := raw[key]; ok {
			t.Errorf("expected key %q to be omitted from JSON output", key)
		}
	}
}

func TestEvalCase_JSONMarshal_IncludesOptionalFieldsWhenSet(t *testing.T) {
	ec := EvalCase{
		Query:            "What is Go?",
		ActualOutput:     "Go is a programming language.",
		RetrievedContext: []agent.Document{},
		ReferenceAnswer:  "Go is a statically typed language.",
		Metadata:         map[string]string{"source": "test"},
	}

	data, err := json.Marshal(ec)
	if err != nil {
		t.Fatalf("unexpected marshal error: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unexpected unmarshal error: %v", err)
	}

	for _, key := range []string{"query", "actual_output", "retrieved_context", "reference_answer", "metadata"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("expected key %q in JSON output", key)
		}
	}
}

func TestEvalCase_JSONRoundTrip(t *testing.T) {
	ec := EvalCase{
		Query:        "What is Go?",
		ActualOutput: "Go is a programming language.",
		RetrievedContext: []agent.Document{
			{Content: "Go was created at Google.", Metadata: map[string]string{"page": "1"}},
		},
		ReferenceAnswer: "Go is a statically typed language.",
		Metadata:        map[string]string{"source": "test"},
	}

	data, err := json.Marshal(ec)
	if err != nil {
		t.Fatalf("unexpected marshal error: %v", err)
	}

	var got EvalCase
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unexpected unmarshal error: %v", err)
	}

	if got.Query != ec.Query {
		t.Errorf("Query: expected %q, got %q", ec.Query, got.Query)
	}
	if got.ActualOutput != ec.ActualOutput {
		t.Errorf("ActualOutput: expected %q, got %q", ec.ActualOutput, got.ActualOutput)
	}
	if got.ReferenceAnswer != ec.ReferenceAnswer {
		t.Errorf("ReferenceAnswer: expected %q, got %q", ec.ReferenceAnswer, got.ReferenceAnswer)
	}
	if len(got.RetrievedContext) != len(ec.RetrievedContext) {
		t.Fatalf("RetrievedContext length: expected %d, got %d", len(ec.RetrievedContext), len(got.RetrievedContext))
	}
	if got.RetrievedContext[0].Content != ec.RetrievedContext[0].Content {
		t.Errorf("RetrievedContext[0].Content: expected %q, got %q", ec.RetrievedContext[0].Content, got.RetrievedContext[0].Content)
	}
	if got.Metadata["source"] != ec.Metadata["source"] {
		t.Errorf("Metadata[source]: expected %q, got %q", ec.Metadata["source"], got.Metadata["source"])
	}
}
