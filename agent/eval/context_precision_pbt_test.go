package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"pgregory.net/rapid"
)

// Feature: llm-evaluation, Property 5: Context precision score equals average precision over relevance judgments
// Validates: Requirements 9.2
func TestProperty_ContextPrecisionAP(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate 1-10 documents.
		numDocs := rapid.IntRange(1, 10).Draw(t, "numDocs")

		// For each document, randomly assign relevance.
		relevant := make([]bool, numDocs)
		for i := 0; i < numDocs; i++ {
			relevant[i] = rapid.Bool().Draw(t, fmt.Sprintf("relevant_%d", i))
		}

		// Independently compute expected average precision.
		// AP = (1/R) * Σ(Precision@k * rel_k)
		totalRelevant := 0
		for _, r := range relevant {
			if r {
				totalRelevant++
			}
		}

		var expectedScore float64
		if totalRelevant == 0 {
			expectedScore = 0.0
		} else {
			var sum float64
			relevantSoFar := 0
			for k, r := range relevant {
				if r {
					relevantSoFar++
					precisionAtK := float64(relevantSoFar) / float64(k+1)
					sum += precisionAtK
				}
			}
			expectedScore = sum / float64(totalRelevant)
		}

		// Build mock provider response with judgments.
		judgments := make([]documentJudgment, numDocs)
		for i := 0; i < numDocs; i++ {
			judgments[i] = documentJudgment{
				DocumentIndex: i,
				Relevant:      relevant[i],
			}
		}

		respJSON, err := json.Marshal(judgmentsResponse{Judgments: judgments})
		if err != nil {
			t.Fatalf("failed to marshal judgments: %v", err)
		}

		provider := newScriptedProvider(
			[]*agent.ProviderResponse{{Text: string(respJSON)}},
			[]error{nil},
		)

		cp := NewContextPrecision(provider)

		// Build retrieved context with numDocs documents.
		docs := make([]agent.Document, numDocs)
		for i := 0; i < numDocs; i++ {
			docs[i] = agent.Document{Content: fmt.Sprintf("document_%d", i)}
		}

		result, err := cp.Evaluate(context.Background(), EvalCase{
			Query:            "test query",
			ReferenceAnswer:  "test reference",
			RetrievedContext: docs,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Assert score equals expected average precision.
		if math.Abs(result.Score-expectedScore) > 1e-9 {
			t.Fatalf("score mismatch: got %f, want %f (numDocs=%d, totalRelevant=%d, relevant=%v)",
				result.Score, expectedScore, numDocs, totalRelevant, relevant)
		}

		// When no documents are relevant, score must be 0.0.
		if totalRelevant == 0 && result.Score != 0.0 {
			t.Fatalf("expected score 0.0 when no documents relevant, got %f", result.Score)
		}

		// Assert evaluator name.
		if result.EvaluatorName != "context_precision" {
			t.Fatalf("evaluator name mismatch: got %q, want %q", result.EvaluatorName, "context_precision")
		}
	})
}
