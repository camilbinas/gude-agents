package conversation

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/camilbinas/gude-agents/agent"
)

// compile-time check
var _ agent.Conversation = (*TokenSummary)(nil)

// TokenSummaryOption configures optional behavior on a TokenSummary strategy.
type TokenSummaryOption func(*TokenSummary) error

// WithTokenSummaryLogger sets an optional logger for error reporting during
// background summarization.
func WithTokenSummaryLogger(l agent.Logger) TokenSummaryOption {
	return func(s *TokenSummary) error {
		s.logger = l
		return nil
	}
}

// WithTokenPreserveRecentMessages sets the number of most-recent turns
// (user+assistant exchanges) that are always kept out of summarization.
// Defaults to 0 (summarize all messages up to cutoff).
func WithTokenPreserveRecentMessages(n int) TokenSummaryOption {
	return func(s *TokenSummary) error {
		if n < 0 {
			return fmt.Errorf("preserve recent turns must be non-negative, got %d", n)
		}
		s.preserveRecent = n * 2
		return nil
	}
}

// WithTokenTriggerThreshold sets the percentage of the token threshold at
// which summarization triggers. Defaults to 80%.
func WithTokenTriggerThreshold(pct int) TokenSummaryOption {
	return func(s *TokenSummary) error {
		if pct < 1 || pct > 100 {
			return fmt.Errorf("trigger threshold must be between 1 and 100, got %d", pct)
		}
		s.triggerPct = pct
		return nil
	}
}

// WithTokenSummaryTimeout sets a per-summarization timeout. Default: no timeout.
func WithTokenSummaryTimeout(d time.Duration) TokenSummaryOption {
	return func(s *TokenSummary) error {
		if d <= 0 {
			return fmt.Errorf("summary timeout must be positive, got %s", d)
		}
		s.timeout = d
		return nil
	}
}

// TokenSummary wraps a Conversation and triggers background summarization when
// the provider-reported input token count exceeds a configured threshold. Unlike
// Summary (which triggers on message count), TokenSummary uses actual token
// usage from the agent loop — available via agent.GetTokenUsage on the context
// passed to Save.
//
// When Save is called outside the agent loop (no token usage in context), the
// strategy does not trigger summarization.
type TokenSummary struct {
	inner          agent.Conversation
	tokenThreshold int
	triggerPct     int
	summarize      SummaryFunc
	logger         agent.Logger
	preserveRecent int
	timeout        time.Duration

	ctx    context.Context
	cancel context.CancelFunc

	mu           sync.Mutex
	summarizing  map[string]bool
	summarizedAt map[string]int // input tokens after last summarization
	wg           sync.WaitGroup
}

// NewTokenSummary creates a TokenSummary strategy that triggers background
// summarization when the provider-reported input token count reaches the
// configured trigger percentage (default 80%) of tokenThreshold.
func NewTokenSummary(inner agent.Conversation, tokenThreshold int, fn SummaryFunc, opts ...TokenSummaryOption) (*TokenSummary, error) {
	if inner == nil {
		return nil, fmt.Errorf("inner conversation must not be nil")
	}
	if fn == nil {
		return nil, fmt.Errorf("summary function must not be nil")
	}
	if tokenThreshold < 1 {
		return nil, fmt.Errorf("token threshold must be at least 1, got %d", tokenThreshold)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &TokenSummary{
		inner:          inner,
		tokenThreshold: tokenThreshold,
		triggerPct:     80,
		summarize:      fn,
		ctx:            ctx,
		cancel:         cancel,
		summarizing:    make(map[string]bool),
		summarizedAt:   make(map[string]int),
	}
	for _, opt := range opts {
		if err := opt(s); err != nil {
			cancel()
			return nil, err
		}
	}
	return s, nil
}

// Load delegates to the inner store.
func (s *TokenSummary) Load(ctx context.Context, conversationID string) ([]agent.Message, error) {
	return s.inner.Load(ctx, conversationID)
}

// triggerTokens returns the input token count at which summarization fires.
func (s *TokenSummary) triggerTokens() int {
	return (s.tokenThreshold * s.triggerPct) / 100
}

// Save delegates to the inner store, then checks whether background
// summarization should be triggered based on actual token usage.
func (s *TokenSummary) Save(ctx context.Context, conversationID string, msgs []agent.Message) error {
	if err := s.inner.Save(ctx, conversationID, msgs); err != nil {
		return err
	}

	// Don't trigger if closed.
	if s.ctx.Err() != nil {
		return nil
	}

	// Only trigger when token usage is available (called from agent loop).
	usage, ok := agent.GetTokenUsage(ctx)
	if !ok {
		return nil
	}

	trigger := s.triggerTokens()
	if usage.InputTokens < trigger {
		s.mu.Lock()
		delete(s.summarizedAt, conversationID)
		s.mu.Unlock()
		return nil
	}

	s.mu.Lock()
	if s.summarizing[conversationID] {
		s.mu.Unlock()
		return nil
	}

	// Skip if token count hasn't grown since last summarization.
	if lastTokens, ok := s.summarizedAt[conversationID]; ok && usage.InputTokens <= lastTokens {
		s.mu.Unlock()
		return nil
	}
	s.summarizing[conversationID] = true
	s.mu.Unlock()

	s.wg.Add(1)
	go s.runSummarize(conversationID, msgs, usage.InputTokens)

	return nil
}

// runSummarize performs background summarization for a conversation.
func (s *TokenSummary) runSummarize(conversationID string, msgs []agent.Message, inputTokens int) {
	defer s.wg.Done()
	ctx := s.ctx
	if s.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.timeout)
		defer cancel()
	}
	success := false

	defer func() {
		s.mu.Lock()
		delete(s.summarizing, conversationID)
		if success {
			s.summarizedAt[conversationID] = inputTokens
		}
		s.mu.Unlock()
	}()

	// Load current messages.
	current, err := s.inner.Load(ctx, conversationID)
	if err != nil {
		if s.logger != nil {
			s.logger.Printf("token_summary: failed to load messages for %s: %v", conversationID, err)
		}
		return
	}

	// Respect preserveRecent.
	summarizeUntil := len(current) - s.preserveRecent
	if summarizeUntil <= 0 {
		if s.logger != nil {
			s.logger.Printf("token_summary: skipping — all messages within preserve_recent window for %s", conversationID)
		}
		return
	}

	// Call SummaryFunc on the messages to summarize.
	summaryPair, err := s.summarize(ctx, current[:summarizeUntil])
	if err != nil {
		if s.logger != nil {
			s.logger.Printf("token_summary: summarization failed for %s: %v", conversationID, err)
		}
		return
	}

	// Re-load to capture messages that arrived during summarization.
	latest, err := s.inner.Load(ctx, conversationID)
	if err != nil {
		if s.logger != nil {
			s.logger.Printf("token_summary: failed to re-load messages for %s: %v", conversationID, err)
		}
		return
	}

	preserveFrom := min(summarizeUntil, len(latest))
	tail := latest[preserveFrom:]

	// Ensure tail starts with a user message after the summary pair
	// (which ends with an assistant message).
	if len(tail) > 0 && tail[0].Role == agent.RoleAssistant {
		preserveFrom++
		if preserveFrom <= len(latest) {
			tail = latest[preserveFrom:]
		} else {
			tail = nil
		}
	}

	// Build new message list: summary pair + preserved tail.
	newMsgs := make([]agent.Message, 0, 2+len(tail))
	newMsgs = append(newMsgs, summaryPair[0], summaryPair[1])
	newMsgs = append(newMsgs, tail...)

	if err := s.inner.Save(ctx, conversationID, newMsgs); err != nil {
		if s.logger != nil {
			s.logger.Printf("token_summary: failed to save summarized messages for %s: %v", conversationID, err)
		}
		return
	}
	success = true
	if s.logger != nil {
		s.logger.Printf("token_summary: condensed %d messages → %d (conversation %q, triggered at %d input tokens)",
			len(current), len(newMsgs), conversationID, inputTokens)
	}
}

// Close cancels in-flight summarization and waits for completion.
func (s *TokenSummary) Close() {
	s.cancel()
	s.wg.Wait()
}

// Wait blocks until all in-flight summarization goroutines have finished.
func (s *TokenSummary) Wait() {
	s.wg.Wait()
}
