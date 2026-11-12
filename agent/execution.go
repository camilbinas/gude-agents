package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ExecutionStatus is the durable lifecycle state of an invocation.
type ExecutionStatus string

const (
	ExecutionRunning   ExecutionStatus = "running"
	ExecutionPaused    ExecutionStatus = "paused"
	ExecutionCompleted ExecutionStatus = "completed"
	ExecutionFailed    ExecutionStatus = "failed"
	ExecutionCanceled  ExecutionStatus = "canceled"
)

// ExecutionPhase describes the latest durable engine boundary. It is not a
// real-time worker heartbeat and this first model does not persist every
// model/tool transition.
type ExecutionPhase string

const (
	ExecutionPhaseModel  ExecutionPhase = "model"
	ExecutionPhaseTools  ExecutionPhase = "tools"
	ExecutionPhasePaused ExecutionPhase = "paused"
	ExecutionPhaseDone   ExecutionPhase = "done"
)

// ExecutionPause is the small runtime payload needed to project a paused
// execution as an Interrupt. Conversation history always remains in the
// ConversationStore and is referenced by the execution cursor.
type ExecutionPause struct {
	Type     InterruptType      `json:"type"`
	Approval *ApprovalInterrupt `json:"approval,omitempty"`
	Input    *InputInterrupt    `json:"input,omitempty"`
}

// ToolExecutionStatus describes the durable state of a call in the currently
// incomplete tool boundary. Completed markers remain only until every sibling
// in that same bounded batch is canonical, then the entire batch is cleared.
type ToolExecutionStatus string

const (
	ToolExecutionPlanned  ToolExecutionStatus = "planned"
	ToolExecutionReady    ToolExecutionStatus = "ready"
	ToolExecutionInFlight ToolExecutionStatus = "in_flight"
	// ToolExecutionCompleted is retained only inside the current incomplete
	// batch so recovery can validate its full canonical ToolUse boundary while
	// never replaying a sibling whose ToolResult is already canonical.
	ToolExecutionCompleted ToolExecutionStatus = "completed"
)

// ToolApprovalDecision is a bounded, active-call decision retained across a
// crash between Resume and handler dispatch. It is never transcript history.
type ToolApprovalDecision struct {
	Allow  bool   `json:"allow"`
	Reason string `json:"reason,omitempty"`
}

// ToolExecution stores the minimal intent necessary to reconcile a call after
// a crash. Input is cleared after canonical ToolUse is verified; results stay
// exclusively in ConversationStore.
type ToolExecution struct {
	CallID         string                `json:"call_id"`
	Name           string                `json:"name"`
	Status         ToolExecutionStatus   `json:"status"`
	IdempotencyKey string                `json:"idempotency_key"`
	InputHash      string                `json:"input_hash"`
	Input          json.RawMessage       `json:"input,omitempty"`
	ReplaySafe     bool                  `json:"replay_safe"`
	Approval       *ToolApprovalDecision `json:"approval,omitempty"`
}

// ToolResolutionClaim owns exactly one manual success/failure reconciliation
// while its ToolResult is being made canonical. Its payload is cleared as soon
// as that result is observed in ConversationStore.
type ToolResolutionClaim struct {
	CallID       string                `json:"call_id"`
	Outcome      ToolResolutionOutcome `json:"outcome"`
	Output       string                `json:"output,omitempty"`
	ErrorMessage string                `json:"error_message,omitempty"`
}

// ToolBatchExecution is the single active recovery boundary. It is cleared as
// soon as all result blocks become canonical conversation history.
type ToolBatchExecution struct {
	Iteration           int                  `json:"iteration"`
	Calls               []ToolExecution      `json:"calls"`
	ToolUseRevision     uint64               `json:"tool_use_revision"`
	ToolUseLastSequence uint64               `json:"tool_use_last_sequence"`
	Resolution          *ToolResolutionClaim `json:"resolution,omitempty"`
}

// ToolResolutionOutcome is an application-confirmed outcome for an ambiguous
// unsafe call.
//
// Experimental: consumed only by ReconcileToolExecution, whose manual
// reconciliation contract may change in a minor release.
type ToolResolutionOutcome string

const (
	ToolResolutionSucceeded ToolResolutionOutcome = "succeeded"
	ToolResolutionFailed    ToolResolutionOutcome = "failed"
	ToolResolutionRetry     ToolResolutionOutcome = "retry"
)

// ToolResolution is the application-confirmed outcome passed to
// ReconcileToolExecution.
//
// Experimental: part of the manual reconciliation path; its shape may change in
// a minor release.
type ToolResolution struct {
	Outcome      ToolResolutionOutcome
	Output       string
	ErrorMessage string
}

var ErrToolExecutionUncertain = errors.New("tool execution outcome uncertain")

type ToolExecutionUncertainError struct {
	ExecutionID    string
	CallID         string
	ToolName       string
	IdempotencyKey string
}

func (e *ToolExecutionUncertainError) Error() string {
	return fmt.Sprintf("%s: execution=%q call=%q tool=%q", ErrToolExecutionUncertain, e.ExecutionID, e.CallID, e.ToolName)
}
func (e *ToolExecutionUncertainError) Unwrap() error { return ErrToolExecutionUncertain }

// ErrExecutionRecoveryUnsupported reports that a durable execution record is in
// a shape recovery cannot act on: a missing or already-completed tool boundary,
// a status/phase mismatch, a duplicate or misaligned call ID, or a transcript
// that does not match the recorded batch. It is deliberately one sentinel so
// callers branch with errors.Is; recoveryUnsupported wraps it with the specific
// invariant that failed so operators can tell a benign race from real
// corruption without losing errors.Is compatibility.
var ErrExecutionRecoveryUnsupported = errors.New("execution recovery state unsupported")

// recoveryUnsupported annotates ErrExecutionRecoveryUnsupported with the
// invariant that failed. errors.Is(err, ErrExecutionRecoveryUnsupported) still
// reports true.
func recoveryUnsupported(invariant string) error {
	return fmt.Errorf("%w: %s", ErrExecutionRecoveryUnsupported, invariant)
}

// Execution is durable runtime state for one invocation. Version is an
// ExecutionStore CAS version, distinct from the ConversationStore revision.
type Execution struct {
	ID string `json:"id"`

	ConversationID       string `json:"conversation_id,omitempty"`
	Revision             uint64 `json:"revision"`
	LastSequence         uint64 `json:"last_sequence"`
	StartRevision        uint64 `json:"start_revision"`
	StartLastSequence    uint64 `json:"start_last_sequence"`
	StartCursorPersisted bool   `json:"start_cursor_persisted"`

	Status    ExecutionStatus `json:"status"`
	Phase     ExecutionPhase  `json:"phase"`
	Iteration int             `json:"iteration"`
	Pause     *ExecutionPause `json:"pause,omitempty"`
	// PendingPause makes a post-tool-result human/approval pause recoverable
	// until its final paused representation has been acknowledged by the store.
	PendingPause *ExecutionPause     `json:"pending_pause,omitempty"`
	ToolBatch    *ToolBatchExecution `json:"tool_batch,omitempty"`
	Usage        TokenUsage          `json:"usage"`

	// FailureCode and FailureMessage are a bounded, inspectable reason for a
	// terminal non-success status. They are set when an execution reaches
	// ExecutionFailed or ExecutionCanceled and are empty otherwise. The message
	// is truncated to keep the durable record small; it is a diagnostic, not a
	// structured error to branch on. Backends that store the execution as one
	// JSON payload (all stores in agent/execution) round-trip these fields with
	// no schema change; older records simply decode them as empty.
	FailureCode    string `json:"failure_code,omitempty"`
	FailureMessage string `json:"failure_message,omitempty"`

	Version uint64 `json:"version"`
}

// Bounded failure-reason codes recorded on a terminal non-success Execution.
// They classify why a run ended; FailureMessage carries the truncated detail.
const (
	// FailureCodeProvider is a provider call failure, including a provider-side
	// WithProviderTimeout deadline.
	FailureCodeProvider = "provider"
	// FailureCodeGuardrail is an input or output guardrail rejection.
	FailureCodeGuardrail = "guardrail"
	// FailureCodeTool is a terminal tool coordination/recovery failure.
	FailureCodeTool = "tool"
	// FailureCodeMaxIterations is loop iteration-limit exhaustion.
	FailureCodeMaxIterations = "max_iterations"
	// FailureCodeTokenBudget is cumulative token-budget exhaustion.
	FailureCodeTokenBudget = "token_budget"
	// FailureCodeRateLimit is a rate-limiter rejection.
	FailureCodeRateLimit = "rate_limit"
	// FailureCodeCanceled is caller cancellation or a caller-set ctx deadline.
	FailureCodeCanceled = "canceled"
	// FailureCodeInternal is any other non-success terminal error.
	FailureCodeInternal = "internal"
)

// maxFailureMessageLen bounds the durable failure message so a pathological
// error string cannot bloat the execution record.
const maxFailureMessageLen = 512

// setFailureReason records a bounded, inspectable reason for a terminal
// non-success status. It is a no-op for a nil error so a canceled-with-no-error
// path leaves the fields empty.
func setFailureReason(execution *Execution, runErr error) {
	if execution == nil || runErr == nil {
		return
	}
	execution.FailureCode = failureCodeFor(runErr)
	execution.FailureMessage = truncateFailureMessage(runErr.Error())
}

func failureCodeFor(err error) string {
	var (
		provErr  *ProviderError
		guardErr *GuardrailError
		toolErr  *ToolExecutionUncertainError
	)
	switch {
	case errors.As(err, &provErr):
		return FailureCodeProvider
	case errors.As(err, &guardErr):
		return FailureCodeGuardrail
	case errors.As(err, &toolErr), errors.Is(err, ErrExecutionRecoveryUnsupported), errors.Is(err, ErrToolExecutionUncertain):
		return FailureCodeTool
	case errors.Is(err, ErrMaxIterationsExceeded):
		return FailureCodeMaxIterations
	case errors.Is(err, ErrTokenBudgetExceeded):
		return FailureCodeTokenBudget
	case errors.Is(err, ErrRateLimitExceeded):
		return FailureCodeRateLimit
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return FailureCodeCanceled
	default:
		return FailureCodeInternal
	}
}

func truncateFailureMessage(msg string) string {
	if len(msg) <= maxFailureMessageLen {
		return msg
	}
	const ellipsis = "…"
	return msg[:maxFailureMessageLen-len(ellipsis)] + ellipsis
}

// ExecutionStore persists small durable execution state. Create is create-only;
// Save uses Version as a CAS token. Implementations must not store transcript
// messages or conversation snapshots.
type ExecutionStore interface {
	Create(ctx context.Context, execution Execution) (Execution, error)
	Load(ctx context.Context, executionID string) (Execution, error)
	Save(ctx context.Context, execution Execution, expectedVersion uint64) (Execution, error)
}

var (
	ErrExecutionNotFound = errors.New("execution not found")
	ErrExecutionConflict = errors.New("execution version conflict")
	ErrNoExecutionStore  = errors.New("no execution store configured")
)

func cloneExecution(in Execution) Execution {
	out := in
	if in.Pause != nil {
		pause := *in.Pause
		if in.Pause.Approval != nil {
			approval := *in.Pause.Approval
			approval.Calls = append([]ApprovalCall(nil), approval.Calls...)
			for i := range approval.Calls {
				approval.Calls[i].Input = cloneRaw(approval.Calls[i].Input)
			}
			pause.Approval = &approval
		}
		if in.Pause.Input != nil {
			input := *in.Pause.Input
			pause.Input = &input
		}
		out.Pause = &pause
	}
	if in.PendingPause != nil {
		out.PendingPause = cloneExecution(Execution{Pause: in.PendingPause}).Pause
	}
	if in.ToolBatch != nil {
		batch := *in.ToolBatch
		batch.Calls = append([]ToolExecution(nil), in.ToolBatch.Calls...)
		for i := range batch.Calls {
			batch.Calls[i].Input = cloneRaw(batch.Calls[i].Input)
			if batch.Calls[i].Approval != nil {
				approval := *batch.Calls[i].Approval
				batch.Calls[i].Approval = &approval
			}
		}
		if in.ToolBatch.Resolution != nil {
			resolution := *in.ToolBatch.Resolution
			batch.Resolution = &resolution
		}
		out.ToolBatch = &batch
	}
	return out
}

// toolExecutionKey is stable across retries and recovery attempts. The length-
// delimited preimage prevents distinct execution/call pairs from sharing an
// ambiguous concatenation. The versioned prefix makes keys safely namespaced
// for downstream idempotency stores.
func toolExecutionKey(executionID, callID string) string {
	preimage := fmt.Sprintf("%d:%s%d:%s", len(executionID), executionID, len(callID), callID)
	sum := sha256.Sum256([]byte(preimage))
	return "gude/tool/v1/" + hex.EncodeToString(sum[:])
}

// toolInputHash computes the stable identity of a JSON input. It rejects
// malformed or trailing JSON and normalizes object-key ordering while retaining
// json.Number lexical representation, so semantically equal object formatting
// does not change a durable tool identity.
func toolInputHash(input json.RawMessage) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", fmt.Errorf("canonical tool input: invalid JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return "", errors.New("canonical tool input: invalid trailing JSON")
		}
		return "", fmt.Errorf("canonical tool input: invalid trailing JSON: %w", err)
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("canonical tool input: encode JSON: %w", err)
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

func newExecutionID(r io.Reader) (string, error) {
	if r == nil {
		r = rand.Reader
	}
	var bytes [16]byte
	if _, err := io.ReadFull(r, bytes[:]); err != nil {
		return "", fmt.Errorf("generate execution ID: %w", err)
	}
	return hex.EncodeToString(bytes[:]), nil
}
