package conversation

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"pgregory.net/rapid"
)

// TestProperty3_SummaryNoRetriggerAfterCompletion verifies that once summarization
// completes for a conversation, subsequent Save calls at the same threshold do not
// launch another summarization goroutine.
func TestProperty3_SummaryNoRetriggerAfterCompletion(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a threshold in turns between 3 and 10 (6–20 messages internally)
		threshold := rapid.IntRange(3, 10).Draw(rt, "threshold")
		internalThreshold := threshold * 2
		triggerThreshold := (internalThreshold * 80) / 100
		if triggerThreshold < 1 {
			triggerThreshold = 1
		}

		// Generate message count at or above trigger threshold
		msgCount := rapid.IntRange(triggerThreshold, internalThreshold+10).Draw(rt, "msgCount")

		store := NewInMemory()
		ctx := context.Background()

		var callCount int64
		summaryDone := make(chan struct{}, 1)

		fn := func(_ context.Context, msgs []agent.Message) ([2]agent.Message, error) {
			atomic.AddInt64(&callCount, 1)
			return [2]agent.Message{
				{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "Here is a summary of our previous conversation: summary"}}},
				{Role: agent.RoleAssistant, Content: []agent.ContentBlock{agent.TextBlock{Text: "Understood. I will use this context to continue our conversation."}}},
			}, nil
		}

		s, err := NewSummary(store, threshold, fn)
		if err != nil {
			rt.Fatalf("NewSummary: %v", err)
		}
		msgs := makeMessages(msgCount)

		// First Save — triggers summarization
		if err := s.Save(ctx, "conv", msgs); err != nil {
			rt.Fatalf("first Save failed: %v", err)
		}

		// Wait for summarization to complete
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			s.mu.Lock()
			done := s.summarizedAt["conv"] > 0
			s.mu.Unlock()
			if done {
				summaryDone <- struct{}{}
				break
			}
			time.Sleep(5 * time.Millisecond)
		}

		select {
		case <-summaryDone:
		default:
			rt.Fatal("summarization did not complete within timeout")
		}

		// Second Save at same count — must NOT re-trigger
		if err := s.Save(ctx, "conv", msgs); err != nil {
			rt.Fatalf("second Save failed: %v", err)
		}

		// Give time for any spurious goroutine
		time.Sleep(50 * time.Millisecond)

		if got := atomic.LoadInt64(&callCount); got != 1 {
			rt.Fatalf("expected SummaryFunc called exactly once, got %d", got)
		}
	})
}

// TestProperty4_ConcurrentSaveTriggersAtMostOneSummarization verifies that
// concurrent Save calls for the same conversation trigger at most one summarization.
func TestProperty4_ConcurrentSaveTriggersAtMostOneSummarization(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate N concurrent callers between 2 and 20
		n := rapid.IntRange(2, 20).Draw(rt, "n")

		// Fixed threshold so triggerThreshold is predictable
		// 5 turns = 10 messages internally, trigger at 8
		threshold := 5
		triggerThreshold := (threshold * 2 * 80) / 100 // = 8

		store := NewInMemory()
		ctx := context.Background()

		var callCount int64
		var mu sync.Mutex
		started := make(chan struct{}, 1)
		allowFinish := make(chan struct{})
		firstStarted := false

		fn := func(_ context.Context, msgs []agent.Message) ([2]agent.Message, error) {
			mu.Lock()
			if !firstStarted {
				firstStarted = true
				select {
				case started <- struct{}{}:
				default:
				}
			}
			mu.Unlock()

			atomic.AddInt64(&callCount, 1)
			// Block until test allows finish, to keep goroutine alive during concurrent saves
			select {
			case <-allowFinish:
			case <-time.After(3 * time.Second):
			}
			return [2]agent.Message{
				{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "Here is a summary of our previous conversation: summary"}}},
				{Role: agent.RoleAssistant, Content: []agent.ContentBlock{agent.TextBlock{Text: "Understood. I will use this context to continue our conversation."}}},
			}, nil
		}

		s, err := NewSummary(store, threshold, fn)
		if err != nil {
			rt.Fatalf("NewSummary: %v", err)
		}
		msgs := makeMessages(triggerThreshold)

		// Launch N concurrent Save calls
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = s.Save(ctx, "conv", msgs)
			}()
		}
		wg.Wait()

		// Allow the summarization goroutine to finish
		close(allowFinish)

		// Wait for goroutine to complete
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			s.mu.Lock()
			running := s.summarizing["conv"]
			s.mu.Unlock()
			if !running {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}

		if got := atomic.LoadInt64(&callCount); got > 1 {
			rt.Fatalf("expected SummaryFunc called at most once, got %d", got)
		}
	})
}

// TestProperty2_Preservation_BelowThresholdUnchanged verifies that conversations with
// message counts below the 80% trigger threshold are stored and loaded identically
// through the Summary wrapper — no modification, no summarization triggered.
//
// This test captures the baseline preservation behavior on UNFIXED code. It must PASS
// on both unfixed and fixed code, confirming that the fix does not regress below-threshold
// behavior.
func TestProperty2_Preservation_BelowThresholdUnchanged(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a random threshold in turns between 3 and 25 (6–50 messages internally)
		threshold := rapid.IntRange(3, 25).Draw(rt, "threshold")
		internalThreshold := threshold * 2
		triggerThreshold := (internalThreshold * 80) / 100
		if triggerThreshold < 1 {
			triggerThreshold = 1
		}

		// Generate message count strictly below the trigger threshold
		// (at least 1 message so we have something to verify)
		if triggerThreshold <= 1 {
			// If trigger threshold is 1, below-threshold means 0 messages — skip
			return
		}
		msgCount := rapid.IntRange(1, triggerThreshold-1).Draw(rt, "msgCount")

		// Generate random preserveRecent in turns (0–5)
		preserveRecent := rapid.IntRange(0, 5).Draw(rt, "preserveRecent")

		// Build messages with random roles and content using the test utility generators
		// Ensure summarizable count (msgCount - preserved) is below trigger threshold.
		// We generate msgCount such that even with 0 preserved, it's below trigger.
		msgs := make([]agent.Message, msgCount)
		roles := []agent.Role{agent.RoleUser, agent.RoleAssistant}
		for i := range msgs {
			msgs[i] = agent.Message{
				Role:    rapid.SampledFrom(roles).Draw(rt, fmt.Sprintf("role_%d", i)),
				Content: []agent.ContentBlock{agent.TextBlock{Text: fmt.Sprintf("content-%d", i)}},
			}
		}

		store := NewInMemory()
		ctx := context.Background()

		// SummaryFunc should NEVER be called for below-threshold conversations
		var summaryCallCount int64
		fn := func(_ context.Context, incoming []agent.Message) ([2]agent.Message, error) {
			atomic.AddInt64(&summaryCallCount, 1)
			return [2]agent.Message{
				{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "Here is a summary of our previous conversation: summary"}}},
				{Role: agent.RoleAssistant, Content: []agent.ContentBlock{agent.TextBlock{Text: "Understood. I will use this context to continue our conversation."}}},
			}, nil
		}

		s, err := NewSummary(store, threshold, fn, WithPreserveRecentMessages(preserveRecent))
		if err != nil {
			rt.Fatalf("NewSummary: %v", err)
		}

		// Save messages
		if err := s.Save(ctx, "conv", msgs); err != nil {
			rt.Fatalf("Save failed: %v", err)
		}

		// Load messages back
		loaded, err := s.Load(ctx, "conv")
		if err != nil {
			rt.Fatalf("Load failed: %v", err)
		}

		// Assert: SummaryFunc was never called
		if got := atomic.LoadInt64(&summaryCallCount); got != 0 {
			rt.Fatalf("expected SummaryFunc not called for below-threshold conversation, got %d calls "+
				"[threshold=%d, triggerThreshold=%d, msgCount=%d]",
				got, threshold, triggerThreshold, msgCount)
		}

		// Assert: loaded messages have the same length as saved messages
		if len(loaded) != len(msgs) {
			rt.Fatalf("expected %d messages after round-trip, got %d "+
				"[threshold=%d, triggerThreshold=%d, msgCount=%d]",
				len(msgs), len(loaded), threshold, triggerThreshold, msgCount)
		}

		// Assert: each loaded message is identical to the saved message (role and content)
		for i := range msgs {
			if loaded[i].Role != msgs[i].Role {
				rt.Fatalf("message[%d] role mismatch: expected %q, got %q "+
					"[threshold=%d, triggerThreshold=%d, msgCount=%d]",
					i, msgs[i].Role, loaded[i].Role, threshold, triggerThreshold, msgCount)
			}

			if len(loaded[i].Content) != len(msgs[i].Content) {
				rt.Fatalf("message[%d] content length mismatch: expected %d, got %d "+
					"[threshold=%d, triggerThreshold=%d, msgCount=%d]",
					i, len(msgs[i].Content), len(loaded[i].Content), threshold, triggerThreshold, msgCount)
			}

			for j := range msgs[i].Content {
				origTB, origOK := msgs[i].Content[j].(agent.TextBlock)
				loadedTB, loadedOK := loaded[i].Content[j].(agent.TextBlock)
				if origOK != loadedOK {
					rt.Fatalf("message[%d].Content[%d] type mismatch", i, j)
				}
				if origOK && origTB.Text != loadedTB.Text {
					rt.Fatalf("message[%d].Content[%d] text mismatch: expected %q, got %q",
						i, j, origTB.Text, loadedTB.Text)
				}
			}
		}
	})
}

// TestProperty1_BugCondition_SummaryMessageRoleViolation is a bug condition exploration
// test that verifies the EXPECTED behavior after the fix: the stored conversation after
// summarization starts with a user-role message containing the summary text, followed by
// an assistant acknowledgment, with strict alternating roles throughout.
//
// On UNFIXED code this test is EXPECTED TO FAIL — failure confirms the bug exists.
// The bug is that runSummarize stores the raw SummaryFunc result (an assistant message)
// as the first message, violating user-first and potentially creating consecutive
// same-role messages when preserveRecent is odd.
func TestProperty1_BugCondition_SummaryMessageRoleViolation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate random threshold in turns (3–25, i.e. 6–50 messages internally)
		threshold := rapid.IntRange(3, 25).Draw(rt, "threshold")
		internalThreshold := threshold * 2
		triggerThreshold := (internalThreshold * 80) / 100
		if triggerThreshold < 1 {
			triggerThreshold = 1
		}

		// Generate random preserveRecent in turns (0–5)
		preserveRecent := rapid.IntRange(0, 5).Draw(rt, "preserveRecent")
		internalPreserve := preserveRecent * 2

		// Generate message count at or above the trigger threshold.
		// Summarizable = msgCount - internalPreserve, must be >= triggerThreshold.
		minMsgs := triggerThreshold + internalPreserve
		if minMsgs < internalPreserve+2 {
			minMsgs = internalPreserve + 2
		}
		msgCount := rapid.IntRange(minMsgs, internalThreshold+20+internalPreserve).Draw(rt, "msgCount")

		// Build alternating user/assistant messages so that odd preserveRecent
		// values can demonstrate the alternation bug.
		msgs := make([]agent.Message, msgCount)
		for i := range msgs {
			role := agent.RoleUser
			if i%2 == 1 {
				role = agent.RoleAssistant
			}
			msgs[i] = agent.Message{
				Role:    role,
				Content: []agent.ContentBlock{agent.TextBlock{Text: fmt.Sprintf("msg%d", i)}},
			}
		}

		store := NewInMemory()
		ctx := context.Background()

		// Deterministic mock SummaryFunc that returns a user+assistant pair with known text.
		fn := func(_ context.Context, incoming []agent.Message) ([2]agent.Message, error) {
			return [2]agent.Message{
				{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "Here is a summary of our previous conversation: summary-text"}}},
				{Role: agent.RoleAssistant, Content: []agent.ContentBlock{agent.TextBlock{Text: "Understood. I will use this context to continue our conversation."}}},
			}, nil
		}

		s, err := NewSummary(store, threshold, fn, WithPreserveRecentMessages(preserveRecent))
		if err != nil {
			rt.Fatalf("NewSummary: %v", err)
		}

		// Save messages — triggers summarization
		if err := s.Save(ctx, "conv", msgs); err != nil {
			rt.Fatalf("Save failed: %v", err)
		}

		// Wait for summarization to complete
		s.Wait()

		// Load the stored result
		loaded, err := s.Load(ctx, "conv")
		if err != nil {
			rt.Fatalf("Load failed: %v", err)
		}

		if len(loaded) == 0 {
			rt.Fatal("expected non-empty stored messages after summarization")
		}

		// Assert: first message must be user role (user-first requirement)
		if loaded[0].Role != agent.RoleUser {
			rt.Fatalf("expected storedResult[0].Role == %q (user-first), got %q "+
				"[threshold=%d, preserveRecent=%d, msgCount=%d]",
				agent.RoleUser, loaded[0].Role, threshold, preserveRecent, msgCount)
		}

		// Assert: first message must contain the summary text
		if len(loaded[0].Content) == 0 {
			rt.Fatal("expected storedResult[0] to have content")
		}
		tb, ok := loaded[0].Content[0].(agent.TextBlock)
		if !ok {
			rt.Fatal("expected storedResult[0].Content[0] to be TextBlock")
		}
		if !strings.Contains(tb.Text, "summary-text") {
			rt.Fatalf("expected storedResult[0] to contain summary text %q, got %q",
				"summary-text", tb.Text)
		}

		// Assert: second message (if present) must be assistant role
		if len(loaded) >= 2 {
			if loaded[1].Role != agent.RoleAssistant {
				rt.Fatalf("expected storedResult[1].Role == %q (assistant acknowledgment), got %q "+
					"[threshold=%d, preserveRecent=%d, msgCount=%d]",
					agent.RoleAssistant, loaded[1].Role, threshold, preserveRecent, msgCount)
			}
		}

		// Assert: no two consecutive messages share the same role (strict alternation)
		for i := 1; i < len(loaded); i++ {
			if loaded[i].Role == loaded[i-1].Role {
				rt.Fatalf("consecutive same-role messages at indices %d and %d: both %q "+
					"[threshold=%d, preserveRecent=%d, msgCount=%d, totalStored=%d]",
					i-1, i, loaded[i].Role, threshold, preserveRecent, msgCount, len(loaded))
			}
		}
	})
}
