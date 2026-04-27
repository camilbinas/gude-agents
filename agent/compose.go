package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// AgentAsTool wraps a child Agent as a tool.Tool that a parent Agent can invoke.
func AgentAsTool(name, description string, child *Agent) tool.Tool {
	return tool.NewRaw(name, description, map[string]any{
		"type": "object",
		"properties": map[string]any{
			"message": map[string]any{
				"type":        "string",
				"description": "The message to send to the sub-agent",
			},
		},
		"required": []string{"message"},
	}, func(ctx context.Context, input json.RawMessage) (string, error) {
		var args struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return "", err
		}
		var result string
		_, err := child.InvokeStream(ctx, args.Message, func(chunk string) {
			result += chunk
		})
		if err != nil {
			return "", fmt.Errorf("child agent %q: %w", name, err)
		}
		return result, nil
	})
}
