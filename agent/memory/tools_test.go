package memory

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/rag"
)

// TestNewRememberTool_MissingIdentifier verifies that NewRememberTool returns
// a descriptive error when the agent context does not have an identifier set.
//
// Validates: Requirement 3.6
func TestNewRememberTool_MissingIdentifier(t *testing.T) {
	memStore := rag.NewMemoryStore()
	scopedStore := rag.NewScopedStore(memStore)
	embedder := &hashEmbedder{dim: 16}

	rememberTool := NewRememberTool(scopedStore, embedder)

	// Use a bare context with no identifier attached.
	ctx := context.Background()
	input, err := json.Marshal(map[string]any{"fact": "some fact"})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	_, err = rememberTool.Handler(ctx, json.RawMessage(input))
	if err == nil {
		t.Fatal("expected error when identifier is missing, got nil")
	}
	const want = "memory: identifier not found in context; use agent.WithIdentifier"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}

// TestNewRecallTool_MissingIdentifier verifies that NewRecallTool returns
// a descriptive error when the agent context does not have an identifier set.
//
// Validates: Requirement 3.7
func TestNewRecallTool_MissingIdentifier(t *testing.T) {
	memStore := rag.NewMemoryStore()
	scopedStore := rag.NewScopedStore(memStore)
	embedder := &hashEmbedder{dim: 16}

	recallTool := NewRecallTool(scopedStore, embedder)

	// Use a bare context with no identifier attached.
	ctx := context.Background()
	input, err := json.Marshal(map[string]any{"query": "anything"})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	_, err = recallTool.Handler(ctx, json.RawMessage(input))
	if err == nil {
		t.Fatal("expected error when identifier is missing, got nil")
	}
	const want = "memory: identifier not found in context; use agent.WithIdentifier"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}

// TestNewRecallTool_NoResults verifies that NewRecallTool returns
// "No relevant memories found." when the underlying store is empty.
//
// Validates: Requirement 3.5
func TestNewRecallTool_NoResults(t *testing.T) {
	memStore := rag.NewMemoryStore()
	scopedStore := rag.NewScopedStore(memStore)
	embedder := &hashEmbedder{dim: 16}

	recallTool := NewRecallTool(scopedStore, embedder)

	ctx := agent.WithIdentifier(context.Background(), "user-1")
	input, err := json.Marshal(map[string]any{"query": "anything"})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	result, err := recallTool.Handler(ctx, json.RawMessage(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	const want = "No relevant memories found."
	if result != want {
		t.Fatalf("result = %q, want %q", result, want)
	}
}

// TestMultipleToolInstances verifies that two tools with distinct names
// can be created and coexist, each having a different Spec.Name.
//
// Validates: Requirement 3.12
func TestMultipleToolInstances(t *testing.T) {
	memStore := rag.NewMemoryStore()
	scopedStore := rag.NewScopedStore(memStore)
	embedder := &hashEmbedder{dim: 16}

	rememberPrefs := NewRememberTool(scopedStore, embedder, WithToolName("remember_preferences"))
	rememberProjects := NewRememberTool(scopedStore, embedder, WithToolName("remember_projects"))

	if rememberPrefs.Spec.Name != "remember_preferences" {
		t.Fatalf("rememberPrefs.Spec.Name = %q, want %q", rememberPrefs.Spec.Name, "remember_preferences")
	}
	if rememberProjects.Spec.Name != "remember_projects" {
		t.Fatalf("rememberProjects.Spec.Name = %q, want %q", rememberProjects.Spec.Name, "remember_projects")
	}
	if rememberPrefs.Spec.Name == rememberProjects.Spec.Name {
		t.Fatal("two tool instances should have distinct names")
	}
}
