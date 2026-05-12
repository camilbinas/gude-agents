package agent

import "context"

// Conversation persists conversation history across invocations.
type Conversation interface {
	// Load retrieves messages for the given conversation ID.
	Load(ctx context.Context, conversationID string) ([]Message, error)

	// Save persists messages for the given conversation ID.
	Save(ctx context.Context, conversationID string, messages []Message) error

	// List returns all conversation IDs in the store.
	List(ctx context.Context) ([]string, error)

	// Delete removes a conversation by ID. Returns nil if not found.
	Delete(ctx context.Context, conversationID string) error
}

// ConversationWaiter is an optional interface that Conversation implementations
// can satisfy to signal that they perform background work after Save. When the
// agent option WithSyncConversation is set, the agent calls Wait after
// each Save, blocking until all background work (e.g. summarization) is complete.
type ConversationWaiter interface {
	Wait()
}

// ForkConversation copies a conversation's history to a new ID, creating an
// independent branch. Both conversations continue independently after the fork.
func ForkConversation(ctx context.Context, store Conversation, sourceID, newID string) error {
	msgs, err := store.Load(ctx, sourceID)
	if err != nil {
		return err
	}
	return store.Save(ctx, newID, msgs)
}
