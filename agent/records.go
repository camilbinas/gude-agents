package agent

import (
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

// ModelCallRecord describes one provider call. The agent loop never retries, so
// each model call is one physical attempt.
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

// Names reported in LimitRecord.Name.
const (
	// LimitMaxIterations: the iteration limit was reached; Limit is WithMaxIterations.
	LimitMaxIterations = "max_iterations"
	// LimitTokenBudget: cumulative usage exceeded WithTokenBudget; Limit is the budget.
	LimitTokenBudget = "token_budget"
	// LimitRateLimit: the rate limiter rejected a provider attempt (Err wraps
	// ErrRateLimitExceeded); Limit is 0.
	LimitRateLimit = "rate_limit"
)

// LimitRecord describes a lifecycle limit being reached.
type LimitRecord struct {
	Phase     Phase
	Name      string
	Limit     int
	Err       error
	Timestamp time.Time
	Duration  time.Duration
}

// ToolLogRecord describes a log message emitted by a tool.
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
