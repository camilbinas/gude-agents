package anthropic

import (
	"context"
	"fmt"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/tool"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
)

// Estimator implements agent.TokenEstimator using the Anthropic
// POST /v1/messages/count_tokens API for exact server-side token counting.
type Estimator struct {
	client anthropicsdk.Client
	model  anthropicsdk.Model
}

// Compile-time check that Estimator satisfies agent.TokenEstimator.
var _ agent.TokenEstimator = (*Estimator)(nil)

// NewEstimator creates an Estimator that uses the given provider's SDK client
// and model to count tokens via the Anthropic API.
func NewEstimator(provider *AnthropicProvider) *Estimator {
	return &Estimator{
		client: provider.client,
		model:  provider.model,
	}
}

// EstimateTokens calls POST /v1/messages/count_tokens with the given converse
// params and returns the exact input token count reported by the Anthropic API.
// On API errors, it returns (0, err).
func (e *Estimator) EstimateTokens(ctx context.Context, params agent.ConverseParams) (int, error) {
	input := anthropicsdk.MessageCountTokensParams{
		Model:    e.model,
		Messages: toAnthropicMessages(params.Messages, false),
	}

	if params.System != "" {
		input.System = anthropicsdk.MessageCountTokensParamsSystemUnion{
			OfTextBlockArray: []anthropicsdk.TextBlockParam{{Text: params.System}},
		}
	}

	if len(params.ToolConfig) > 0 {
		input.Tools = toCountTokensTools(params.ToolConfig)
	}

	res, err := e.client.Messages.CountTokens(ctx, input)
	if err != nil {
		return 0, fmt.Errorf("anthropic count_tokens: %w", err)
	}

	return int(res.InputTokens), nil
}

// toCountTokensTools converts tool.Spec slices to the count_tokens tool param type.
func toCountTokensTools(specs []tool.Spec) []anthropicsdk.MessageCountTokensToolUnionParam {
	tools := make([]anthropicsdk.MessageCountTokensToolUnionParam, len(specs))
	for i, s := range specs {
		props, _ := s.InputSchema["properties"]
		required, _ := s.InputSchema["required"].([]string)
		if required == nil {
			if reqAny, ok := s.InputSchema["required"].([]any); ok {
				for _, r := range reqAny {
					if str, ok := r.(string); ok {
						required = append(required, str)
					}
				}
			}
		}

		tools[i] = anthropicsdk.MessageCountTokensToolUnionParam{
			OfTool: &anthropicsdk.ToolParam{
				Name:        s.Name,
				Description: anthropicsdk.String(s.Description),
				InputSchema: anthropicsdk.ToolInputSchemaParam{
					Properties: props,
					Required:   required,
				},
			},
		}
	}
	return tools
}
