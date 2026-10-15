package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// InterruptType identifies why an invocation paused.
type InterruptType string

const (
	InterruptApproval   InterruptType = "approval"
	InterruptHumanInput InterruptType = "human_input"
)

// Interrupt is a public projection of a paused Execution. It references
// canonical history with ExecutionID, ExecutionVersion, ConversationID,
// Revision, and LastSequence. A pause is always backed by both a
// ConversationStore (the transcript) and an ExecutionStore (the resumable
// state); see ErrPauseStorageRequired.
type Interrupt struct {
	ExecutionID      string `json:"execution_id"`
	ExecutionVersion uint64 `json:"execution_version"`

	Type           InterruptType `json:"type"`
	ConversationID string        `json:"conversation_id,omitempty"`
	Revision       uint64        `json:"revision"`
	LastSequence   uint64        `json:"last_sequence"`

	Approval *ApprovalInterrupt `json:"approval,omitempty"`
	Input    *InputInterrupt    `json:"input,omitempty"`
}

type ApprovalInterrupt struct {
	Calls []ApprovalCall `json:"calls"`
}
type ApprovalCall struct {
	CallID string          `json:"call_id"`
	Name   string          `json:"name"`
	Input  json.RawMessage `json:"input,omitempty"`
}
type InputInterrupt struct {
	Reason   string `json:"reason"`
	Question string `json:"question"`
}

// ResumeResponse is the human answer to an Interrupt. Build it with Approve,
// Deny, Decide, or Respond.
type ResumeResponse struct {
	kind      resumeKind
	decision  tool.Decision
	decisions map[string]tool.Decision
	text      string
}
type resumeKind int

const (
	resumeNone resumeKind = iota
	resumeAll
	resumeDecide
	resumeRespond
)

func Approve() ResumeResponse { return ResumeResponse{kind: resumeAll, decision: tool.Allow()} }
func Deny(reason string) ResumeResponse {
	return ResumeResponse{kind: resumeAll, decision: tool.Deny(reason)}
}
func Decide(decisions map[string]tool.Decision) ResumeResponse {
	cp := make(map[string]tool.Decision, len(decisions))
	for k, v := range decisions {
		cp[k] = v
	}
	return ResumeResponse{kind: resumeDecide, decisions: cp}
}
func Respond(text string) ResumeResponse { return ResumeResponse{kind: resumeRespond, text: text} }

// ErrInterruptNotFound is retained as the public projection error returned
// when a requested execution is absent or not currently paused.
var ErrInterruptNotFound = errors.New("interrupt not found")

// LoadInterrupt projects a paused durable Execution. It is a convenience API;
// applications never manipulate persisted execution records directly.
func (a *Agent) LoadInterrupt(ctx context.Context, executionID string) (*Interrupt, error) {
	if a.executionStore == nil {
		return nil, ErrNoExecutionStore
	}
	execution, err := a.executionStore.Load(ctx, executionID)
	if err != nil {
		if errors.Is(err, ErrExecutionNotFound) {
			return nil, fmt.Errorf("load interrupt %q: %w", executionID, errors.Join(ErrInterruptNotFound, err))
		}
		return nil, fmt.Errorf("load execution: %w", err)
	}
	if execution.Status != ExecutionPaused || execution.Pause == nil {
		return nil, fmt.Errorf("load interrupt %q: %w", executionID, ErrInterruptNotFound)
	}
	return interruptFromExecution(execution), nil
}

func interruptFromExecution(execution Execution) *Interrupt {
	execution = cloneExecution(execution)
	in := &Interrupt{ExecutionID: execution.ID, ExecutionVersion: execution.Version, ConversationID: execution.ConversationID, Revision: execution.Revision, LastSequence: execution.LastSequence}
	if execution.Pause != nil {
		in.Type = execution.Pause.Type
		in.Approval = execution.Pause.Approval
		in.Input = execution.Pause.Input
	}
	return in
}

func pauseFromInterrupt(in *Interrupt) *ExecutionPause {
	if in == nil {
		return nil
	}
	return cloneExecution(Execution{Pause: &ExecutionPause{Type: in.Type, Approval: in.Approval, Input: in.Input}}).Pause
}

func validateResume(in *Interrupt, r ResumeResponse) error {
	if in == nil {
		return fmt.Errorf("resume: nil interrupt")
	}
	switch in.Type {
	case InterruptApproval:
		if in.Approval == nil || len(in.Approval.Calls) == 0 {
			return fmt.Errorf("resume: approval interrupt has no calls")
		}
		seen := make(map[string]struct{}, len(in.Approval.Calls))
		for _, call := range in.Approval.Calls {
			if call.CallID == "" {
				return fmt.Errorf("resume: approval call %q has empty call ID", call.Name)
			}
			if _, dup := seen[call.CallID]; dup {
				return fmt.Errorf("resume: duplicate call ID %q", call.CallID)
			}
			seen[call.CallID] = struct{}{}
		}
		switch r.kind {
		case resumeAll:
			return nil
		case resumeDecide:
			for id := range seen {
				if _, ok := r.decisions[id]; !ok {
					return fmt.Errorf("resume: missing decision for call ID %q", id)
				}
			}
			for id := range r.decisions {
				if _, ok := seen[id]; !ok {
					return fmt.Errorf("resume: unexpected decision for call ID %q", id)
				}
			}
			return nil
		case resumeRespond:
			return fmt.Errorf("resume: Respond is only valid for human_input interrupts")
		default:
			return fmt.Errorf("resume: empty response")
		}
	case InterruptHumanInput:
		if r.kind != resumeRespond {
			return fmt.Errorf("resume: human_input interrupts require Respond")
		}
		return nil
	default:
		return fmt.Errorf("resume: unknown interrupt type %q", in.Type)
	}
}
func (r ResumeResponse) decisionFor(callID string) tool.Decision {
	if r.kind == resumeDecide {
		return r.decisions[callID]
	}
	return r.decision
}

const humanInputPausedResult = "Paused — waiting for human input."

func NewHumanInputTool(name, description string) tool.Tool {
	base := "Pause execution and ask a human for input, a decision, or approval. Use when you need information you cannot determine on your own."
	if description != "" {
		base += " " + description
	}
	schema := map[string]any{"type": "object", "properties": map[string]any{"reason": map[string]any{"type": "string", "description": "Why you need human input"}, "question": map[string]any{"type": "string", "description": "The specific question or request for the human"}}, "required": []string{"reason", "question"}}
	return tool.NewRaw(name, base, schema, func(ctx context.Context, input json.RawMessage) (string, error) {
		var p struct {
			Reason   string `json:"reason"`
			Question string `json:"question"`
		}
		if err := json.Unmarshal(input, &p); err != nil {
			return "", fmt.Errorf("invalid human input request: %w", err)
		}
		c := FromContext(ctx)
		if c == nil || c.call == nil {
			return "", fmt.Errorf("human input tool must run inside an agent tool call")
		}
		c.call.setHumanInput(&InputInterrupt{Reason: p.Reason, Question: p.Question})
		return humanInputPausedResult, nil
	})
}
