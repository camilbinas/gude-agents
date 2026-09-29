package conversation

import (
	"context"
	"errors"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/testutil"
	"pgregory.net/rapid"
)

// Thin wrappers around testutil generators to keep existing call sites unchanged.
func genContentBlock(t *rapid.T) agent.ContentBlock  { return testutil.GenContentBlock(t) }
func genMessage(t *rapid.T) agent.Message            { return testutil.GenMessage(t) }
func genMessages(t *rapid.T) []agent.Message         { return testutil.GenMessages(t, 100) }
func genMessagesWithText(t *rapid.T) []agent.Message { return testutil.GenMessagesWithText(t, 100) }

func saveLatest(ctx context.Context, store agent.ConversationStore, id string, messages []agent.Message) error {
	for {
		snapshot, err := store.Load(ctx, id)
		if err != nil {
			return err
		}
		_, err = store.Save(ctx, id, messages, snapshot.Revision)
		if !errors.Is(err, agent.ErrConversationConflict) {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}

func loadMessages(ctx context.Context, store agent.ConversationStore, id string) ([]agent.Message, error) {
	snapshot, err := store.Load(ctx, id)
	return snapshot.Messages, err
}
