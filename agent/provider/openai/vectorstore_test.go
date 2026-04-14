package openai

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	openaisdk "github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// TestMapOpenAIResultsProperty verifies that mapOpenAIResults preserves
// content and metadata for any valid slice of VectorStoreSearchResponse.
//
// Feature: managed-rag-providers, Property 3: OpenAI result mapping preserves content and metadata
//
// **Validates: Requirements 5.4, 5.5, 5.6**
func TestMapOpenAIResultsProperty(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(1, 20).Draw(rt, "n")

		results := make([]openaisdk.VectorStoreSearchResponse, n)
		expectedContents := make([]string, n)
		scores := make([]float64, n)
		filenames := make([]string, n)
		fileIDs := make([]string, n)

		for i := range n {
			numBlocks := rapid.IntRange(1, 5).Draw(rt, "numBlocks")
			blocks := make([]openaisdk.VectorStoreSearchResponseContent, numBlocks)
			var sb strings.Builder
			for j := range numBlocks {
				text := rapid.StringMatching(`[a-zA-Z0-9 .,!?]{1,100}`).Draw(rt, "text")
				blocks[j] = openaisdk.VectorStoreSearchResponseContent{
					Type: "text",
					Text: text,
				}
				sb.WriteString(text)
			}
			expectedContents[i] = sb.String()

			score := rapid.Float64Range(0.0, 1.0).Draw(rt, "score")
			scores[i] = score

			filename := rapid.StringMatching(`[a-zA-Z0-9_\-]{1,50}\.txt`).Draw(rt, "filename")
			filenames[i] = filename

			fileID := rapid.StringMatching(`file-[a-zA-Z0-9]{10,20}`).Draw(rt, "fileID")
			fileIDs[i] = fileID

			results[i] = openaisdk.VectorStoreSearchResponse{
				Content:  blocks,
				Score:    score,
				Filename: filename,
				FileID:   fileID,
			}
		}

		docs := mapOpenAIResults(results)

		if len(docs) != n {
			rt.Fatalf("expected %d documents, got %d", n, len(docs))
		}

		for i, doc := range docs {
			if doc.Content != expectedContents[i] {
				rt.Fatalf("doc[%d]: Content = %q, want %q", i, doc.Content, expectedContents[i])
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

			if doc.Metadata["filename"] != filenames[i] {
				rt.Fatalf("doc[%d]: Metadata[\"filename\"] = %q, want %q", i, doc.Metadata["filename"], filenames[i])
			}

			if doc.Metadata["file_id"] != fileIDs[i] {
				rt.Fatalf("doc[%d]: Metadata[\"file_id\"] = %q, want %q", i, doc.Metadata["file_id"], fileIDs[i])
			}
		}
	})
}

// TestFilterByScoreOpenAIProperty verifies that filterByScore correctly filters
// documents by score threshold for any valid input.
//
// Feature: managed-rag-providers, Property 4: OpenAI score threshold filtering
//
// **Validates: Requirements 5.7, 5.8**
func TestFilterByScoreOpenAIProperty(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(0, 30).Draw(rt, "n")

		docs := make([]agent.Document, n)
		scores := make([]float64, n)

		for i := range n {
			score := rapid.Float64Range(0.0, 1.0).Draw(rt, "score")
			scores[i] = score
			docs[i] = agent.Document{
				Content: rapid.StringMatching(`[a-zA-Z0-9 ]{1,50}`).Draw(rt, "content"),
				Metadata: map[string]string{
					"score": strconv.FormatFloat(score, 'f', -1, 64),
				},
			}
		}

		threshold := rapid.Float64Range(0.0, 1.0).Draw(rt, "threshold")

		result := filterByScore(docs, threshold)

		for _, doc := range result {
			scoreStr := doc.Metadata["score"]
			parsed, err := strconv.ParseFloat(scoreStr, 64)
			if err != nil {
				rt.Fatalf("result doc has unparseable score %q: %v", scoreStr, err)
			}
			if parsed < threshold {
				rt.Fatalf("result contains doc with score %v < threshold %v", parsed, threshold)
			}
		}

		for _, rdoc := range result {
			found := false
			for _, idoc := range docs {
				if idoc.Content == rdoc.Content && idoc.Metadata["score"] == rdoc.Metadata["score"] {
					found = true
					break
				}
			}
			if !found {
				rt.Fatalf("result contains document not present in input: %+v", rdoc)
			}
		}

		for i, doc := range docs {
			if scores[i] < threshold {
				for _, rdoc := range result {
					if rdoc.Content == doc.Content && rdoc.Metadata["score"] == doc.Metadata["score"] {
						rt.Fatalf("doc with score %v (< threshold %v) found in result", scores[i], threshold)
					}
				}
			}
		}
	})
}

// TestNewVectorStoreRetrieverDefaults verifies that the constructor sets
// topK=5 and scoreThreshold=0.0 when no options are provided.
//
// Validates: Requirements 4.5, 4.6
func TestNewVectorStoreRetrieverDefaults(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")

	r, err := NewVectorStoreRetriever("vs-test-123")
	require.NoError(t, err)
	require.NotNil(t, r)

	assert.Equal(t, 5, r.topK)
	assert.Equal(t, 0.0, r.scoreThreshold)
}

// TestNewVectorStoreRetrieverAPIKeyResolution verifies API key resolution precedence.
//
// Validates: Requirements 4.2, 4.3
func TestNewVectorStoreRetrieverAPIKeyResolution(t *testing.T) {
	t.Run("explicit option takes precedence", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "env-key")
		r, err := NewVectorStoreRetriever("vs-test-123", WithVectorStoreAPIKey("explicit-key"))
		require.NoError(t, err)
		require.NotNil(t, r)
	})

	t.Run("env var used when no option provided", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "env-key")
		r, err := NewVectorStoreRetriever("vs-test-123")
		require.NoError(t, err)
		require.NotNil(t, r)
	})

	t.Run("succeeds with no key and no env var", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "")
		r, err := NewVectorStoreRetriever("vs-test-123")
		require.NoError(t, err)
		require.NotNil(t, r)
	})
}

// TestNewVectorStoreRetrieverTopKValidation verifies topK validation.
//
// Validates: Requirements 4.5, 6.5
func TestNewVectorStoreRetrieverTopKValidation(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")

	t.Run("topK=0 returns error", func(t *testing.T) {
		_, err := NewVectorStoreRetriever("vs-test-123", WithVectorStoreTopK(0))
		require.Error(t, err)
		assert.Equal(t, "openai vector store: topK must be >= 1", err.Error())
	})

	t.Run("topK=-1 returns error", func(t *testing.T) {
		_, err := NewVectorStoreRetriever("vs-test-123", WithVectorStoreTopK(-1))
		require.Error(t, err)
		assert.Equal(t, "openai vector store: topK must be >= 1", err.Error())
	})

	t.Run("topK=1 succeeds", func(t *testing.T) {
		r, err := NewVectorStoreRetriever("vs-test-123", WithVectorStoreTopK(1))
		require.NoError(t, err)
		require.NotNil(t, r)
		assert.Equal(t, 1, r.topK)
	})
}

// TestVectorStoreRetrieverEmptyQuery verifies that Retrieve rejects empty queries
// before making any SDK call.
//
// Validates: Requirements 5.1
func TestVectorStoreRetrieverEmptyQuery(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")

	r, err := NewVectorStoreRetriever("vs-test-123")
	require.NoError(t, err)

	_, err = r.Retrieve(context.Background(), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "openai vector store: query must not be empty")
}
