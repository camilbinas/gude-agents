// Package bedrock provides Bedrock-backed RAG components: an embedder and a
// Knowledge Base retriever.
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

// Embedder implements agent.Embedder using the AWS Bedrock InvokeModel API.
// It supports Amazon Titan Embeddings V2 and Cohere Embed v3/v4 models,
// selecting the correct request/response format by model ID prefix.
type Embedder struct {
	client  *bedrockruntime.Client
	modelID string
}

// embedderOptions holds configuration for the Embedder constructor.
type embedderOptions struct {
	region string
}

// EmbedderOption configures the Embedder.
type EmbedderOption func(*embedderOptions)

// WithRegion sets a custom AWS region for the Bedrock embedder client.
func WithRegion(region string) EmbedderOption {
	return func(o *embedderOptions) { o.region = region }
}

// NewEmbedder creates a new Embedder. It loads AWS config from the default
// credential chain and accepts optional configuration.
func NewEmbedder(modelID string, opts ...EmbedderOption) (*Embedder, error) {
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

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(), awsconfig.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("bedrock embedder: load aws config: %w", err)
	}

	return &Embedder{
		client:  bedrockruntime.NewFromConfig(cfg),
		modelID: modelID,
	}, nil
}

// TitanEmbedV2 creates an Embedder for Amazon Titan Embeddings V2.
func TitanEmbedV2(opts ...EmbedderOption) (*Embedder, error) {
	return NewEmbedder("amazon.titan-embed-text-v2:0", opts...)
}

// CohereEmbedEnglishV3 creates an Embedder for Cohere Embed English v3.
func CohereEmbedEnglishV3(opts ...EmbedderOption) (*Embedder, error) {
	return NewEmbedder("cohere.embed-english-v3", opts...)
}

// CohereEmbedMultilingualV3 creates an Embedder for Cohere Embed Multilingual v3.
func CohereEmbedMultilingualV3(opts ...EmbedderOption) (*Embedder, error) {
	return NewEmbedder("cohere.embed-multilingual-v3", opts...)
}

// CohereEmbedV4 creates an Embedder for Cohere Embed v4 (multimodal, EU cross-region).
func CohereEmbedV4(opts ...EmbedderOption) (*Embedder, error) {
	return NewEmbedder("eu.cohere.embed-v4:0", opts...)
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
type cohereEmbedV4Response struct {
	Embeddings struct {
		Float [][]float64 `json:"float"`
	} `json:"embeddings"`
}

// Embed converts text into a float vector using the Bedrock InvokeModel API.
func (e *Embedder) Embed(ctx context.Context, text string) ([]float64, error) {
	if text == "" {
		return nil, fmt.Errorf("bedrock embedder: text must not be empty")
	}

	var reqBody []byte
	var err error

	isCohere := strings.HasPrefix(e.modelID, "cohere.") ||
		strings.HasPrefix(e.modelID, "eu.cohere.") ||
		strings.HasPrefix(e.modelID, "us.cohere.") ||
		strings.HasPrefix(e.modelID, "global.cohere.")

	if isCohere {
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

	if isCohere {
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
