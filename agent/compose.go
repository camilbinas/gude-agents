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

// AgentAsToolOption configures an AgentAsTool wrapper.
type AgentAsToolOption func(*agentAsToolConfig)

type agentAsToolConfig struct {
	propagatePause bool
}

// WithPropagatePause makes a child pause pause the parent instead of failing
// the tool call. The caller sees the child's approval or question on the
// parent's Result and resumes the PARENT with the normal API (Approve, Deny,
// Decide, Respond, RespondWith); the child continues from its pause and its
// final answer becomes the tool result. The child must have both a
// ConversationStore and an ExecutionStore (ErrPauseStorageRequired otherwise).
// Without this option the default is unchanged: a child pause is canceled and
// the parent sees a tool error.
func WithPropagatePause() AgentAsToolOption {
	return func(c *agentAsToolConfig) { c.propagatePause = true }
}

// AgentAsTool wraps a child Runtime as a tool.Tool that a parent Runtime can invoke.
func AgentAsTool(name, description string, child *Runtime, opts ...AgentAsToolOption) tool.Tool {
	var cfg agentAsToolConfig
	for _, opt := range opts {
		opt(&cfg)
	}
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

			// Parent resume routed to this exact call: resume the paused child
			// instead of invoking it fresh.
			if directive := parent.nestedResumeFor(name); directive != nil {
				return resumeChild(ctx, name, child, parent, directive)
			}

			childCtx := parent.Clone()
			childConvID := ""
			if child.conversation != nil {
				if parent.ConversationID() == "" || parent.call == nil || parent.call.id == "" {
					return "", fmt.Errorf("child agent %q requires a parent conversation and tool call ID", name)
				}
				childConvID = childConversationID(parent.ConversationID(), parent.call.id, name, child.Name())
				childCtx.WithConversationID(childConvID)
			}

			res, err := child.Invoke(childCtx, args.Message)
			if res.StopReason == StopInterrupt && res.Interrupt != nil {
				return handleChildPause(ctx, name, child, parent, cfg, res.Interrupt, err)
			}
			if err != nil {
				return "", fmt.Errorf("child agent %q: %w", name, err)
			}
			return res.Text, nil
		},
	)
}

// nestedResumeFor returns the resume directive routed to the AgentAsTool call
// named toolName, or nil. It matches by the parent tool call ID so a batch with
// several sub-agent calls resumes only the one that paused.
func (c *Context) nestedResumeFor(toolName string) *nestedResumeDirective {
	if c == nil || c.cfg.nestedResume == nil || c.call == nil {
		return nil
	}
	if c.cfg.nestedResume.callID != c.call.id {
		return nil
	}
	return c.cfg.nestedResume
}

// handleChildPause reacts to a child that paused. With propagate-pause it
// records the pause on the parent tool call so the engine pauses the parent;
// otherwise it keeps the legacy behavior of canceling the child and failing.
func handleChildPause(ctx context.Context, name string, child *Runtime, parent *Context, cfg agentAsToolConfig, in *Interrupt, invokeErr error) (string, error) {
	if cfg.propagatePause && parent.call != nil {
		// The child pause is durable in the child stores. Hand the parent engine
		// the child projection; the parent pauses with an InterruptNested.
		parent.call.setNestedPause(&NestedInterrupt{Child: cloneInterrupt(in)})
		return nestedPausedResult, invokeErr
	}
	cleanupErr := consumeChildExecution(ctx, child, in)
	pauseErr := fmt.Errorf("child agent %q paused with a %s interrupt; interrupts cannot propagate through AgentAsTool", name, in.Type)
	if cleanupErr != nil {
		pauseErr = errors.Join(pauseErr, cleanupErr)
	}
	if invokeErr != nil {
		return "", fmt.Errorf("child agent %q: %w", name, errors.Join(invokeErr, pauseErr))
	}
	return "", pauseErr
}

// resumeChild resumes a paused child with the parent caller's response. A fresh
// child pause pauses the parent again (grandchild or re-pause); a final answer
// becomes the tool result.
func resumeChild(ctx context.Context, name string, child *Runtime, parent *Context, directive *nestedResumeDirective) (string, error) {
	if child.executionStore == nil || child.conversation == nil {
		return "", fmt.Errorf("child agent %q: %w", name, ErrPauseStorageRequired)
	}
	childCtx := parent.Clone().WithConversationID(directive.child.ConversationID)
	res, err := child.Resume(childCtx, directive.child, directive.response)
	if res.StopReason == StopInterrupt && res.Interrupt != nil {
		// Re-pause (same child paused again, e.g. a second question, or a
		// grandchild bubbled up): pause the parent once more.
		parent.call.setNestedPause(&NestedInterrupt{Child: cloneInterrupt(res.Interrupt)})
		return nestedPausedResult, err
	}
	if err != nil {
		return "", fmt.Errorf("child agent %q: %w", name, err)
	}
	return res.Text, nil
}

// nestedPausedResult is the sentinel handler output for a propagate-pause child
// that paused. The engine never commits it: the paused call is excluded from
// the batch and the child's real answer becomes the tool result on resume.
const nestedPausedResult = "Paused — waiting for a nested sub-agent."

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

func consumeChildExecution(ctx context.Context, child *Runtime, interrupt *Interrupt) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if child.executionStore != nil {
		execution, err := child.executionStore.Load(cleanupCtx, interrupt.ExecutionID)
		if err != nil {
			if errors.Is(err, ErrExecutionNotFound) {
				return nil
			}
			return fmt.Errorf("load child execution %q: %w", interrupt.ExecutionID, err)
		}
		if execution.Status != ExecutionPaused || execution.Version != interrupt.ExecutionVersion {
			return nil
		}
		execution.Status, execution.Phase, execution.Pause = ExecutionCanceled, ExecutionPhaseDone, nil
		if _, err := child.executionStore.Save(cleanupCtx, execution, execution.Version); err != nil {
			if errors.Is(err, ErrExecutionConflict) {
				return nil
			}
			return fmt.Errorf("cancel child execution %q: %w", interrupt.ExecutionID, err)
		}
		return nil
	}
	// A child can only report an interrupt when it has an ExecutionStore, so
	// there is no other pause state to consume.
	return nil
}
