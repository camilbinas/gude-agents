package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// NewTypedRememberTool creates a composable tool that stores typed values.
// The schemaFunc returns the JSON Schema for the LLM input shape, enabling
// the LLM to understand the structure of type T when calling the tool.
// Documented in docs/memory.md — update when changing behavior.
func NewTypedRememberTool[T any](
	store *TypedMemoryStore[T],
	embedder agent.Embedder,
	contentFunc func(T) string,
	schemaFunc func() map[string]any,
	opts ...ToolOption,
) tool.Tool {
	cfg := &toolConfig{
		name:        "remember",
		description: defaultRememberDescription,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	schema := schemaFunc()

	return tool.NewRaw(cfg.name, cfg.description, schema,
		func(ctx context.Context, input json.RawMessage) (string, error) {
			id := agent.GetIdentifier(ctx)
			if id == "" {
				return "", errors.New("typed memory: identifier not found in context; use agent.WithIdentifier")
			}

			var value T
			if err := json.Unmarshal(input, &value); err != nil {
				return "", err
			}

			content := contentFunc(value)
			if content == "" {
				return "", errors.New("typed memory: content must not be empty")
			}

			embedding, err := embedder.Embed(ctx, content)
			if err != nil {
				return "", fmt.Errorf("typed memory: embed: %w", err)
			}

			if err := store.Add(ctx, id, []T{value}, contentFunc, [][]float64{embedding}); err != nil {
				return "", fmt.Errorf("typed memory: store add: %w", err)
			}

			return "Remembered.", nil
		},
	)
}

// NewTypedRecallTool creates a composable tool that retrieves typed values.
// Results are formatted as a human-readable list with JSON-marshaled values.
// Documented in docs/memory.md — update when changing behavior.
func NewTypedRecallTool[T any](
	store *TypedMemoryStore[T],
	embedder agent.Embedder,
	opts ...ToolOption,
) tool.Tool {
	cfg := &toolConfig{
		name:        "recall",
		description: defaultRecallDescription,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "A natural-language query describing what to recall.",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Maximum number of results to return. Defaults to 5.",
			},
		},
		"required": []any{"query"},
	}

	return tool.NewRaw(cfg.name, cfg.description, schema,
		func(ctx context.Context, input json.RawMessage) (string, error) {
			id := agent.GetIdentifier(ctx)
			if id == "" {
				return "", errors.New("typed memory: identifier not found in context; use agent.WithIdentifier")
			}

			var params struct {
				Query string `json:"query"`
				Limit int    `json:"limit"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return "", err
			}

			if params.Limit == 0 {
				params.Limit = 5
			}

			embedding, err := embedder.Embed(ctx, params.Query)
			if err != nil {
				return "", fmt.Errorf("typed memory: embed query: %w", err)
			}

			results, err := store.Search(ctx, id, embedding, params.Limit)
			if err != nil {
				return "", err
			}

			if len(results) == 0 {
				return "No relevant memories found.", nil
			}

			return formatTypedResults(results), nil
		},
	)
}

// formatTypedResults renders a slice of TypedScoredResult values as a
// human-readable string. Each value is JSON-marshaled for display.
func formatTypedResults[T any](results []TypedScoredResult[T]) string {
	var b strings.Builder
	for i, r := range results {
		if i > 0 {
			b.WriteString("\n")
		}

		data, err := json.Marshal(r.Value)
		if err != nil {
			fmt.Fprintf(&b, "- Value: (marshal error: %v)\n", err)
		} else {
			fmt.Fprintf(&b, "- Value: %s\n", string(data))
		}
		fmt.Fprintf(&b, "  Score: %.4f", r.Score)
	}
	return b.String()
}
