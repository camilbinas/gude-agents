package memory

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/camilbinas/gude-agents/agent"
)

// compile-time check
var _ agent.Memory = (*Summary)(nil)

// SummaryFunc condenses a slice of messages into a single summary message.
type SummaryFunc func(ctx context.Context, messages []agent.Message) (agent.Message, error)

// SummaryOption configures optional behavior on a Summary strategy.
type SummaryOption func(*Summary)

// WithSummaryLogger sets an optional logger for error reporting during
// background summarization.
func WithSummaryLogger(l agent.Logger) SummaryOption {
	return func(s *Summary) {
		s.logger = l
	}
}

// WithPreserveRecentMessages sets the number of most-recent messages that are
// always kept out of summarization. When summarization triggers, only messages
// before the last n are passed to the SummaryFunc — the tail is always preserved
// verbatim after the summary. Defaults to 0 (summarize all messages up to cutoff).
func WithPreserveRecentMessages(n int) SummaryOption {
	return func(s *Summary) {
		s.preserveRecent = n
	}
}

// summaryState tracks per-conversation summarization progress.
type summaryState struct {
	cutoffIndex int
}

// Summary wraps a Memory and triggers background summarization when the
// conversation size reaches 80% of the configured threshold.
// Documented in docs/memory.md — update when changing threshold, behavior, or options.
type Summary struct {
	inner          agent.Memory
	threshold      int
	summarize      SummaryFunc
	logger         agent.Logger
	preserveRecent int // number of recent messages to always keep out of summarization

	mu             sync.Mutex
	summarizing    map[string]bool // per-conversation summarization lock
	summarized     map[string]bool // set after summarization completes; cleared when count drops below threshold
	pendingSummary map[string]*summaryState
}

// NewSummary creates a Summary strategy that triggers background summarization
// when the message count reaches 80% of threshold.
func NewSummary(inner agent.Memory, threshold int, fn SummaryFunc, opts ...SummaryOption) *Summary {
	s := &Summary{
		inner:          inner,
		threshold:      threshold,
		summarize:      fn,
		summarizing:    make(map[string]bool),
		summarized:     make(map[string]bool),
		pendingSummary: make(map[string]*summaryState),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// DefaultSummaryFunc returns a SummaryFunc that uses the given Provider to
// condense messages into a single summary. This is the batteries-included
// default — pass it to NewSummary so you don't have to write your own.
// Documented in docs/memory.md — update when changing behavior.
func DefaultSummaryFunc(provider agent.Provider) SummaryFunc {
	return func(ctx context.Context, msgs []agent.Message) (agent.Message, error) {
		var sb strings.Builder
		for _, m := range msgs {
			sb.WriteString(string(m.Role))
			sb.WriteString(": ")
			for _, b := range m.Content {
				if tb, ok := b.(agent.TextBlock); ok {
					sb.WriteString(tb.Text)
				}
			}
			sb.WriteString("\n")
		}

		resp, err := provider.Converse(ctx, agent.ConverseParams{
			System: "Summarize the following conversation into a single concise paragraph. " +
				"Preserve all key facts, names, and decisions.",
			Messages: []agent.Message{{
				Role:    agent.RoleUser,
				Content: []agent.ContentBlock{agent.TextBlock{Text: sb.String()}},
			}},
		})
		if err != nil {
			return agent.Message{}, fmt.Errorf("default summary func: %w", err)
		}

		return agent.Message{
			Role:    agent.RoleAssistant,
			Content: []agent.ContentBlock{agent.TextBlock{Text: resp.Text}},
		}, nil
	}
}

// Load delegates to the inner store and returns the current state,
// whether summarized or not.
func (s *Summary) Load(ctx context.Context, conversationID string) ([]agent.Message, error) {
	return s.inner.Load(ctx, conversationID)
}

// Save delegates to the inner store, then checks whether background
// summarization should be triggered based on the 80% threshold.
func (s *Summary) Save(ctx context.Context, conversationID string, msgs []agent.Message) error {
	if err := s.inner.Save(ctx, conversationID, msgs); err != nil {
		return err
	}

	triggerThreshold := (s.threshold * 80) / 100

	s.mu.Lock()
	if len(msgs) < triggerThreshold {
		delete(s.summarized, conversationID)
		s.mu.Unlock()
		return nil
	}

	if s.summarized[conversationID] || s.summarizing[conversationID] {
		s.mu.Unlock()
		return nil
	}
	s.summarizing[conversationID] = true
	cutoff := len(msgs)
	s.pendingSummary[conversationID] = &summaryState{cutoffIndex: cutoff}
	s.mu.Unlock()

	go s.runSummarize(conversationID, cutoff)

	return nil
}

// runSummarize performs background summarization for a conversation.
func (s *Summary) runSummarize(conversationID string, cutoff int) {
	ctx := context.Background()
	success := false
	reTriggered := false

	defer func() {
		s.mu.Lock()
		// Only clear summarizing if we didn't spawn a new goroutine to replace us.
		if !reTriggered {
			delete(s.summarizing, conversationID)
		}
		delete(s.pendingSummary, conversationID)
		if success && !reTriggered {
			s.summarized[conversationID] = true
		}
		s.mu.Unlock()
	}()

	// Load the messages that existed when summarization was triggered.
	// We snapshot up to cutoff — anything beyond that arrived after the trigger.
	preSummarize, err := s.inner.Load(ctx, conversationID)
	if err != nil {
		if s.logger != nil {
			s.logger.Printf("summary: failed to load messages for %s: %v", conversationID, err)
		}
		return
	}

	// Guard against cutoff exceeding current message count.
	if cutoff > len(preSummarize) {
		cutoff = len(preSummarize)
	}

	// Respect preserveRecent: never summarize the last N messages.
	// If preserveRecent >= cutoff there's nothing left to summarize.
	summarizeUntil := cutoff - s.preserveRecent
	if summarizeUntil <= 0 {
		if s.logger != nil {
			s.logger.Printf("summary: skipping summarization for %s — all messages within preserve_recent window", conversationID)
		}
		return
	}

	// Call SummaryFunc on the messages up to summarizeUntil.
	// This may be slow (LLM call) — no locks held during this.
	summaryMsg, err := s.summarize(ctx, preSummarize[:summarizeUntil])
	if err != nil {
		if s.logger != nil {
			s.logger.Printf("summary: summarization failed for %s: %v", conversationID, err)
		}
		return
	}

	// Re-load under the summary mutex to capture any messages that arrived
	// after the cutoff while the LLM was running, then write atomically.
	s.mu.Lock()
	latest, loadErr := s.inner.Load(ctx, conversationID)
	if loadErr != nil {
		s.mu.Unlock()
		if s.logger != nil {
			s.logger.Printf("summary: failed to re-load messages for %s: %v", conversationID, loadErr)
		}
		return
	}

	// Anything in latest beyond summarizeUntil is preserved verbatim after the summary:
	// - messages in [summarizeUntil, cutoff) are the preserved-recent window
	// - messages beyond cutoff arrived while the LLM was running
	preserveFrom := min(summarizeUntil, len(latest))
	tail := latest[preserveFrom:]
	newMsgs := make([]agent.Message, 0, 1+len(tail))
	newMsgs = append(newMsgs, summaryMsg)
	newMsgs = append(newMsgs, tail...)

	saveErr := s.inner.Save(ctx, conversationID, newMsgs)
	s.mu.Unlock()

	if saveErr != nil {
		if s.logger != nil {
			s.logger.Printf("summary: failed to save summarized messages for %s: %v", conversationID, saveErr)
		}
		return
	}
	success = true
	if s.logger != nil {
		s.logger.Printf("summary: condensed %d messages → %d (conversation %q)", cutoff, len(newMsgs), conversationID)
	}

	// If the merged result is already above the trigger threshold (fast-paced
	// conversation that grew during the LLM call), re-trigger immediately.
	triggerThreshold := (s.threshold * 80) / 100
	if len(newMsgs) >= triggerThreshold {
		newCutoff := len(newMsgs)
		s.mu.Lock()
		reTriggered = true
		// summarizing[conv] stays true — the new goroutine inherits it.
		s.pendingSummary[conversationID] = &summaryState{cutoffIndex: newCutoff}
		s.mu.Unlock()
		go s.runSummarize(conversationID, newCutoff)
	}
}
