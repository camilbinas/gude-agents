package bedrock

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
)

// BedrockEmbedder implements agent.Embedder using the AWS Bedrock
// InvokeModel API. It supports Amazon Titan Embeddings V2 and Cohere Embed v3
// models, selecting the correct request/response format by model ID prefix.
// Documented in docs/rag.md and docs/providers.md — update when changing constructor or options.
type BedrockEmbedder struct {
	client  *bedrockruntime.Client
	modelID string
}

// embedderOptions holds configuration for the BedrockEmbedder constructor.
type embedderOptions struct {
	region string
}

// EmbedderOption configures the BedrockEmbedder.
type EmbedderOption func(*embedderOptions)

// WithEmbedderRegion sets a custom AWS region for the Bedrock embedder client.
func WithEmbedderRegion(region string) EmbedderOption {
	return func(o *embedderOptions) { o.region = region }
}

// NewBedrockEmbedder creates a new BedrockEmbedder. It loads AWS config from
// the default credential chain and accepts optional configuration.
func NewBedrockEmbedder(modelID string, opts ...EmbedderOption) (*BedrockEmbedder, error) {
	o := &embedderOptions{}
	for _, fn := range opts {
		fn(o)
	}

	region := o.region
	if region == "" {
		region = os.Getenv("AWS_REGION")
	}
	if region == "" {
		region = "us-east-1"
	}

	var cfgOpts []func(*awsconfig.LoadOptions) error
	cfgOpts = append(cfgOpts, awsconfig.WithRegion(region))

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(), cfgOpts...)
	if err != nil {
		return nil, fmt.Errorf("bedrock embedder: load aws config: %w", err)
	}

	return &BedrockEmbedder{
		client:  bedrockruntime.NewFromConfig(cfg),
		modelID: modelID,
	}, nil
}

// titanEmbedRequest is the JSON request body for Titan Embeddings V2.
type titanEmbedRequest struct {
	InputText  string `json:"inputText"`
	Dimensions int    `json:"dimensions"`
	Normalize  bool   `json:"normalize"`
}

// titanEmbedResponse is the JSON response body from Titan Embeddings V2.
type titanEmbedResponse struct {
	Embedding []float64 `json:"embedding"`
}

// cohereEmbedRequest is the JSON request body for Cohere Embed v3 on Bedrock.
type cohereEmbedRequest struct {
	Texts     []string `json:"texts"`
	InputType string   `json:"input_type"`
	Truncate  string   `json:"truncate"`
}

// cohereEmbedV4Request is the JSON request body for Cohere Embed v4 on Bedrock.
// v4 is multimodal and requires both texts and images fields (images can be empty for text-only).
type cohereEmbedV4Request struct {
	Texts     []string `json:"texts"`
	Images    []string `json:"images"`
	InputType string   `json:"input_type"`
}

// cohereEmbedResponse is the JSON response body from Cohere Embed v3 on Bedrock.
type cohereEmbedResponse struct {
	Embeddings [][]float64 `json:"embeddings"`
}

// cohereEmbedV4Response is the JSON response body from Cohere Embed v4 on Bedrock.
// v4 returns embeddings nested under a type key: {"embeddings": {"float": [[...]]}}
type cohereEmbedV4Response struct {
	Embeddings struct {
		Float [][]float64 `json:"float"`
	} `json:"embeddings"`
}

// Embed converts text into a float vector using the Bedrock InvokeModel API.
// Returns an error if text is empty or the API call fails.
func (e *BedrockEmbedder) Embed(ctx context.Context, text string) ([]float64, error) {
	if text == "" {
		return nil, fmt.Errorf("bedrock embedder: text must not be empty")
	}

	var reqBody []byte
	var err error

	if strings.HasPrefix(e.modelID, "cohere.") || strings.HasPrefix(e.modelID, "eu.cohere.") || strings.HasPrefix(e.modelID, "us.cohere.") || strings.HasPrefix(e.modelID, "global.cohere.") {
		// v4 uses a single text field; v3 uses a texts array.
		if strings.Contains(e.modelID, "embed-v4") {
			reqBody, err = json.Marshal(cohereEmbedV4Request{
				Texts:     []string{text},
				Images:    []string{},
				InputType: "search_document",
			})
		} else {
			reqBody, err = json.Marshal(cohereEmbedRequest{
				Texts:     []string{text},
				InputType: "search_document",
				Truncate:  "END",
			})
		}
	} else {
		// Default: Titan Embeddings V2 format.
		reqBody, err = json.Marshal(titanEmbedRequest{
			InputText:  text,
			Dimensions: 1024,
			Normalize:  true,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("bedrock embedder: marshal request: %w", err)
	}

	out, err := e.client.InvokeModel(ctx, &bedrockruntime.InvokeModelInput{
		ModelId:     aws.String(e.modelID),
		ContentType: aws.String("application/json"),
		Accept:      aws.String("application/json"),
		Body:        reqBody,
	})
	if err != nil {
		return nil, fmt.Errorf("bedrock embedder: %w", err)
	}

	if strings.HasPrefix(e.modelID, "cohere.") || strings.HasPrefix(e.modelID, "eu.cohere.") || strings.HasPrefix(e.modelID, "us.cohere.") || strings.HasPrefix(e.modelID, "global.cohere.") {
		if strings.Contains(e.modelID, "embed-v4") {
			var resp cohereEmbedV4Response
			if err := json.Unmarshal(out.Body, &resp); err != nil {
				return nil, fmt.Errorf("bedrock embedder: unmarshal response: %w", err)
			}
			if len(resp.Embeddings.Float) == 0 {
				return nil, fmt.Errorf("bedrock embedder: empty embeddings in response")
			}
			return resp.Embeddings.Float[0], nil
		}
		var resp cohereEmbedResponse
		if err := json.Unmarshal(out.Body, &resp); err != nil {
			return nil, fmt.Errorf("bedrock embedder: unmarshal response: %w", err)
		}
		if len(resp.Embeddings) == 0 {
			return nil, fmt.Errorf("bedrock embedder: empty embeddings in response")
		}
		return resp.Embeddings[0], nil
	}

	var resp titanEmbedResponse
	if err := json.Unmarshal(out.Body, &resp); err != nil {
		return nil, fmt.Errorf("bedrock embedder: unmarshal response: %w", err)
	}
	return resp.Embedding, nil
}
