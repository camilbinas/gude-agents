package conversation

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
)

// TestSummary_IndependentConversationsCanSummarizeConcurrently verifies that
// summarization of one conversation does not block summarization of another.
// This was a bug: a single global `summarizing` bool blocked all conversations.
func TestSummary_IndependentConversationsCanSummarizeConcurrently(t *testing.T) {
	store := NewInMemory()
	ctx := context.Background()

	var mu sync.Mutex
	calledFor := map[string]bool{}
	bothStarted := make(chan struct{})
	startCount := 0
	allowFinish := make(chan struct{})

	fn := func(_ context.Context, msgs []agent.Message) ([2]agent.Message, error) {
		// Figure out which conversation this is by checking the store.
		// We use the message count as a proxy.
		convID := "unknown"
		mu.Lock()
		if len(msgs) == 8 {
			if !calledFor["conv-A"] {
				convID = "conv-A"
			} else {
				convID = "conv-B"
			}
		}
		calledFor[convID] = true
		startCount++
		if startCount >= 2 {
			close(bothStarted)
		}
		mu.Unlock()

		<-allowFinish
		return [2]agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "Here is a summary of our previous conversation: summary of " + convID}}},
			{Role: agent.RoleAssistant, Content: []agent.ContentBlock{agent.TextBlock{Text: "Understood. I will use this context to continue our conversation."}}},
		}, nil
	}

	s, err := NewSummary(store, 5, fn)
	if err != nil {
		t.Fatal(err)
	}

	// Save 8 messages to conv-A — triggers summarization.
	if err := s.Save(ctx, "conv-A", makeMessages(8)); err != nil {
		t.Fatal(err)
	}

	// Save 8 messages to conv-B — should ALSO trigger summarization
	// (not be blocked by conv-A's in-progress summarization).
	if err := s.Save(ctx, "conv-B", makeMessages(8)); err != nil {
		t.Fatal(err)
	}

	// Both should start concurrently.
	select {
	case <-bothStarted:
		// Both conversations triggered summarization concurrently.
	case <-time.After(2 * time.Second):
		mu.Lock()
		t.Fatalf("expected both conversations to summarize concurrently, only %d started", startCount)
		mu.Unlock()
	}

	close(allowFinish)

	// Give goroutines time to finish.
	time.Sleep(100 * time.Millisecond)
}

// TestSummary_SameConversationStillSkipsDuplicate verifies that the per-conversation
// lock still prevents duplicate summarization of the SAME conversation.
func TestSummary_SameConversationStillSkipsDuplicate(t *testing.T) {
	store := NewInMemory()
	ctx := context.Background()

	callCount := 0
	var mu sync.Mutex
	firstStarted := make(chan struct{})
	allowFinish := make(chan struct{})

	fn := func(_ context.Context, msgs []agent.Message) ([2]agent.Message, error) {
		mu.Lock()
		callCount++
		count := callCount
		mu.Unlock()

		if count == 1 {
			close(firstStarted)
			<-allowFinish
		}
		return [2]agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "Here is a summary of our previous conversation: summary"}}},
			{Role: agent.RoleAssistant, Content: []agent.ContentBlock{agent.TextBlock{Text: "Understood. I will use this context to continue our conversation."}}},
		}, nil
	}

	s, err := NewSummary(store, 5, fn)
	if err != nil {
		t.Fatal(err)
	}

	// First save triggers summarization for conv-X.
	if err := s.Save(ctx, "conv-X", makeMessages(8)); err != nil {
		t.Fatal(err)
	}

	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first summarization did not start")
	}

	// Second save for the SAME conversation while first is in progress — should skip.
	if err := s.Save(ctx, "conv-X", makeMessages(10)); err != nil {
		t.Fatal(err)
	}

	close(allowFinish)
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if callCount != 1 {
		t.Fatalf("expected SummaryFunc called once for same conversation, got %d", callCount)
	}
}
