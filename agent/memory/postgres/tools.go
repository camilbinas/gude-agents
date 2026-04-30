package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/memory"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// Option configures NewRememberTool or NewRecallTool. Both ToolOption and
// RecallOption satisfy this interface, so you can pass them interchangeably.
type Option interface {
	applyTool(*toolConfig)
}

// ToolOption configures tool metadata (name, description).
type ToolOption func(*toolConfig)

func (f ToolOption) applyTool(c *toolConfig) { f(c) }

// recallToolOption wraps a RecallOption as an Option for NewRecallTool.
type recallToolOption struct {
	fn RecallOption
}

func (r recallToolOption) applyTool(c *toolConfig) {
	c.recallOpts = append(c.recallOpts, r.fn)
}

type toolConfig struct {
	name        string
	description string
	recallOpts  []memory.RecallOption
}

// WithToolName sets the tool name. Default: "remember" / "recall".
func WithToolName(name string) ToolOption {
	return func(c *toolConfig) {
		if name != "" {
			c.name = name
		}
	}
}

// WithToolDescription sets the tool description.
func WithToolDescription(desc string) ToolOption {
	return func(c *toolConfig) {
		if desc != "" {
			c.description = desc
		}
	}
}

// NewRememberTool creates a tool that stores values into a Store.
func NewRememberTool[T any](
	store *Store[T],
	opts ...Option,
) tool.Tool {
	cfg := &toolConfig{
		name:        "remember",
		description: "Store a structured memory entry for later recall.",
	}
	for _, opt := range opts {
		opt.applyTool(cfg)
	}

	schema := GenerateInputSchema[T]()

	return tool.NewRaw(cfg.name, cfg.description, schema,
		func(ctx context.Context, input json.RawMessage) (string, error) {
			id := agent.GetIdentifier(ctx)
			if id == "" {
				return "", errors.New("postgres: identifier not found in context; use agent.WithIdentifier")
			}

			var value T
			if err := json.Unmarshal(input, &value); err != nil {
				return "", fmt.Errorf("postgres: unmarshal: %w", err)
			}

			if err := store.Remember(ctx, id, value); err != nil {
				return "", err
			}

			return "Remembered.", nil
		},
	)
}

// NewUpdateTool creates a tool that updates an existing entry by ID.
func NewUpdateTool[T any](
	store *Store[T],
	opts ...Option,
) tool.Tool {
	cfg := &toolConfig{
		name:        "update",
		description: "Update an existing memory entry by its ID.",
	}
	for _, opt := range opts {
		opt.applyTool(cfg)
	}

	schema := GenerateInputSchema[T]()
	props := schema["properties"].(map[string]any)
	props["id"] = map[string]any{"type": "string", "description": "The ID of the entry to update (from a previous recall)."}
	if req, ok := schema["required"].([]string); ok {
		schema["required"] = append(req, "id")
	} else {
		schema["required"] = []any{"id"}
	}

	return tool.NewRaw(cfg.name, cfg.description, schema,
		func(ctx context.Context, input json.RawMessage) (string, error) {
			identifier := agent.GetIdentifier(ctx)
			if identifier == "" {
				return "", errors.New("postgres: identifier not found in context; use agent.WithIdentifier")
			}

			var params struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return "", err
			}

			var value T
			if err := json.Unmarshal(input, &value); err != nil {
				return "", fmt.Errorf("postgres: unmarshal: %w", err)
			}

			if err := store.Update(ctx, identifier, params.ID, value); err != nil {
				return "", err
			}

			return "Updated.", nil
		},
	)
}

// NewForgetTool creates a tool that removes a single entry by ID.
func NewForgetTool[T any](
	store *Store[T],
	opts ...Option,
) tool.Tool {
	cfg := &toolConfig{
		name:        "forget",
		description: "Remove a specific memory entry by its ID.",
	}
	for _, opt := range opts {
		opt.applyTool(cfg)
	}

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id": map[string]any{
				"type":        "string",
				"description": "The ID of the memory entry to forget (from a previous recall).",
			},
		},
		"required": []any{"id"},
	}

	return tool.NewRaw(cfg.name, cfg.description, schema,
		func(ctx context.Context, input json.RawMessage) (string, error) {
			identifier := agent.GetIdentifier(ctx)
			if identifier == "" {
				return "", errors.New("postgres: identifier not found in context; use agent.WithIdentifier")
			}

			var params struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return "", err
			}

			if err := store.Forget(ctx, identifier, params.ID); err != nil {
				return "", err
			}

			return "Forgotten.", nil
		},
	)
}

// NewRecallTool creates a tool that retrieves values from a Store.
// RecallOptions passed here apply as default filters to every call.
func NewRecallTool[T any](
	store *Store[T],
	opts ...Option,
) tool.Tool {
	cfg := &toolConfig{
		name:        "recall",
		description: "Retrieve relevant memory entries by semantic similarity.",
	}
	for _, opt := range opts {
		opt.applyTool(cfg)
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

	recallOpts := cfg.recallOpts

	return tool.NewRaw(cfg.name, cfg.description, schema,
		func(ctx context.Context, input json.RawMessage) (string, error) {
			id := agent.GetIdentifier(ctx)
			if id == "" {
				return "", errors.New("postgres: identifier not found in context; use agent.WithIdentifier")
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

			results, err := store.Recall(ctx, id, params.Query, params.Limit, recallOpts...)
			if err != nil {
				return "", err
			}

			if len(results) == 0 {
				return "No relevant memories found.", nil
			}

			return formatResults(results), nil
		},
	)
}

// formatResults renders typed entries as a human-readable string.
func formatResults[T any](results []memory.Entry[T]) string {
	var b strings.Builder
	for i, r := range results {
		if i > 0 {
			b.WriteString("\n")
		}
		data, err := json.Marshal(r.Value)
		if err != nil {
			fmt.Fprintf(&b, "- (marshal error: %v) Score: %.4f", err, r.Score)
		} else {
			fmt.Fprintf(&b, "- %s\n  ID: %s\n  Score: %.4f", string(data), r.ID, r.Score)
		}
	}
	return b.String()
}
