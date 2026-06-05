package agent

import (
	"encoding/json"
	"fmt"
	"time"
)

// AuditConfig bundles an AuditHook implementation with content-capture preferences.
// Pass it to WithAuditHook when constructing an Agent.
type AuditConfig struct {
	Hook           AuditHook
	CaptureContent bool
}

// AuditEventType is the discriminator field present on all audit records.
// It identifies which hook method a record was emitted from.
type AuditEventType = string

const (
	AuditEventToolCall        AuditEventType = "tool_call"
	AuditEventInvokeStart     AuditEventType = "invoke_start"
	AuditEventInvokeEnd       AuditEventType = "invoke_end"
	AuditEventHandoff         AuditEventType = "handoff"
	AuditEventApprovalRequest AuditEventType = "approval_request"
)

// AuditRecord is emitted after every tool call when an AuditHook is configured.
// The struct is JSON-serializable: Err is encoded as "error" (omitted when nil),
// and Duration is encoded as "duration_ms" (milliseconds).
type AuditRecord struct {
	Event          AuditEventType  `json:"event"` // always "tool_call"
	Principal      Principal       `json:"principal"`
	ToolName       string          `json:"tool_name"`
	ToolInput      json.RawMessage `json:"tool_input,omitempty"`  // nil when CaptureContent is false
	ToolOutput     string          `json:"tool_output,omitempty"` // empty when CaptureContent is false
	Err            error           `json:"-"`                     // encoded as "error" by MarshalJSON
	Allowed        bool            `json:"allowed"`
	DenialReason   string          `json:"denial_reason,omitempty"`   // empty when Allowed is true
	ConversationID string          `json:"conversation_id,omitempty"` // empty when no conversation ID is resolved
	Duration       time.Duration   `json:"-"`                         // encoded as "duration_ms" by MarshalJSON
	Timestamp      time.Time       `json:"timestamp"`
}

func (r AuditRecord) MarshalJSON() ([]byte, error) {
	type wire struct {
		Event          AuditEventType  `json:"event"`
		Principal      Principal       `json:"principal"`
		ToolName       string          `json:"tool_name"`
		ToolInput      json.RawMessage `json:"tool_input,omitempty"`
		ToolOutput     string          `json:"tool_output,omitempty"`
		Error          string          `json:"error,omitempty"`
		Allowed        bool            `json:"allowed"`
		DenialReason   string          `json:"denial_reason,omitempty"`
		ConversationID string          `json:"conversation_id,omitempty"`
		DurationMS     int64           `json:"duration_ms"`
		Timestamp      time.Time       `json:"timestamp"`
	}
	var errStr string
	if r.Err != nil {
		errStr = r.Err.Error()
	}
	return json.Marshal(wire{
		Event:          r.Event,
		Principal:      r.Principal,
		ToolName:       r.ToolName,
		ToolInput:      r.ToolInput,
		ToolOutput:     r.ToolOutput,
		Error:          errStr,
		Allowed:        r.Allowed,
		DenialReason:   r.DenialReason,
		ConversationID: r.ConversationID,
		DurationMS:     r.Duration.Milliseconds(),
		Timestamp:      r.Timestamp,
	})
}

// InvokeAuditRecord carries audit data for a single Invoke / InvokeStream lifetime.
// It is emitted by OnInvokeStart at invocation start and by OnInvokeEnd at invocation end.
// The struct is JSON-serializable: Err is encoded as "error" (omitted when nil),
// and Duration is encoded as "duration_ms" (milliseconds).
type InvokeAuditRecord struct {
	Event          AuditEventType `json:"event"` // "invoke_start" or "invoke_end"
	Principal      Principal      `json:"principal"`
	ConversationID string         `json:"conversation_id,omitempty"`
	AgentName      string         `json:"agent_name,omitempty"`
	UserMessage    string         `json:"user_message,omitempty"` // empty when CaptureContent is false
	Response       string         `json:"response,omitempty"`     // populated in OnInvokeEnd when CaptureContent is true
	Err            error          `json:"-"`                      // encoded as "error" by MarshalJSON
	Usage          TokenUsage     `json:"usage"`
	Duration       time.Duration  `json:"-"` // encoded as "duration_ms" by MarshalJSON
	Timestamp      time.Time      `json:"timestamp"`
}

func (r InvokeAuditRecord) MarshalJSON() ([]byte, error) {
	type wire struct {
		Event          AuditEventType `json:"event"`
		Principal      Principal      `json:"principal"`
		ConversationID string         `json:"conversation_id,omitempty"`
		AgentName      string         `json:"agent_name,omitempty"`
		UserMessage    string         `json:"user_message,omitempty"`
		Response       string         `json:"response,omitempty"`
		Error          string         `json:"error,omitempty"`
		Usage          TokenUsage     `json:"usage"`
		DurationMS     int64          `json:"duration_ms,omitempty"`
		Timestamp      time.Time      `json:"timestamp"`
	}
	var errStr string
	if r.Err != nil {
		errStr = r.Err.Error()
	}
	return json.Marshal(wire{
		Event:          r.Event,
		Principal:      r.Principal,
		ConversationID: r.ConversationID,
		AgentName:      r.AgentName,
		UserMessage:    r.UserMessage,
		Response:       r.Response,
		Error:          errStr,
		Usage:          r.Usage,
		DurationMS:     r.Duration.Milliseconds(),
		Timestamp:      r.Timestamp,
	})
}

// HandoffAuditRecord carries audit data for a human-handoff pause event.
// It is emitted by OnHandoff when the agent enters the isHandoffResult branch.
type HandoffAuditRecord struct {
	Event          AuditEventType `json:"event"` // always "handoff"
	Principal      Principal      `json:"principal"`
	ConversationID string         `json:"conversation_id,omitempty"`
	Reason         string         `json:"reason,omitempty"`
	Question       string         `json:"question,omitempty"`
	Timestamp      time.Time      `json:"timestamp"`
}

// ApprovalAuditRecord carries audit data for a tool-approval pause event.
// It is emitted by OnApprovalRequest when the agent enters the isApprovalResult branch.
type ApprovalAuditRecord struct {
	Event          AuditEventType  `json:"event"` // always "approval_request"
	Principal      Principal       `json:"principal"`
	ConversationID string          `json:"conversation_id,omitempty"`
	ToolName       string          `json:"tool_name,omitempty"`
	ToolInput      json.RawMessage `json:"tool_input,omitempty"` // nil when CaptureContent is false
	Timestamp      time.Time       `json:"timestamp"`
}

// AuditHook receives audit events from the agent.
// Embed NoopAuditHook to satisfy the interface without implementing every method.
type AuditHook interface {
	// OnToolCall is called after every tool call.
	OnToolCall(record AuditRecord)
	// OnInvokeStart is called at the start of every Invoke / InvokeStream.
	OnInvokeStart(record InvokeAuditRecord)
	// OnInvokeEnd is called at the end of every Invoke / InvokeStream.
	OnInvokeEnd(record InvokeAuditRecord)
	// OnHandoff is called when the agent pauses for human input.
	OnHandoff(record HandoffAuditRecord)
	// OnApprovalRequest is called when a tool requires explicit approval.
	OnApprovalRequest(record ApprovalAuditRecord)
}

// NoopAuditHook provides empty implementations of all AuditHook methods.
// Embed it in custom hook types to satisfy the interface without implementing every method.
//
//	type MySIEMHook struct {
//	    agent.NoopAuditHook        // free pass on OnInvokeStart, OnInvokeEnd, OnHandoff, OnApprovalRequest
//	}
//	func (h *MySIEMHook) OnToolCall(r agent.AuditRecord) { /* ... */ }
type NoopAuditHook struct{}

func (NoopAuditHook) OnToolCall(AuditRecord)                {}
func (NoopAuditHook) OnInvokeStart(InvokeAuditRecord)       {}
func (NoopAuditHook) OnInvokeEnd(InvokeAuditRecord)         {}
func (NoopAuditHook) OnHandoff(HandoffAuditRecord)          {}
func (NoopAuditHook) OnApprovalRequest(ApprovalAuditRecord) {}

// DenialReasonRolePolicy is set when the caller's roles did not satisfy the tool's role policy.
const DenialReasonRolePolicy = "role_policy"

// DenialReasonAttrCondition is set when the caller's attributes did not satisfy an AllowWhen condition.
const DenialReasonAttrCondition = "attr_condition"

// DenialReasonGuard is set when a tool Guard function returned a denial decision.
const DenialReasonGuard = "guard"

// DenialReasonToolApprovalDenied is set when a human reviewer denied a pending tool call via ResumeWithApproval.
const DenialReasonToolApprovalDenied = "tool_approval_denied"

// WithAuditHook attaches an AuditHook to the agent.
// Returns an error if cfg.Hook is nil.
//
// Set CaptureContent: true to include ToolInput, ToolOutput, UserMessage, and
// Response in audit records. The default (false) omits all payloads — use this
// in production to avoid logging sensitive data.
func WithAuditHook(cfg AuditConfig) Option {
	return func(a *Agent) error {
		if cfg.Hook == nil {
			return fmt.Errorf("WithAuditHook: Hook must not be nil")
		}
		a.auditHook = cfg.Hook
		a.auditCaptureContent = cfg.CaptureContent
		return nil
	}
}

// inputForAudit returns input for an audit record, or nil when capture is disabled.
func inputForAudit(input json.RawMessage, capture bool) json.RawMessage {
	if !capture {
		return nil
	}
	return input
}

// outputForAudit returns output for an audit record, or "" when capture is disabled.
func outputForAudit(output string, capture bool) string {
	if !capture {
		return ""
	}
	return output
}
