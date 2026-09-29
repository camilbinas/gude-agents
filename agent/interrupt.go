package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// InterruptType identifies why an invocation paused.
type InterruptType string

const (
	// InterruptApproval means one or more tool calls marked with
	// tool.RequiresApproval are waiting for a human decision.
	InterruptApproval InterruptType = "approval"
	// InterruptHumanInput means the model called the human-input tool
	// (NewHumanInputTool) and waits for a human answer.
	InterruptHumanInput InterruptType = "human_input"
)

// Interrupt describes a paused invocation. It is returned in
// Result.Interrupt and emitted as EventInterrupt. Its Result normally has a
// nil error; post-commit persistence failures return the usable interrupt
// together with the error. Continue it with Agent.Resume / Agent.ResumeStream.
type Interrupt struct {
	// ID uniquely identifies the interrupt (key for InterruptStore).
	ID string `json:"id"`
	// Type is InterruptApproval or InterruptHumanInput.
	Type InterruptType `json:"type"`
	// ConversationID is the conversation the paused invocation belongs to.
	// Empty means the invocation is stateless; Resume must preserve that.
	ConversationID string `json:"conversation_id,omitempty"`
	// Revision is the committed revision of Messages for a persisted
	// conversation. It is zero for stateless interrupts.
	Revision uint64 `json:"revision"`
	// Approval lists the pending calls (Type == InterruptApproval).
	Approval *ApprovalInterrupt `json:"approval,omitempty"`
	// Input describes the human ask (Type == InterruptHumanInput).
	Input *InputInterrupt `json:"input,omitempty"`
	// Messages is the resumable conversation snapshot at the pause. It is
	// exported for durability (see conversation.MarshalInterrupt) and omitted
	// from the event JSON encoding.
	Messages []Message `json:"-"`
}

// ApprovalInterrupt lists the tool calls awaiting approval, in provider order.
type ApprovalInterrupt struct {
	Calls []ApprovalCall `json:"calls"`
}

// ApprovalCall identifies one tool call awaiting human approval.
type ApprovalCall struct {
	CallID string          `json:"call_id"`
	Name   string          `json:"name"`
	Input  json.RawMessage `json:"input,omitempty"`
}

// InputInterrupt describes what the model asked a human for.
type InputInterrupt struct {
	Reason   string `json:"reason"`
	Question string `json:"question"`
}

// ResumeResponse is the human answer to an Interrupt. Build it with Approve,
// Deny, Decide or Respond.
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

// Approve approves every pending call of an approval interrupt.
func Approve() ResumeResponse {
	return ResumeResponse{kind: resumeAll, decision: tool.Allow()}
}

// Deny denies every pending call of an approval interrupt with reason.
func Deny(reason string) ResumeResponse {
	return ResumeResponse{kind: resumeAll, decision: tool.Deny(reason)}
}

// Decide supplies one decision per pending call, keyed by CallID. The map
// must cover every pending call exactly; it is validated before any tool runs.
func Decide(decisions map[string]tool.Decision) ResumeResponse {
	cp := make(map[string]tool.Decision, len(decisions))
	for k, v := range decisions {
		cp[k] = v
	}
	return ResumeResponse{kind: resumeDecide, decisions: cp}
}

// Respond answers a human-input interrupt with text.
func Respond(text string) ResumeResponse {
	return ResumeResponse{kind: resumeRespond, text: text}
}

// InterruptStore persists pending interrupts so they can be resumed from
// another process. Implementations must be safe for concurrent use.
type InterruptStore interface {
	// Save creates a pending interrupt under in.ID. It must fail rather than
	// replace an existing pending or consumed interrupt.
	Save(ctx context.Context, in *Interrupt) error
	// Load returns the pending interrupt stored under id, or an error wrapping
	// ErrInterruptNotFound when none exists or it was already consumed.
	Load(ctx context.Context, id string) (*Interrupt, error)
	// Claim atomically consumes and returns the pending interrupt under id.
	// Missing and previously consumed interrupts return an error wrapping
	// ErrInterruptNotFound. Exactly one concurrent claimant may succeed.
	Claim(ctx context.Context, id string) (*Interrupt, error)
}

// ErrInterruptNotFound is returned (wrapped) when an interrupt does not exist.
var ErrInterruptNotFound = errors.New("interrupt not found")

// ErrNoInterruptStore is returned by LoadInterrupt when no store is configured.
var ErrNoInterruptStore = errors.New("no interrupt store configured")

// WithInterruptStore configures durable persistence of interrupts. When set,
// the configured store is authoritative: every executable pause must be
// durably saved there, and Resume atomically claims it before any lifecycle,
// provider, guardrail, or tool work begins. Persistence errors return the
// pause as a recovery snapshot, but do not make it locally executable.
func WithInterruptStore(s InterruptStore) Option {
	return func(a *Agent) error {
		if s == nil {
			return fmt.Errorf("WithInterruptStore: store must not be nil")
		}
		a.interruptStore = s
		a.interruptStoreConfigured = true
		return nil
	}
}

// LoadInterrupt loads a pending interrupt from the explicitly configured store.
func (a *Agent) LoadInterrupt(ctx context.Context, id string) (*Interrupt, error) {
	if !a.interruptStoreConfigured {
		return nil, ErrNoInterruptStore
	}
	in, err := a.interruptStore.Load(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("load interrupt: %w", err)
	}
	if in == nil {
		return nil, fmt.Errorf("load interrupt %q: %w", id, ErrInterruptNotFound)
	}
	return in, nil
}

// validateResume checks that r is a valid answer to in. It runs before any
// state changes or tool handlers.
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

// decisionFor returns the decision for callID (response already validated).
func (r ResumeResponse) decisionFor(callID string) tool.Decision {
	if r.kind == resumeDecide {
		return r.decisions[callID]
	}
	return r.decision
}

func newInterruptID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// humanInputPausedResult is the tool result recorded for the human-input call.
const humanInputPausedResult = "Paused — waiting for human input."

// NewHumanInputTool creates a tool that lets the model pause the invocation
// and ask a human for input. When the model calls it, Invoke returns a Result
// with StopReason StopInterrupt and an Interrupt of type InterruptHumanInput;
// answer it with Agent.Resume(ctx, in, Respond(text)).
//
// name is the tool name exposed to the model; description is appended to
// the base description to define when the handoff should occur.
func NewHumanInputTool(name, description string) tool.Tool {
	base := "Pause execution and ask a human for input, a decision, or approval. " +
		"Use when you need information you cannot determine on your own."
	if description != "" {
		base += " " + description
	}
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"reason": map[string]any{
				"type":        "string",
				"description": "Why you need human input",
			},
			"question": map[string]any{
				"type":        "string",
				"description": "The specific question or request for the human",
			},
		},
		"required": []string{"reason", "question"},
	}
	return tool.NewRaw(
		name,
		base,
		func(ctx context.Context, input json.RawMessage) (string, error) {
			var params struct {
				Reason   string `json:"reason"`
				Question string `json:"question"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return "", fmt.Errorf("invalid human input request: %w", err)
			}
			c := FromContext(ctx)
			if c == nil || c.call == nil {
				return "", fmt.Errorf("human input tool must run inside an agent tool call")
			}
			c.call.setHumanInput(&InputInterrupt{Reason: params.Reason, Question: params.Question})
			return humanInputPausedResult, nil
		},
		tool.WithSchema(schema),
	)
}

// memoryInterruptStore is the private one-shot store used when no durable
// InterruptStore is configured. It keeps direct Resume calls safe from replay
// while LoadInterrupt remains reserved for explicitly configured stores.
type memoryInterruptStore struct {
	mu       sync.Mutex
	pending  map[string]*Interrupt
	consumed map[string]struct{}
}

func newMemoryInterruptStore() *memoryInterruptStore {
	return &memoryInterruptStore{
		pending:  make(map[string]*Interrupt),
		consumed: make(map[string]struct{}),
	}
}

func (s *memoryInterruptStore) Save(_ context.Context, in *Interrupt) error {
	if in == nil {
		return fmt.Errorf("interrupt store: interrupt is required")
	}
	if in.ID == "" {
		return fmt.Errorf("interrupt store: interrupt ID is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.pending[in.ID]; exists {
		return fmt.Errorf("interrupt store: interrupt %q already exists", in.ID)
	}
	if _, exists := s.consumed[in.ID]; exists {
		return fmt.Errorf("interrupt store: interrupt %q was already consumed", in.ID)
	}
	s.pending[in.ID] = cloneInterrupt(in)
	return nil
}

func (s *memoryInterruptStore) Load(_ context.Context, id string) (*Interrupt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	in, ok := s.pending[id]
	if !ok {
		return nil, fmt.Errorf("interrupt store: %q: %w", id, ErrInterruptNotFound)
	}
	return cloneInterrupt(in), nil
}

func (s *memoryInterruptStore) Claim(_ context.Context, id string) (*Interrupt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	in, ok := s.pending[id]
	if !ok {
		return nil, fmt.Errorf("interrupt store: %q: %w", id, ErrInterruptNotFound)
	}
	delete(s.pending, id)
	s.consumed[id] = struct{}{}
	return cloneInterrupt(in), nil
}

func cloneInterrupt(in *Interrupt) *Interrupt {
	if in == nil {
		return nil
	}
	out := *in
	if in.Approval != nil {
		approval := *in.Approval
		approval.Calls = append([]ApprovalCall(nil), in.Approval.Calls...)
		for i := range approval.Calls {
			approval.Calls[i].Input = append(json.RawMessage(nil), approval.Calls[i].Input...)
		}
		out.Approval = &approval
	}
	if in.Input != nil {
		input := *in.Input
		out.Input = &input
	}
	out.Messages = make([]Message, len(in.Messages))
	for i, msg := range in.Messages {
		out.Messages[i].Role = msg.Role
		out.Messages[i].Content = make([]ContentBlock, len(msg.Content))
		for j, block := range msg.Content {
			out.Messages[i].Content[j] = cloneContentBlock(block)
		}
	}
	return &out
}

func cloneContentBlock(block ContentBlock) ContentBlock {
	switch b := block.(type) {
	case ToolUseBlock:
		b.Input = append(json.RawMessage(nil), b.Input...)
		return b
	case ToolResultBlock:
		b.Images = append([]ImageBlock(nil), b.Images...)
		for i := range b.Images {
			b.Images[i].Source.Data = append([]byte(nil), b.Images[i].Source.Data...)
		}
		return b
	case ImageBlock:
		b.Source.Data = append([]byte(nil), b.Source.Data...)
		return b
	case DocumentBlock:
		b.Source.Data = append([]byte(nil), b.Source.Data...)
		return b
	case WidgetBlock:
		b.Payload = append(json.RawMessage(nil), b.Payload...)
		return b
	default:
		return block
	}
}
