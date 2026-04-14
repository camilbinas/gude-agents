package openai

import (
	"context"
	"fmt"
	"os"

	openaisdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

// OpenAIEmbedder implements agent.Embedder using the OpenAI Embeddings API.
// Documented in docs/rag.md and docs/providers.md — update when changing constructor or options.
type OpenAIEmbedder struct {
	client *openaisdk.Client
	model  string
}

// embedderOptions holds configuration for the OpenAIEmbedder constructor.
type embedderOptions struct {
	apiKey  string
	baseURL string
	model   string
}

// EmbedderOption configures the OpenAIEmbedder.
type EmbedderOption func(*embedderOptions)

// WithEmbedderAPIKey sets the OpenAI API key for the embedder.
// Defaults to the OPENAI_API_KEY environment variable.
func WithEmbedderAPIKey(key string) EmbedderOption {
	return func(o *embedderOptions) { o.apiKey = key }
}

// WithEmbedderBaseURL sets a custom base URL for OpenAI-compatible endpoints.
func WithEmbedderBaseURL(url string) EmbedderOption {
	return func(o *embedderOptions) { o.baseURL = url }
}

// WithEmbedderModel sets the embedding model name. Defaults to "text-embedding-3-small".
func WithEmbedderModel(model string) EmbedderOption {
	return func(o *embedderOptions) { o.model = model }
}

// NewOpenAIEmbedder creates a new OpenAIEmbedder. It accepts optional
// configuration for API key, base URL, and model name.
func NewOpenAIEmbedder(opts ...EmbedderOption) (*OpenAIEmbedder, error) {
	o := &embedderOptions{
		model: "text-embedding-3-small",
	}
	for _, fn := range opts {
		fn(o)
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
	return &OpenAIEmbedder{
		client: &client,
		model:  o.model,
	}, nil
}

// Embed converts text into a float vector using the OpenAI Embeddings API.
// Returns an error if text is empty or the API call fails.
func (e *OpenAIEmbedder) Embed(ctx context.Context, text string) ([]float64, error) {
	if text == "" {
		return nil, fmt.Errorf("openai embedder: text must not be empty")
	}

	resp, err := e.client.Embeddings.New(ctx, openaisdk.EmbeddingNewParams{
		Model: openaisdk.EmbeddingModel(e.model),
		Input: openaisdk.EmbeddingNewParamsInputUnion{
			OfString: openaisdk.String(text),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("openai embedder: %w", err)
	}

	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("openai embedder: no embedding returned")
	}

	return resp.Data[0].Embedding, nil
}
