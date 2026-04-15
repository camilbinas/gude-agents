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
				blocks[j] = openaisdk.VectorStoreSearchResponseContent{Type: "text", Text: text}
				sb.WriteString(text)
			}
			expectedContents[i] = sb.String()
			scores[i] = rapid.Float64Range(0.0, 1.0).Draw(rt, "score")
			filenames[i] = rapid.StringMatching(`[a-zA-Z0-9_\-]{1,50}\.txt`).Draw(rt, "filename")
			fileIDs[i] = rapid.StringMatching(`file-[a-zA-Z0-9]{10,20}`).Draw(rt, "fileID")

			results[i] = openaisdk.VectorStoreSearchResponse{
				Content:  blocks,
				Score:    scores[i],
				Filename: filenames[i],
				FileID:   fileIDs[i],
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
			parsed, err := strconv.ParseFloat(doc.Metadata["score"], 64)
			if err != nil || parsed != scores[i] {
				rt.Fatalf("doc[%d]: score mismatch: got %v, want %v", i, parsed, scores[i])
			}
			if doc.Metadata["filename"] != filenames[i] {
				rt.Fatalf("doc[%d]: filename mismatch", i)
			}
			if doc.Metadata["file_id"] != fileIDs[i] {
				rt.Fatalf("doc[%d]: file_id mismatch", i)
			}
		}
	})
}

func TestFilterByScoreOpenAIProperty(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(0, 30).Draw(rt, "n")
		docs := make([]agent.Document, n)
		scores := make([]float64, n)
		for i := range n {
			score := rapid.Float64Range(0.0, 1.0).Draw(rt, "score")
			scores[i] = score
			docs[i] = agent.Document{
				Content:  rapid.StringMatching(`[a-zA-Z0-9 ]{1,50}`).Draw(rt, "content"),
				Metadata: map[string]string{"score": strconv.FormatFloat(score, 'f', -1, 64)},
			}
		}
		threshold := rapid.Float64Range(0.0, 1.0).Draw(rt, "threshold")
		result := filterByScore(docs, threshold)

		for _, doc := range result {
			parsed, err := strconv.ParseFloat(doc.Metadata["score"], 64)
			if err != nil {
				rt.Fatalf("result doc has unparseable score: %v", err)
			}
			if parsed < threshold {
				rt.Fatalf("result contains doc with score %v < threshold %v", parsed, threshold)
			}
		}
	})
}

func TestNewVectorStoreRetrieverDefaults(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	r, err := NewVectorStoreRetriever("vs-test-123")
	require.NoError(t, err)
	require.NotNil(t, r)
	assert.Equal(t, 5, r.topK)
	assert.Equal(t, 0.0, r.scoreThreshold)
}

func TestNewVectorStoreRetrieverTopKValidation(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")

	_, err := NewVectorStoreRetriever("vs-test-123", WithVectorStoreTopK(0))
	require.Error(t, err)
	assert.Equal(t, "openai vector store: topK must be >= 1", err.Error())

	r, err := NewVectorStoreRetriever("vs-test-123", WithVectorStoreTopK(1))
	require.NoError(t, err)
	assert.Equal(t, 1, r.topK)
}

func TestVectorStoreRetrieverEmptyQuery(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	r, err := NewVectorStoreRetriever("vs-test-123")
	require.NoError(t, err)
	_, err = r.Retrieve(context.Background(), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "openai vector store: query must not be empty")
}

func TestVectorStoreRetrieverToolCompat(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	retriever, err := NewVectorStoreRetriever("vs-test-id")
	require.NoError(t, err)
	tool := agent.NewRetrieverTool("vs", "Vector store tool", retriever)
	assert.NotNil(t, tool)
}
