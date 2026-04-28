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

// Feature: llm-evaluation, Property 4: Faithfulness score equals fraction of supported claims
// Validates: Requirements 7.2, 7.4
func TestProperty_FaithfulnessClaimFraction(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate 0-10 claims.
		numClaims := rapid.IntRange(0, 10).Draw(t, "numClaims")

		claims := make([]string, numClaims)
		verdicts := make([]verdict, numClaims)
		supportedCount := 0

		for i := 0; i < numClaims; i++ {
			claimText := fmt.Sprintf("claim_%d", i)
			claims[i] = claimText

			isSupported := rapid.Bool().Draw(t, fmt.Sprintf("supported_%d", i))
			if isSupported {
				verdicts[i] = verdict{Claim: claimText, Verdict: "supported"}
				supportedCount++
			} else {
				verdicts[i] = verdict{Claim: claimText, Verdict: "unsupported"}
			}
		}

		// Compute expected score.
		var expectedScore float64
		if numClaims == 0 {
			expectedScore = 1.0
		} else {
			expectedScore = float64(supportedCount) / float64(numClaims)
		}

		// Build mock provider responses.
		claimsJSON, err := json.Marshal(claimsResponse{Claims: claims})
		if err != nil {
			t.Fatalf("failed to marshal claims: %v", err)
		}
		verdictsJSON, err := json.Marshal(verdictsResponse{Verdicts: verdicts})
		if err != nil {
			t.Fatalf("failed to marshal verdicts: %v", err)
		}

		var responses []*agent.ProviderResponse
		var errs []error
		if numClaims == 0 {
			// Only the claims extraction step is called when there are no claims.
			responses = []*agent.ProviderResponse{
				{Text: string(claimsJSON)},
			}
			errs = []error{nil}
		} else {
			responses = []*agent.ProviderResponse{
				{Text: string(claimsJSON)},
				{Text: string(verdictsJSON)},
			}
			errs = []error{nil, nil}
		}

		provider := newScriptedProvider(responses, errs)
		f := NewFaithfulness(provider)

		result, err := f.Evaluate(context.Background(), EvalCase{
			ActualOutput:     "some output text",
			RetrievedContext: []agent.Document{{Content: "some context"}},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Assert score equals expected fraction.
		if math.Abs(result.Score-expectedScore) > 1e-9 {
			t.Fatalf("score mismatch: got %f, want %f (numClaims=%d, supportedCount=%d)",
				result.Score, expectedScore, numClaims, supportedCount)
		}

		// Assert evaluator name.
		if result.EvaluatorName != "faithfulness" {
			t.Fatalf("evaluator name mismatch: got %q, want %q", result.EvaluatorName, "faithfulness")
		}
	})
}
