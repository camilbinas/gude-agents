package conversation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
)

// helper: create N simple user messages
func makeMessages(n int) []agent.Message {
	msgs := make([]agent.Message, n)
	for i := range n {
		msgs[i] = agent.Message{
			Role:    agent.RoleUser,
			Content: []agent.ContentBlock{agent.TextBlock{Text: "msg"}},
		}
	}
	return msgs
}

// TestSummary_TriggersAt80Percent verifies that summarization is triggered
// when the message count reaches 80% of the threshold.
//
func TestSummary_TriggersAt80Percent(t *testing.T) {
	store := NewInMemory()
	ctx := context.Background()

	summaryCalled := make(chan struct{}, 1)
	summaryDone := make(chan struct{})

	fn := func(_ context.Context, msgs []agent.Message) ([2]agent.Message, error) {
		summaryCalled <- struct{}{}
		<-summaryDone
		return [2]agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "Here is a summary of our previous conversation: summary"}}},
			{Role: agent.RoleAssistant, Content: []agent.ContentBlock{agent.TextBlock{Text: "Understood. I will use this context to continue our conversation."}}},
		}, nil
	}

	// threshold=5 turns (10 messages internally) → 80% = 8 messages triggers summarization
	s, err := NewSummary(store, 5, fn)
	if err != nil {
		t.Fatal(err)
	}

	// Save 7 messages — should NOT trigger
	msgs7 := makeMessages(7)
	if err := s.Save(ctx, "conv", msgs7); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	select {
	case <-summaryCalled:
		t.Fatal("summarization should not trigger at 7 messages (below 80% of 10)")
	case <-time.After(50 * time.Millisecond):
		// expected: no trigger
	}

	// Save 8 messages — should trigger
	msgs8 := makeMessages(8)
	if err := s.Save(ctx, "conv", msgs8); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	select {
	case <-summaryCalled:
		// expected: triggered
	case <-time.After(2 * time.Second):
		t.Fatal("summarization should trigger at 8 messages (80% of 10)")
	}

	close(summaryDone)
}

// TestSummary_SkipsWhenAlreadySummarizing verifies that a second summarization
// is not triggered while one is already in progress.
//
func TestSummary_SkipsWhenAlreadySummarizing(t *testing.T) {
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

	// First save triggers summarization
	msgs := makeMessages(8)
	if err := s.Save(ctx, "conv", msgs); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Wait for first summarization to start
	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first summarization did not start")
	}

	// Second save while first is in progress — should skip
	msgs2 := makeMessages(10)
	if err := s.Save(ctx, "conv", msgs2); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Let first finish
	close(allowFinish)

	// Give time for any spurious second call
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if callCount != 1 {
		t.Fatalf("expected SummaryFunc called once, got %d", callCount)
	}
}

// TestSummary_PreservesMessagesOnFailure verifies that when SummaryFunc returns
// an error, the original messages remain unchanged in the store.
//
func TestSummary_PreservesMessagesOnFailure(t *testing.T) {
	store := NewInMemory()
	ctx := context.Background()

	summaryDone := make(chan struct{})

	fn := func(_ context.Context, msgs []agent.Message) ([2]agent.Message, error) {
		defer close(summaryDone)
		return [2]agent.Message{}, errors.New("summarization failed")
	}

	s, err := NewSummary(store, 5, fn)
	if err != nil {
		t.Fatal(err)
	}

	msgs := makeMessages(8)
	if err := s.Save(ctx, "conv", msgs); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Wait for background goroutine to finish
	select {
	case <-summaryDone:
	case <-time.After(2 * time.Second):
		t.Fatal("summarization goroutine did not complete")
	}

	// Give a moment for the deferred cleanup
	time.Sleep(50 * time.Millisecond)

	// Messages should be unchanged
	loaded, err := s.Load(ctx, "conv")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if len(loaded) != 8 {
		t.Fatalf("expected 8 messages preserved after failure, got %d", len(loaded))
	}
}

// TestSummary_MergesCorrectly verifies that after summarization completes,
// the store contains [user summary, assistant ack] + [new messages added after cutoff].
//
func TestSummary_MergesCorrectly(t *testing.T) {
	store := NewInMemory()
	ctx := context.Background()

	summaryDone := make(chan struct{})

	fn := func(_ context.Context, msgs []agent.Message) ([2]agent.Message, error) {
		defer close(summaryDone)
		return [2]agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "Here is a summary of our previous conversation: summary-of-old"}}},
			{Role: agent.RoleAssistant, Content: []agent.ContentBlock{agent.TextBlock{Text: "Understood. I will use this context to continue our conversation."}}},
		}, nil
	}

	s, err := NewSummary(store, 5, fn)
	if err != nil {
		t.Fatal(err)
	}

	// Save 8 messages to trigger summarization (cutoff = 8)
	msgs := makeMessages(8)
	if err := s.Save(ctx, "conv", msgs); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Wait for summarization to complete
	select {
	case <-summaryDone:
	case <-time.After(2 * time.Second):
		t.Fatal("summarization did not complete")
	}

	// Give time for the save in runSummarize to complete
	time.Sleep(100 * time.Millisecond)

	loaded, err := s.Load(ctx, "conv")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// cutoff was 8 (all messages), so result should be [user summary, assistant ack] = 2 messages
	if len(loaded) != 2 {
		t.Fatalf("expected 2 messages after summarization, got %d", len(loaded))
	}

	// First message: user role containing the summary text
	if loaded[0].Role != agent.RoleUser {
		t.Fatalf("expected loaded[0].Role == RoleUser, got %q", loaded[0].Role)
	}
	tb0, ok := loaded[0].Content[0].(agent.TextBlock)
	if !ok {
		t.Fatal("expected TextBlock in summary user message")
	}
	if !strings.Contains(tb0.Text, "summary-of-old") {
		t.Fatalf("expected summary user message to contain %q, got %q", "summary-of-old", tb0.Text)
	}

	// Second message: assistant acknowledgment
	if loaded[1].Role != agent.RoleAssistant {
		t.Fatalf("expected loaded[1].Role == RoleAssistant, got %q", loaded[1].Role)
	}
}

// TestSummary_ConcurrentLoadSaveDuringSummarization verifies that Load and Save
// operations succeed without panics or races while summarization is in progress.
//
func TestSummary_ConcurrentLoadSaveDuringSummarization(t *testing.T) {
	store := NewInMemory()
	ctx := context.Background()

	started := make(chan struct{})
	allowFinish := make(chan struct{})

	fn := func(_ context.Context, msgs []agent.Message) ([2]agent.Message, error) {
		close(started)
		<-allowFinish
		return [2]agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "Here is a summary of our previous conversation: summary"}}},
			{Role: agent.RoleAssistant, Content: []agent.ContentBlock{agent.TextBlock{Text: "Understood. I will use this context to continue our conversation."}}},
		}, nil
	}

	s, err := NewSummary(store, 5, fn)
	if err != nil {
		t.Fatal(err)
	}

	// Trigger summarization
	msgs := makeMessages(8)
	if err := s.Save(ctx, "conv", msgs); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Wait for summarization goroutine to start
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("summarization did not start")
	}

	// Perform concurrent Load and Save while summarization is blocked
	var wg sync.WaitGroup
	errs := make(chan error, 20)

	for i := range 10 {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			_, err := s.Load(ctx, "conv")
			if err != nil {
				errs <- err
			}
		}(i)
		go func(i int) {
			defer wg.Done()
			newMsgs := makeMessages(3)
			err := s.Save(ctx, "other-conv", newMsgs)
			if err != nil {
				errs <- err
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent operation failed: %v", err)
	}

	// Unblock summarization and let it finish
	close(allowFinish)

	// Give time for cleanup
	time.Sleep(100 * time.Millisecond)
}

// TestSummary_NoRetriggerAfterCompletion verifies that once summarization completes,
// calling Save again at the same threshold does NOT trigger a second summarization.
//
func TestSummary_NoRetriggerAfterCompletion(t *testing.T) {
	store := NewInMemory()
	ctx := context.Background()

	var mu sync.Mutex
	callCount := 0
	summaryDone := make(chan struct{})

	fn := func(_ context.Context, msgs []agent.Message) ([2]agent.Message, error) {
		mu.Lock()
		callCount++
		mu.Unlock()
		return [2]agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "Here is a summary of our previous conversation: summary"}}},
			{Role: agent.RoleAssistant, Content: []agent.ContentBlock{agent.TextBlock{Text: "Understood. I will use this context to continue our conversation."}}},
		}, nil
	}

	// threshold=5 turns (10 messages internally) → triggerThreshold = 8
	s, err := NewSummary(store, 5, fn)
	if err != nil {
		t.Fatal(err)
	}

	// First Save at threshold — triggers summarization
	msgs := makeMessages(8)
	if err := s.Save(ctx, "conv", msgs); err != nil {
		t.Fatalf("first Save failed: %v", err)
	}

	// Wait for the goroutine to complete by polling summarized state
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		done := s.summarizedAt["conv"] > 0
		s.mu.Unlock()
		if done {
			close(summaryDone)
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	select {
	case <-summaryDone:
	default:
		t.Fatal("summarization did not complete within timeout")
	}

	// Second Save at threshold — should NOT trigger again
	if err := s.Save(ctx, "conv", msgs); err != nil {
		t.Fatalf("second Save failed: %v", err)
	}

	// Give time for any spurious goroutine to run
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if callCount != 1 {
		t.Fatalf("expected SummaryFunc called exactly once, got %d", callCount)
	}
}

// TestSummary_MessageArrivingDuringSummarizationIsPreserved verifies that a message
// saved while summarization is in progress is not lost when the summary is written back.
//
// Scenario: [msg1..msg8] triggers summarization. While the LLM is running, msg9 arrives.
// After summarization completes the store should contain [user summary, assistant ack, msg9].
func TestSummary_MessageArrivingDuringSummarizationIsPreserved(t *testing.T) {
	store := NewInMemory()
	ctx := context.Background()

	llmStarted := make(chan struct{})
	allowFinish := make(chan struct{})

	fn := func(_ context.Context, msgs []agent.Message) ([2]agent.Message, error) {
		close(llmStarted)
		<-allowFinish
		return [2]agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "Here is a summary of our previous conversation: summary"}}},
			{Role: agent.RoleAssistant, Content: []agent.ContentBlock{agent.TextBlock{Text: "Understood. I will use this context to continue our conversation."}}},
		}, nil
	}

	s, err := NewSummary(store, 5, fn) // 5 turns = 10 messages, trigger at 8
	if err != nil {
		t.Fatal(err)
	}

	// Trigger summarization with 8 messages (cutoff = 8).
	if err := s.Save(ctx, "conv", makeMessages(8)); err != nil {
		t.Fatal(err)
	}

	// Wait for the LLM call to start.
	select {
	case <-llmStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("summarization did not start")
	}

	// Simulate a new message arriving while the LLM is still running.
	// The agent appends to the existing history and saves the full slice.
	extra := append(makeMessages(8), agent.Message{
		Role:    agent.RoleUser,
		Content: []agent.ContentBlock{agent.TextBlock{Text: "msg9"}},
	})
	if err := s.Save(ctx, "conv", extra); err != nil {
		t.Fatal(err)
	}

	// Let the LLM finish.
	close(allowFinish)

	// Wait for the goroutine to complete.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		done := s.summarizedAt["conv"] > 0
		s.mu.Unlock()
		if done {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	loaded, err := s.Load(ctx, "conv")
	if err != nil {
		t.Fatal(err)
	}

	// Expect [user summary, assistant ack, msg9] — msg9 must not be lost.
	if len(loaded) != 3 {
		t.Fatalf("expected 3 messages [user summary, assistant ack, msg9], got %d: %+v", len(loaded), loaded)
	}
	// First message: user role containing summary text.
	if loaded[0].Role != agent.RoleUser {
		t.Errorf("expected loaded[0].Role == RoleUser, got %q", loaded[0].Role)
	}
	tb0, ok := loaded[0].Content[0].(agent.TextBlock)
	if !ok || !strings.Contains(tb0.Text, "summary") {
		t.Errorf("expected first message to contain summary text, got %+v", loaded[0])
	}
	// Second message: assistant acknowledgment.
	if loaded[1].Role != agent.RoleAssistant {
		t.Errorf("expected loaded[1].Role == RoleAssistant, got %q", loaded[1].Role)
	}
	// Third message: the msg9 that arrived during summarization.
	tb2, ok := loaded[2].Content[0].(agent.TextBlock)
	if !ok || tb2.Text != "msg9" {
		t.Errorf("expected third message to be msg9, got %+v", loaded[2])
	}
}

// TestSummary_RetriggersWhenResultStillAboveThreshold verifies that if the merged
// result after summarization is still above the trigger threshold (because many
// messages arrived during the slow LLM call), a second summarization fires automatically.
func TestSummary_RetriggersWhenResultStillAboveThreshold(t *testing.T) {
	store := NewInMemory()
	ctx := context.Background()

	var mu sync.Mutex
	callCount := 0
	firstDone := make(chan struct{})
	secondDone := make(chan struct{})
	allowFirst := make(chan struct{})

	fn := func(_ context.Context, msgs []agent.Message) ([2]agent.Message, error) {
		mu.Lock()
		callCount++
		n := callCount
		mu.Unlock()

		if n == 1 {
			<-allowFirst // block until test injects extra messages
		}

		summary := [2]agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "Here is a summary of our previous conversation: summary"}}},
			{Role: agent.RoleAssistant, Content: []agent.ContentBlock{agent.TextBlock{Text: "Understood. I will use this context to continue our conversation."}}},
		}

		mu.Lock()
		if n == 1 {
			close(firstDone)
		} else if n == 2 {
			close(secondDone)
		}
		mu.Unlock()

		return summary, nil
	}

	// threshold=5 turns (10 messages internally), trigger at 8 messages
	s, err := NewSummary(store, 5, fn)
	if err != nil {
		t.Fatal(err)
	}

	// Trigger first summarization with 8 messages.
	if err := s.Save(ctx, "conv", makeMessages(8)); err != nil {
		t.Fatal(err)
	}

	// While the LLM is blocked, add 8 more messages (total 16 in store).
	// This simulates a fast-paced conversation overflowing during summarization.
	if err := s.Save(ctx, "conv", makeMessages(16)); err != nil {
		t.Fatal(err)
	}

	// Unblock the first LLM call. The merge will produce [summary + 8 tail msgs] = 9,
	// which is still above the trigger threshold of 8 — so a second run should fire.
	close(allowFirst)

	select {
	case <-secondDone:
		// Second summarization fired automatically.
	case <-time.After(3 * time.Second):
		mu.Lock()
		t.Fatalf("expected second summarization to fire, callCount=%d", callCount)
		mu.Unlock()
	}

	mu.Lock()
	defer mu.Unlock()
	if callCount < 2 {
		t.Fatalf("expected at least 2 summarization calls, got %d", callCount)
	}
}

// TestSummary_PreserveRecentMessages verifies that WithPreserveRecentMessages
// keeps the last N messages out of the SummaryFunc and places them after the
// summary in the result.
func TestSummary_PreserveRecentMessages(t *testing.T) {
	store := NewInMemory()
	ctx := context.Background()

	var summarizedCount int
	summaryDone := make(chan struct{})

	fn := func(_ context.Context, msgs []agent.Message) ([2]agent.Message, error) {
		summarizedCount = len(msgs)
		defer close(summaryDone)
		return [2]agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "Here is a summary of our previous conversation: summary"}}},
			{Role: agent.RoleAssistant, Content: []agent.ContentBlock{agent.TextBlock{Text: "Understood. I will use this context to continue our conversation."}}},
		}, nil
	}

	// threshold=4 turns (8 messages internally, trigger at 6 summarizable),
	// preserve last 2 turns (4 messages).
	// With 10 messages: summarizable = 10 - 4 = 6, trigger = (8*80)/100 = 6 → triggers.
	s, err := NewSummary(store, 4, fn, WithPreserveRecentMessages(2))
	if err != nil {
		t.Fatal(err)
	}

	// Build 10 messages with distinct text so we can identify them.
	msgs := make([]agent.Message, 10)
	for i := range msgs {
		msgs[i] = agent.Message{
			Role:    agent.RoleUser,
			Content: []agent.ContentBlock{agent.TextBlock{Text: fmt.Sprintf("msg%d", i)}},
		}
	}

	if err := s.Save(ctx, "conv", msgs); err != nil {
		t.Fatal(err)
	}

	select {
	case <-summaryDone:
	case <-time.After(2 * time.Second):
		t.Fatal("summarization did not complete")
	}

	time.Sleep(50 * time.Millisecond)

	// preserveRecent=2 turns = 4 messages, so SummaryFunc receives 10-4=6 messages.
	if summarizedCount != 6 {
		t.Fatalf("expected SummaryFunc to receive 6 messages, got %d", summarizedCount)
	}

	loaded, err := s.Load(ctx, "conv")
	if err != nil {
		t.Fatal(err)
	}

	// Result: [user summary, assistant ack, msg6, msg7, msg8, msg9] = 6 messages.
	if len(loaded) != 6 {
		t.Fatalf("expected 6 messages [summary turn + 2 preserved turns], got %d", len(loaded))
	}

	// First two messages are the summary turn.
	if loaded[0].Role != agent.RoleUser {
		t.Errorf("expected loaded[0].Role == RoleUser, got %q", loaded[0].Role)
	}
	tb0, _ := loaded[0].Content[0].(agent.TextBlock)
	if !strings.Contains(tb0.Text, "summary") {
		t.Errorf("expected first message to contain summary text, got %q", tb0.Text)
	}
	if loaded[1].Role != agent.RoleAssistant {
		t.Errorf("expected loaded[1].Role == RoleAssistant, got %q", loaded[1].Role)
	}

	// Preserved messages at indices 2..5: msg6, msg7, msg8, msg9.
	for i, want := range []string{"msg6", "msg7", "msg8", "msg9"} {
		tb, _ := loaded[i+2].Content[0].(agent.TextBlock)
		if tb.Text != want {
			t.Errorf("loaded[%d]: expected %q, got %q", i+2, want, tb.Text)
		}
	}
}

// TestSummary_PreserveRecentMessages_SkipsWhenAllPreserved verifies that when
// preserveRecent >= message count, summarization is skipped entirely.
func TestSummary_PreserveRecentMessages_SkipsWhenAllPreserved(t *testing.T) {
	store := NewInMemory()
	ctx := context.Background()

	called := false
	fn := func(_ context.Context, msgs []agent.Message) ([2]agent.Message, error) {
		called = true
		return [2]agent.Message{}, nil
	}

	// threshold=5 turns (10 messages internally, trigger at 8), preserve all 5 turns (10 messages) — nothing to summarize.
	s, err := NewSummary(store, 5, fn, WithPreserveRecentMessages(5))
	if err != nil {
		t.Fatal(err)
	}

	if err := s.Save(ctx, "conv", makeMessages(8)); err != nil {
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond)

	if called {
		t.Fatal("SummaryFunc should not be called when all messages are within preserve window")
	}
}
