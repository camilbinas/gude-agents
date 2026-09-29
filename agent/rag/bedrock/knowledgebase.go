package bedrock

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime/types"
	"github.com/camilbinas/gude-agents/agent/rag"
)

// Compile-time assertion that KnowledgeBaseRetriever satisfies rag.Retriever.
var _ rag.Retriever = (*KnowledgeBaseRetriever)(nil)

// KnowledgeBaseRetriever retrieves documents from an AWS Bedrock Knowledge Base.
type KnowledgeBaseRetriever struct {
	client          *bedrockagentruntime.Client
	knowledgeBaseID string
	maxResults      int
	scoreThreshold  float64
}

// KnowledgeBaseOption configures a KnowledgeBaseRetriever.
type KnowledgeBaseOption func(*knowledgeBaseOptions)

type knowledgeBaseOptions struct {
	region         string
	maxResults     int
	scoreThreshold float64
}

// WithKnowledgeBaseRegion sets the AWS region for the Bedrock agent runtime client.
func WithKnowledgeBaseRegion(region string) KnowledgeBaseOption {
	return func(o *knowledgeBaseOptions) { o.region = region }
}

// WithKnowledgeBaseMaxResults sets the maximum number of results to retrieve.
func WithKnowledgeBaseMaxResults(n int) KnowledgeBaseOption {
	return func(o *knowledgeBaseOptions) { o.maxResults = n }
}

// WithKnowledgeBaseScoreThreshold sets the minimum relevance score for returned documents.
func WithKnowledgeBaseScoreThreshold(t float64) KnowledgeBaseOption {
	return func(o *knowledgeBaseOptions) { o.scoreThreshold = t }
}

// NewKnowledgeBaseRetriever creates a KnowledgeBaseRetriever for the given Knowledge Base ID.
// Region is resolved in order: WithKnowledgeBaseRegion option → AWS_REGION env → "us-east-1".
// Defaults: maxResults=5, scoreThreshold=0.0.
func NewKnowledgeBaseRetriever(knowledgeBaseID string, opts ...KnowledgeBaseOption) (*KnowledgeBaseRetriever, error) {
	o := &knowledgeBaseOptions{
		maxResults:     5,
		scoreThreshold: 0.0,
	}
	for _, fn := range opts {
		fn(o)
	}

	if o.maxResults < 1 {
		return nil, fmt.Errorf("bedrock knowledge base: maxResults must be >= 1")
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
		maxResults:      o.maxResults,
		scoreThreshold:  o.scoreThreshold,
	}, nil
}

// Retrieve fetches relevant documents from the Bedrock Knowledge Base for the given query.
func (r *KnowledgeBaseRetriever) Retrieve(ctx context.Context, query string) ([]rag.Document, error) {
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
				NumberOfResults: aws.Int32(int32(r.maxResults)),
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

// mapBedrockResults maps Bedrock retrieval results to rag.Document values.
func mapBedrockResults(results []types.KnowledgeBaseRetrievalResult) []rag.Document {
	docs := make([]rag.Document, 0, len(results))
	for _, result := range results {
		doc := rag.Document{Metadata: make(map[string]string)}

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
func filterByScore(docs []rag.Document, threshold float64) []rag.Document {
	if threshold <= 0.0 {
		return docs
	}
	result := []rag.Document{}
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
