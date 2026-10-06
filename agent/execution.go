package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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

// Execution is durable runtime state for one invocation. Version is an
// ExecutionStore CAS version, distinct from the ConversationStore revision.
type Execution struct {
	ID string `json:"id"`

	ConversationID string `json:"conversation_id,omitempty"`
	Revision       uint64 `json:"revision"`
	LastSequence   uint64 `json:"last_sequence"`

	Status    ExecutionStatus `json:"status"`
	Phase     ExecutionPhase  `json:"phase"`
	Iteration int             `json:"iteration"`
	Pause     *ExecutionPause `json:"pause,omitempty"`
	Usage     TokenUsage      `json:"usage"`
	Version   uint64          `json:"version"`
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
	return out
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
