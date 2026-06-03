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
	client         *bedrockruntime.Client
	modelID        string
	dimensions     int      // Cohere v4: output_dimension
	embeddingTypes []string // Cohere v3/v4: embedding_types
}

// embedderOptions holds configuration for the Embedder constructor.
type embedderOptions struct {
	region         string
	dimensions     int      // Cohere v4 only: output_dimension (256, 512, 1024, 1536)
	embeddingTypes []string // Cohere v3/v4: embedding_types (float, int8, uint8, binary, ubinary)
}

// EmbedderOption configures the Embedder.
type EmbedderOption func(*embedderOptions)

// WithRegion sets a custom AWS region for the Bedrock embedder client.
func WithRegion(region string) EmbedderOption {
	return func(o *embedderOptions) { o.region = region }
}

// WithDimensions sets the output vector dimension for Cohere Embed v4 models.
// Allowed values are 256, 512, 1024, and 1536 (default 1536 when unset).
// This option has no effect on Cohere v3 or Titan models.
func WithDimensions(d int) EmbedderOption {
	return func(o *embedderOptions) { o.dimensions = d }
}

// WithEmbeddingTypes sets the embedding type(s) returned by Cohere v3/v4 models.
// Accepted values: "float" (default), "int8", "uint8", "binary", "ubinary".
// When exactly one type is specified the embedder returns that vector directly.
// When multiple types are specified the model returns a keyed map and the embedder
// returns the "float" vector; if "float" is absent it returns the first type found.
// This option has no effect on Titan models.
func WithEmbeddingTypes(types ...string) EmbedderOption {
	return func(o *embedderOptions) { o.embeddingTypes = types }
}

// MustEmbedder is a helper that wraps a (*Embedder, error) call and panics on error.
// Use it to collapse embedder creation into a single line in examples and scripts.
//
//	embedder := bedrock.MustEmbedder(bedrock.TitanEmbedV2())
func MustEmbedder(e *Embedder, err error) *Embedder {
	if err != nil {
		panic("bedrock embedder: " + err.Error())
	}
	return e
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
		client:         bedrockruntime.NewFromConfig(cfg),
		modelID:        modelID,
		dimensions:     o.dimensions,
		embeddingTypes: o.embeddingTypes,
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
	Texts          []string `json:"texts"`
	InputType      string   `json:"input_type"`
	Truncate       string   `json:"truncate"`
	EmbeddingTypes []string `json:"embedding_types,omitempty"`
}

// cohereEmbedV4Request is the JSON request body for Cohere Embed v4 on Bedrock.
type cohereEmbedV4Request struct {
	Texts           []string `json:"texts"`
	Images          []string `json:"images"`
	InputType       string   `json:"input_type"`
	EmbeddingTypes  []string `json:"embedding_types,omitempty"`
	OutputDimension int      `json:"output_dimension,omitempty"`
}

// cohereEmbedResponse is the JSON response body from Cohere Embed v3 on Bedrock
// when no embedding_types are requested (returns float embeddings directly).
type cohereEmbedResponse struct {
	Embeddings [][]float64 `json:"embeddings"`
}

// cohereEmbedTypedResponse is the JSON response body from Cohere Embed v3/v4
// when embedding_types are specified (embeddings keyed by type name).
type cohereEmbedTypedResponse struct {
	Embeddings map[string][][]float64 `json:"embeddings"`
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
				Texts:           []string{text},
				Images:          []string{},
				InputType:       "search_document",
				EmbeddingTypes:  e.embeddingTypes,
				OutputDimension: e.dimensions,
			})
		} else {
			reqBody, err = json.Marshal(cohereEmbedRequest{
				Texts:          []string{text},
				InputType:      "search_document",
				Truncate:       "END",
				EmbeddingTypes: e.embeddingTypes,
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
			// When embedding_types are specified the v4 response has the same keyed
			// structure as v3; when unspecified it uses the legacy float array shape.
			if len(e.embeddingTypes) > 0 {
				return parseCohereTypedResponse(out.Body, e.embeddingTypes)
			}
			var resp cohereEmbedV4Response
			if err := json.Unmarshal(out.Body, &resp); err != nil {
				return nil, fmt.Errorf("bedrock embedder: unmarshal response: %w", err)
			}
			if len(resp.Embeddings.Float) == 0 {
				return nil, fmt.Errorf("bedrock embedder: empty embeddings in response")
			}
			return resp.Embeddings.Float[0], nil
		}
		// Cohere v3: when embedding_types are set the response shape changes to a
		// keyed map; otherwise it returns a plain float array.
		if len(e.embeddingTypes) > 0 {
			return parseCohereTypedResponse(out.Body, e.embeddingTypes)
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

// parseCohereTypedResponse extracts a float64 vector from a typed Cohere
// response ({"embeddings": {"float": [[...]], "int8": [[...]]}, ...}).
// It prefers the "float" type; if absent it returns the first type in the
// requestedTypes list that is present in the response.
func parseCohereTypedResponse(body []byte, requestedTypes []string) ([]float64, error) {
	var resp cohereEmbedTypedResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("bedrock embedder: unmarshal response: %w", err)
	}
	if len(resp.Embeddings) == 0 {
		return nil, fmt.Errorf("bedrock embedder: empty embeddings in response")
	}
	// Prefer float if present.
	if vecs, ok := resp.Embeddings["float"]; ok && len(vecs) > 0 {
		return vecs[0], nil
	}
	// Fall back to first requested type that is present.
	for _, t := range requestedTypes {
		if vecs, ok := resp.Embeddings[t]; ok && len(vecs) > 0 {
			return vecs[0], nil
		}
	}
	return nil, fmt.Errorf("bedrock embedder: no usable float embeddings in typed response")
}
