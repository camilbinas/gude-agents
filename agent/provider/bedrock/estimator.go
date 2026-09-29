package bedrock

import (
	"context"
	"net/http"

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
	client         *bedrockruntime.Client
	fetchClient    *http.Client
	urlFetchPolicy urlFetchPolicy
	model          string
}

// NewEstimator creates a token estimator that uses the Bedrock CountTokens API.
// It requires a BedrockProvider to obtain the underlying SDK client and model ID.
func NewEstimator(provider *BedrockProvider) *Estimator {
	return &Estimator{
		client:         provider.client,
		fetchClient:    provider.fetchClient,
		urlFetchPolicy: provider.urlFetchPolicy,
		model:          provider.model,
	}
}

// EstimateTokens calls the Bedrock CountTokens API with the given model
// request and returns the exact input token count reported by the service.
// On any API error, it returns (0, err).
func (e *Estimator) EstimateTokens(ctx context.Context, req agent.ModelRequest) (int, error) {
	msgs, err := toBedrockMessagesWithFetcher(ctx, e.fetchClient, e.urlFetchPolicy, req.Messages, e.model, false)
	if err != nil {
		return 0, err
	}

	converseReq := types.ConverseTokensRequest{
		Messages: msgs,
	}

	if req.System != "" {
		converseReq.System = []types.SystemContentBlock{
			&types.SystemContentBlockMemberText{Value: req.System},
		}
	}

	if tc := toToolConfig(req.Tools); tc != nil {
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
