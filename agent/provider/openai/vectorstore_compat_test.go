//go:build !integration

package openai

import (
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVectorStoreRetrieverToolCompat verifies that VectorStoreRetriever can be
// passed directly to agent.NewRetrieverTool without a type assertion or cast.
//
// Validates: Requirements 7.3, 8.2, 8.4
func TestVectorStoreRetrieverToolCompat(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")

	retriever, err := NewVectorStoreRetriever("vs-test-id")
	require.NoError(t, err)

	tool := agent.NewRetrieverTool("vs", "Vector store tool", retriever)
	assert.NotNil(t, tool)
}
