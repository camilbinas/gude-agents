package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"
)

// AuditSink receives stable JSON-serializable audit records.
type AuditSink interface {
	WriteAudit(context.Context, any)
}

// AuditOption configures the internal audit observer.
type AuditOption func(*auditOptions)

type auditOptions struct {
	captureContent bool
}

// WithAuditContent enables user-message, response, tool-input and tool-output
// capture. Audit content is redacted by default.
func WithAuditContent() AuditOption {
	return func(options *auditOptions) { options.captureContent = true }
}

// WithAudit registers an internal invoke/tool/interrupt observer that writes
// stable records to sink.
func WithAudit(sink AuditSink, options ...AuditOption) Option {
	return func(a *Agent) error {
		if sink == nil || (reflect.ValueOf(sink).Kind() == reflect.Ptr && reflect.ValueOf(sink).IsNil()) {
			return fmt.Errorf("WithAudit: sink must not be nil")
		}
		cfg := auditOptions{}
		for _, option := range options {
			if option != nil {
				option(&cfg)
			}
		}
		a.observers = append(a.observers, &auditObserver{sink: sink, captureContent: cfg.captureContent})
		return nil
	}
}

// AuditEventType is the discriminator field present on all audit records.
type AuditEventType = string

const (
	AuditEventToolCall        AuditEventType = "tool_call"
	AuditEventInvokeStart     AuditEventType = "invoke_start"
	AuditEventInvokeEnd       AuditEventType = "invoke_end"
	AuditEventHandoff         AuditEventType = "handoff"
	AuditEventApprovalRequest AuditEventType = "approval_request"
)

// AuditRecord is the stable tool-call audit wire record.
type AuditRecord struct {
	Event          AuditEventType  `json:"event"`
	Principal      Principal       `json:"principal"`
	CallID         string          `json:"call_id,omitempty"`
	ToolName       string          `json:"tool_name"`
	ToolInput      json.RawMessage `json:"tool_input,omitempty"`
	ToolOutput     string          `json:"tool_output,omitempty"`
	ResultIsError  bool            `json:"result_is_error,omitempty"`
	Err            error           `json:"-"`
	Allowed        bool            `json:"allowed"`
	DenialReason   string          `json:"denial_reason,omitempty"`
	ConversationID string          `json:"conversation_id,omitempty"`
	Duration       time.Duration   `json:"-"`
	Timestamp      time.Time       `json:"timestamp"`
}

func (r AuditRecord) MarshalJSON() ([]byte, error) {
	type wire struct {
		Event          AuditEventType  `json:"event"`
		Principal      Principal       `json:"principal"`
		CallID         string          `json:"call_id,omitempty"`
		ToolName       string          `json:"tool_name"`
		ToolInput      json.RawMessage `json:"tool_input,omitempty"`
		ToolOutput     string          `json:"tool_output,omitempty"`
		ResultIsError  bool            `json:"result_is_error,omitempty"`
		Error          string          `json:"error,omitempty"`
		Allowed        bool            `json:"allowed"`
		DenialReason   string          `json:"denial_reason,omitempty"`
		ConversationID string          `json:"conversation_id,omitempty"`
		DurationMS     int64           `json:"duration_ms"`
		Timestamp      time.Time       `json:"timestamp"`
	}
	var errorText string
	if r.Err != nil {
		errorText = r.Err.Error()
	}
	return json.Marshal(wire{r.Event, r.Principal, r.CallID, r.ToolName, r.ToolInput, r.ToolOutput,
		r.ResultIsError, errorText, r.Allowed, r.DenialReason, r.ConversationID, r.Duration.Milliseconds(), r.Timestamp})
}

// InvokeAuditRecord is the stable invocation audit wire record.
type InvokeAuditRecord struct {
	Event          AuditEventType `json:"event"`
	Principal      Principal      `json:"principal"`
	ConversationID string         `json:"conversation_id,omitempty"`
	AgentName      string         `json:"agent_name,omitempty"`
	UserMessage    string         `json:"user_message,omitempty"`
	Response       string         `json:"response,omitempty"`
	Err            error          `json:"-"`
	Usage          TokenUsage     `json:"usage"`
	Duration       time.Duration  `json:"-"`
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
	var errorText string
	if r.Err != nil {
		errorText = r.Err.Error()
	}
	return json.Marshal(wire{r.Event, r.Principal, r.ConversationID, r.AgentName, r.UserMessage,
		r.Response, errorText, r.Usage, r.Duration.Milliseconds(), r.Timestamp})
}

// HandoffAuditRecord is the stable human-input interrupt audit wire record.
type HandoffAuditRecord struct {
	Event          AuditEventType `json:"event"`
	Principal      Principal      `json:"principal"`
	ConversationID string         `json:"conversation_id,omitempty"`
	InterruptID    string         `json:"interrupt_id,omitempty"`
	Reason         string         `json:"reason,omitempty"`
	Question       string         `json:"question,omitempty"`
	Timestamp      time.Time      `json:"timestamp"`
}

// ApprovalAuditRecord is the stable per-call approval audit wire record.
type ApprovalAuditRecord struct {
	Event          AuditEventType  `json:"event"`
	Principal      Principal       `json:"principal"`
	ConversationID string          `json:"conversation_id,omitempty"`
	InterruptID    string          `json:"interrupt_id,omitempty"`
	CallID         string          `json:"call_id,omitempty"`
	ToolName       string          `json:"tool_name,omitempty"`
	ToolInput      json.RawMessage `json:"tool_input,omitempty"`
	Timestamp      time.Time       `json:"timestamp"`
}

const (
	DenialReasonRolePolicy         = "role_policy"
	DenialReasonAttrCondition      = "attr_condition"
	DenialReasonGuard              = "guard"
	DenialReasonToolApprovalDenied = "tool_approval_denied"
)

type auditObserver struct {
	sink           AuditSink
	captureContent bool
}

func (o *auditObserver) ObserveInvoke(ctx context.Context, record InvokeRecord) context.Context {
	event := AuditEventInvokeStart
	if record.Phase == End {
		event = AuditEventInvokeEnd
	}
	out := InvokeAuditRecord{
		Event: event, Principal: record.Principal, ConversationID: record.ConversationID,
		AgentName: record.AgentName, Err: record.Err, Usage: record.Usage,
		Duration: record.Duration, Timestamp: record.Timestamp,
	}
	if o.captureContent {
		out.UserMessage, out.Response = record.UserMessage, record.Response
	}
	o.sink.WriteAudit(ctx, out)
	return ctx
}

func (o *auditObserver) ObserveTool(ctx context.Context, record ToolCallRecord) context.Context {
	if record.Phase != End {
		return ctx
	}
	out := AuditRecord{
		Event: AuditEventToolCall, Principal: record.Principal, CallID: record.CallID,
		ToolName: record.Name, ResultIsError: record.ResultIsError, Err: record.Err,
		Allowed: record.Allowed, DenialReason: record.DenialReason,
		ConversationID: record.ConversationID, Duration: record.Duration, Timestamp: record.Timestamp,
	}
	if o.captureContent {
		out.ToolInput, out.ToolOutput = cloneRaw(record.Input), record.Output
	}
	o.sink.WriteAudit(ctx, out)
	return ctx
}

func (o *auditObserver) ObserveInterrupt(ctx context.Context, record InterruptRecord) context.Context {
	if record.Phase != End {
		return ctx
	}
	switch record.Type {
	case InterruptHumanInput:
		o.sink.WriteAudit(ctx, HandoffAuditRecord{
			Event: AuditEventHandoff, Principal: record.Principal, ConversationID: record.ConversationID,
			InterruptID: record.InterruptID, Reason: record.Reason, Question: record.Question, Timestamp: record.Timestamp,
		})
	case InterruptApproval:
		for _, call := range record.ApprovalCalls {
			input := json.RawMessage(nil)
			if o.captureContent {
				input = cloneRaw(call.Input)
			}
			o.sink.WriteAudit(ctx, ApprovalAuditRecord{
				Event: AuditEventApprovalRequest, Principal: record.Principal, ConversationID: record.ConversationID,
				InterruptID: record.InterruptID, CallID: call.CallID, ToolName: call.Name,
				ToolInput: input, Timestamp: record.Timestamp,
			})
		}
	}
	return ctx
}
