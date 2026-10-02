package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// ConversationCursor identifies a position in an append-only conversation.
// Revision counts successful append operations; LastSequence counts immutable
// messages. They intentionally advance independently.
type ConversationCursor struct {
	Revision     uint64
	LastSequence uint64
}

// ConversationSnapshot is an immutable view of canonical conversation
// history. Load returns the entire transcript; LoadAfter returns a contiguous
// suffix whose first message is after the requested sequence.
type ConversationSnapshot struct {
	Messages     []Message
	Revision     uint64
	LastSequence uint64
}

// ConversationStore persists the canonical, append-only event log.
//
// Messages are assigned contiguous, immutable sequences by the store starting
// at 1. Sequence numbers deliberately stay outside Message because they are
// storage metadata, not provider-facing content. Append is a revision-CAS:
// it atomically appends all supplied messages and advances Revision once, or
// returns ErrConversationConflict without changing history. An empty Append is
// a checked no-op: it requires the expected revision and returns the current
// cursor without creating a conversation or advancing either value.
type ConversationStore interface {
	// Load returns the complete canonical transcript. It is for export,
	// auditing, debugging and forking; normal model calls should use LoadAfter.
	Load(ctx context.Context, conversationID string) (ConversationSnapshot, error)
	// LoadAfter returns canonical messages with sequence > afterSequence and
	// always returns current Revision and LastSequence, including an empty tail.
	LoadAfter(ctx context.Context, conversationID string, afterSequence uint64) (ConversationSnapshot, error)
	// Append atomically appends one complete batch when expectedRevision matches.
	Append(ctx context.Context, conversationID string, messages []Message, expectedRevision uint64) (ConversationCursor, error)
}

// ContextStateSnapshot is one namespaced, derived context-state value. Its
// revision is independent from ConversationCursor.Revision.
type ContextStateSnapshot struct {
	Data     json.RawMessage
	Revision uint64
}

// ContextStateStore stores derived model-context state. It must not alter
// canonical transcript messages, conversation revision, last sequence, or
// canonical activity ordering. Values are namespaced by key so independent
// context strategies cannot collide.
type ContextStateStore interface {
	LoadContextState(ctx context.Context, conversationID, key string) (ContextStateSnapshot, error)
	SaveContextState(ctx context.Context, conversationID, key string, data json.RawMessage, expectedRevision uint64) (uint64, error)
}

// ConversationManager is a ConversationStore that can enumerate and delete
// conversations. Delete removes canonical history and any same-backend
// context-state data.
type ConversationManager interface {
	ConversationStore
	List(ctx context.Context) ([]string, error)
	Delete(ctx context.Context, conversationID string) error
}

// Flusher is implemented by stores that perform asynchronous persistence work.
// Flush waits until work accepted before the call has completed or ctx expires.
type Flusher interface {
	Flush(ctx context.Context) error
}

// ErrConversationConflict indicates Append's expected revision was stale.
var ErrConversationConflict = errors.New("conversation revision conflict")

// ErrContextStateConflict indicates a stale ContextStateStore revision.
var ErrContextStateConflict = errors.New("context state revision conflict")

// ForkConversation copies canonical history to a new conversation. Derived
// context state is intentionally not copied: it is a disposable projection and
// may encode a strategy unavailable to the destination Agent.
func ForkConversation(ctx context.Context, store ConversationStore, sourceID, newID string) error {
	snapshot, err := store.Load(ctx, sourceID)
	if err != nil {
		return err
	}
	if _, err := store.Append(ctx, newID, snapshot.Messages, 0); err != nil {
		return fmt.Errorf("fork conversation: %w", err)
	}
	return nil
}

// ContextManager builds a disposable provider-facing projection of canonical
// history. HistoryBoundary is called before LoadAfter so managers can prevent
// a whole-transcript read. A manager must never mutate or persist Message
// values as canonical history; derived state belongs in ContextStateStore.
type ContextManager interface {
	// HistoryBoundary returns the greatest canonical sequence already
	// represented by durable derived context. Zero requires a read from start.
	HistoryBoundary(ctx context.Context, conversationID string) (uint64, error)
	// Prepare renders provider-facing history from the recent canonical suffix
	// and current unsaved canonical messages. Returned Messages are model-only.
	Prepare(ctx context.Context, input ContextManagerInput) (ContextManagerOutput, error)
}

// ContextManagerInput carries only the information required to project model
// context. Recent messages start immediately after Boundary; Current are the
// genuine, uncommitted messages from this invocation.
type ContextManagerInput struct {
	ConversationID string
	Boundary       uint64
	Revision       uint64
	LastSequence   uint64
	Recent         []Message
	Current        []Message
	Transient      []Message // RAG or other model-only content; never summarized/persisted
	System         string
	Tools          []tool.Spec
}

// ContextManagerOutput is a model-only projection. Messages in it must never
// be sent to ConversationStore.Append.
type ContextManagerOutput struct{ Messages []Message }
