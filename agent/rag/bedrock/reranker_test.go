package bedrock

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime"
	"github.com/camilbinas/gude-agents/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// testAgentRuntimeClient creates a bedrockagentruntime client pointed at a
// local HTTP test server with dummy credentials, fully isolated from the
// host environment.
func testAgentRuntimeClient(t *testing.T, endpoint string) *bedrockagentruntime.Client {
	t.Helper()
	for _, key := range []string{
		"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN",
		"AWS_REGION", "AWS_DEFAULT_REGION", "AWS_PROFILE",
		"AWS_BEARER_TOKEN_BEDROCK",
	} {
		t.Setenv(key, "")
	}
	return bedrockagentruntime.New(bedrockagentruntime.Options{
		Region:       "us-east-1",
		Credentials:  credentials.NewStaticCredentialsProvider("AKID", "SECRET", "SESSION"),
		BaseEndpoint: aws.String(endpoint),
	})
}

func TestNewReranker_EmptyModelID(t *testing.T) {
	_, err := NewReranker("")
	require.Error(t, err)
	assert.Equal(t, "bedrock reranker: modelID must not be empty", err.Error())
}

func TestNewReranker_Defaults(t *testing.T) {
	t.Setenv("AWS_REGION", "us-east-1")
	r, err := NewReranker("cohere.rerank-v3-5:0")
	require.NoError(t, err)
	require.NotNil(t, r)
	assert.Equal(t, "arn:aws:bedrock:us-east-1::foundation-model/cohere.rerank-v3-5:0", r.modelARN)
	assert.Equal(t, 0, r.topN)
}

func TestNewReranker_WithFullARN(t *testing.T) {
	t.Setenv("AWS_REGION", "us-east-1")
	arn := "arn:aws:bedrock:eu-west-1::foundation-model/cohere.rerank-v3-5:0"
	r, err := NewReranker(arn)
	require.NoError(t, err)
	assert.Equal(t, arn, r.modelARN, "full ARN should be used as-is")
}

func TestNewReranker_WithOptions(t *testing.T) {
	t.Setenv("AWS_REGION", "us-east-1")
	r, err := NewReranker(
		"cohere.rerank-v3-5:0",
		WithRerankerRegion("eu-west-1"),
		WithRerankerTopN(5),
	)
	require.NoError(t, err)
	assert.Equal(t, 5, r.topN)
	assert.Contains(t, r.modelARN, "eu-west-1")
}

func TestCohereRerank35(t *testing.T) {
	t.Setenv("AWS_REGION", "us-west-2")
	r, err := CohereRerank35()
	require.NoError(t, err)
	assert.Contains(t, r.modelARN, "cohere.rerank-v3-5:0")
	assert.Contains(t, r.modelARN, "us-west-2")
}

func TestAmazonRerank10(t *testing.T) {
	t.Setenv("AWS_REGION", "us-east-1")
	r, err := AmazonRerank10()
	require.NoError(t, err)
	assert.Contains(t, r.modelARN, "amazon.rerank-v1:0")
	assert.Contains(t, r.modelARN, "us-east-1")
}

func TestCohereRerank35_WithRegionOverride(t *testing.T) {
	t.Setenv("AWS_REGION", "us-east-1")
	r, err := CohereRerank35(WithRerankerRegion("ap-southeast-1"))
	require.NoError(t, err)
	assert.Contains(t, r.modelARN, "ap-southeast-1")
}

func TestMustReranker_Panics(t *testing.T) {
	assert.Panics(t, func() {
		MustReranker(nil, fmt.Errorf("boom"))
	})
}

func TestMustReranker_NoPanic(t *testing.T) {
	t.Setenv("AWS_REGION", "us-east-1")
	r, err := NewReranker("cohere.rerank-v3-5:0")
	require.NoError(t, err)
	assert.NotPanics(t, func() {
		got := MustReranker(r, nil)
		assert.Equal(t, r, got)
	})
}

func TestReranker_EmptyDocs(t *testing.T) {
	r := &Reranker{
		client:   bedrockagentruntime.New(bedrockagentruntime.Options{}),
		modelARN: "arn:aws:bedrock:us-east-1::foundation-model/cohere.rerank-v3-5:0",
	}
	docs, err := r.Rerank(context.Background(), "query", nil)
	require.NoError(t, err)
	assert.Nil(t, docs)
}

func TestReranker_EmptyDocsSlice(t *testing.T) {
	r := &Reranker{
		client:   bedrockagentruntime.New(bedrockagentruntime.Options{}),
		modelARN: "arn:aws:bedrock:us-east-1::foundation-model/cohere.rerank-v3-5:0",
	}
	docs, err := r.Rerank(context.Background(), "query", []agent.Document{})
	require.NoError(t, err)
	assert.Empty(t, docs)
}

// rerankAPIResponse mimics the Bedrock Rerank API JSON response.
type rerankAPIResponse struct {
	Results []rerankAPIResult `json:"results"`
}

type rerankAPIResult struct {
	Index          int     `json:"index"`
	RelevanceScore float32 `json:"relevanceScore"`
}

func TestReranker_ReordersDocuments(t *testing.T) {
	// Mock server returns results in a specific order by relevance.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := rerankAPIResponse{
			Results: []rerankAPIResult{
				{Index: 2, RelevanceScore: 0.95},
				{Index: 0, RelevanceScore: 0.80},
				{Index: 1, RelevanceScore: 0.30},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	r := &Reranker{
		client:   testAgentRuntimeClient(t, srv.URL),
		modelARN: "arn:aws:bedrock:us-east-1::foundation-model/cohere.rerank-v3-5:0",
	}

	docs := []agent.Document{
		{Content: "doc-0"},
		{Content: "doc-1"},
		{Content: "doc-2"},
	}

	result, err := r.Rerank(context.Background(), "test query", docs)
	require.NoError(t, err)
	require.Len(t, result, 3)
	assert.Equal(t, "doc-2", result[0].Content) // highest score
	assert.Equal(t, "doc-0", result[1].Content)
	assert.Equal(t, "doc-1", result[2].Content) // lowest score
}

func TestReranker_APIErrorWrapping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"message":"simulated API error"}`))
	}))
	defer srv.Close()

	r := &Reranker{
		client:   testAgentRuntimeClient(t, srv.URL),
		modelARN: "arn:aws:bedrock:us-east-1::foundation-model/cohere.rerank-v3-5:0",
	}

	docs := []agent.Document{{Content: "hello"}}
	_, err := r.Rerank(context.Background(), "query", docs)
	require.Error(t, err)
	assert.True(t, strings.HasPrefix(err.Error(), "bedrock reranker: "),
		"expected error to start with 'bedrock reranker: ', got: %s", err.Error())
}

func TestReranker_RequestFormat(t *testing.T) {
	var capturedBody map[string]interface{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedBody)
		resp := rerankAPIResponse{
			Results: []rerankAPIResult{
				{Index: 0, RelevanceScore: 0.9},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	r := &Reranker{
		client:   testAgentRuntimeClient(t, srv.URL),
		modelARN: "arn:aws:bedrock:us-east-1::foundation-model/cohere.rerank-v3-5:0",
	}

	docs := []agent.Document{{Content: "test doc"}}
	_, err := r.Rerank(context.Background(), "test query", docs)
	require.NoError(t, err)

	// Verify the request has the expected structure.
	require.NotNil(t, capturedBody)

	queries, ok := capturedBody["queries"].([]interface{})
	require.True(t, ok, "expected queries array")
	require.Len(t, queries, 1)

	sources, ok := capturedBody["sources"].([]interface{})
	require.True(t, ok, "expected sources array")
	require.Len(t, sources, 1)

	rerankConfig, ok := capturedBody["rerankingConfiguration"].(map[string]interface{})
	require.True(t, ok, "expected rerankingConfiguration object")
	assert.Equal(t, "BEDROCK_RERANKING_MODEL", rerankConfig["type"])
}

func TestReranker_TopNSetsNumberOfResults(t *testing.T) {
	var capturedBody map[string]interface{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedBody)
		resp := rerankAPIResponse{
			Results: []rerankAPIResult{
				{Index: 0, RelevanceScore: 0.9},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	r := &Reranker{
		client:   testAgentRuntimeClient(t, srv.URL),
		modelARN: "arn:aws:bedrock:us-east-1::foundation-model/cohere.rerank-v3-5:0",
		topN:     3,
	}

	docs := []agent.Document{
		{Content: "a"}, {Content: "b"}, {Content: "c"}, {Content: "d"}, {Content: "e"},
	}
	_, err := r.Rerank(context.Background(), "query", docs)
	require.NoError(t, err)

	// Verify numberOfResults is set in the reranking configuration.
	rerankConfig := capturedBody["rerankingConfiguration"].(map[string]interface{})
	bedrockConfig := rerankConfig["bedrockRerankingConfiguration"].(map[string]interface{})
	numResults, ok := bedrockConfig["numberOfResults"]
	require.True(t, ok, "expected numberOfResults to be set when topN > 0")
	assert.Equal(t, float64(3), numResults)
}

func TestReranker_DescendingScoreOrder(t *testing.T) {
	// Clear AWS env vars once at the outer test level so the rapid inner
	// iterations can create isolated clients without needing t.Setenv.
	for _, key := range []string{
		"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN",
		"AWS_REGION", "AWS_DEFAULT_REGION", "AWS_PROFILE",
		"AWS_BEARER_TOKEN_BEDROCK",
	} {
		t.Setenv(key, "")
	}

	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(1, 10).Draw(rt, "numDocs")

		// Generate random scores for each document.
		results := make([]rerankAPIResult, n)
		for i := range n {
			results[i] = rerankAPIResult{
				Index:          i,
				RelevanceScore: float32(rapid.Float64Range(0.0, 1.0).Draw(rt, fmt.Sprintf("score[%d]", i))),
			}
		}

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resp := rerankAPIResponse{Results: results}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		}))
		defer srv.Close()

		client := bedrockagentruntime.New(bedrockagentruntime.Options{
			Region:       "us-east-1",
			Credentials:  credentials.NewStaticCredentialsProvider("AKID", "SECRET", "SESSION"),
			BaseEndpoint: aws.String(srv.URL),
		})

		rr := &Reranker{
			client:   client,
			modelARN: "arn:aws:bedrock:us-east-1::foundation-model/cohere.rerank-v3-5:0",
		}

		docs := make([]agent.Document, n)
		for i := range n {
			docs[i] = agent.Document{Content: fmt.Sprintf("doc-%d", i)}
		}

		reranked, err := rr.Rerank(context.Background(), "query", docs)
		if err != nil {
			rt.Fatalf("Rerank failed: %v", err)
		}

		if len(reranked) != n {
			rt.Fatalf("expected %d docs, got %d", n, len(reranked))
		}

		// Build a content→score lookup from the mock results.
		scoreByContent := make(map[string]float32, n)
		for _, res := range results {
			scoreByContent[docs[res.Index].Content] = res.RelevanceScore
		}

		// Verify descending score order.
		for i := 0; i < len(reranked)-1; i++ {
			scoreI := scoreByContent[reranked[i].Content]
			scoreJ := scoreByContent[reranked[i+1].Content]
			if scoreI < scoreJ {
				rt.Fatalf("docs not in descending score order: [%d] score=%f < [%d] score=%f",
					i, scoreI, i+1, scoreJ)
			}
		}
	})
}

func TestReranker_PreservesMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := rerankAPIResponse{
			Results: []rerankAPIResult{
				{Index: 1, RelevanceScore: 0.9},
				{Index: 0, RelevanceScore: 0.5},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	rr := &Reranker{
		client:   testAgentRuntimeClient(t, srv.URL),
		modelARN: "arn:aws:bedrock:us-east-1::foundation-model/cohere.rerank-v3-5:0",
	}

	docs := []agent.Document{
		{Content: "first", Metadata: map[string]string{"source": "a.pdf", "page": "1"}},
		{Content: "second", Metadata: map[string]string{"source": "b.pdf", "page": "42"}},
	}

	result, err := rr.Rerank(context.Background(), "query", docs)
	require.NoError(t, err)
	require.Len(t, result, 2)

	// Second doc should come first (higher score).
	assert.Equal(t, "second", result[0].Content)
	assert.Equal(t, "b.pdf", result[0].Metadata["source"])
	assert.Equal(t, "42", result[0].Metadata["page"])

	assert.Equal(t, "first", result[1].Content)
	assert.Equal(t, "a.pdf", result[1].Metadata["source"])
	assert.Equal(t, "1", result[1].Metadata["page"])
}

// TestRerankerInterfaceCompat verifies that Reranker satisfies agent.Reranker
// and can be passed to rag.WithReranker without a type assertion.
func TestRerankerInterfaceCompat(t *testing.T) {
	t.Setenv("AWS_REGION", "us-east-1")
	r, err := NewReranker("cohere.rerank-v3-5:0")
	require.NoError(t, err)

	// This assignment proves the interface is satisfied at compile time.
	var iface agent.Reranker = r
	assert.NotNil(t, iface)
}
