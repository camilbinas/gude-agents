package bedrock

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
)

// testClient creates a Bedrock client pointed at a local HTTP test server
// with dummy credentials, fully isolated from the host environment.
//
// The AWS SDK v2 picks up env vars like AWS_BEARER_TOKEN_BEDROCK regardless
// of what credentials you pass explicitly — the bearer token middleware is
// separate from the credentials provider. When a bearer token is present,
// the SDK refuses to send requests over plain HTTP (requires HTTPS), which
// breaks httptest.NewServer-based tests. Clearing all AWS env vars ensures
// the client only uses the explicitly provided static credentials.
func testClient(t *testing.T, endpoint string) *bedrockruntime.Client {
	t.Helper()
	for _, key := range []string{
		"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN",
		"AWS_REGION", "AWS_DEFAULT_REGION", "AWS_PROFILE",
		"AWS_BEARER_TOKEN_BEDROCK",
	} {
		t.Setenv(key, "")
	}
	return bedrockruntime.New(bedrockruntime.Options{
		Region:       "us-east-1",
		Credentials:  credentials.NewStaticCredentialsProvider("AKID", "SECRET", "SESSION"),
		BaseEndpoint: aws.String(endpoint),
	})
}

func TestBedrockEmbedder_EmptyTextError(t *testing.T) {
	e := &Embedder{
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

func TestBedrockEmbedder_ErrorPrefixWrapping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"message":"simulated API error"}`))
	}))
	defer srv.Close()

	e := &Embedder{client: testClient(t, srv.URL), modelID: "amazon.titan-embed-text-v2:0"}

	_, err := e.Embed(context.Background(), "hello world")
	if err == nil {
		t.Fatal("expected error from mock server, got nil")
	}
	if !strings.HasPrefix(err.Error(), "bedrock embedder: ") {
		t.Errorf("expected error to start with %q, got %q", "bedrock embedder: ", err.Error())
	}
}

func TestNewEmbedder_WithRegionOption(t *testing.T) {
	emb, err := NewEmbedder("amazon.titan-embed-text-v2:0", WithRegion("eu-west-1"))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if emb == nil {
		t.Fatal("expected non-nil embedder")
	}
}

func TestBedrockEmbedder_CohereRequestFormat(t *testing.T) {
	var capturedBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody = make([]byte, r.ContentLength)
		r.Body.Read(capturedBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"abc","embeddings":[[0.1,0.2,0.3]],"texts":["hello"],"response_type":"embeddings"}`))
	}))
	defer srv.Close()

	e := &Embedder{client: testClient(t, srv.URL), modelID: "cohere.embed-english-v3"}

	vec, err := e.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vec) != 3 {
		t.Fatalf("expected 3 dimensions, got %d", len(vec))
	}

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

	e := &Embedder{client: testClient(t, srv.URL), modelID: "amazon.titan-embed-text-v2:0"}

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

	e := &Embedder{client: testClient(t, srv.URL), modelID: "cohere.embed-multilingual-v3"}

	_, err := e.Embed(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error for empty embeddings, got nil")
	}
	if !strings.Contains(err.Error(), "empty embeddings") {
		t.Errorf("expected 'empty embeddings' error, got: %v", err)
	}
}
