// Package gemini provides a Gemini-backed embedder for the gude-agents RAG pipeline.
package gemini

import (
	"context"
	"fmt"
	"os"

	"google.golang.org/genai"
)

// Embedder implements agent.Embedder using the Gemini Embeddings API.
type Embedder struct {
	client *genai.Client
	model  string
}

// embedderOptions holds configuration for the Embedder constructor.
type embedderOptions struct {
	apiKey string
	model  string
}

// EmbedderOption configures the Embedder.
type EmbedderOption func(*embedderOptions)

// WithAPIKey sets the Gemini API key. Defaults to GEMINI_API_KEY env var,
// falling back to GOOGLE_API_KEY.
func WithAPIKey(key string) EmbedderOption {
	return func(o *embedderOptions) { o.apiKey = key }
}

// WithModel sets the embedding model. Default: "gemini-embedding-001".
func WithModel(model string) EmbedderOption {
	return func(o *embedderOptions) { o.model = model }
}

// MustEmbedder is a helper that wraps a (*Embedder, error) call and panics on error.
// Use it to collapse embedder creation into a single line in examples and scripts.
//
//	embedder := raggemini.MustEmbedder(raggemini.GeminiEmbedding001())
func MustEmbedder(e *Embedder, err error) *Embedder {
	if err != nil {
		panic("gemini embedder: " + err.Error())
	}
	return e
}

// NewEmbedder creates a new Gemini Embedder.
func NewEmbedder(opts ...EmbedderOption) (*Embedder, error) {
	o := &embedderOptions{
		model: "gemini-embedding-001",
	}
	for _, fn := range opts {
		fn(o)
	}

	apiKey := o.apiKey
	if apiKey == "" {
		apiKey = os.Getenv("GEMINI_API_KEY")
	}
	if apiKey == "" {
		apiKey = os.Getenv("GOOGLE_API_KEY")
	}

	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("gemini embedder: create client: %w", err)
	}

	return &Embedder{
		client: client,
		model:  o.model,
	}, nil
}

// GeminiEmbedding001 creates an Embedder using gemini-embedding-001 (768 dimensions).
func GeminiEmbedding001(opts ...EmbedderOption) (*Embedder, error) {
	return NewEmbedder(append([]EmbedderOption{WithModel("gemini-embedding-001")}, opts...)...)
}

// GeminiEmbedding002 creates an Embedder using gemini-embedding-002 (multimodal).
func GeminiEmbedding002(opts ...EmbedderOption) (*Embedder, error) {
	return NewEmbedder(append([]EmbedderOption{WithModel("gemini-embedding-002")}, opts...)...)
}

// Embed converts text into a float vector using the Gemini Embeddings API.
func (e *Embedder) Embed(ctx context.Context, text string) ([]float64, error) {
	if text == "" {
		return nil, fmt.Errorf("gemini embedder: text must not be empty")
	}

	resp, err := e.client.Models.EmbedContent(ctx, e.model,
		[]*genai.Content{
			{Parts: []*genai.Part{genai.NewPartFromText(text)}},
		},
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("gemini embedder: %w", err)
	}

	if len(resp.Embeddings) == 0 || len(resp.Embeddings[0].Values) == 0 {
		return nil, fmt.Errorf("gemini embedder: no embedding returned")
	}

	// Convert float32 to float64 to match the agent.Embedder interface.
	f32 := resp.Embeddings[0].Values
	vec := make([]float64, len(f32))
	for i, v := range f32 {
		vec[i] = float64(v)
	}
	return vec, nil
}
