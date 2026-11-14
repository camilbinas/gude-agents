package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

// snapshotFormatVersion is the on-wire version of a Snapshot blob. Bump it only
// for an incompatible layout change; Restore rejects an unknown version.
const snapshotFormatVersion = 1

// ErrSnapshotFormat is returned by Restore when a blob is empty, is not a
// snapshot, or carries an unsupported format version.
var ErrSnapshotFormat = errors.New("agent: unrecognized or unsupported snapshot format")

// ErrSnapshotIntegrity is returned by Restore when a blob is internally
// inconsistent: its execution is not a resumable pause, its cursor disagrees
// with the bundled transcript, or its checksum does not match.
var ErrSnapshotIntegrity = errors.New("agent: snapshot integrity check failed")

// ErrSnapshotStoreRequired is returned by Snapshot or Restore when the Runtime
// has no ConversationStore or no ExecutionStore.
var ErrSnapshotStoreRequired = errors.New("agent: Snapshot/Restore require both a ConversationStore and an ExecutionStore")

// snapshotBlob is the self-describing envelope serialized by Snapshot. It
// bundles the paused Execution record with the canonical conversation messages
// its cursor points at, so a pause can move between processes as one value.
type snapshotBlob struct {
	Format    int           `json:"format"`
	Execution Execution     `json:"execution"`
	Messages  []jsonMessage `json:"messages"`
	Checksum  string        `json:"checksum"`
}

// Snapshot serializes a paused run as one portable, versioned blob. The blob
// bundles the durable Execution record with the canonical transcript its cursor
// references, so a small setup can pause in one process and resume in another
// without sharing a ConversationStore and an ExecutionStore. Restore loads it
// back; the usual Resume then continues the run.
//
// in must be a pause projected from this Runtime (LoadInterrupt, or the
// Interrupt returned by Invoke/Resume). Snapshot does not mutate any store.
func (a *Runtime) Snapshot(ctx context.Context, in *Interrupt) ([]byte, error) {
	if in == nil {
		return nil, fmt.Errorf("snapshot: nil interrupt")
	}
	if a.conversation == nil || a.executionStore == nil {
		return nil, ErrSnapshotStoreRequired
	}
	conversation, executions := a.conversation, a.executionStore
	if in.ExecutionID == "" {
		return nil, fmt.Errorf("snapshot: interrupt has no execution ID")
	}
	execution, err := executions.Load(ctx, in.ExecutionID)
	if err != nil {
		return nil, fmt.Errorf("snapshot: load execution: %w", err)
	}
	if execution.Status != ExecutionPaused || execution.Pause == nil {
		return nil, fmt.Errorf("snapshot %q: %w", in.ExecutionID, ErrInterruptNotFound)
	}
	if execution.Version != in.ExecutionVersion {
		return nil, fmt.Errorf("snapshot %q: %w", in.ExecutionID, ErrExecutionConflict)
	}
	if execution.ConversationID == "" {
		return nil, fmt.Errorf("snapshot %q: paused execution has no conversation", in.ExecutionID)
	}
	snap, err := conversation.Load(ctx, execution.ConversationID)
	if err != nil {
		return nil, fmt.Errorf("snapshot: load conversation: %w", err)
	}
	// Revision is authoritative. LastSequence is compared only when both sides
	// report it, matching Resume's tolerance for stores whose Load predates
	// sequence metadata.
	if snap.Revision != execution.Revision || (snap.LastSequence != 0 && execution.LastSequence != 0 && snap.LastSequence != execution.LastSequence) {
		return nil, fmt.Errorf("snapshot %q: transcript (%d,%d) disagrees with pause cursor (%d,%d): %w",
			execution.ID, snap.Revision, snap.LastSequence, execution.Revision, execution.LastSequence, ErrConversationConflict)
	}
	messages := marshalMessages(snap.Messages)
	// Version is a destination store concern, never carried in the blob.
	execution.Version = 0
	blob := snapshotBlob{Format: snapshotFormatVersion, Execution: execution, Messages: messages}
	blob.Checksum = snapshotChecksum(blob)
	out, err := json.Marshal(blob)
	if err != nil {
		return nil, fmt.Errorf("snapshot: marshal: %w", err)
	}
	return out, nil
}

// Restore loads a Snapshot blob into this Runtime's pause stores and returns
// the paused Interrupt, ready for Resume. It create-only writes the bundled
// conversation and execution: a duplicate conversation or execution ID returns
// a conflict rather than overwriting, so Restore never weakens the CAS
// guarantees of a real store.
//
// The destination stores need not be the ones that produced the blob; in-memory
// stores (conversation.NewInMemory, execution.NewMemory) are enough.
func (a *Runtime) Restore(ctx context.Context, blob []byte) (*Interrupt, error) {
	if a.conversation == nil || a.executionStore == nil {
		return nil, ErrSnapshotStoreRequired
	}
	conversation, executions := a.conversation, a.executionStore
	if len(blob) == 0 {
		return nil, fmt.Errorf("restore: empty blob: %w", ErrSnapshotFormat)
	}
	var decoded snapshotBlob
	if err := json.Unmarshal(blob, &decoded); err != nil {
		return nil, fmt.Errorf("restore: %w: %v", ErrSnapshotFormat, err)
	}
	if decoded.Format != snapshotFormatVersion {
		return nil, fmt.Errorf("restore: format %d unsupported (want %d): %w", decoded.Format, snapshotFormatVersion, ErrSnapshotFormat)
	}
	execution := decoded.Execution
	execution.Version = 0
	check := snapshotBlob{Format: decoded.Format, Execution: execution, Messages: decoded.Messages}
	if snapshotChecksum(check) != decoded.Checksum {
		return nil, fmt.Errorf("restore: %w: checksum mismatch", ErrSnapshotIntegrity)
	}
	if execution.ID == "" || execution.ConversationID == "" {
		return nil, fmt.Errorf("restore: %w: missing execution or conversation ID", ErrSnapshotIntegrity)
	}
	if execution.Status != ExecutionPaused || execution.Pause == nil {
		return nil, fmt.Errorf("restore: %w: execution is not a resumable pause", ErrSnapshotIntegrity)
	}
	messages, err := unmarshalMessages(decoded.Messages)
	if err != nil {
		return nil, fmt.Errorf("restore: %w: %v", ErrSnapshotIntegrity, err)
	}
	// The transcript is written as one atomic batch into an absent conversation.
	// The resulting cursor becomes the execution's pause cursor, so a later
	// Resume's conflict check matches regardless of the store's revision scheme.
	cursor, err := conversation.Append(ctx, execution.ConversationID, messages, 0)
	if errors.Is(err, ErrConversationConflict) {
		// The conversation already exists (a shared store, or a retried restore).
		// Accept it only when its transcript already equals the bundled one, so a
		// divergent conversation is rejected rather than silently merged.
		existing, lerr := conversation.Load(ctx, execution.ConversationID)
		if lerr != nil {
			return nil, fmt.Errorf("restore: load existing conversation %q: %w", execution.ConversationID, lerr)
		}
		if !sameTranscript(existing.Messages, decoded.Messages) {
			return nil, fmt.Errorf("restore: conversation %q already exists with different history: %w", execution.ConversationID, ErrConversationConflict)
		}
		cursor = ConversationCursor{Revision: existing.Revision, LastSequence: existing.LastSequence}
	} else if err != nil {
		return nil, fmt.Errorf("restore: seed conversation %q: %w", execution.ConversationID, err)
	}
	execution.Revision = cursor.Revision
	execution.LastSequence = cursor.LastSequence
	if execution.StartCursorPersisted {
		// Keep a start cursor bounded by the restored transcript.
		if execution.StartRevision > cursor.Revision {
			execution.StartRevision = cursor.Revision
		}
		if execution.StartLastSequence > cursor.LastSequence {
			execution.StartLastSequence = cursor.LastSequence
		}
	}
	created, err := executions.Create(ctx, execution)
	if err != nil {
		return nil, fmt.Errorf("restore: create execution %q: %w", execution.ID, err)
	}
	return interruptFromExecution(created), nil
}

// sameTranscript reports whether an existing transcript equals the bundled one
// by their canonical JSON envelopes.
func sameTranscript(existing []Message, want []jsonMessage) bool {
	a, err := json.Marshal(marshalMessages(existing))
	if err != nil {
		return false
	}
	b, err := json.Marshal(want)
	if err != nil {
		return false
	}
	return string(a) == string(b)
}

// snapshotChecksum binds the format, execution and messages so a tampered or
// torn blob is rejected. Execution.Version is already zeroed by the caller.
func snapshotChecksum(blob snapshotBlob) string {
	payload := struct {
		Format    int           `json:"format"`
		Execution Execution     `json:"execution"`
		Messages  []jsonMessage `json:"messages"`
	}{blob.Format, blob.Execution, blob.Messages}
	data, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
