package bedrock

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
)

// ---------------------------------------------------------------------------
// Empty text error
// ---------------------------------------------------------------------------

func TestBedrockEmbedder_EmptyTextError(t *testing.T) {
	// Create an embedder with a dummy client — the empty-text check fires
	// before any network call, so the client is never used.
	e := &BedrockEmbedder{
		client:  bedrockruntime.New(bedrockruntime.Options{}),
		modelID: "amazon.titan-embed-text-v2:0",
	}

	_, err := e.Embed(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty text, got nil")
	}
	want := "bedrock embedder: text must not be empty"
	if err.Error() != want {
		t.Errorf("expected error %q, got %q", want, err.Error())
	}
}

// ---------------------------------------------------------------------------
// Error prefix wrapping
// ---------------------------------------------------------------------------

func TestBedrockEmbedder_ErrorPrefixWrapping(t *testing.T) {
	// Stand up a test HTTP server that always returns 400 Bad Request.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"message":"simulated API error"}`))
	}))
	defer srv.Close()

	// Build an AWS config that points at our test server with dummy creds.
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("AKID", "SECRET", "SESSION")),
	)
	if err != nil {
		t.Fatalf("failed to load aws config: %v", err)
	}

	client := bedrockruntime.NewFromConfig(cfg, func(o *bedrockruntime.Options) {
		o.BaseEndpoint = aws.String(srv.URL)
	})

	e := &BedrockEmbedder{
		client:  client,
		modelID: "amazon.titan-embed-text-v2:0",
	}

	_, err = e.Embed(context.Background(), "hello world")
	if err == nil {
		t.Fatal("expected error from mock server, got nil")
	}
	if !strings.HasPrefix(err.Error(), "bedrock embedder: ") {
		t.Errorf("expected error to start with %q, got %q", "bedrock embedder: ", err.Error())
	}
}

// ---------------------------------------------------------------------------
// Constructor with region option
// ---------------------------------------------------------------------------

func TestNewBedrockEmbedder_WithRegionOption(t *testing.T) {
	// NewBedrockEmbedder loads real AWS config, which should succeed even
	// without valid credentials — we just verify it doesn't error out.
	emb, err := NewBedrockEmbedder("amazon.titan-embed-text-v2:0", WithEmbedderRegion("eu-west-1"))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if emb == nil {
		t.Fatal("expected non-nil embedder")
	}
}

// ---------------------------------------------------------------------------
// Cohere request/response format
// ---------------------------------------------------------------------------

func TestBedrockEmbedder_CohereRequestFormat(t *testing.T) {
	var capturedBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody = make([]byte, r.ContentLength)
		r.Body.Read(capturedBody)

		// Return a valid Cohere embed response.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"abc","embeddings":[[0.1,0.2,0.3]],"texts":["hello"],"response_type":"embeddings"}`))
	}))
	defer srv.Close()

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("AKID", "SECRET", "SESSION")),
	)
	if err != nil {
		t.Fatalf("failed to load aws config: %v", err)
	}

	client := bedrockruntime.NewFromConfig(cfg, func(o *bedrockruntime.Options) {
		o.BaseEndpoint = aws.String(srv.URL)
	})

	e := &BedrockEmbedder{client: client, modelID: "cohere.embed-english-v3"}

	vec, err := e.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the returned vector matches the mock response.
	if len(vec) != 3 {
		t.Fatalf("expected 3 dimensions, got %d", len(vec))
	}

	// Verify the request used Cohere format (texts array, not inputText).
	body := string(capturedBody)
	if !strings.Contains(body, `"texts"`) {
		t.Errorf("expected Cohere request to contain 'texts', got: %s", body)
	}
	if !strings.Contains(body, `"input_type"`) {
		t.Errorf("expected Cohere request to contain 'input_type', got: %s", body)
	}
	if strings.Contains(body, `"inputText"`) {
		t.Errorf("Cohere request should not contain Titan's 'inputText' field, got: %s", body)
	}
}

func TestBedrockEmbedder_TitanRequestFormat(t *testing.T) {
	var capturedBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody = make([]byte, r.ContentLength)
		r.Body.Read(capturedBody)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"embedding":[0.1,0.2,0.3],"inputTextTokenCount":3}`))
	}))
	defer srv.Close()

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("AKID", "SECRET", "SESSION")),
	)
	if err != nil {
		t.Fatalf("failed to load aws config: %v", err)
	}

	client := bedrockruntime.NewFromConfig(cfg, func(o *bedrockruntime.Options) {
		o.BaseEndpoint = aws.String(srv.URL)
	})

	e := &BedrockEmbedder{client: client, modelID: "amazon.titan-embed-text-v2:0"}

	vec, err := e.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(vec) != 3 {
		t.Fatalf("expected 3 dimensions, got %d", len(vec))
	}

	body := string(capturedBody)
	if !strings.Contains(body, `"inputText"`) {
		t.Errorf("expected Titan request to contain 'inputText', got: %s", body)
	}
	if strings.Contains(body, `"texts"`) {
		t.Errorf("Titan request should not contain Cohere's 'texts' field, got: %s", body)
	}
}

func TestBedrockEmbedder_CohereEmptyEmbeddingsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"abc","embeddings":[],"texts":[],"response_type":"embeddings"}`))
	}))
	defer srv.Close()

	cfg, _ := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("AKID", "SECRET", "SESSION")),
	)
	client := bedrockruntime.NewFromConfig(cfg, func(o *bedrockruntime.Options) {
		o.BaseEndpoint = aws.String(srv.URL)
	})

	e := &BedrockEmbedder{client: client, modelID: "cohere.embed-multilingual-v3"}

	_, err := e.Embed(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error for empty embeddings, got nil")
	}
	if !strings.Contains(err.Error(), "empty embeddings") {
		t.Errorf("expected 'empty embeddings' error, got: %v", err)
	}
}
