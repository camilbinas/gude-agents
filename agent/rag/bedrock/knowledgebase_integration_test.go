//go:build !integration

package bedrock

import (
	"testing"

	"github.com/camilbinas/gude-agents/agent/rag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestKnowledgeBaseRetrieverToolCompat verifies that KnowledgeBaseRetriever can be
// passed directly to rag.NewRetrieverTool without a type assertion or cast.
func TestKnowledgeBaseRetrieverToolCompat(t *testing.T) {
	t.Setenv("AWS_REGION", "us-east-1")

	retriever, err := NewKnowledgeBaseRetriever("kb-test-id")
	require.NoError(t, err)

	tool := rag.NewRetrieverTool("kb", "Knowledge base tool", retriever)
	assert.NotNil(t, tool)
}
