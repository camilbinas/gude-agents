package gemini

import (
	"context"

	"github.com/camilbinas/gude-agents/agent"
	"google.golang.org/genai"
)

// Compile-time check: Estimator satisfies agent.TokenEstimator.
var _ agent.TokenEstimator = (*Estimator)(nil)

// Estimator is a TokenEstimator that calls the Gemini countTokens API
// for exact server-side token counting. It reuses the existing GenAI client
// from a GeminiProvider.
type Estimator struct {
	client *genai.Client
	model  string
}

// NewEstimator creates a token estimator that uses the Gemini countTokens API.
// It requires a GeminiProvider to obtain the underlying SDK client and model ID.
func NewEstimator(provider *GeminiProvider) *Estimator {
	return &Estimator{
		client: provider.client,
		model:  provider.model,
	}
}

// EstimateTokens calls the Gemini models/{model}:countTokens API with the given
// converse parameters and returns the exact input token count reported by the service.
// On any API error, it returns (0, err).
func (e *Estimator) EstimateTokens(ctx context.Context, params agent.ConverseParams) (int, error) {
	contents, err := toGeminiContents(params.Messages)
	if err != nil {
		return 0, err
	}

	config := &genai.CountTokensConfig{}

	if params.System != "" {
		config.SystemInstruction = &genai.Content{
			Parts: []*genai.Part{genai.NewPartFromText(params.System)},
		}
	}

	if tools := toGeminiTools(params.ToolConfig); tools != nil {
		config.Tools = tools
	}

	resp, err := e.client.Models.CountTokens(ctx, e.model, contents, config)
	if err != nil {
		return 0, err
	}

	return int(resp.TotalTokens), nil
}
