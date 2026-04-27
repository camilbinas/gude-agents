package memory

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
)

// TestNewRememberTool_MissingIdentifier verifies that NewRememberTool returns
// a descriptive error when the agent context does not have an identifier set.
func TestNewRememberTool_MissingIdentifier(t *testing.T) {
	store := NewInMemoryStore()
	embedder := &hashEmbedder{dim: 16}

	rememberTool := NewRememberTool(store, embedder)

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
func TestNewRecallTool_MissingIdentifier(t *testing.T) {
	store := NewInMemoryStore()
	embedder := &hashEmbedder{dim: 16}

	recallTool := NewRecallTool(store, embedder)

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
func TestNewRecallTool_NoResults(t *testing.T) {
	store := NewInMemoryStore()
	embedder := &hashEmbedder{dim: 16}

	recallTool := NewRecallTool(store, embedder)

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
func TestMultipleToolInstances(t *testing.T) {
	store := NewInMemoryStore()
	embedder := &hashEmbedder{dim: 16}

	rememberPrefs := NewRememberTool(store, embedder, WithToolName("remember_preferences"))
	rememberProjects := NewRememberTool(store, embedder, WithToolName("remember_projects"))

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

// TestToolSchemaValidation verifies that the tool schemas for NewRememberTool
// and NewRecallTool have the expected structure: correct type, required fields,
// and property definitions.
func TestToolSchemaValidation(t *testing.T) {
	store := NewInMemoryStore()
	embedder := &hashEmbedder{dim: 16}

	t.Run("remember_tool_schema", func(t *testing.T) {
		rememberTool := NewRememberTool(store, embedder)

		// Spec.Name defaults to "remember".
		if rememberTool.Spec.Name != "remember" {
			t.Fatalf("Spec.Name = %q, want %q", rememberTool.Spec.Name, "remember")
		}

		// Spec.Description should be non-empty.
		if rememberTool.Spec.Description == "" {
			t.Fatal("Spec.Description is empty")
		}

		// Verify the input schema structure.
		schema := rememberTool.Spec.InputSchema
		schemaBytes, err := json.Marshal(schema)
		if err != nil {
			t.Fatalf("marshal schema: %v", err)
		}

		var schemaMap map[string]any
		if err := json.Unmarshal(schemaBytes, &schemaMap); err != nil {
			t.Fatalf("unmarshal schema: %v", err)
		}

		if schemaMap["type"] != "object" {
			t.Fatalf("schema type = %v, want %q", schemaMap["type"], "object")
		}

		props, ok := schemaMap["properties"].(map[string]any)
		if !ok {
			t.Fatal("schema missing 'properties' object")
		}

		if _, ok := props["fact"]; !ok {
			t.Fatal("schema missing 'fact' property")
		}

		required, ok := schemaMap["required"].([]any)
		if !ok {
			t.Fatal("schema missing 'required' array")
		}
		foundFact := false
		for _, r := range required {
			if r == "fact" {
				foundFact = true
			}
		}
		if !foundFact {
			t.Fatal("'fact' not in required fields")
		}
	})

	t.Run("recall_tool_schema", func(t *testing.T) {
		recallTool := NewRecallTool(store, embedder)

		// Spec.Name defaults to "recall".
		if recallTool.Spec.Name != "recall" {
			t.Fatalf("Spec.Name = %q, want %q", recallTool.Spec.Name, "recall")
		}

		// Spec.Description should be non-empty.
		if recallTool.Spec.Description == "" {
			t.Fatal("Spec.Description is empty")
		}

		// Verify the input schema structure.
		schema := recallTool.Spec.InputSchema
		schemaBytes, err := json.Marshal(schema)
		if err != nil {
			t.Fatalf("marshal schema: %v", err)
		}

		var schemaMap map[string]any
		if err := json.Unmarshal(schemaBytes, &schemaMap); err != nil {
			t.Fatalf("unmarshal schema: %v", err)
		}

		if schemaMap["type"] != "object" {
			t.Fatalf("schema type = %v, want %q", schemaMap["type"], "object")
		}

		props, ok := schemaMap["properties"].(map[string]any)
		if !ok {
			t.Fatal("schema missing 'properties' object")
		}

		if _, ok := props["query"]; !ok {
			t.Fatal("schema missing 'query' property")
		}

		if _, ok := props["limit"]; !ok {
			t.Fatal("schema missing 'limit' property")
		}

		required, ok := schemaMap["required"].([]any)
		if !ok {
			t.Fatal("schema missing 'required' array")
		}
		foundQuery := false
		for _, r := range required {
			if r == "query" {
				foundQuery = true
			}
		}
		if !foundQuery {
			t.Fatal("'query' not in required fields")
		}
	})
}
