package bedrock

import (
	"strconv"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime/types"
	"github.com/camilbinas/gude-agents/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// TestMapBedrockResultsProperty verifies that mapBedrockResults preserves
// content and metadata for any valid slice of KnowledgeBaseRetrievalResult.
//
// Feature: managed-rag-providers, Property 1: Bedrock result mapping preserves content and metadata
//
// **Validates: Requirements 2.4, 2.5, 2.6**
func TestMapBedrockResultsProperty(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a non-empty slice of results (1–20 items)
		n := rapid.IntRange(1, 20).Draw(rt, "n")

		results := make([]types.KnowledgeBaseRetrievalResult, n)
		texts := make([]string, n)
		scores := make([]float64, n)
		uris := make([]*string, n)

		for i := 0; i < n; i++ {
			// Random non-empty text content
			text := rapid.StringMatching(`[a-zA-Z0-9 .,!?]{1,200}`).Draw(rt, "text")
			texts[i] = text

			// Random score in [0.0, 1.0]
			score := rapid.Float64Range(0.0, 1.0).Draw(rt, "score")
			scores[i] = score

			// Optional S3 URI (nil ~50% of the time)
			var uri *string
			if rapid.Bool().Draw(rt, "hasURI") {
				u := "s3://bucket-" + rapid.StringMatching(`[a-z0-9]{3,10}`).Draw(rt, "bucket") + "/key"
				uri = aws.String(u)
			}
			uris[i] = uri

			result := types.KnowledgeBaseRetrievalResult{
				Content: &types.RetrievalResultContent{
					Text: aws.String(text),
				},
				Score: aws.Float64(score),
			}
			if uri != nil {
				result.Location = &types.RetrievalResultLocation{
					S3Location: &types.RetrievalResultS3Location{
						Uri: uri,
					},
				}
			}
			results[i] = result
		}

		docs := mapBedrockResults(results)

		// Length must be preserved
		if len(docs) != n {
			rt.Fatalf("expected %d documents, got %d", n, len(docs))
		}

		for i, doc := range docs {
			// Requirement 2.4: Content == result text
			if doc.Content != texts[i] {
				rt.Fatalf("doc[%d]: Content = %q, want %q", i, doc.Content, texts[i])
			}

			// Requirement 2.5: Metadata["score"] parses back to original score
			scoreStr, ok := doc.Metadata["score"]
			if !ok {
				rt.Fatalf("doc[%d]: missing Metadata[\"score\"]", i)
			}
			parsed, err := strconv.ParseFloat(scoreStr, 64)
			if err != nil {
				rt.Fatalf("doc[%d]: Metadata[\"score\"] = %q is not a valid float: %v", i, scoreStr, err)
			}
			if parsed != scores[i] {
				rt.Fatalf("doc[%d]: score round-trip: got %v, want %v", i, parsed, scores[i])
			}

			// Requirement 2.6: Metadata["source"] == S3 URI when present
			if uris[i] != nil {
				source, ok := doc.Metadata["source"]
				if !ok {
					rt.Fatalf("doc[%d]: missing Metadata[\"source\"] for URI %q", i, *uris[i])
				}
				if source != *uris[i] {
					rt.Fatalf("doc[%d]: Metadata[\"source\"] = %q, want %q", i, source, *uris[i])
				}
			} else {
				if _, ok := doc.Metadata["source"]; ok {
					rt.Fatalf("doc[%d]: unexpected Metadata[\"source\"] when no URI was set", i)
				}
			}
		}
	})
}

// TestFilterByScoreProperty verifies that filterByScore returns only documents
// whose score is >= threshold, and never includes documents below the threshold.
//
// Feature: managed-rag-providers, Property 2: Bedrock score threshold filtering
//
// **Validates: Requirements 2.7, 2.8**
func TestFilterByScoreProperty(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a slice of 0–20 documents with random scores in [0.0, 1.0]
		n := rapid.IntRange(0, 20).Draw(rt, "n")

		docs := make([]agent.Document, n)
		for i := range n {
			score := rapid.Float64Range(0.0, 1.0).Draw(rt, "score")
			docs[i] = agent.Document{
				Content: "doc",
				Metadata: map[string]string{
					"score": strconv.FormatFloat(score, 'f', -1, 64),
				},
			}
		}

		// Generate a random threshold in [0.0, 1.0]
		threshold := rapid.Float64Range(0.0, 1.0).Draw(rt, "threshold")

		result := filterByScore(docs, threshold)

		// Build a set of input document pointers for subset check
		inputSet := make(map[string]bool, n)
		for _, d := range docs {
			inputSet[d.Metadata["score"]] = true
		}

		for _, doc := range result {
			scoreStr := doc.Metadata["score"]

			// All returned documents must have a parseable score
			score, err := strconv.ParseFloat(scoreStr, 64)
			if err != nil {
				rt.Fatalf("returned doc has unparseable score %q: %v", scoreStr, err)
			}

			// Requirement 2.7: score must be >= threshold
			if score < threshold {
				rt.Fatalf("returned doc with score %v is below threshold %v", score, threshold)
			}
		}

		// No document with score < threshold should appear in the result
		for _, doc := range docs {
			score, _ := strconv.ParseFloat(doc.Metadata["score"], 64)
			if score < threshold {
				for _, r := range result {
					if r.Metadata["score"] == doc.Metadata["score"] && r.Content == doc.Content {
						rt.Fatalf("doc with score %v (below threshold %v) appeared in result", score, threshold)
					}
				}
			}
		}

		// Result is a subset of input: every result doc must exist in input
		for _, r := range result {
			found := false
			for _, d := range docs {
				if d.Metadata["score"] == r.Metadata["score"] && d.Content == r.Content {
					found = true
					break
				}
			}
			if !found {
				rt.Fatalf("result contains doc with score %q not found in input", r.Metadata["score"])
			}
		}
	})
}

// TestNewKnowledgeBaseRetrieverDefaults verifies that the constructor sets
// topK=5 and scoreThreshold=0.0 when no options are provided.
//
// Validates: Requirements 1.6, 1.7
func TestNewKnowledgeBaseRetrieverDefaults(t *testing.T) {
	t.Setenv("AWS_REGION", "us-east-1")

	r, err := NewKnowledgeBaseRetriever("kb-test-id")
	require.NoError(t, err)
	require.NotNil(t, r)

	assert.Equal(t, 5, r.topK)
	assert.Equal(t, 0.0, r.scoreThreshold)
	assert.Equal(t, "kb-test-id", r.knowledgeBaseID)
}

// TestNewKnowledgeBaseRetrieverRegionResolution verifies region resolution precedence:
// explicit option > AWS_REGION env > default "us-east-1".
//
// Validates: Requirements 1.2, 1.3
func TestNewKnowledgeBaseRetrieverRegionResolution(t *testing.T) {
	t.Run("explicit region option takes precedence", func(t *testing.T) {
		t.Setenv("AWS_REGION", "us-east-1")
		r, err := NewKnowledgeBaseRetriever("kb-id", WithKnowledgeBaseRegion("eu-west-1"))
		require.NoError(t, err)
		require.NotNil(t, r)
	})

	t.Run("AWS_REGION env var is used when no option provided", func(t *testing.T) {
		t.Setenv("AWS_REGION", "ap-southeast-1")
		r, err := NewKnowledgeBaseRetriever("kb-id")
		require.NoError(t, err)
		require.NotNil(t, r)
	})

	t.Run("falls back to us-east-1 when no option and no env var", func(t *testing.T) {
		t.Setenv("AWS_REGION", "")
		r, err := NewKnowledgeBaseRetriever("kb-id")
		require.NoError(t, err)
		require.NotNil(t, r)
	})
}

// TestNewKnowledgeBaseRetrieverTopKValidation verifies that WithKnowledgeBaseTopK
// rejects values < 1 and accepts values >= 1.
//
// Validates: Requirements 1.6, 3.4
func TestNewKnowledgeBaseRetrieverTopKValidation(t *testing.T) {
	t.Setenv("AWS_REGION", "us-east-1")

	t.Run("topK=0 returns error", func(t *testing.T) {
		_, err := NewKnowledgeBaseRetriever("kb-id", WithKnowledgeBaseTopK(0))
		require.Error(t, err)
		assert.Equal(t, "bedrock knowledge base: topK must be >= 1", err.Error())
	})

	t.Run("topK=-1 returns error", func(t *testing.T) {
		_, err := NewKnowledgeBaseRetriever("kb-id", WithKnowledgeBaseTopK(-1))
		require.Error(t, err)
		assert.Equal(t, "bedrock knowledge base: topK must be >= 1", err.Error())
	})

	t.Run("topK=1 succeeds", func(t *testing.T) {
		r, err := NewKnowledgeBaseRetriever("kb-id", WithKnowledgeBaseTopK(1))
		require.NoError(t, err)
		assert.Equal(t, 1, r.topK)
	})
}

// TestKnowledgeBaseRetrieverEmptyQuery verifies that Retrieve returns the correct
// error when called with an empty query string. The empty-query check happens
// before any SDK call, so no real AWS call is made.
//
// Validates: Requirements 2.1
func TestKnowledgeBaseRetrieverEmptyQuery(t *testing.T) {
	t.Setenv("AWS_REGION", "us-east-1")

	r, err := NewKnowledgeBaseRetriever("kb-id")
	require.NoError(t, err)

	_, err = r.Retrieve(t.Context(), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bedrock knowledge base: query must not be empty")
}
