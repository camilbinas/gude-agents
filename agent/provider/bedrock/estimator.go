package bedrock

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/camilbinas/gude-agents/agent"
)

// Compile-time check: Estimator satisfies agent.TokenEstimator.
var _ agent.TokenEstimator = (*Estimator)(nil)

// Estimator is a TokenEstimator that calls the Bedrock CountTokens runtime API
// for exact server-side token counting. It reuses the existing AWS SDK client
// from a BedrockProvider.
type Estimator struct {
	client *bedrockruntime.Client
	model  string
}

// NewEstimator creates a token estimator that uses the Bedrock CountTokens API.
// It requires a BedrockProvider to obtain the underlying SDK client and model ID.
func NewEstimator(provider *BedrockProvider) *Estimator {
	return &Estimator{
		client: provider.client,
		model:  provider.model,
	}
}

// EstimateTokens calls the Bedrock CountTokens API with the given converse
// parameters and returns the exact input token count reported by the service.
// On any API error, it returns (0, err).
func (e *Estimator) EstimateTokens(ctx context.Context, params agent.ConverseParams) (int, error) {
	msgs, err := toBedrockMessages(params.Messages, e.model, false)
	if err != nil {
		return 0, err
	}

	converseReq := types.ConverseTokensRequest{
		Messages: msgs,
	}

	if params.System != "" {
		converseReq.System = []types.SystemContentBlock{
			&types.SystemContentBlockMemberText{Value: params.System},
		}
	}

	if tc := toToolConfig(params.ToolConfig); tc != nil {
		converseReq.ToolConfig = tc
	}

	out, err := e.client.CountTokens(ctx, &bedrockruntime.CountTokensInput{
		ModelId: aws.String(e.model),
		Input:   &types.CountTokensInputMemberConverse{Value: converseReq},
	})
	if err != nil {
		return 0, err
	}

	return int(aws.ToInt32(out.InputTokens)), nil
}
