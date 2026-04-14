package bedrock

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/camilbinas/gude-agents/agent"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime/types"
)

// Compile-time assertion that KnowledgeBaseRetriever satisfies agent.Retriever.
var _ agent.Retriever = (*KnowledgeBaseRetriever)(nil)

// KnowledgeBaseRetriever retrieves documents from an AWS Bedrock Knowledge Base.
type KnowledgeBaseRetriever struct {
	client          *bedrockagentruntime.Client
	knowledgeBaseID string
	topK            int
	scoreThreshold  float64
}

// KnowledgeBaseOption configures a KnowledgeBaseRetriever.
type KnowledgeBaseOption func(*knowledgeBaseOptions)

type knowledgeBaseOptions struct {
	region         string
	topK           int
	scoreThreshold float64
}

// WithKnowledgeBaseRegion sets the AWS region for the Bedrock agent runtime client.
func WithKnowledgeBaseRegion(region string) KnowledgeBaseOption {
	return func(o *knowledgeBaseOptions) { o.region = region }
}

// WithKnowledgeBaseTopK sets the maximum number of results to retrieve.
func WithKnowledgeBaseTopK(k int) KnowledgeBaseOption {
	return func(o *knowledgeBaseOptions) { o.topK = k }
}

// WithKnowledgeBaseScoreThreshold sets the minimum relevance score for returned documents.
func WithKnowledgeBaseScoreThreshold(t float64) KnowledgeBaseOption {
	return func(o *knowledgeBaseOptions) { o.scoreThreshold = t }
}

// NewKnowledgeBaseRetriever creates a KnowledgeBaseRetriever for the given Knowledge Base ID.
// Region is resolved in order: WithKnowledgeBaseRegion option → AWS_REGION env → "us-east-1".
// Defaults: topK=5, scoreThreshold=0.0.
func NewKnowledgeBaseRetriever(knowledgeBaseID string, opts ...KnowledgeBaseOption) (*KnowledgeBaseRetriever, error) {
	o := &knowledgeBaseOptions{
		topK:           5,
		scoreThreshold: 0.0,
	}
	for _, fn := range opts {
		fn(o)
	}

	if o.topK < 1 {
		return nil, fmt.Errorf("bedrock knowledge base: topK must be >= 1")
	}

	region := o.region
	if region == "" {
		region = os.Getenv("AWS_REGION")
	}
	if region == "" {
		region = "us-east-1"
	}

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(), awsconfig.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("bedrock knowledge base: load aws config: %w", err)
	}

	return &KnowledgeBaseRetriever{
		client:          bedrockagentruntime.NewFromConfig(cfg),
		knowledgeBaseID: knowledgeBaseID,
		topK:            o.topK,
		scoreThreshold:  o.scoreThreshold,
	}, nil
}

// Retrieve fetches relevant documents from the Bedrock Knowledge Base for the given query.
func (r *KnowledgeBaseRetriever) Retrieve(ctx context.Context, query string) ([]agent.Document, error) {
	if query == "" {
		return nil, fmt.Errorf("bedrock knowledge base: query must not be empty")
	}

	input := &bedrockagentruntime.RetrieveInput{
		KnowledgeBaseId: &r.knowledgeBaseID,
		RetrievalQuery: &types.KnowledgeBaseQuery{
			Text: &query,
		},
		RetrievalConfiguration: &types.KnowledgeBaseRetrievalConfiguration{
			VectorSearchConfiguration: &types.KnowledgeBaseVectorSearchConfiguration{
				NumberOfResults: aws.Int32(int32(r.topK)),
			},
		},
	}

	output, err := r.client.Retrieve(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("bedrock knowledge base: retrieve: %w", err)
	}

	docs := mapBedrockResults(output.RetrievalResults)
	docs = filterByScore(docs, r.scoreThreshold)
	return docs, nil
}

// mapBedrockResults maps Bedrock retrieval results to agent.Document values.
func mapBedrockResults(results []types.KnowledgeBaseRetrievalResult) []agent.Document {
	docs := make([]agent.Document, 0, len(results))
	for _, result := range results {
		doc := agent.Document{
			Metadata: make(map[string]string),
		}

		if result.Content != nil && result.Content.Text != nil {
			doc.Content = *result.Content.Text
		}

		if result.Score != nil {
			doc.Metadata["score"] = strconv.FormatFloat(*result.Score, 'f', -1, 64)
		}

		if result.Location != nil && result.Location.S3Location != nil && result.Location.S3Location.Uri != nil {
			doc.Metadata["source"] = *result.Location.S3Location.Uri
		}

		docs = append(docs, doc)
	}
	return docs
}

// filterByScore filters documents whose score metadata is below the threshold.
// Returns []agent.Document{} (not nil) when no documents pass.
func filterByScore(docs []agent.Document, threshold float64) []agent.Document {
	if threshold <= 0.0 {
		return docs
	}
	result := []agent.Document{}
	for _, doc := range docs {
		score, err := strconv.ParseFloat(doc.Metadata["score"], 64)
		if err != nil {
			continue
		}
		if score >= threshold {
			result = append(result, doc)
		}
	}
	return result
}
