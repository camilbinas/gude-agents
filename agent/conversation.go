package agent

import (
	"context"
	"errors"
	"fmt"
)

// ConversationSnapshot is an immutable view of persisted conversation state.
// Revision is zero when the conversation does not exist.
type ConversationSnapshot struct {
	Messages []Message
	Revision uint64
}

// ConversationStore persists conversation history with compare-and-swap
// semantics. Save succeeds only when expectedRevision matches the current
// revision and returns the newly committed revision.
type ConversationStore interface {
	Load(ctx context.Context, conversationID string) (ConversationSnapshot, error)
	Save(ctx context.Context, conversationID string, messages []Message, expectedRevision uint64) (uint64, error)
}

// ConversationManager is a ConversationStore that can enumerate and delete
// conversations.
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

// ErrConversationConflict indicates that Save's expected revision was stale.
var ErrConversationConflict = errors.New("conversation revision conflict")

// ForkConversation copies a conversation's history to a new ID. The new ID
// must not already exist.
func ForkConversation(ctx context.Context, store ConversationStore, sourceID, newID string) error {
	snapshot, err := store.Load(ctx, sourceID)
	if err != nil {
		return err
	}
	if _, err := store.Save(ctx, newID, snapshot.Messages, 0); err != nil {
		return fmt.Errorf("fork conversation: %w", err)
	}
	return nil
}
