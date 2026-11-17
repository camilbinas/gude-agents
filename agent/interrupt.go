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
	// InterruptNested is a parent pause caused by a paused child reached through
	// a paused AgentAsTool call. Its Nested field carries the child
	// projection and the path (parent call -> child execution) for resume.
	InterruptNested InterruptType = "nested"
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
	Nested   *NestedInterrupt   `json:"nested,omitempty"`
}

// NestedInterrupt carries a child pause surfaced through an AgentAsTool call.
// Child is the child's own projection (approval or human_input) that the caller
// answers; CallID is the parent tool call whose child paused, so Resume routes
// the parent's response to the right child execution.
type NestedInterrupt struct {
	CallID string     `json:"call_id"`
	Child  *Interrupt `json:"child"`
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
	// Tool is the name of the pausing tool; set for a NewHumanInput[R] pause so
	// resume can dispatch the typed answer to its handler.
	Tool string `json:"tool,omitempty"`
	// Schema is the JSON schema of the typed answer R expected by a
	// NewHumanInput[R] tool, so a UI can render a form. Empty for a free-text
	// NewHumanInputTool pause.
	Schema json.RawMessage `json:"schema,omitempty"`
}

// ResumeResponse is the human answer to an Interrupt. Build it with Approve,
// Deny, Decide, Respond, or RespondWith.
type ResumeResponse struct {
	kind      resumeKind
	decision  tool.Decision
	decisions map[string]tool.Decision
	text      string
	typed     json.RawMessage
}
type resumeKind int

const (
	resumeNone resumeKind = iota
	resumeAll
	resumeDecide
	resumeRespond
	resumeRespondTyped
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

// RespondWith answers a human_input interrupt with a typed value. Resume
// marshals v to JSON and validates it against the interrupt's Input.Schema
// before the run continues; a schema-invalid value returns a
// *ResumeValidationError and leaves the run paused. Use it with a
// NewHumanInput[R] tool.
func RespondWith(v any) ResumeResponse {
	raw, err := json.Marshal(v)
	if err != nil {
		return ResumeResponse{kind: resumeRespondTyped, typed: nil, text: err.Error()}
	}
	return ResumeResponse{kind: resumeRespondTyped, typed: raw}
}

// ResumeValidationError reports that a RespondWith value failed validation
// against the interrupt's answer schema. The run stays paused; fix the value
// and call Resume again.
type ResumeValidationError struct{ Cause error }

func (e *ResumeValidationError) Error() string {
	return "resume: typed answer failed schema validation: " + e.Cause.Error()
}
func (e *ResumeValidationError) Unwrap() error { return e.Cause }

// ErrInterruptNotFound is retained as the public projection error returned
// when a requested execution is absent or not currently paused.
var ErrInterruptNotFound = errors.New("interrupt not found")

// LoadInterrupt projects a paused durable Execution. It is a convenience API;
// applications never manipulate persisted execution records directly.
func (a *Runtime) LoadInterrupt(ctx context.Context, executionID string) (*Interrupt, error) {
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
		if execution.Pause.Nested != nil {
			in.Nested = &NestedInterrupt{CallID: execution.Pause.Nested.CallID, Child: execution.Pause.Nested.Child}
		}
	}
	return in
}

func pauseFromInterrupt(in *Interrupt) *ExecutionPause {
	if in == nil {
		return nil
	}
	pause := &ExecutionPause{Type: in.Type, Approval: in.Approval, Input: in.Input}
	if in.Nested != nil {
		pause.Nested = &NestedPause{CallID: in.Nested.CallID, Child: in.Nested.Child}
	}
	return cloneExecution(Execution{Pause: pause}).Pause
}

// cloneInterrupt deep-copies an Interrupt, including a nested child chain.
func cloneInterrupt(in *Interrupt) *Interrupt {
	if in == nil {
		return nil
	}
	out := *in
	if in.Approval != nil {
		approval := *in.Approval
		approval.Calls = append([]ApprovalCall(nil), in.Approval.Calls...)
		for i := range approval.Calls {
			approval.Calls[i].Input = cloneRaw(approval.Calls[i].Input)
		}
		out.Approval = &approval
	}
	if in.Input != nil {
		input := *in.Input
		input.Schema = cloneRaw(in.Input.Schema)
		out.Input = &input
	}
	if in.Nested != nil {
		nested := *in.Nested
		nested.Child = cloneInterrupt(in.Nested.Child)
		out.Nested = &nested
	}
	return &out
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
		case resumeRespond, resumeRespondTyped:
			return fmt.Errorf("resume: Respond/RespondWith are only valid for human_input interrupts")
		default:
			return fmt.Errorf("resume: empty response")
		}
	case InterruptHumanInput:
		switch r.kind {
		case resumeRespond:
			if in.Input != nil && len(in.Input.Schema) != 0 {
				return fmt.Errorf("resume: this human_input interrupt expects a typed answer; use RespondWith")
			}
			return nil
		case resumeRespondTyped:
			if in.Input == nil || len(in.Input.Schema) == 0 {
				return fmt.Errorf("resume: this human_input interrupt expects free text; use Respond")
			}
			if r.typed == nil {
				return &ResumeValidationError{Cause: fmt.Errorf("marshal typed answer: %s", r.text)}
			}
			var schema map[string]any
			if err := json.Unmarshal(in.Input.Schema, &schema); err != nil {
				return &ResumeValidationError{Cause: fmt.Errorf("decode answer schema: %w", err)}
			}
			if err := ValidateToolInput(schema, r.typed); err != nil {
				return &ResumeValidationError{Cause: err}
			}
			return nil
		default:
			return fmt.Errorf("resume: human_input interrupts require Respond or RespondWith")
		}
	case InterruptNested:
		if in.Nested == nil || in.Nested.Child == nil {
			return fmt.Errorf("resume: nested interrupt has no child")
		}
		if in.Nested.CallID == "" {
			return fmt.Errorf("resume: nested interrupt has no call ID")
		}
		// The caller answers the child's own pause; validate against it.
		return validateResume(in.Nested.Child, r)
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

// NewHumanInput pauses a run to ask a human for a typed answer of type R, like
// NewHumanInputTool but with a schema. The pause carries GenerateSchema[R] in
// Interrupt.Input.Schema so a UI can render a form; resume the run with
// RespondWith(R). On resume the typed value is validated against R's schema
// and handler receives it, and handler's result becomes the tool's output.
func NewHumanInput[R any](name, description string, handler func(ctx *Context, answer R) (string, error)) tool.Tool {
	base := "Pause execution and ask a human for a typed answer. Use when you need a structured decision you cannot determine on your own."
	if description != "" {
		base += " " + description
	}
	promptSchema := map[string]any{"type": "object", "properties": map[string]any{"reason": map[string]any{"type": "string", "description": "Why you need human input"}, "question": map[string]any{"type": "string", "description": "The specific question or request for the human"}}, "required": []string{"reason", "question"}}
	answerSchema := tool.GenerateSchema[R]()
	answerSchemaRaw, _ := json.Marshal(answerSchema)

	resume := func(ctx context.Context, raw json.RawMessage) (string, error) {
		var answer R
		if err := json.Unmarshal(raw, &answer); err != nil {
			return "", fmt.Errorf("decode typed answer: %w", err)
		}
		c := FromContext(ctx)
		if c == nil {
			return "", fmt.Errorf("human input resume must run inside an agent context")
		}
		if handler == nil {
			return string(raw), nil
		}
		return handler(c, answer)
	}

	pause := func(ctx context.Context, input json.RawMessage) (string, error) {
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
		c.call.setHumanInput(&InputInterrupt{Reason: p.Reason, Question: p.Question, Schema: cloneRaw(answerSchemaRaw)})
		return humanInputPausedResult, nil
	}
	return tool.NewRaw(name, base, promptSchema, pause, tool.WithResumeHandler(answerSchema, resume))
}
