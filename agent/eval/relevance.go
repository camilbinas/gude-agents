package eval

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/camilbinas/gude-agents/agent"
)

// Relevance is an LLM-based evaluator that measures how well the agent's
// actual output addresses the original query. It prompts the LLM to rate
// relevance on a scale of [0, 1] and provide a justification.
//
// Score = LLM's rating normalized to [0, 1].
type Relevance struct {
	provider agent.Provider
	cfg      evaluatorConfig
}

// NewRelevance creates a Relevance evaluator that uses the given provider
// as the LLM judge.
func NewRelevance(provider agent.Provider, opts ...EvaluatorOption) *Relevance {
	cfg := applyOptions(opts)
	return &Relevance{
		provider: provider,
		cfg:      cfg,
	}
}

// relevanceResponse is the expected JSON structure from the LLM judge.
type relevanceResponse struct {
	Score         float64 `json:"score"`
	Justification string  `json:"justification"`
}

// Evaluate prompts the LLM to rate how well the actual output addresses the query.
// Provider errors are propagated without partial results.
func (r *Relevance) Evaluate(ctx context.Context, ec EvalCase) (EvalResult, error) {
	score, justification, err := r.judgeRelevance(ctx, ec.Query, ec.ActualOutput)
	if err != nil {
		return EvalResult{}, err
	}

	// Clamp score to [0.0, 1.0].
	if score < 0.0 {
		score = 0.0
	}
	if score > 1.0 {
		score = 1.0
	}

	return EvalResult{
		EvaluatorName: r.Name(),
		Score:         score,
		Pass:          applyThreshold(score, r.cfg),
		Explanation:   justification,
	}, nil
}

// Name returns the evaluator name.
func (r *Relevance) Name() string {
	return "relevance"
}

// judgeRelevance prompts the LLM to rate how well the actual output addresses the query.
func (r *Relevance) judgeRelevance(ctx context.Context, query, actualOutput string) (float64, string, error) {
	system := `You are a relevance judge. Rate how well the given answer addresses the user's question.
Return your response as a JSON object with two keys:
- "score": a float between 0.0 and 1.0 where 0.0 means completely irrelevant and 1.0 means perfectly relevant
- "justification": a brief explanation of your rating
Return ONLY the JSON object, no other text.`

	userMsg := fmt.Sprintf("Question:\n%s\n\nAnswer:\n%s\n\nRate how well the answer addresses the question.", query, actualOutput)

	resp, err := r.provider.Converse(ctx, agent.ConverseParams{
		System: system,
		Messages: []agent.Message{
			{
				Role:    agent.RoleUser,
				Content: []agent.ContentBlock{agent.TextBlock{Text: userMsg}},
			},
		},
	})
	if err != nil {
		return 0, "", fmt.Errorf("relevance: judgment failed: %w", err)
	}

	var parsed relevanceResponse
	if err := json.Unmarshal([]byte(extractJSON(resp.Text)), &parsed); err != nil {
		return 0, "", fmt.Errorf("relevance: failed to parse response: %w", err)
	}

	return parsed.Score, parsed.Justification, nil
}
