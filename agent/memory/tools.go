package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// toolConfig holds configuration for composable tool constructors.
type toolConfig struct {
	name        string
	description string
}

// ToolOption configures a NewRememberTool or NewRecallTool.
type ToolOption func(*toolConfig)

// WithToolName sets the tool name. Defaults to "remember" or "recall".
func WithToolName(name string) ToolOption {
	return func(c *toolConfig) { c.name = name }
}

// WithToolDescription sets the tool description.
func WithToolDescription(desc string) ToolOption {
	return func(c *toolConfig) { c.description = desc }
}

// defaultRememberDescription is the default description for RememberTool / NewRememberTool.
const defaultRememberDescription = "Store a fact, preference, or decision into long-term memory for later recall. " +
	"Use this when the user shares something worth remembering across conversations."

// defaultRecallDescription is the default description for RecallTool / NewRecallTool.
const defaultRecallDescription = "Retrieve previously stored facts, preferences, and decisions from long-term memory. " +
	"Use this to recall context from past conversations."

// NewRememberTool creates a composable RememberTool backed by a MemoryStore
// and Embedder. The tool extracts the identifier from the agent context via
// agent.GetIdentifier, embeds the fact, and stores it in the memory store.
func NewRememberTool(store MemoryStore, embedder agent.Embedder, opts ...ToolOption) tool.Tool {
	cfg := &toolConfig{
		name:        "remember",
		description: defaultRememberDescription,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"fact": map[string]any{
				"type":        "string",
				"description": "The fact, preference, or decision to remember for later.",
			},
			"metadata": map[string]any{
				"type":                 "object",
				"description":          "Optional key-value pairs for categorization (e.g. {\"category\": \"preference\"}).",
				"additionalProperties": map[string]any{"type": "string"},
			},
		},
		"required": []any{"fact"},
	}

	return tool.NewRaw(cfg.name, cfg.description, schema,
		func(ctx context.Context, input json.RawMessage) (string, error) {
			id := agent.GetIdentifier(ctx)
			if id == "" {
				return "", errors.New("memory: identifier not found in context; use agent.WithIdentifier")
			}

			var params struct {
				Fact     string            `json:"fact"`
				Metadata map[string]string `json:"metadata"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return "", err
			}

			embedding, err := embedder.Embed(ctx, params.Fact)
			if err != nil {
				return "", fmt.Errorf("memory: embed fact: %w", err)
			}

			meta := make(map[string]string, len(params.Metadata)+1)
			for k, v := range params.Metadata {
				meta[k] = v
			}
			meta["created_at"] = time.Now().UTC().Format(time.RFC3339)

			doc := agent.Document{
				Content:  params.Fact,
				Metadata: meta,
			}

			if err := store.Add(ctx, id, []agent.Document{doc}, [][]float64{embedding}); err != nil {
				return "", err
			}

			return "Remembered.", nil
		},
	)
}

// NewRecallTool creates a composable RecallTool backed by a MemoryStore
// and Embedder. The tool extracts the identifier from the agent context via
// agent.GetIdentifier, embeds the query, and searches the memory store.
func NewRecallTool(store MemoryStore, embedder agent.Embedder, opts ...ToolOption) tool.Tool {
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
				return "", errors.New("memory: identifier not found in context; use agent.WithIdentifier")
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
				return "", fmt.Errorf("memory: embed query: %w", err)
			}

			results, err := store.Search(ctx, id, embedding, params.Limit)
			if err != nil {
				return "", err
			}

			if len(results) == 0 {
				return "No relevant memories found.", nil
			}

			return formatScoredDocuments(results), nil
		},
	)
}

// formatScoredDocuments renders a slice of ScoredDocuments as a human-readable
// string, matching the format used by formatEntries. Internal metadata keys
// (created_at) are handled specially: created_at is shown as the Time field
// and excluded from the metadata display.
func formatScoredDocuments(results []agent.ScoredDocument) string {
	var b strings.Builder
	for i, sd := range results {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "- Fact: %s\n", sd.Document.Content)

		// Collect user metadata (exclude internal keys).
		var metaParts []string
		for k, v := range sd.Document.Metadata {
			if k == "created_at" {
				continue
			}
			metaParts = append(metaParts, fmt.Sprintf("%s=%s", k, v))
		}
		if len(metaParts) > 0 {
			fmt.Fprintf(&b, "  Metadata: %s\n", strings.Join(metaParts, " "))
		}

		if ts, ok := sd.Document.Metadata["created_at"]; ok {
			fmt.Fprintf(&b, "  Time: %s\n", ts)
		}
		fmt.Fprintf(&b, "  Score: %.4f", sd.Score)
	}
	return b.String()
}

// RememberTool returns a tool.Tool that stores facts into memory.
// The tool extracts the identifier from the agent context via agent.GetIdentifier.
func RememberTool(m Memory) tool.Tool {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"fact": map[string]any{
				"type":        "string",
				"description": "The fact, preference, or decision to remember for later.",
			},
			"metadata": map[string]any{
				"type":                 "object",
				"description":          "Optional key-value pairs for categorization (e.g. {\"category\": \"preference\"}).",
				"additionalProperties": map[string]any{"type": "string"},
			},
		},
		"required": []any{"fact"},
	}

	return tool.NewRaw("remember",
		"Store a fact, preference, or decision into long-term memory for later recall. "+
			"Use this when the user shares something worth remembering across conversations.",
		schema,
		func(ctx context.Context, input json.RawMessage) (string, error) {
			id := agent.GetIdentifier(ctx)
			if id == "" {
				return "", errors.New("memory: identifier not found in context; use agent.WithIdentifier")
			}

			var params struct {
				Fact     string            `json:"fact"`
				Metadata map[string]string `json:"metadata"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return "", err
			}

			if err := m.Remember(ctx, id, params.Fact, params.Metadata); err != nil {
				return "", err
			}

			return "Remembered.", nil
		},
	)
}

// RecallTool returns a tool.Tool that retrieves facts from memory.
// The tool extracts the identifier from the agent context via agent.GetIdentifier.
func RecallTool(m Memory) tool.Tool {
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

	return tool.NewRaw("recall",
		"Retrieve previously stored facts, preferences, and decisions from long-term memory. "+
			"Use this to recall context from past conversations.",
		schema,
		func(ctx context.Context, input json.RawMessage) (string, error) {
			id := agent.GetIdentifier(ctx)
			if id == "" {
				return "", errors.New("memory: identifier not found in context; use agent.WithIdentifier")
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

			entries, err := m.Recall(ctx, id, params.Query, params.Limit)
			if err != nil {
				return "", err
			}

			if len(entries) == 0 {
				return "No relevant memories found.", nil
			}

			return formatEntries(entries), nil
		},
	)
}

// formatEntries renders a slice of entries as a human-readable string.
func formatEntries(entries []Entry) string {
	var b strings.Builder
	for i, entry := range entries {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "- Fact: %s\n", entry.Fact)
		if len(entry.Metadata) > 0 {
			fmt.Fprintf(&b, "  Metadata:")
			for k, v := range entry.Metadata {
				fmt.Fprintf(&b, " %s=%s", k, v)
			}
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "  Time: %s\n", entry.CreatedAt.Format("2006-01-02T15:04:05Z07:00"))
		fmt.Fprintf(&b, "  Score: %.4f", entry.Score)
	}
	return b.String()
}
