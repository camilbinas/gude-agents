package agent

import (
	"context"
	"encoding/json"
	"time"
)

// Phase identifies whether an observation marks the beginning or end of a
// lifecycle operation.
type Phase string

const (
	Start Phase = "start"
	End   Phase = "end"
)

// InvokeRecord describes one complete agent invocation.
type InvokeRecord struct {
	Phase           Phase
	AgentName       string
	ModelID         string
	MaxIterations   int
	ConversationID  string
	Principal       Principal
	UserMessage     string
	SystemPrompt    string
	InferenceConfig *InferenceConfig
	ImageCount      int
	DocumentCount   int
	Response        string
	Usage           TokenUsage
	StopReason      StopReason
	Interrupt       *Interrupt
	Err             error
	Timestamp       time.Time
	Duration        time.Duration
}

// IterationRecord describes one model/tool loop iteration.
type IterationRecord struct {
	Phase     Phase
	Iteration int
	ToolCount int
	IsFinal   bool
	Err       error
	Timestamp time.Time
	Duration  time.Duration
}

// ModelCallRecord describes one logical provider call. Provider retries are
// represented by the same model observation.
type ModelCallRecord struct {
	Phase           Phase
	ModelID         string
	Iteration       int
	System          string
	MessageCount    int
	InferenceConfig *InferenceConfig
	ResponseText    string
	Usage           TokenUsage
	ToolCallCount   int
	StopReason      string
	Err             error
	Timestamp       time.Time
	Duration        time.Duration
}

// ToolCallRecord describes one attempted tool call.
type ToolCallRecord struct {
	Phase          Phase
	CallID         string
	Name           string
	Input          json.RawMessage
	Output         string
	Err            error
	ResultIsError  bool
	Principal      Principal
	ConversationID string
	Allowed        bool
	DenialReason   string
	IdempotencyKey string
	ReplaySafe     bool
	RecoveryReplay bool
	Timestamp      time.Time
	Duration       time.Duration
}

// GuardrailRecord describes one input or output guardrail evaluation.
type GuardrailRecord struct {
	Phase     Phase
	Direction string
	Input     string
	Output    string
	Blocked   bool
	Err       error
	Timestamp time.Time
	Duration  time.Duration
}

// ConversationRecord describes one conversation-store operation.
type ConversationRecord struct {
	Phase            Phase
	Operation        string
	ConversationID   string
	MessageCount     int
	Usage            TokenUsage
	ExpectedRevision uint64
	Revision         uint64
	Err              error
	Timestamp        time.Time
	Duration         time.Duration
}

// RetrievalRecord describes one retrieval operation.
type RetrievalRecord struct {
	Phase         Phase
	Query         string
	DocumentCount int
	Err           error
	Timestamp     time.Time
	Duration      time.Duration
}

// AttachmentRecord describes validated invocation attachments.
type AttachmentRecord struct {
	Phase         Phase
	ImageCount    int
	DocumentCount int
	Err           error
	Timestamp     time.Time
	Duration      time.Duration
}

// LimitRecord describes a lifecycle limit being reached.
type LimitRecord struct {
	Phase     Phase
	Name      string
	Limit     int
	Err       error
	Timestamp time.Time
	Duration  time.Duration
}

// ToolLogRecord describes a log message emitted by a tool or background-tool
// lifecycle operation.
type ToolLogRecord struct {
	Phase          Phase
	CallID         string
	Name           string
	Message        string
	Principal      Principal
	ConversationID string
	Err            error
	Timestamp      time.Time
	Duration       time.Duration
}

// InterruptRecord describes an invocation pause for approval or human input.
type InterruptRecord struct {
	Phase          Phase
	InterruptID    string
	Type           InterruptType
	Principal      Principal
	ConversationID string
	ApprovalCalls  []ApprovalCall
	Reason         string
	Question       string
	Err            error
	Timestamp      time.Time
	Duration       time.Duration
}

// Observer capability interfaces are intentionally small. Implement only the
// lifecycle categories an adapter needs. Methods may be called concurrently.
// Returning nil preserves the input context.
type InvokeObserver interface {
	ObserveInvoke(context.Context, InvokeRecord) context.Context
}

type IterationObserver interface {
	ObserveIteration(context.Context, IterationRecord) context.Context
}

type ModelObserver interface {
	ObserveModel(context.Context, ModelCallRecord) context.Context
}

type ToolObserver interface {
	ObserveTool(context.Context, ToolCallRecord) context.Context
}

type GuardrailObserver interface {
	ObserveGuardrail(context.Context, GuardrailRecord) context.Context
}

type ConversationObserver interface {
	ObserveConversation(context.Context, ConversationRecord) context.Context
}

type RetrievalObserver interface {
	ObserveRetrieval(context.Context, RetrievalRecord) context.Context
}

type AttachmentObserver interface {
	ObserveAttachment(context.Context, AttachmentRecord) context.Context
}

type LimitObserver interface {
	ObserveLimit(context.Context, LimitRecord) context.Context
}

type ToolLogObserver interface {
	ObserveToolLog(context.Context, ToolLogRecord) context.Context
}

type InterruptObserver interface {
	ObserveInterrupt(context.Context, InterruptRecord) context.Context
}

// Observer is any value implementing at least one observer capability
// interface: InvokeObserver, IterationObserver, ModelObserver, ToolObserver,
// GuardrailObserver, ConversationObserver, RetrievalObserver,
// AttachmentObserver, LimitObserver, ToolLogObserver, or InterruptObserver.
//
// Go cannot express "one of these interfaces" as a single static type when
// the interfaces have methods, so Observer is an alias for any and the
// capability check happens at registration time in WithObserver. Implement
// only the capabilities an adapter needs, and add a compile-time assertion
// per implemented capability so a typo'd method name fails to build instead
// of silently dropping that capability:
//
//	var _ agent.ToolObserver = (*MyObserver)(nil)
type Observer = any
