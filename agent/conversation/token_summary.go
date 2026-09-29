package conversation

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/camilbinas/gude-agents/agent"
)

// compile-time checks
var (
	_ agent.ConversationStore = (*TokenSummary)(nil)
	_ agent.Flusher           = (*TokenSummary)(nil)
)

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

// WithTokenMediaSummaryFunc sets a function that summarizes messages containing
// non-text content (images, documents) into text-only equivalents before
// the main SummaryFunc runs.
func WithTokenMediaSummaryFunc(fn MediaSummaryFunc) TokenSummaryOption {
	return func(s *TokenSummary) error {
		if fn == nil {
			return fmt.Errorf("media summary function must not be nil")
		}
		s.mediaSummaryFunc = fn
		return nil
	}
}

// WithTokenMediaSummaryConcurrency sets the maximum number of parallel media
// summary calls. Defaults to 3 when not set.
func WithTokenMediaSummaryConcurrency(n int) TokenSummaryOption {
	return func(s *TokenSummary) error {
		if n < 1 {
			return fmt.Errorf("media summary concurrency must be >= 1, got %d", n)
		}
		s.mediaSummaryConcurrency = n
		return nil
	}
}

// TokenSummary wraps a ConversationStore and triggers background summarization when
// the provider-reported input token count exceeds a configured threshold. Unlike
// Summary (which triggers on message count), TokenSummary uses actual token
// usage from the agent loop — available via agent.GetTokenUsage on the context
// passed to Save.
//
// When Save is called outside the agent loop (no token usage in context), the
// strategy does not trigger summarization.
type TokenSummary struct {
	inner          agent.ConversationStore
	tokenThreshold int
	triggerPct     int
	summarize      SummaryFunc
	logger         agent.Logger
	preserveRecent int
	timeout        time.Duration

	mediaSummaryFunc        MediaSummaryFunc
	mediaSummaryConcurrency int

	ctx    context.Context
	cancel context.CancelFunc

	mu           sync.Mutex
	summarizing  map[string]bool
	summarizedAt map[string]int // input tokens after last summarization
	workDone     map[string]chan struct{}
	flushErr     error
	pending      int
	idle         chan struct{} // closed whenever all accepted summary work is complete
}

// NewTokenSummary creates a TokenSummary strategy that triggers background
// summarization when the provider-reported input token count reaches the
// configured trigger percentage (default 80%) of tokenThreshold.
func NewTokenSummary(inner agent.ConversationStore, tokenThreshold int, fn SummaryFunc, opts ...TokenSummaryOption) (*TokenSummary, error) {
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
		workDone:       make(map[string]chan struct{}),
		idle:           make(chan struct{}),
	}
	close(s.idle)
	for _, opt := range opts {
		if err := opt(s); err != nil {
			cancel()
			return nil, err
		}
	}
	return s, nil
}

// Load waits for summary work that was already accepted for this conversation,
// then returns the current state. Workers use inner.Load directly so they never
// wait on their own barrier.
func (s *TokenSummary) Load(ctx context.Context, conversationID string) (agent.ConversationSnapshot, error) {
	s.mu.Lock()
	done := s.workDone[conversationID]
	s.mu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return agent.ConversationSnapshot{}, ctx.Err()
		}
	}
	return s.inner.Load(ctx, conversationID)
}

// triggerTokens returns the input token count at which summarization fires.
func (s *TokenSummary) triggerTokens() int {
	return (s.tokenThreshold * s.triggerPct) / 100
}

// Save delegates to the inner store, then checks whether background
// summarization should be triggered based on actual token usage.
func (s *TokenSummary) Save(ctx context.Context, conversationID string, msgs []agent.Message, expectedRevision uint64) (uint64, error) {
	revision, err := s.inner.Save(ctx, conversationID, msgs, expectedRevision)
	if err != nil {
		return 0, err
	}

	// Don't trigger if closed.
	if s.ctx.Err() != nil {
		return revision, nil
	}

	// Approval and human-input checkpoints leave tool calls unresolved. Keep the
	// full transcript intact until every tool use has a corresponding result.
	if hasUnresolvedToolCalls(msgs) {
		return revision, nil
	}

	// Only trigger when token usage is available (called from agent loop).
	usage, ok := agent.GetTokenUsage(ctx)
	if !ok {
		return revision, nil
	}

	trigger := s.triggerTokens()
	if usage.InputTokens < trigger {
		s.mu.Lock()
		delete(s.summarizedAt, conversationID)
		s.mu.Unlock()
		return revision, nil
	}

	s.mu.Lock()
	if s.summarizing[conversationID] {
		s.mu.Unlock()
		return revision, nil
	}

	// Skip if token count hasn't grown since last summarization.
	if lastTokens, ok := s.summarizedAt[conversationID]; ok && usage.InputTokens <= lastTokens {
		s.mu.Unlock()
		return revision, nil
	}
	s.summarizing[conversationID] = true
	s.workDone[conversationID] = make(chan struct{})
	if s.pending == 0 {
		s.idle = make(chan struct{})
	}
	s.pending++
	s.mu.Unlock()

	go s.runSummarize(conversationID, msgs, usage.InputTokens)

	return revision, nil
}

// runSummarize performs background summarization for a conversation.
func (s *TokenSummary) runSummarize(conversationID string, _ []agent.Message, inputTokens int) {
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
		if done := s.workDone[conversationID]; done != nil {
			close(done)
			delete(s.workDone, conversationID)
		}
		if success {
			s.summarizedAt[conversationID] = inputTokens
		}
		s.pending--
		if s.pending == 0 {
			close(s.idle)
		}
		s.mu.Unlock()
	}()

	// Load current messages.
	currentSnapshot, err := s.inner.Load(ctx, conversationID)
	if err != nil {
		s.recordError(fmt.Errorf("token_summary: load %q: %w", conversationID, err))
		if s.logger != nil {
			s.logger.Printf("token_summary: failed to load messages for %s: %v", conversationID, err)
		}
		return
	}
	current := currentSnapshot.Messages

	// Respect preserveRecent.
	summarizeUntil := len(current) - s.preserveRecent
	if summarizeUntil <= 0 {
		if s.logger != nil {
			s.logger.Printf("token_summary: skipping — all messages within preserve_recent window for %s", conversationID)
		}
		return
	}

	if summarizeUntil < len(current) && current[summarizeUntil].Role == agent.RoleAssistant {
		// Keep the tail at a complete user/assistant turn boundary instead of
		// dropping an assistant message during merge.
		summarizeUntil--
	}
	if summarizeUntil <= 0 {
		if s.logger != nil {
			s.logger.Printf("token_summary: skipping — no complete turn before preserve_recent window for %s", conversationID)
		}
		return
	}

	// Preprocess media messages: summarize messages containing non-text content into text-only equivalents.
	toSummarize := current[:summarizeUntil]
	if s.mediaSummaryFunc != nil {
		toSummarize = s.preprocessMediaMessages(ctx, toSummarize)
	}

	// Call SummaryFunc on the messages to summarize.
	summaryPair, err := s.summarize(ctx, toSummarize)
	if err != nil {
		s.recordError(fmt.Errorf("token_summary: summarize %q: %w", conversationID, err))
		if s.logger != nil {
			s.logger.Printf("token_summary: summarization failed for %s: %v", conversationID, err)
		}
		return
	}

	var newMsgs []agent.Message
	for attempt := 0; ; attempt++ {
		latest, loadErr := s.inner.Load(ctx, conversationID)
		if loadErr != nil {
			s.recordError(fmt.Errorf("token_summary: reload %q: %w", conversationID, loadErr))
			if s.logger != nil {
				s.logger.Printf("token_summary: failed to re-load messages for %s: %v", conversationID, loadErr)
			}
			return
		}
		newMsgs = mergeSummary(latest.Messages, summarizeUntil, summaryPair)
		if _, saveErr := s.inner.Save(ctx, conversationID, newMsgs, latest.Revision); saveErr != nil {
			if errors.Is(saveErr, agent.ErrConversationConflict) {
				if ctx.Err() != nil {
					s.recordError(ctx.Err())
					return
				}
				if attempt >= 15 {
					s.recordError(fmt.Errorf("token_summary: save %q after CAS retries: %w", conversationID, saveErr))
					return
				}
				continue
			}
			s.recordError(fmt.Errorf("token_summary: save %q: %w", conversationID, saveErr))
			if s.logger != nil {
				s.logger.Printf("token_summary: failed to save summarized messages for %s: %v", conversationID, saveErr)
			}
			return
		}
		break
	}
	success = true
	if s.logger != nil {
		s.logger.Printf("token_summary: condensed %d messages → %d (conversation %q, triggered at %d input tokens)",
			len(current), len(newMsgs), conversationID, inputTokens)
	}
}

// preprocessMediaMessages summarizes messages containing non-text content
// using the configured MediaSummaryFunc with bounded concurrency.
func (s *TokenSummary) preprocessMediaMessages(ctx context.Context, msgs []agent.Message) []agent.Message {
	type indexedResult struct {
		idx int
		msg agent.Message
		err error
	}

	var needsSummary []int
	for i, m := range msgs {
		if hasNonTextContent(m) {
			needsSummary = append(needsSummary, i)
		}
	}

	if len(needsSummary) == 0 {
		return msgs
	}

	concurrency := s.mediaSummaryConcurrency
	if concurrency < 1 {
		concurrency = 3
	}
	sem := make(chan struct{}, concurrency)

	results := make(chan indexedResult, len(needsSummary))
	for _, idx := range needsSummary {
		go func(i int) {
			sem <- struct{}{}
			defer func() { <-sem }()
			summarized, err := s.mediaSummaryFunc(ctx, msgs[i])
			results <- indexedResult{idx: i, msg: summarized, err: err}
		}(idx)
	}

	processed := make([]agent.Message, len(msgs))
	copy(processed, msgs)

	for range needsSummary {
		r := <-results
		if r.err != nil {
			if s.logger != nil {
				s.logger.Printf("token_summary: media summarization failed for message %d: %v", r.idx, r.err)
			}
			processed[r.idx] = stripNonTextBlocks(msgs[r.idx])
		} else {
			processed[r.idx] = r.msg
		}
	}

	return processed
}

// Close cancels in-flight summarization and waits for completion.
func (s *TokenSummary) Close() {
	s.cancel()
	_ = s.Flush(context.Background())
}

func (s *TokenSummary) Flush(ctx context.Context) error {
	s.mu.Lock()
	idle := s.idle
	s.mu.Unlock()
	select {
	case <-idle:
	case <-ctx.Done():
		return ctx.Err()
	}
	s.mu.Lock()
	err := s.flushErr
	s.flushErr = nil
	s.mu.Unlock()
	if flusher, ok := s.inner.(agent.Flusher); ok {
		err = errors.Join(err, flusher.Flush(ctx))
	}
	return err
}

func (s *TokenSummary) recordError(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	s.flushErr = errors.Join(s.flushErr, err)
	s.mu.Unlock()
}

func (s *TokenSummary) List(ctx context.Context) ([]string, error) {
	manager, ok := s.inner.(agent.ConversationManager)
	if !ok {
		return nil, fmt.Errorf("conversation: inner store does not support List")
	}
	return manager.List(ctx)
}

func (s *TokenSummary) Delete(ctx context.Context, conversationID string) error {
	manager, ok := s.inner.(agent.ConversationManager)
	if !ok {
		return fmt.Errorf("conversation: inner store does not support Delete")
	}
	return manager.Delete(ctx, conversationID)
}
