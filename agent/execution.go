package agent

import (
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
// incomplete tool boundary. No completed historical calls are retained.
type ToolExecutionStatus string

const (
	ToolExecutionPlanned  ToolExecutionStatus = "planned"
	ToolExecutionReady    ToolExecutionStatus = "ready"
	ToolExecutionInFlight ToolExecutionStatus = "in_flight"
)

// ToolExecution stores the minimal intent necessary to reconcile a call after
// a crash. Input is cleared after canonical ToolUse is verified; results stay
// exclusively in ConversationStore.
type ToolExecution struct {
	CallID         string              `json:"call_id"`
	Name           string              `json:"name"`
	Status         ToolExecutionStatus `json:"status"`
	IdempotencyKey string              `json:"idempotency_key"`
	InputHash      string              `json:"input_hash"`
	Input          json.RawMessage     `json:"input,omitempty"`
	ReplaySafe     bool                `json:"replay_safe"`
}

// ToolBatchExecution is the single active recovery boundary. It is cleared as
// soon as all result blocks become canonical conversation history.
type ToolBatchExecution struct {
	Iteration           int             `json:"iteration"`
	Calls               []ToolExecution `json:"calls"`
	ToolUseRevision     uint64          `json:"tool_use_revision"`
	ToolUseLastSequence uint64          `json:"tool_use_last_sequence"`
}

// ToolResolutionOutcome is an application-confirmed outcome for an ambiguous
// unsafe call.
type ToolResolutionOutcome string

const (
	ToolResolutionSucceeded ToolResolutionOutcome = "succeeded"
	ToolResolutionFailed    ToolResolutionOutcome = "failed"
	ToolResolutionRetry     ToolResolutionOutcome = "retry"
)

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

var ErrExecutionRecoveryUnsupported = errors.New("execution recovery state unsupported")

// Execution is durable runtime state for one invocation. Version is an
// ExecutionStore CAS version, distinct from the ConversationStore revision.
type Execution struct {
	ID string `json:"id"`

	ConversationID string `json:"conversation_id,omitempty"`
	Revision       uint64 `json:"revision"`
	LastSequence   uint64 `json:"last_sequence"`

	Status    ExecutionStatus     `json:"status"`
	Phase     ExecutionPhase      `json:"phase"`
	Iteration int                 `json:"iteration"`
	Pause     *ExecutionPause     `json:"pause,omitempty"`
	ToolBatch *ToolBatchExecution `json:"tool_batch,omitempty"`
	Usage     TokenUsage          `json:"usage"`
	Version   uint64              `json:"version"`
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
	if in.ToolBatch != nil {
		batch := *in.ToolBatch
		batch.Calls = append([]ToolExecution(nil), in.ToolBatch.Calls...)
		for i := range batch.Calls {
			batch.Calls[i].Input = cloneRaw(batch.Calls[i].Input)
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

func toolInputHash(input json.RawMessage) string {
	sum := sha256.Sum256(input)
	return hex.EncodeToString(sum[:])
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
