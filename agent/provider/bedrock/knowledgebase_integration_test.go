//go:build !integration

package bedrock

import (
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestKnowledgeBaseRetrieverToolCompat verifies that KnowledgeBaseRetriever can be
// passed directly to agent.NewRetrieverTool without a type assertion or cast.
//
// Validates: Requirements 7.3, 8.1, 8.3
func TestKnowledgeBaseRetrieverToolCompat(t *testing.T) {
	t.Setenv("AWS_REGION", "us-east-1")

	retriever, err := NewKnowledgeBaseRetriever("kb-test-id")
	require.NoError(t, err)

	tool := agent.NewRetrieverTool("kb", "Knowledge base tool", retriever)
	assert.NotNil(t, tool)
}
