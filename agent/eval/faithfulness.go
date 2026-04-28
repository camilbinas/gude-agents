package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
)

// extractJSON strips optional markdown code fences from an LLM response
// and returns the inner JSON string. Many LLMs wrap JSON in ```json ... ```
// even when instructed not to.
func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	// Strip ```json ... ``` or ``` ... ```
	if strings.HasPrefix(s, "```") {
		// Remove opening fence (with optional language tag).
		if idx := strings.Index(s, "\n"); idx != -1 {
			s = s[idx+1:]
		}
		// Remove closing fence.
		if idx := strings.LastIndex(s, "```"); idx != -1 {
			s = s[:idx]
		}
		s = strings.TrimSpace(s)
	}
	return s
}

// Faithfulness is an LLM-based evaluator that measures whether the agent's
// answer is factually supported by the retrieved context. It uses a two-step
// prompting approach: first extracting claims from the actual output, then
// judging each claim against the retrieved context.
//
// Score = supported claims / total claims. When no claims are found, the
// score is 1.0.
type Faithfulness struct {
	provider agent.Provider
	cfg      evaluatorConfig
}

// NewFaithfulness creates a Faithfulness evaluator that uses the given provider
// as the LLM judge.
func NewFaithfulness(provider agent.Provider, opts ...EvaluatorOption) *Faithfulness {
	cfg := applyOptions(opts)
	return &Faithfulness{
		provider: provider,
		cfg:      cfg,
	}
}

// claimsResponse is the expected JSON structure from the claims extraction step.
type claimsResponse struct {
	Claims []string `json:"claims"`
}

// verdict represents a single claim judgment.
type verdict struct {
	Claim   string `json:"claim"`
	Verdict string `json:"verdict"`
}

// verdictsResponse is the expected JSON structure from the verdict step.
type verdictsResponse struct {
	Verdicts []verdict `json:"verdicts"`
}

// Evaluate uses two-step LLM prompting to measure faithfulness:
//  1. Extract claims from the actual output.
//  2. Judge each claim against the retrieved context.
//
// Provider errors are propagated without partial results.
func (f *Faithfulness) Evaluate(ctx context.Context, ec EvalCase) (EvalResult, error) {
	// Step 1: Extract claims from the actual output.
	claims, err := f.extractClaims(ctx, ec.ActualOutput)
	if err != nil {
		return EvalResult{}, err
	}

	// No claims found — score is 1.0.
	if len(claims) == 0 {
		return EvalResult{
			EvaluatorName: f.Name(),
			Score:         1.0,
			Pass:          applyThreshold(1.0, f.cfg),
			Explanation:   "no claims found in actual output",
		}, nil
	}

	// Step 2: Judge each claim against the retrieved context.
	verdicts, err := f.judgeClaims(ctx, claims, ec.RetrievedContext)
	if err != nil {
		return EvalResult{}, err
	}

	// Compute score = supported / total.
	supported := 0
	var unsupported []string
	for _, v := range verdicts {
		if v.Verdict == "supported" {
			supported++
		} else {
			unsupported = append(unsupported, v.Claim)
		}
	}

	total := len(verdicts)
	score := float64(supported) / float64(total)

	// Clamp score to [0.0, 1.0].
	if score < 0.0 {
		score = 0.0
	}
	if score > 1.0 {
		score = 1.0
	}

	var explanation string
	if len(unsupported) > 0 {
		explanation = fmt.Sprintf("unsupported claims: %s", strings.Join(unsupported, "; "))
	}

	return EvalResult{
		EvaluatorName: f.Name(),
		Score:         score,
		Pass:          applyThreshold(score, f.cfg),
		Explanation:   explanation,
	}, nil
}

// Name returns the evaluator name.
func (f *Faithfulness) Name() string {
	return "faithfulness"
}

// extractClaims prompts the LLM to extract factual claims from the given text.
func (f *Faithfulness) extractClaims(ctx context.Context, text string) ([]string, error) {
	system := `You are a claim extraction assistant. Extract all factual claims from the given text.
Return your response as a JSON object with a single key "claims" containing an array of claim strings.
If there are no factual claims, return {"claims": []}.
Return ONLY the JSON object, no other text.`

	userMsg := fmt.Sprintf("Extract all factual claims from the following text:\n\n%s", text)

	resp, err := f.provider.Converse(ctx, agent.ConverseParams{
		System: system,
		Messages: []agent.Message{
			{
				Role:    agent.RoleUser,
				Content: []agent.ContentBlock{agent.TextBlock{Text: userMsg}},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("faithfulness: claims extraction failed: %w", err)
	}

	var parsed claimsResponse
	if err := json.Unmarshal([]byte(extractJSON(resp.Text)), &parsed); err != nil {
		return nil, fmt.Errorf("faithfulness: failed to parse claims response: %w", err)
	}

	return parsed.Claims, nil
}

// judgeClaims prompts the LLM to judge each claim against the retrieved context.
func (f *Faithfulness) judgeClaims(ctx context.Context, claims []string, docs []agent.Document) ([]verdict, error) {
	// Format retrieved context.
	var contextParts []string
	for i, doc := range docs {
		contextParts = append(contextParts, fmt.Sprintf("[%d] %s", i+1, doc.Content))
	}
	contextStr := strings.Join(contextParts, "\n")

	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return nil, fmt.Errorf("faithfulness: failed to marshal claims: %w", err)
	}

	system := `You are a faithfulness judge. For each claim, determine if it is supported by the provided context.
Return your response as a JSON object with a single key "verdicts" containing an array of objects.
Each object must have a "claim" field (the original claim text) and a "verdict" field (either "supported" or "unsupported").
Return ONLY the JSON object, no other text.`

	userMsg := fmt.Sprintf("Context:\n%s\n\nClaims:\n%s\n\nJudge each claim as \"supported\" or \"unsupported\" based on the context.", contextStr, string(claimsJSON))

	resp, err := f.provider.Converse(ctx, agent.ConverseParams{
		System: system,
		Messages: []agent.Message{
			{
				Role:    agent.RoleUser,
				Content: []agent.ContentBlock{agent.TextBlock{Text: userMsg}},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("faithfulness: verdict judgment failed: %w", err)
	}

	var parsed verdictsResponse
	if err := json.Unmarshal([]byte(extractJSON(resp.Text)), &parsed); err != nil {
		return nil, fmt.Errorf("faithfulness: failed to parse verdicts response: %w", err)
	}

	return parsed.Verdicts, nil
}
