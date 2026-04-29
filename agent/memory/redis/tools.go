package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/memory"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// Option configures NewRememberTool or NewRecallTool. Both ToolOption and
// RecallOption satisfy this interface.
type Option interface {
	applyTool(*toolConfig)
}

// ToolOption configures tool metadata (name, description).
type ToolOption func(*toolConfig)

func (f ToolOption) applyTool(c *toolConfig) { f(c) }

type toolConfig struct {
	name        string
	description string
	recallOpts  []RecallOption
}

// WithToolName sets the tool name.
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

// NewRememberTool creates a tool that stores typed values into a Redis Store.
// The LLM schema is auto-generated from struct tags (fields with pk, identifier,
// or noinput are excluded).
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

	schema := generateRedisInputSchema[T]()

	return tool.NewRaw(cfg.name, cfg.description, schema,
		func(ctx context.Context, input json.RawMessage) (string, error) {
			id := agent.GetIdentifier(ctx)
			if id == "" {
				return "", errors.New("redis: identifier not found in context; use agent.WithIdentifier")
			}

			var value T
			if err := json.Unmarshal(input, &value); err != nil {
				return "", fmt.Errorf("redis: unmarshal: %w", err)
			}

			if err := store.Remember(ctx, id, value); err != nil {
				return "", err
			}

			return "Remembered.", nil
		},
	)
}

// NewRecallTool creates a tool that retrieves typed values from a Redis Store.
// RecallOptions passed here become default filters for every call.
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
				return "", errors.New("redis: identifier not found in context; use agent.WithIdentifier")
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

			return formatTypedResults(results), nil
		},
	)
}

// NewForgetTool creates a tool that removes a single entry by its Redis key.
// The LLM should first recall to see entry IDs, then call this tool with the
// ID of the entry to remove.
func NewForgetTool[T any](
	store *Store[T],
	opts ...Option,
) tool.Tool {
	cfg := &toolConfig{
		name:        "forget",
		description: "Remove a specific memory entry by its ID. First recall to find the entry, then forget it by ID.",
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
				return "", errors.New("redis: identifier not found in context; use agent.WithIdentifier")
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

// formatTypedResults renders entries as human-readable text.
func formatTypedResults[T any](results []memory.Entry[T]) string {
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

// generateRedisInputSchema produces a JSON Schema excluding pk/identifier/noinput fields.
func generateRedisInputSchema[T any]() map[string]any {
	var zero T
	t := reflect.TypeOf(zero)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	properties := make(map[string]any)
	var required []string

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}

		dbTag := field.Tag.Get("db")
		if dbTag == "" || dbTag == "-" {
			continue
		}
		parts := strings.Split(dbTag, ",")
		skip := false
		for _, p := range parts[1:] {
			switch strings.TrimSpace(p) {
			case "pk", "identifier", "noinput":
				skip = true
			}
		}
		if skip {
			continue
		}

		name := field.Name
		if jsonTag := field.Tag.Get("json"); jsonTag != "" {
			jsonParts := strings.Split(jsonTag, ",")
			if jsonParts[0] == "-" {
				continue
			}
			if jsonParts[0] != "" {
				name = jsonParts[0]
			}
		}

		prop := goTypeToSchema(field.Type)
		if desc := field.Tag.Get("description"); desc != "" {
			prop["description"] = desc
		}
		if field.Tag.Get("required") == "true" {
			required = append(required, name)
		}

		properties[name] = prop
	}

	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func goTypeToSchema(t reflect.Type) map[string]any {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": "array", "items": goTypeToSchema(t.Elem())}
	default:
		return map[string]any{"type": "string"}
	}
}
