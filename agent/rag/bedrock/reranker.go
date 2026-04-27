package bedrock

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime/types"
	"github.com/camilbinas/gude-agents/agent"
)

// Compile-time assertion that Reranker satisfies agent.Reranker.
var _ agent.Reranker = (*Reranker)(nil)

// Reranker implements agent.Reranker using the Bedrock Rerank API.
type Reranker struct {
	client   *bedrockagentruntime.Client
	modelARN string
	topN     int
}

// RerankerOption configures a Reranker.
type RerankerOption func(*rerankerOptions)

type rerankerOptions struct {
	region string
	topN   int
}

// WithRerankerRegion sets the AWS region for the Bedrock agent runtime client.
func WithRerankerRegion(region string) RerankerOption {
	return func(o *rerankerOptions) { o.region = region }
}

// WithRerankerTopN sets the maximum number of documents to return after
// reranking. Default is 0, which means all input documents are returned
// in reranked order.
func WithRerankerTopN(n int) RerankerOption {
	return func(o *rerankerOptions) { o.topN = n }
}

// resolveRerankerRegion returns the effective region from options, env, or default.
func resolveRerankerRegion(o *rerankerOptions) string {
	if o.region != "" {
		return o.region
	}
	if r := os.Getenv("AWS_REGION"); r != "" {
		return r
	}
	return "us-east-1"
}

// modelARN builds a Bedrock foundation-model ARN from a model ID and region.
// If the modelID already looks like an ARN, it is returned as-is.
func modelARN(modelID, region string) string {
	if strings.HasPrefix(modelID, "arn:") {
		return modelID
	}
	return "arn:aws:bedrock:" + region + "::foundation-model/" + modelID
}

// NewReranker creates a Reranker for the given model ID.
// The model ID follows the same format as other Bedrock components
// (e.g. "cohere.rerank-v3-5:0", "amazon.rerank-v1:0"). A full ARN
// is also accepted and used as-is.
//
// Region is resolved in order: WithRerankerRegion option → AWS_REGION env → "us-east-1".
func NewReranker(modelID string, opts ...RerankerOption) (*Reranker, error) {
	if modelID == "" {
		return nil, fmt.Errorf("bedrock reranker: modelID must not be empty")
	}

	o := &rerankerOptions{}
	for _, fn := range opts {
		fn(o)
	}

	region := resolveRerankerRegion(o)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(), awsconfig.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("bedrock reranker: load aws config: %w", err)
	}

	return &Reranker{
		client:   bedrockagentruntime.NewFromConfig(cfg),
		modelARN: modelARN(modelID, region),
		topN:     o.topN,
	}, nil
}

// CohereRerank35 creates a Reranker using Cohere Rerank 3.5.
func CohereRerank35(opts ...RerankerOption) (*Reranker, error) {
	return NewReranker("cohere.rerank-v3-5:0", opts...)
}

// AmazonRerank10 creates a Reranker using Amazon Rerank 1.0.
func AmazonRerank10(opts ...RerankerOption) (*Reranker, error) {
	return NewReranker("amazon.rerank-v1:0", opts...)
}

// MustReranker is a helper that wraps a (*Reranker, error) call and panics on error.
// Use it to collapse reranker creation into a single line in examples and scripts.
//
//	reranker := bedrock.MustReranker(bedrock.CohereRerank35())
func MustReranker(r *Reranker, err error) *Reranker {
	if err != nil {
		panic("bedrock reranker: " + err.Error())
	}
	return r
}

// Rerank calls the Bedrock Rerank API to re-score and reorder the given
// documents for the query. Documents are returned in descending relevance
// order.
func (r *Reranker) Rerank(ctx context.Context, query string, docs []agent.Document) ([]agent.Document, error) {
	if len(docs) == 0 {
		return docs, nil
	}

	sources := make([]types.RerankSource, len(docs))
	for i, doc := range docs {
		sources[i] = types.RerankSource{
			Type: types.RerankSourceTypeInline,
			InlineDocumentSource: &types.RerankDocument{
				Type:         types.RerankDocumentTypeText,
				TextDocument: &types.RerankTextDocument{Text: aws.String(doc.Content)},
			},
		}
	}

	input := &bedrockagentruntime.RerankInput{
		Queries: []types.RerankQuery{
			{
				Type:      types.RerankQueryContentTypeText,
				TextQuery: &types.RerankTextDocument{Text: aws.String(query)},
			},
		},
		Sources: sources,
		RerankingConfiguration: &types.RerankingConfiguration{
			Type: types.RerankingConfigurationTypeBedrockRerankingModel,
			BedrockRerankingConfiguration: &types.BedrockRerankingConfiguration{
				ModelConfiguration: &types.BedrockRerankingModelConfiguration{
					ModelArn: aws.String(r.modelARN),
				},
			},
		},
	}

	if r.topN > 0 {
		input.RerankingConfiguration.BedrockRerankingConfiguration.NumberOfResults = aws.Int32(int32(r.topN))
	}

	out, err := r.client.Rerank(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("bedrock reranker: %w", err)
	}

	// Sort results by relevance score descending.
	sort.Slice(out.Results, func(i, j int) bool {
		return aws.ToFloat32(out.Results[i].RelevanceScore) > aws.ToFloat32(out.Results[j].RelevanceScore)
	})

	reranked := make([]agent.Document, 0, len(out.Results))
	for _, result := range out.Results {
		idx := int(aws.ToInt32(result.Index))
		if idx >= 0 && idx < len(docs) {
			reranked = append(reranked, docs[idx])
		}
	}
	return reranked, nil
}
