package eval

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"pgregory.net/rapid"
)

// Feature: llm-evaluation, Property 6: Threshold determines pass/fail
// Validates: Requirements 12.2, 12.3
func TestProperty_ThresholdPassFail(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		score := rapid.Float64Range(0.0, 1.0).Draw(t, "score")
		threshold := rapid.Float64Range(0.0, 1.0).Draw(t, "threshold")

		result := applyThreshold(score, evaluatorConfig{threshold: threshold})
		expected := score >= threshold

		if result != expected {
			t.Fatalf("applyThreshold(%v, {threshold: %v}) = %v, want %v",
				score, threshold, result, expected)
		}
	})
}

// genDocument generates a random agent.Document with random Content and Metadata.
func genDocument(t *rapid.T, label string) agent.Document {
	content := rapid.StringMatching(`[a-zA-Z0-9 ]{0,80}`).Draw(t, label+"_content")
	numMeta := rapid.IntRange(0, 3).Draw(t, label+"_numMeta")
	meta := make(map[string]string, numMeta)
	for i := 0; i < numMeta; i++ {
		key := rapid.StringMatching(`[a-z]{1,10}`).Draw(t, fmt.Sprintf("%s_metaKey_%d", label, i))
		val := rapid.StringMatching(`[a-zA-Z0-9]{0,20}`).Draw(t, fmt.Sprintf("%s_metaVal_%d", label, i))
		meta[key] = val
	}
	return agent.Document{Content: content, Metadata: meta}
}

// genEvalCase generates a random EvalCase.
func genEvalCase(t *rapid.T, label string) EvalCase {
	numDocs := rapid.IntRange(0, 3).Draw(t, label+"_numDocs")
	docs := make([]agent.Document, numDocs)
	for i := 0; i < numDocs; i++ {
		docs[i] = genDocument(t, fmt.Sprintf("%s_doc_%d", label, i))
	}

	numCaseMeta := rapid.IntRange(0, 2).Draw(t, label+"_numCaseMeta")
	var caseMeta map[string]string
	if numCaseMeta > 0 {
		caseMeta = make(map[string]string, numCaseMeta)
		for i := 0; i < numCaseMeta; i++ {
			key := rapid.StringMatching(`[a-z]{1,8}`).Draw(t, fmt.Sprintf("%s_caseMetaKey_%d", label, i))
			val := rapid.StringMatching(`[a-zA-Z0-9]{0,15}`).Draw(t, fmt.Sprintf("%s_caseMetaVal_%d", label, i))
			caseMeta[key] = val
		}
	}

	return EvalCase{
		Query:            rapid.StringMatching(`[a-zA-Z0-9 ?]{1,50}`).Draw(t, label+"_query"),
		ActualOutput:     rapid.StringMatching(`[a-zA-Z0-9 .]{1,80}`).Draw(t, label+"_actualOutput"),
		RetrievedContext: docs,
		ReferenceAnswer:  rapid.StringMatching(`[a-zA-Z0-9 .]{0,50}`).Draw(t, label+"_refAnswer"),
		Metadata:         caseMeta,
	}
}

// genEvalResult generates a random EvalResult.
func genEvalResult(t *rapid.T, label string) EvalResult {
	return EvalResult{
		EvaluatorName: rapid.StringMatching(`[a-z_]{1,20}`).Draw(t, label+"_evalName"),
		Score:         rapid.Float64Range(0.0, 1.0).Draw(t, label+"_score"),
		Pass:          rapid.Bool().Draw(t, label+"_pass"),
		Explanation:   rapid.StringMatching(`[a-zA-Z0-9 .]{0,60}`).Draw(t, label+"_explanation"),
	}
}

// genCaseResults generates a random CaseResults.
func genCaseResults(t *rapid.T, label string) CaseResults {
	numResults := rapid.IntRange(0, 3).Draw(t, label+"_numResults")
	results := make([]EvalResult, numResults)
	for i := 0; i < numResults; i++ {
		results[i] = genEvalResult(t, fmt.Sprintf("%s_result_%d", label, i))
	}

	errStr := ""
	if rapid.Bool().Draw(t, label+"_hasError") {
		errStr = rapid.StringMatching(`[a-zA-Z0-9 ]{1,30}`).Draw(t, label+"_error")
	}

	return CaseResults{
		Case:    genEvalCase(t, label+"_case"),
		Results: results,
		Error:   errStr,
	}
}

// genEvalSummary generates a random EvalSummary.
func genEvalSummary(t *rapid.T, label string) EvalSummary {
	return EvalSummary{
		EvaluatorName: rapid.StringMatching(`[a-z_]{1,20}`).Draw(t, label+"_summaryName"),
		MeanScore:     rapid.Float64Range(0.0, 1.0).Draw(t, label+"_meanScore"),
		Passed:        rapid.IntRange(0, 100).Draw(t, label+"_passed"),
		Failed:        rapid.IntRange(0, 100).Draw(t, label+"_failed"),
	}
}

// Feature: llm-evaluation, Property 8: EvalReport JSON round-trip
// Validates: Requirements 15.2
func TestProperty_EvalReportRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate random EvalReport.
		numCases := rapid.IntRange(0, 5).Draw(t, "numCases")
		caseResults := make([]CaseResults, numCases)
		for i := 0; i < numCases; i++ {
			caseResults[i] = genCaseResults(t, fmt.Sprintf("cr_%d", i))
		}

		numSummaries := rapid.IntRange(0, 3).Draw(t, "numSummaries")
		summaries := make(map[string]EvalSummary, numSummaries)
		for i := 0; i < numSummaries; i++ {
			s := genEvalSummary(t, fmt.Sprintf("summary_%d", i))
			summaries[s.EvaluatorName] = s
		}

		// Truncate time to seconds to avoid nanosecond precision issues with JSON.
		ts := time.Now().Add(time.Duration(rapid.IntRange(-1000000, 1000000).Draw(t, "tsOffset")) * time.Second).Truncate(time.Second)

		original := EvalReport{
			Timestamp:  ts,
			TotalCases: rapid.IntRange(0, 100).Draw(t, "totalCases"),
			Results:    caseResults,
			Summaries:  summaries,
		}

		// Marshal to JSON.
		data, err := json.Marshal(original)
		if err != nil {
			t.Fatalf("json.Marshal failed: %v", err)
		}

		// Unmarshal back.
		var restored EvalReport
		if err := json.Unmarshal(data, &restored); err != nil {
			t.Fatalf("json.Unmarshal failed: %v", err)
		}

		// Compare timestamps separately since time.Time equality can be tricky.
		if !original.Timestamp.Equal(restored.Timestamp) {
			t.Fatalf("Timestamp mismatch: original=%v, restored=%v",
				original.Timestamp, restored.Timestamp)
		}

		// Zero out timestamps for DeepEqual comparison of remaining fields.
		original.Timestamp = time.Time{}
		restored.Timestamp = time.Time{}

		// Normalize nil vs empty slices/maps for comparison.
		normalizeReport(&original)
		normalizeReport(&restored)

		if !reflect.DeepEqual(original, restored) {
			origJSON, _ := json.MarshalIndent(original, "", "  ")
			restJSON, _ := json.MarshalIndent(restored, "", "  ")
			t.Fatalf("round-trip mismatch:\noriginal:\n%s\nrestored:\n%s", origJSON, restJSON)
		}
	})
}

// normalizeReport ensures nil and empty slices/maps are treated equivalently
// for DeepEqual comparison after JSON round-trip.
func normalizeReport(r *EvalReport) {
	if r.Results == nil {
		r.Results = []CaseResults{}
	}
	if r.Summaries == nil {
		r.Summaries = map[string]EvalSummary{}
	}
	for i := range r.Results {
		if r.Results[i].Results == nil {
			r.Results[i].Results = []EvalResult{}
		}
		if r.Results[i].Case.RetrievedContext == nil {
			r.Results[i].Case.RetrievedContext = []agent.Document{}
		}
		if r.Results[i].Case.Metadata == nil {
			r.Results[i].Case.Metadata = map[string]string{}
		}
		for j := range r.Results[i].Case.RetrievedContext {
			if r.Results[i].Case.RetrievedContext[j].Metadata == nil {
				r.Results[i].Case.RetrievedContext[j].Metadata = map[string]string{}
			}
		}
	}
}
