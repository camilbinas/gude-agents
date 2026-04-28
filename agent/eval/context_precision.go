package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
)

// ContextPrecision is an LLM-based evaluator that measures the relevance of
// retrieved documents to the query and reference answer. It prompts the LLM to
// judge each document's relevance, then computes average precision.
//
// Score = AP = (1/R) * Σ(Precision@k * rel_k) where R = number of relevant
// documents. When R is zero, the score is 0.0.
type ContextPrecision struct {
	provider agent.Provider
	cfg      evaluatorConfig
}

// NewContextPrecision creates a ContextPrecision evaluator that uses the given
// provider as the LLM judge.
func NewContextPrecision(provider agent.Provider, opts ...EvaluatorOption) *ContextPrecision {
	cfg := applyOptions(opts)
	return &ContextPrecision{
		provider: provider,
		cfg:      cfg,
	}
}

// documentJudgment represents the LLM's relevance judgment for a single document.
type documentJudgment struct {
	DocumentIndex int  `json:"document_index"`
	Relevant      bool `json:"relevant"`
}

// judgmentsResponse is the expected JSON structure from the LLM judge.
type judgmentsResponse struct {
	Judgments []documentJudgment `json:"judgments"`
}

// Evaluate prompts the LLM to judge relevance of each retrieved document to the
// query and reference answer, then computes average precision.
// Provider errors are propagated without partial results.
func (cp *ContextPrecision) Evaluate(ctx context.Context, ec EvalCase) (EvalResult, error) {
	if len(ec.RetrievedContext) == 0 {
		return EvalResult{
			EvaluatorName: cp.Name(),
			Score:         0.0,
			Pass:          applyThreshold(0.0, cp.cfg),
			Explanation:   "no retrieved documents to evaluate",
		}, nil
	}

	judgments, err := cp.judgeDocuments(ctx, ec.Query, ec.ReferenceAnswer, ec.RetrievedContext)
	if err != nil {
		return EvalResult{}, err
	}

	// Build a relevance vector indexed by document position.
	relevant := make([]bool, len(ec.RetrievedContext))
	for _, j := range judgments {
		if j.DocumentIndex >= 0 && j.DocumentIndex < len(relevant) {
			relevant[j.DocumentIndex] = j.Relevant
		}
	}

	// Compute average precision.
	score := computeAveragePrecision(relevant)

	// Clamp score to [0.0, 1.0].
	if score < 0.0 {
		score = 0.0
	}
	if score > 1.0 {
		score = 1.0
	}

	// Build per-document explanation.
	explanation := formatJudgments(relevant)

	return EvalResult{
		EvaluatorName: cp.Name(),
		Score:         score,
		Pass:          applyThreshold(score, cp.cfg),
		Explanation:   explanation,
	}, nil
}

// Name returns the evaluator name.
func (cp *ContextPrecision) Name() string {
	return "context_precision"
}

// computeAveragePrecision computes AP = (1/R) * Σ(Precision@k * rel_k).
// For each position k (0-indexed), if document k is relevant:
//   - Precision@k = (number of relevant docs in positions 0..k) / (k+1)
//   - Add Precision@k to the sum
//
// AP = sum / R where R = total relevant docs. If R = 0, AP = 0.0.
func computeAveragePrecision(relevant []bool) float64 {
	totalRelevant := 0
	for _, r := range relevant {
		if r {
			totalRelevant++
		}
	}

	if totalRelevant == 0 {
		return 0.0
	}

	var sum float64
	relevantSoFar := 0
	for k, r := range relevant {
		if r {
			relevantSoFar++
			precisionAtK := float64(relevantSoFar) / float64(k+1)
			sum += precisionAtK
		}
	}

	return sum / float64(totalRelevant)
}

// formatJudgments builds a human-readable explanation of per-document relevance.
func formatJudgments(relevant []bool) string {
	parts := make([]string, len(relevant))
	for i, r := range relevant {
		if r {
			parts[i] = fmt.Sprintf("doc %d: relevant", i)
		} else {
			parts[i] = fmt.Sprintf("doc %d: not relevant", i)
		}
	}
	return strings.Join(parts, ", ")
}

// judgeDocuments prompts the LLM to judge the relevance of each retrieved document.
func (cp *ContextPrecision) judgeDocuments(ctx context.Context, query, referenceAnswer string, docs []agent.Document) ([]documentJudgment, error) {
	// Format retrieved documents.
	var docParts []string
	for i, doc := range docs {
		docParts = append(docParts, fmt.Sprintf("[%d] %s", i, doc.Content))
	}
	docsStr := strings.Join(docParts, "\n")

	system := `You are a document relevance judge. For each retrieved document, determine if it is relevant to answering the given query based on the reference answer.
Return your response as a JSON object with a single key "judgments" containing an array of objects.
Each object must have a "document_index" field (integer, 0-based) and a "relevant" field (boolean).
Return ONLY the JSON object, no other text.`

	userMsg := fmt.Sprintf("Query:\n%s\n\nReference Answer:\n%s\n\nRetrieved Documents:\n%s\n\nJudge the relevance of each document to the query.", query, referenceAnswer, docsStr)

	resp, err := cp.provider.Converse(ctx, agent.ConverseParams{
		System: system,
		Messages: []agent.Message{
			{
				Role:    agent.RoleUser,
				Content: []agent.ContentBlock{agent.TextBlock{Text: userMsg}},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("context_precision: judgment failed: %w", err)
	}

	var parsed judgmentsResponse
	if err := json.Unmarshal([]byte(extractJSON(resp.Text)), &parsed); err != nil {
		return nil, fmt.Errorf("context_precision: failed to parse response: %w", err)
	}

	return parsed.Judgments, nil
}
