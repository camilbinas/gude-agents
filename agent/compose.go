package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// AgentAsTool wraps a child Agent as a tool.Tool that a parent Agent can invoke.
func AgentAsTool(name, description string, child *Agent) tool.Tool {
	return tool.NewRaw(
		name,
		description,
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"message": map[string]any{
					"type":        "string",
					"description": "The message to send to the sub-agent",
				},
			},
			"required": []string{"message"},
		},
		func(ctx context.Context, input json.RawMessage) (string, error) {
			var args struct {
				Message string `json:"message"`
			}
			if err := json.Unmarshal(input, &args); err != nil {
				return "", err
			}

			parent := FromContext(ctx)
			if parent == nil {
				parent = NewContext(ctx)
			}
			childCtx := parent.Clone()
			if child.conversation != nil {
				if parent.ConversationID() == "" || parent.call == nil || parent.call.id == "" {
					return "", fmt.Errorf("child agent %q requires a parent conversation and tool call ID", name)
				}
				childCtx.WithConversationID(childConversationID(parent.ConversationID(), parent.call.id, name, child.Name()))
			}

			res, err := child.Invoke(childCtx, args.Message)
			if res.StopReason == StopInterrupt && res.Interrupt != nil {
				cleanupErr := consumeChildInterrupt(ctx, child, res.Interrupt.ID)
				pauseErr := fmt.Errorf("child agent %q paused with a %s interrupt; interrupts cannot propagate through AgentAsTool", name, res.Interrupt.Type)
				if cleanupErr != nil {
					pauseErr = errors.Join(pauseErr, cleanupErr)
				}
				if err != nil {
					return "", fmt.Errorf("child agent %q: %w", name, errors.Join(err, pauseErr))
				}
				return "", pauseErr
			}
			if err != nil {
				return "", fmt.Errorf("child agent %q: %w", name, err)
			}
			return res.Text, nil
		},
	)
}

func childConversationID(parentConversationID, toolCallID, toolName, childName string) string {
	h := sha256.New()
	for _, part := range []string{"agent-as-tool/v1", parentConversationID, toolCallID, toolName, childName} {
		var length [8]byte
		for i := 0; i < len(length); i++ {
			length[len(length)-1-i] = byte(len(part) >> (8 * i))
		}
		_, _ = h.Write(length[:])
		_, _ = h.Write([]byte(part))
	}
	return "agent-as-tool/v1/" + hex.EncodeToString(h.Sum(nil))
}

func consumeChildInterrupt(ctx context.Context, child *Agent, id string) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, err := child.interruptStore.Claim(cleanupCtx, id)
	if errors.Is(err, ErrInterruptNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("consume child interrupt %q: %w", id, err)
	}
	return nil
}
