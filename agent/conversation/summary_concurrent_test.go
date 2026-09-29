package conversation

import (
	"context"
	"errors"
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
	if err := saveLatest(ctx, s, "conv-A", makeMessages(8)); err != nil {
		t.Fatal(err)
	}

	// Save 8 messages to conv-B — should ALSO trigger summarization
	// (not be blocked by conv-A's in-progress summarization).
	if err := saveLatest(ctx, s, "conv-B", makeMessages(8)); err != nil {
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
	revision, err := s.Save(ctx, "conv-X", makeMessages(8), 0)
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first summarization did not start")
	}

	// Second save for the SAME conversation while first is in progress — should skip.
	if _, err := s.Save(ctx, "conv-X", makeMessages(10), revision); err != nil {
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

type summaryRaceStore struct {
	inner *InMemory
	mu    sync.Mutex
	saves int
}

func (s *summaryRaceStore) Load(ctx context.Context, id string) (agent.ConversationSnapshot, error) {
	return s.inner.Load(ctx, id)
}

func (s *summaryRaceStore) Save(ctx context.Context, id string, messages []agent.Message, expectedRevision uint64) (uint64, error) {
	s.mu.Lock()
	s.saves++
	inject := s.saves == 2
	s.mu.Unlock()
	if inject {
		latest, err := s.inner.Load(ctx, id)
		if err != nil {
			return 0, err
		}
		newest := append(append([]agent.Message(nil), latest.Messages...), agent.Message{
			Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "newest turn"}},
		})
		if _, err := s.inner.Save(ctx, id, newest, latest.Revision); err != nil {
			return 0, err
		}
	}
	return s.inner.Save(ctx, id, messages, expectedRevision)
}

func TestSummaryFinalCASRacePreservesNewestMessage(t *testing.T) {
	store := &summaryRaceStore{inner: NewInMemory()}
	summary, err := NewSummary(store, 1, func(context.Context, []agent.Message) ([2]agent.Message, error) {
		return [2]agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "summary"}}},
			{Role: agent.RoleAssistant, Content: []agent.ContentBlock{agent.TextBlock{Text: "ack"}}},
		}, nil
	}, WithPreserveRecentMessages(1))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := summary.Save(ctx, "conv", makeMessages(4), 0); err != nil {
		t.Fatal(err)
	}
	if err := summary.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Load(ctx, "conv")
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range snapshot.Messages {
		for _, block := range message.Content {
			if text, ok := block.(agent.TextBlock); ok && text.Text == "newest turn" {
				return
			}
		}
	}
	t.Fatalf("newest message lost after summary CAS retry: %#v", snapshot.Messages)
}

func TestTokenSummaryFinalCASRacePreservesNewestMessage(t *testing.T) {
	store := &summaryRaceStore{inner: NewInMemory()}
	summary, err := NewTokenSummary(store, 1, func(context.Context, []agent.Message) ([2]agent.Message, error) {
		return [2]agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "summary"}}},
			{Role: agent.RoleAssistant, Content: []agent.ContentBlock{agent.TextBlock{Text: "ack"}}},
		}, nil
	}, WithTokenPreserveRecentMessages(1))
	if err != nil {
		t.Fatal(err)
	}
	ctx := agent.WithTokenUsage(context.Background(), agent.TokenUsage{InputTokens: 100})
	if _, err := summary.Save(ctx, "conv", makeMessages(4), 0); err != nil {
		t.Fatal(err)
	}
	if err := summary.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Load(ctx, "conv")
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range snapshot.Messages {
		for _, block := range message.Content {
			if text, ok := block.(agent.TextBlock); ok && text.Text == "newest turn" {
				return
			}
		}
	}
	t.Fatalf("newest message lost after token summary CAS retry: %#v", snapshot.Messages)
}

type persistentSummaryConflictStore struct {
	inner *InMemory
	mu    sync.Mutex
	saves int
}

func (s *persistentSummaryConflictStore) Load(ctx context.Context, id string) (agent.ConversationSnapshot, error) {
	return s.inner.Load(ctx, id)
}

func (s *persistentSummaryConflictStore) Save(ctx context.Context, id string, messages []agent.Message, expectedRevision uint64) (uint64, error) {
	s.mu.Lock()
	s.saves++
	save := s.saves
	s.mu.Unlock()
	if save > 1 {
		return 0, agent.ErrConversationConflict
	}
	return s.inner.Save(ctx, id, messages, expectedRevision)
}

func TestSummaryFlushReportsPersistentCASConflict(t *testing.T) {
	store := &persistentSummaryConflictStore{inner: NewInMemory()}
	summary, err := NewSummary(store, 1, func(context.Context, []agent.Message) ([2]agent.Message, error) {
		return [2]agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "summary"}}},
			{Role: agent.RoleAssistant, Content: []agent.ContentBlock{agent.TextBlock{Text: "ack"}}},
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := summary.Save(context.Background(), "conv", makeMessages(2), 0); err != nil {
		t.Fatal(err)
	}
	if err := summary.Flush(context.Background()); !errors.Is(err, agent.ErrConversationConflict) {
		t.Fatalf("Flush error = %v, want ErrConversationConflict", err)
	}
}

type summaryBarrierStore interface {
	agent.ConversationStore
	agent.Flusher
}

type summaryWrapperFactory struct {
	name string
	new  func(t *testing.T, store agent.ConversationStore, fn SummaryFunc) (summaryBarrierStore, context.Context)
}

func summaryWrapperFactories() []summaryWrapperFactory {
	return []summaryWrapperFactory{
		{
			name: "Summary",
			new: func(t *testing.T, store agent.ConversationStore, fn SummaryFunc) (summaryBarrierStore, context.Context) {
				t.Helper()
				summary, err := NewSummary(store, 1, fn)
				if err != nil {
					t.Fatal(err)
				}
				return summary, context.Background()
			},
		},
		{
			name: "TokenSummary",
			new: func(t *testing.T, store agent.ConversationStore, fn SummaryFunc) (summaryBarrierStore, context.Context) {
				t.Helper()
				summary, err := NewTokenSummary(store, 1, fn)
				if err != nil {
					t.Fatal(err)
				}
				ctx := agent.WithTokenUsage(context.Background(), agent.TokenUsage{InputTokens: 100})
				return summary, ctx
			},
		},
	}
}

type observeDoneContext struct {
	context.Context
	once     sync.Once
	observed chan struct{}
}

func newObserveDoneContext(ctx context.Context) *observeDoneContext {
	return &observeDoneContext{Context: ctx, observed: make(chan struct{})}
}

func (c *observeDoneContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.observed) })
	return c.Context.Done()
}

func testSummaryPair() [2]agent.Message {
	return [2]agent.Message{
		{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "summary"}}},
		{Role: agent.RoleAssistant, Content: []agent.ContentBlock{agent.TextBlock{Text: "ack"}}},
	}
}

func TestSummaryWrappersLoadWaitsForAcceptedSameIDWork(t *testing.T) {
	for _, factory := range summaryWrapperFactories() {
		t.Run(factory.name, func(t *testing.T) {
			inner := NewInMemory()
			started := make(chan struct{})
			release := make(chan struct{})
			wrapper, saveCtx := factory.new(t, inner, func(context.Context, []agent.Message) ([2]agent.Message, error) {
				close(started)
				<-release
				return testSummaryPair(), nil
			})

			foregroundRevision, err := wrapper.Save(saveCtx, "blocked", makeMessages(2), 0)
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("summary did not start")
			}

			differentResult := make(chan error, 1)
			go func() {
				_, loadErr := wrapper.Load(context.Background(), "independent")
				differentResult <- loadErr
			}()
			select {
			case err := <-differentResult:
				if err != nil {
					t.Fatalf("different-ID Load failed: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("different-ID Load was blocked by unrelated summary work")
			}

			loadCtx := newObserveDoneContext(context.Background())
			loaded := make(chan agent.ConversationSnapshot, 1)
			loadErr := make(chan error, 1)
			go func() {
				snapshot, err := wrapper.Load(loadCtx, "blocked")
				if err != nil {
					loadErr <- err
					return
				}
				loaded <- snapshot
			}()
			select {
			case <-loadCtx.observed:
			case <-time.After(2 * time.Second):
				t.Fatal("same-ID Load did not enter the accepted-work barrier")
			}
			select {
			case snapshot := <-loaded:
				t.Fatalf("same-ID Load returned before summary completed: revision %d", snapshot.Revision)
			case err := <-loadErr:
				t.Fatalf("same-ID Load failed before summary completed: %v", err)
			default:
			}

			close(release)
			select {
			case snapshot := <-loaded:
				if snapshot.Revision <= foregroundRevision {
					t.Fatalf("Load revision = %d, want newer than foreground revision %d", snapshot.Revision, foregroundRevision)
				}
				if len(snapshot.Messages) != 2 {
					t.Fatalf("Load returned %d messages, want summarized pair", len(snapshot.Messages))
				}
			case err := <-loadErr:
				t.Fatalf("same-ID Load failed: %v", err)
			case <-time.After(2 * time.Second):
				t.Fatal("same-ID Load did not unblock after summary completed")
			}
			if err := wrapper.Flush(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSummaryWrappersLoadCancellationUnblocks(t *testing.T) {
	for _, factory := range summaryWrapperFactories() {
		t.Run(factory.name, func(t *testing.T) {
			started := make(chan struct{})
			release := make(chan struct{})
			wrapper, saveCtx := factory.new(t, NewInMemory(), func(context.Context, []agent.Message) ([2]agent.Message, error) {
				close(started)
				<-release
				return testSummaryPair(), nil
			})
			if _, err := wrapper.Save(saveCtx, "conv", makeMessages(2), 0); err != nil {
				t.Fatal(err)
			}
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("summary did not start")
			}

			baseCtx, cancel := context.WithCancel(context.Background())
			loadCtx := newObserveDoneContext(baseCtx)
			result := make(chan error, 1)
			go func() {
				_, err := wrapper.Load(loadCtx, "conv")
				result <- err
			}()
			select {
			case <-loadCtx.observed:
			case <-time.After(2 * time.Second):
				t.Fatal("Load did not enter the accepted-work barrier")
			}
			cancel()
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("Load error = %v, want context.Canceled", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("cancellation did not unblock Load")
			}

			close(release)
			if err := wrapper.Flush(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSummaryWrappersSkipUnresolvedToolCalls(t *testing.T) {
	toolUse := func(id string) agent.Message {
		return agent.Message{
			Role: agent.RoleAssistant,
			Content: []agent.ContentBlock{agent.ToolUseBlock{
				ToolUseID: id,
				Name:      "test-tool",
			}},
		}
	}
	toolResult := func(id string) agent.Message {
		return agent.Message{
			Role: agent.RoleUser,
			Content: []agent.ContentBlock{agent.ToolResultBlock{
				ToolUseID: id,
				Content:   "done",
			}},
		}
	}

	tests := []struct {
		name          string
		messages      []agent.Message
		wantSummarize bool
	}{
		{
			name: "unresolved",
			messages: []agent.Message{
				toolUse("pending-tool"),
				{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "awaiting approval"}}},
			},
		},
		{
			name: "completed",
			messages: []agent.Message{
				toolUse("completed-tool"),
				toolResult("completed-tool"),
			},
			wantSummarize: true,
		},
		{
			name: "reused ID with later unresolved use",
			messages: []agent.Message{
				toolUse("reused-tool"),
				toolResult("reused-tool"),
				toolUse("reused-tool"),
			},
		},
		{
			name: "result before use",
			messages: []agent.Message{
				toolResult("ordered-tool"),
				toolUse("ordered-tool"),
			},
		},
		{
			name: "two uses one result",
			messages: []agent.Message{
				toolUse("duplicate-tool"),
				toolUse("duplicate-tool"),
				toolResult("duplicate-tool"),
			},
		},
		{
			name: "fully matched repeated IDs",
			messages: []agent.Message{
				toolUse("repeated-tool"),
				toolResult("repeated-tool"),
				toolUse("repeated-tool"),
				toolResult("repeated-tool"),
			},
			wantSummarize: true,
		},
	}

	for _, factory := range summaryWrapperFactories() {
		t.Run(factory.name, func(t *testing.T) {
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					called := make(chan struct{}, 2)
					wrapper, saveCtx := factory.new(t, NewInMemory(), func(context.Context, []agent.Message) ([2]agent.Message, error) {
						called <- struct{}{}
						return testSummaryPair(), nil
					})

					if _, err := wrapper.Save(saveCtx, "conv", tt.messages, 0); err != nil {
						t.Fatal(err)
					}
					if err := wrapper.Flush(context.Background()); err != nil {
						t.Fatal(err)
					}
					select {
					case <-called:
						if !tt.wantSummarize {
							t.Fatal("SummaryFunc invoked with an unresolved tool call")
						}
					default:
						if tt.wantSummarize {
							t.Fatal("SummaryFunc was not invoked with all tool calls resolved")
						}
					}
				})
			}
		})
	}
}

func TestSummaryLoadBarrierSpansImmediateRetriggerChain(t *testing.T) {
	inner := NewInMemory()
	firstStarted := make(chan struct{})
	secondStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	releaseSecond := make(chan struct{})
	var mu sync.Mutex
	calls := 0

	summary, err := NewSummary(inner, 1, func(context.Context, []agent.Message) ([2]agent.Message, error) {
		mu.Lock()
		calls++
		call := calls
		mu.Unlock()
		switch call {
		case 1:
			close(firstStarted)
			<-releaseFirst
		case 2:
			close(secondStarted)
			<-releaseSecond
		}
		return testSummaryPair(), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	foregroundRevision, err := summary.Save(context.Background(), "conv", makeMessages(2), 0)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first summary did not start")
	}
	if _, err := summary.Save(context.Background(), "conv", makeMessages(4), foregroundRevision); err != nil {
		t.Fatal(err)
	}

	loadCtx := newObserveDoneContext(context.Background())
	loaded := make(chan agent.ConversationSnapshot, 1)
	go func() {
		snapshot, _ := summary.Load(loadCtx, "conv")
		loaded <- snapshot
	}()
	select {
	case <-loadCtx.observed:
	case <-time.After(2 * time.Second):
		t.Fatal("Load did not enter the accepted-work barrier")
	}

	close(releaseFirst)
	select {
	case <-secondStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("immediate re-trigger did not start")
	}
	select {
	case snapshot := <-loaded:
		t.Fatalf("Load escaped between chained summaries at revision %d", snapshot.Revision)
	default:
	}

	close(releaseSecond)
	select {
	case snapshot := <-loaded:
		if len(snapshot.Messages) != 2 {
			t.Fatalf("Load returned %d messages, want final summarized pair", len(snapshot.Messages))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Load did not unblock after re-trigger chain completed")
	}
	if err := summary.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
}
