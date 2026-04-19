// Package openai provides OpenAI-backed RAG components: an embedder and a
// Vector Store retriever.
package openai

import (
	"context"
	"fmt"
	"os"

	openaisdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

// Embedder implements agent.Embedder using the OpenAI Embeddings API.
type Embedder struct {
	client *openaisdk.Client
	model  string
}

// embedderOptions holds configuration for the Embedder constructor.
type embedderOptions struct {
	apiKey  string
	baseURL string
	model   string
}

// EmbedderOption configures the Embedder.
type EmbedderOption func(*embedderOptions)

// WithEmbedderAPIKey sets the OpenAI API key. Defaults to OPENAI_API_KEY env var.
func WithEmbedderAPIKey(key string) EmbedderOption {
	return func(o *embedderOptions) { o.apiKey = key }
}

// WithEmbedderBaseURL sets a custom base URL for OpenAI-compatible endpoints.
func WithEmbedderBaseURL(url string) EmbedderOption {
	return func(o *embedderOptions) { o.baseURL = url }
}

// WithEmbedderModel sets the embedding model name. Default: "text-embedding-3-small".
func WithEmbedderModel(model string) EmbedderOption {
	return func(o *embedderOptions) { o.model = model }
}

// MustEmbedder is a helper that wraps a (*Embedder, error) call and panics on error.
// Use it to collapse embedder creation into a single line in examples and scripts.
//
//	embedder := ragopenai.MustEmbedder(ragopenai.EmbeddingSmall())
func MustEmbedder(e *Embedder, err error) *Embedder {
	if err != nil {
		panic("openai embedder: " + err.Error())
	}
	return e
}

// NewEmbedder creates a new Embedder.
func NewEmbedder(opts ...EmbedderOption) (*Embedder, error) {
	o := &embedderOptions{model: "text-embedding-3-small"}
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
	return &Embedder{client: &client, model: o.model}, nil
}

// EmbeddingSmall creates an Embedder using text-embedding-3-small.
func EmbeddingSmall(opts ...EmbedderOption) (*Embedder, error) {
	return NewEmbedder(append([]EmbedderOption{WithEmbedderModel("text-embedding-3-small")}, opts...)...)
}

// EmbeddingLarge creates an Embedder using text-embedding-3-large.
func EmbeddingLarge(opts ...EmbedderOption) (*Embedder, error) {
	return NewEmbedder(append([]EmbedderOption{WithEmbedderModel("text-embedding-3-large")}, opts...)...)
}

// Embed converts text into a float vector using the OpenAI Embeddings API.
func (e *Embedder) Embed(ctx context.Context, text string) ([]float64, error) {
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
