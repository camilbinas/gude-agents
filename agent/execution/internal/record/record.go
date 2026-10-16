// Package record holds the serialization, validation, and error mapping shared
// by every agent.ExecutionStore implementation in the agent/execution family.
//
// It is implementation sharing only. It is internal on purpose: the public
// contract for resumable execution persistence is agent.ExecutionStore, and
// this package must never become a second one.
package record

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/camilbinas/gude-agents/agent"
)

// ValidateID rejects an empty execution ID.
func ValidateID(id string) error {
	if id == "" {
		return errors.New("execution: execution ID is required")
	}
	return nil
}

// Marshal serializes an execution for storage. The backend owns Version, so it
// is never part of the stored payload: a caller-supplied value can neither be
// trusted nor persisted. The payload carries runtime state and the conversation
// cursor only; transcript ownership remains with agent.ConversationStore.
func Marshal(execution agent.Execution) ([]byte, error) {
	execution.Version = 0
	data, err := json.Marshal(execution)
	if err != nil {
		return nil, fmt.Errorf("execution: marshal execution: %w", err)
	}
	return data, nil
}

// Unmarshal decodes a stored payload and stamps it with the backend's durable
// version. It verifies that the payload belongs to wantID so a misplaced record
// can never be returned for a different execution.
func Unmarshal(data []byte, wantID string, version uint64) (agent.Execution, error) {
	var execution agent.Execution
	if err := json.Unmarshal(data, &execution); err != nil {
		return agent.Execution{}, fmt.Errorf("execution: unmarshal execution: %w", err)
	}
	if execution.ID != wantID {
		return agent.Execution{}, fmt.Errorf("execution: stored execution ID %q does not match requested ID %q", execution.ID, wantID)
	}
	execution.Version = version
	return execution, nil
}

// NotFound reports a missing execution as agent.ErrExecutionNotFound.
func NotFound(id string) error {
	return fmt.Errorf("execution: %q: %w", id, agent.ErrExecutionNotFound)
}

// Conflict reports a failed create or compare-and-swap as
// agent.ErrExecutionConflict. A missing record on Save is a conflict, not a
// not-found, because Save's contract is "this exact version is current".
func Conflict(operation, id string) error {
	return fmt.Errorf("execution: %s %q: %w", operation, id, agent.ErrExecutionConflict)
}

// Wrap adds the operation to a backend failure without classifying it.
func Wrap(operation string, err error) error {
	return fmt.Errorf("execution: %s: %w", operation, err)
}

// Version converts a stored integer version to the contract's uint64, rejecting
// values no backend should ever have produced.
func Version(n int64) (uint64, error) {
	if n <= 0 {
		return 0, fmt.Errorf("execution: stored version %d is not positive", n)
	}
	return uint64(n), nil
}

// CheckExpected validates the expectedVersion argument of Save. Versions start
// at 1, so zero can never be current, and values beyond int64 cannot be stored
// by any backend.
func CheckExpected(id string, expected uint64) error {
	if expected == 0 || expected > uint64(1<<63-1) {
		return Conflict("save", id)
	}
	return nil
}
