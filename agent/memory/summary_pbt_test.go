package memory

import (
	"context"
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
//
// Feature: agent-framework-improvements, Property 3: Summary memory does not re-trigger after completion
//
// **Validates: Requirements 3.1, 3.3**
func TestProperty3_SummaryNoRetriggerAfterCompletion(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a threshold between 5 and 20
		threshold := rapid.IntRange(5, 20).Draw(rt, "threshold")
		triggerThreshold := (threshold * 80) / 100
		if triggerThreshold < 1 {
			triggerThreshold = 1
		}

		// Generate message count at or above trigger threshold
		msgCount := rapid.IntRange(triggerThreshold, threshold+10).Draw(rt, "msgCount")

		store := NewStore()
		ctx := context.Background()

		var callCount int64
		summaryDone := make(chan struct{}, 1)

		fn := func(_ context.Context, msgs []agent.Message) (agent.Message, error) {
			atomic.AddInt64(&callCount, 1)
			return agent.Message{
				Role:    agent.RoleAssistant,
				Content: []agent.ContentBlock{agent.TextBlock{Text: "summary"}},
			}, nil
		}

		s := NewSummary(store, threshold, fn)
		msgs := makeMessages(msgCount)

		// First Save — triggers summarization
		if err := s.Save(ctx, "conv", msgs); err != nil {
			rt.Fatalf("first Save failed: %v", err)
		}

		// Wait for summarization to complete
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			s.mu.Lock()
			done := s.summarized["conv"]
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
//
// Feature: agent-framework-improvements, Property 4: Concurrent Save triggers at most one summarization
//
// **Validates: Requirements 3.1, 3.2, 3.3**
func TestProperty4_ConcurrentSaveTriggersAtMostOneSummarization(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate N concurrent callers between 2 and 20
		n := rapid.IntRange(2, 20).Draw(rt, "n")

		// Fixed threshold so triggerThreshold is predictable
		threshold := 10
		triggerThreshold := (threshold * 80) / 100 // = 8

		store := NewStore()
		ctx := context.Background()

		var callCount int64
		var mu sync.Mutex
		started := make(chan struct{}, 1)
		allowFinish := make(chan struct{})
		firstStarted := false

		fn := func(_ context.Context, msgs []agent.Message) (agent.Message, error) {
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
			return agent.Message{
				Role:    agent.RoleAssistant,
				Content: []agent.ContentBlock{agent.TextBlock{Text: "summary"}},
			}, nil
		}

		s := NewSummary(store, threshold, fn)
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
