package openai

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
	openaisdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

// Compile-time assertion that VectorStoreRetriever satisfies agent.Retriever.
var _ agent.Retriever = (*VectorStoreRetriever)(nil)

// VectorStoreRetriever retrieves documents from an OpenAI Vector Store.
type VectorStoreRetriever struct {
	client         *openaisdk.Client
	vectorStoreID  string
	topK           int
	scoreThreshold float64
}

// VectorStoreOption configures the VectorStoreRetriever.
type VectorStoreOption func(*vectorStoreOptions)

type vectorStoreOptions struct {
	apiKey         string
	baseURL        string
	topK           int
	scoreThreshold float64
}

// WithVectorStoreAPIKey sets the OpenAI API key. Defaults to OPENAI_API_KEY env var.
func WithVectorStoreAPIKey(key string) VectorStoreOption {
	return func(o *vectorStoreOptions) { o.apiKey = key }
}

// WithVectorStoreBaseURL sets a custom base URL for OpenAI-compatible endpoints.
func WithVectorStoreBaseURL(url string) VectorStoreOption {
	return func(o *vectorStoreOptions) { o.baseURL = url }
}

// WithVectorStoreTopK sets the maximum number of results to return.
func WithVectorStoreTopK(k int) VectorStoreOption {
	return func(o *vectorStoreOptions) { o.topK = k }
}

// WithVectorStoreScoreThreshold sets the minimum relevance score for returned documents.
func WithVectorStoreScoreThreshold(t float64) VectorStoreOption {
	return func(o *vectorStoreOptions) { o.scoreThreshold = t }
}

// NewVectorStoreRetriever creates a new VectorStoreRetriever for the given vector store ID.
func NewVectorStoreRetriever(vectorStoreID string, opts ...VectorStoreOption) (*VectorStoreRetriever, error) {
	o := &vectorStoreOptions{topK: 5}
	for _, fn := range opts {
		fn(o)
	}

	if o.topK < 1 {
		return nil, fmt.Errorf("openai vector store: topK must be >= 1")
	}

	apiKey := o.apiKey
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}

	var clientOpts []option.RequestOption
	if apiKey != "" {
		clientOpts = append(clientOpts, option.WithAPIKey(apiKey))
	}
	if o.baseURL != "" {
		clientOpts = append(clientOpts, option.WithBaseURL(o.baseURL))
	}

	client := openaisdk.NewClient(clientOpts...)
	return &VectorStoreRetriever{
		client:         &client,
		vectorStoreID:  vectorStoreID,
		topK:           o.topK,
		scoreThreshold: o.scoreThreshold,
	}, nil
}

// Retrieve fetches relevant documents from the OpenAI Vector Store for the given query.
func (r *VectorStoreRetriever) Retrieve(ctx context.Context, query string) ([]agent.Document, error) {
	if query == "" {
		return nil, fmt.Errorf("openai vector store: query must not be empty")
	}

	params := openaisdk.VectorStoreSearchParams{
		Query:         openaisdk.VectorStoreSearchParamsQueryUnion{OfString: openaisdk.String(query)},
		MaxNumResults: openaisdk.Int(int64(r.topK)),
	}

	pager := r.client.VectorStores.SearchAutoPaging(ctx, r.vectorStoreID, params)

	var raw []openaisdk.VectorStoreSearchResponse
	for pager.Next() {
		raw = append(raw, pager.Current())
	}
	if err := pager.Err(); err != nil {
		return nil, fmt.Errorf("openai vector store: search: %w", err)
	}

	docs := mapOpenAIResults(raw)
	docs = filterByScore(docs, r.scoreThreshold)
	return docs, nil
}

// mapOpenAIResults maps OpenAI vector store search responses to agent.Document values.
func mapOpenAIResults(results []openaisdk.VectorStoreSearchResponse) []agent.Document {
	docs := make([]agent.Document, 0, len(results))
	for _, result := range results {
		doc := agent.Document{Metadata: make(map[string]string)}

		var sb strings.Builder
		for _, block := range result.Content {
			if block.Type == "text" {
				sb.WriteString(block.Text)
			}
		}
		doc.Content = sb.String()
		doc.Metadata["score"] = strconv.FormatFloat(result.Score, 'f', -1, 64)
		doc.Metadata["filename"] = result.Filename
		doc.Metadata["file_id"] = result.FileID

		docs = append(docs, doc)
	}
	return docs
}

// filterByScore filters documents whose score metadata is below the threshold.
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
