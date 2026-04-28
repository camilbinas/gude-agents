package eval

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
)

// RetrievalOrdering is a rule-based evaluator that compares the order of
// retrieved documents against an expected priority order using Normalized
// Discounted Cumulative Gain (NDCG). The score is 1.0 when the retrieved
// order matches the expected order exactly, and 0.0 when none of the expected
// documents appear in the retrieved context.
type RetrievalOrdering struct {
	expectedIDs []string
	idExtractor func(agent.Document) string
	cfg         evaluatorConfig
}

// NewRetrievalOrdering creates a RetrievalOrdering evaluator that compares
// retrieved document order against expectedIDs using NDCG. The idExtractor
// function defines how to extract an identifier from an agent.Document (e.g.,
// from Metadata["id"] or Content). It returns an error if expectedIDs is empty
// or idExtractor is nil.
func NewRetrievalOrdering(expectedIDs []string, idExtractor func(agent.Document) string, opts ...EvaluatorOption) (*RetrievalOrdering, error) {
	if len(expectedIDs) == 0 {
		return nil, fmt.Errorf("expectedIDs must not be empty")
	}
	if idExtractor == nil {
		return nil, fmt.Errorf("idExtractor must not be nil")
	}
	cfg := applyOptions(opts)
	return &RetrievalOrdering{
		expectedIDs: expectedIDs,
		idExtractor: idExtractor,
		cfg:         cfg,
	}, nil
}

// Evaluate computes the NDCG score for the retrieved context ordering against
// the expected document order. Relevance grades are assigned based on position
// in the expected order: the first expected document gets grade n, the second
// gets n-1, etc. Documents not in the expected list get grade 0.
func (r *RetrievalOrdering) Evaluate(_ context.Context, ec EvalCase) (EvalResult, error) {
	n := len(r.expectedIDs)

	// Build a map from expected ID to relevance grade.
	// First expected doc gets grade n, second gets n-1, etc.
	relevanceMap := make(map[string]float64, n)
	for i, id := range r.expectedIDs {
		relevanceMap[id] = float64(n - i)
	}

	// Extract IDs from retrieved documents and compute relevance grades.
	k := len(ec.RetrievedContext)
	actualGrades := make([]float64, k)
	foundAny := false
	for i, doc := range ec.RetrievedContext {
		id := r.idExtractor(doc)
		if grade, ok := relevanceMap[id]; ok {
			actualGrades[i] = grade
			foundAny = true
		}
	}

	// If no expected documents were found, return 0.0.
	if !foundAny {
		var explanation string
		if k == 0 {
			explanation = "no documents in retrieved context"
		} else {
			explanation = fmt.Sprintf("none of the expected documents found in %d retrieved documents", k)
		}
		return EvalResult{
			EvaluatorName: r.Name(),
			Score:         0.0,
			Pass:          applyThreshold(0.0, r.cfg),
			Explanation:   explanation,
		}, nil
	}

	// Compute DCG from actual order.
	dcg := computeDCG(actualGrades)

	// Compute IDCG from ideal order (expected grades sorted descending).
	// The ideal ordering places the highest-graded documents first.
	idealGrades := make([]float64, 0, n)
	for i := 0; i < n; i++ {
		idealGrades = append(idealGrades, float64(n-i))
	}
	idcg := computeDCG(idealGrades)

	// NDCG = DCG / IDCG. If IDCG is 0, return 0.0.
	if idcg == 0 {
		return EvalResult{
			EvaluatorName: r.Name(),
			Score:         0.0,
			Pass:          applyThreshold(0.0, r.cfg),
			Explanation:   "ideal DCG is zero",
		}, nil
	}

	score := dcg / idcg

	// Clamp score to [0.0, 1.0].
	if score < 0.0 {
		score = 0.0
	}
	if score > 1.0 {
		score = 1.0
	}

	var explanation string
	if score < 1.0 {
		var retrievedIDs []string
		for _, doc := range ec.RetrievedContext {
			retrievedIDs = append(retrievedIDs, r.idExtractor(doc))
		}
		explanation = fmt.Sprintf("NDCG=%.4f, retrieved order: [%s], expected order: [%s]",
			score,
			strings.Join(retrievedIDs, ", "),
			strings.Join(r.expectedIDs, ", "))
	}

	return EvalResult{
		EvaluatorName: r.Name(),
		Score:         score,
		Pass:          applyThreshold(score, r.cfg),
		Explanation:   explanation,
	}, nil
}

// Name returns the evaluator name.
func (r *RetrievalOrdering) Name() string {
	return "retrieval_ordering"
}

// computeDCG computes the Discounted Cumulative Gain for a slice of relevance
// grades: DCG = Σ (grade_i / log2(i + 2)) for i = 0..len(grades)-1.
func computeDCG(grades []float64) float64 {
	var dcg float64
	for i, grade := range grades {
		dcg += grade / math.Log2(float64(i+2))
	}
	return dcg
}
