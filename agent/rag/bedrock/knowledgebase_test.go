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

func TestMapBedrockResultsProperty(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(1, 20).Draw(rt, "n")

		results := make([]types.KnowledgeBaseRetrievalResult, n)
		texts := make([]string, n)
		scores := make([]float64, n)
		uris := make([]*string, n)

		for i := 0; i < n; i++ {
			text := rapid.StringMatching(`[a-zA-Z0-9 .,!?]{1,200}`).Draw(rt, "text")
			texts[i] = text
			score := rapid.Float64Range(0.0, 1.0).Draw(rt, "score")
			scores[i] = score

			var uri *string
			if rapid.Bool().Draw(rt, "hasURI") {
				u := "s3://bucket-" + rapid.StringMatching(`[a-z0-9]{3,10}`).Draw(rt, "bucket") + "/key"
				uri = aws.String(u)
			}
			uris[i] = uri

			result := types.KnowledgeBaseRetrievalResult{
				Content: &types.RetrievalResultContent{Text: aws.String(text)},
				Score:   aws.Float64(score),
			}
			if uri != nil {
				result.Location = &types.RetrievalResultLocation{
					S3Location: &types.RetrievalResultS3Location{Uri: uri},
				}
			}
			results[i] = result
		}

		docs := mapBedrockResults(results)

		if len(docs) != n {
			rt.Fatalf("expected %d documents, got %d", n, len(docs))
		}

		for i, doc := range docs {
			if doc.Content != texts[i] {
				rt.Fatalf("doc[%d]: Content = %q, want %q", i, doc.Content, texts[i])
			}
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

func TestFilterByScoreProperty(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(0, 20).Draw(rt, "n")
		docs := make([]agent.Document, n)
		for i := range n {
			score := rapid.Float64Range(0.0, 1.0).Draw(rt, "score")
			docs[i] = agent.Document{
				Content:  "doc",
				Metadata: map[string]string{"score": strconv.FormatFloat(score, 'f', -1, 64)},
			}
		}
		threshold := rapid.Float64Range(0.0, 1.0).Draw(rt, "threshold")
		result := filterByScore(docs, threshold)

		for _, doc := range result {
			score, err := strconv.ParseFloat(doc.Metadata["score"], 64)
			if err != nil {
				rt.Fatalf("returned doc has unparseable score %q: %v", doc.Metadata["score"], err)
			}
			if score < threshold {
				rt.Fatalf("returned doc with score %v is below threshold %v", score, threshold)
			}
		}
	})
}

func TestNewKnowledgeBaseRetrieverDefaults(t *testing.T) {
	t.Setenv("AWS_REGION", "us-east-1")
	r, err := NewKnowledgeBaseRetriever("kb-test-id")
	require.NoError(t, err)
	require.NotNil(t, r)
	assert.Equal(t, 5, r.topK)
	assert.Equal(t, 0.0, r.scoreThreshold)
	assert.Equal(t, "kb-test-id", r.knowledgeBaseID)
}

func TestNewKnowledgeBaseRetrieverTopKValidation(t *testing.T) {
	t.Setenv("AWS_REGION", "us-east-1")

	_, err := NewKnowledgeBaseRetriever("kb-id", WithKnowledgeBaseTopK(0))
	require.Error(t, err)
	assert.Equal(t, "bedrock knowledge base: topK must be >= 1", err.Error())

	r, err := NewKnowledgeBaseRetriever("kb-id", WithKnowledgeBaseTopK(1))
	require.NoError(t, err)
	assert.Equal(t, 1, r.topK)
}

func TestKnowledgeBaseRetrieverEmptyQuery(t *testing.T) {
	t.Setenv("AWS_REGION", "us-east-1")
	r, err := NewKnowledgeBaseRetriever("kb-id")
	require.NoError(t, err)
	_, err = r.Retrieve(t.Context(), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bedrock knowledge base: query must not be empty")
}
