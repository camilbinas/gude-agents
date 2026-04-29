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
	recallOpts  []RecallOption
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

// NewRememberTool creates a tool that stores typed values into a Store.
// The LLM sends a JSON object matching the struct schema (fields tagged with
// pk, identifier, or noinput are excluded from the LLM input). The tool
// deserializes the input, embeds the content field, and inserts a row.
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

// NewRecallTool creates a tool that retrieves typed values from a Store.
// The LLM provides a query string and optional limit; results are returned as
// formatted JSON entries with similarity scores.
//
// Pass RecallOptions directly to set default filters (e.g. WithFieldGT,
// WithOrderBy) — they apply to every recall call the LLM makes.
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
