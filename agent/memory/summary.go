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

// SummaryFunc condenses a slice of messages into a user+assistant turn.
// The first element must be a user-role message containing the summary text.
// The second element must be an assistant-role acknowledgment message.
// This ensures the summarized conversation always starts with a user message
// and maintains strict alternation.
type SummaryFunc func(ctx context.Context, messages []agent.Message) ([2]agent.Message, error)

// SummaryOption configures optional behavior on a Summary strategy.
// Returns an error if the configuration is invalid.
type SummaryOption func(*Summary) error

// WithSummaryLogger sets an optional logger for error reporting during
// background summarization.
func WithSummaryLogger(l agent.Logger) SummaryOption {
	return func(s *Summary) error {
		s.logger = l
		return nil
	}
}

// WithPreserveRecentMessages sets the number of most-recent turns
// (user+assistant exchanges) that are always kept out of summarization. When
// summarization triggers, only messages before the last n turns are passed
// to the SummaryFunc — the tail is always preserved verbatim after the summary.
// Defaults to 0 (summarize all messages up to cutoff).
func WithPreserveRecentMessages(n int) SummaryOption {
	return func(s *Summary) error {
		if n < 0 {
			return fmt.Errorf("preserve recent turns must be non-negative, got %d", n)
		}
		s.preserveRecent = n * 2 // convert turns to individual messages
		return nil
	}
}

// WithTriggerThreshold sets the percentage of the threshold at which
// summarization triggers. Defaults to 80 (summarization fires when the
// summarizable message count reaches 80% of the threshold). The value
// is clamped to the range [1, 100].
func WithTriggerThreshold(pct int) SummaryOption {
	return func(s *Summary) error {
		if pct < 1 || pct > 100 {
			return fmt.Errorf("trigger threshold must be between 1 and 100, got %d", pct)
		}
		s.triggerPct = pct
		return nil
	}
}

// summaryState tracks per-conversation summarization progress.
type summaryState struct {
	cutoffIndex int
}

// Summary wraps a Memory and triggers background summarization when the
// summarizable message count (total minus preserved) reaches the configured
// trigger percentage of the threshold.
// Documented in docs/memory.md — update when changing threshold, behavior, or options.
type Summary struct {
	inner          agent.Memory
	threshold      int
	triggerPct     int // percentage of threshold at which to trigger (default 80)
	summarize      SummaryFunc
	logger         agent.Logger
	preserveRecent int // number of recent messages to always keep out of summarization

	mu             sync.Mutex
	summarizing    map[string]bool // per-conversation summarization lock
	summarized     map[string]bool // set after summarization completes; cleared when count drops below threshold
	pendingSummary map[string]*summaryState
	wg             sync.WaitGroup // tracks all in-flight summarization goroutines
}

// NewSummary creates a Summary strategy that triggers background summarization
// when the summarizable message count reaches the configured trigger percentage
// (default 80%) of the threshold. The threshold is specified in turns
// (user+assistant exchanges). Preserved messages are excluded from the trigger
// count — only the summarizable portion is compared against the threshold.
func NewSummary(inner agent.Memory, threshold int, fn SummaryFunc, opts ...SummaryOption) (*Summary, error) {
	if threshold < 1 {
		return nil, fmt.Errorf("threshold must be at least 1 turn, got %d", threshold)
	}
	s := &Summary{
		inner:          inner,
		threshold:      threshold * 2, // convert turns to individual messages
		triggerPct:     80,
		summarize:      fn,
		summarizing:    make(map[string]bool),
		summarized:     make(map[string]bool),
		pendingSummary: make(map[string]*summaryState),
	}
	for _, opt := range opts {
		if err := opt(s); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// NewSummaryFunc returns a SummaryFunc that uses the given Provider and system prompt
// to condense messages. Use this to customise what the summariser focuses on without
// having to deal with message formatting or provider calls directly.
//
// Example:
//
//	memory.NewSummaryFunc(provider,
//	    "Summarise this analytics conversation. Preserve table names, "+
//	    "domain metrics, and specific numbers.",
//	)
func NewSummaryFunc(provider agent.Provider, systemPrompt string) SummaryFunc {
	return func(ctx context.Context, msgs []agent.Message) ([2]agent.Message, error) {
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
			System: systemPrompt,
			Messages: []agent.Message{{
				Role:    agent.RoleUser,
				Content: []agent.ContentBlock{agent.TextBlock{Text: sb.String()}},
			}},
		})
		if err != nil {
			return [2]agent.Message{}, fmt.Errorf("summary func: %w", err)
		}

		return [2]agent.Message{
			{
				Role:    agent.RoleUser,
				Content: []agent.ContentBlock{agent.TextBlock{Text: "Here is a summary of our previous conversation: " + resp.Text}},
			},
			{
				Role:    agent.RoleAssistant,
				Content: []agent.ContentBlock{agent.TextBlock{Text: "Understood. I will use this context to continue our conversation."}},
			},
		}, nil
	}
}

// DefaultSummaryFunc returns a SummaryFunc that uses the given Provider to
// condense messages into a single summary. This is the batteries-included
// default — pass it to NewSummary so you don't have to write your own.
// Documented in docs/memory.md — update when changing behavior.
func DefaultSummaryFunc(provider agent.Provider) SummaryFunc {
	return NewSummaryFunc(provider,
		"Summarize the following conversation into a single concise paragraph. "+
			"Preserve all key facts, names, and decisions.",
	)
}

// Load delegates to the inner store and returns the current state,
// whether summarized or not.
func (s *Summary) Load(ctx context.Context, conversationID string) ([]agent.Message, error) {
	return s.inner.Load(ctx, conversationID)
}

// triggerThreshold returns the number of summarizable messages (total minus
// preserved) at which summarization fires.
func (s *Summary) triggerThreshold() int {
	return (s.threshold * s.triggerPct) / 100
}

// Save delegates to the inner store, then checks whether background
// summarization should be triggered. The trigger compares the summarizable
// message count (total minus preserved) against the configured threshold
// percentage.
func (s *Summary) Save(ctx context.Context, conversationID string, msgs []agent.Message) error {
	if err := s.inner.Save(ctx, conversationID, msgs); err != nil {
		return err
	}

	trigger := s.triggerThreshold()
	summarizable := len(msgs) - s.preserveRecent

	s.mu.Lock()
	if summarizable < trigger {
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

	s.wg.Add(1)
	go s.runSummarize(conversationID, cutoff)

	return nil
}

// runSummarize performs background summarization for a conversation.
func (s *Summary) runSummarize(conversationID string, cutoff int) {
	defer s.wg.Done()
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
	summaryPair, err := s.summarize(ctx, preSummarize[:summarizeUntil])
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

	// Ensure the tail starts with a user message so it alternates correctly
	// after the summary pair (which ends with an assistant message).
	// If the tail starts with an assistant message, include it in the
	// summarized portion by advancing preserveFrom by one.
	if len(tail) > 0 && tail[0].Role == agent.RoleAssistant {
		preserveFrom++
		if preserveFrom <= len(latest) {
			tail = latest[preserveFrom:]
		} else {
			tail = nil
		}
	}

	// Use the summary pair directly from SummaryFunc.
	newMsgs := make([]agent.Message, 0, 2+len(tail))
	newMsgs = append(newMsgs, summaryPair[0], summaryPair[1])
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
	// Skip re-trigger if the summarizable portion is too small to compress further
	// (e.g., only the summary turn remains outside the preserve window).
	trigger := s.triggerThreshold()
	summarizable := len(newMsgs) - s.preserveRecent
	if summarizable >= trigger && summarizable > 2 {
		newCutoff := len(newMsgs)
		s.mu.Lock()
		reTriggered = true
		// summarizing[conv] stays true — the new goroutine inherits it.
		s.pendingSummary[conversationID] = &summaryState{cutoffIndex: newCutoff}
		s.mu.Unlock()
		s.wg.Add(1)
		go s.runSummarize(conversationID, newCutoff)
	}
}

// Wait blocks until all in-flight background summarization goroutines have
// finished. Useful in tests and CLI tools where you want to inspect the store
// immediately after the last Save.
func (s *Summary) Wait() {
	s.wg.Wait()
}
