package conversation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/camilbinas/gude-agents/agent"
)

// compile-time checks
var (
	_ agent.ConversationStore = (*Summary)(nil)
	_ agent.Flusher           = (*Summary)(nil)
)

// SummaryFunc condenses a slice of messages into a user+assistant turn.
// The first element must be a user-role message containing the summary text.
// The second element must be an assistant-role acknowledgment message.
// This ensures the summarized conversation always starts with a user message
// and maintains strict alternation.
type SummaryFunc func(ctx context.Context, messages []agent.Message) ([2]agent.Message, error)

// SummaryOption configures optional behavior on a Summary strategy.
// Returns an error if the configuration is invalid.
type SummaryOption func(*Summary) error

// MediaSummaryFunc takes a message containing non-text content blocks and
// returns a text-only message with the same role. The returned message must
// contain only TextBlock content.
type MediaSummaryFunc func(ctx context.Context, msg agent.Message) (agent.Message, error)

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
// summarization triggers. Defaults to 80%.
func WithTriggerThreshold(pct int) SummaryOption {
	return func(s *Summary) error {
		if pct < 1 || pct > 100 {
			return fmt.Errorf("trigger threshold must be between 1 and 100, got %d", pct)
		}
		s.triggerPct = pct
		return nil
	}
}

// WithSummaryTimeout sets a per-summarization timeout. Each background
// summarization goroutine gets a context with this deadline. If the LLM
// call doesn't complete in time, the summarization is cancelled and the
// conversation is left unsummarized until the next trigger. Default: no timeout.
func WithSummaryTimeout(d time.Duration) SummaryOption {
	return func(s *Summary) error {
		if d <= 0 {
			return fmt.Errorf("summary timeout must be positive, got %s", d)
		}
		s.timeout = d
		return nil
	}
}

// WithMediaSummaryFunc sets a function that summarizes messages containing
// non-text content (images, documents) into text-only equivalents before
// the main SummaryFunc runs.
func WithMediaSummaryFunc(fn MediaSummaryFunc) SummaryOption {
	return func(s *Summary) error {
		if fn == nil {
			return fmt.Errorf("media summary function must not be nil")
		}
		s.mediaSummaryFunc = fn
		return nil
	}
}

// WithMediaSummaryConcurrency sets the maximum number of parallel media
// summary calls. Defaults to 3 when not set.
func WithMediaSummaryConcurrency(n int) SummaryOption {
	return func(s *Summary) error {
		if n < 1 {
			return fmt.Errorf("media summary concurrency must be >= 1, got %d", n)
		}
		s.mediaSummaryConcurrency = n
		return nil
	}
}

// summaryState tracks per-conversation summarization progress.
type summaryState struct {
	cutoffIndex int
}

// Summary wraps a ConversationStore and triggers background summarization when the
// summarizable message count (total minus preserved) reaches the configured
// trigger percentage of the threshold.
type Summary struct {
	inner          agent.ConversationStore
	threshold      int
	triggerPct     int // percentage of threshold at which to trigger (default 80)
	summarize      SummaryFunc
	logger         agent.Logger
	preserveRecent int           // number of recent messages to always keep out of summarization
	timeout        time.Duration // per-summarization timeout; 0 = no timeout

	mediaSummaryFunc        MediaSummaryFunc
	mediaSummaryConcurrency int

	ctx    context.Context // cancelled by Close to stop in-flight summarization
	cancel context.CancelFunc

	mu             sync.Mutex
	summarizing    map[string]bool // per-conversation summarization lock
	summarizedAt   map[string]int  // message count after last summarization; re-triggers when exceeded
	pendingSummary map[string]*summaryState
	workDone       map[string]chan struct{} // closed when the accepted chain for a conversation completes
	flushErr       error
	pending        int
	idle           chan struct{} // closed whenever all accepted summary work is complete
}

// NewSummary creates a Summary strategy that triggers background summarization
// when the summarizable message count reaches the configured trigger percentage
// (default 80%) of the threshold. The threshold is specified in turns
// (user+assistant exchanges). Preserved messages are excluded from the trigger
// count — only the summarizable portion is compared against the threshold.
func NewSummary(inner agent.ConversationStore, threshold int, fn SummaryFunc, opts ...SummaryOption) (*Summary, error) {
	if inner == nil {
		return nil, fmt.Errorf("inner conversation must not be nil")
	}
	if fn == nil {
		return nil, fmt.Errorf("summary function must not be nil")
	}
	if threshold < 1 {
		return nil, fmt.Errorf("threshold must be at least 1 turn, got %d", threshold)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Summary{
		inner:          inner,
		threshold:      threshold * 2, // convert turns to individual messages
		triggerPct:     80,
		summarize:      fn,
		ctx:            ctx,
		cancel:         cancel,
		summarizing:    make(map[string]bool),
		summarizedAt:   make(map[string]int),
		pendingSummary: make(map[string]*summaryState),
		workDone:       make(map[string]chan struct{}),
		idle:           make(chan struct{}),
	}
	close(s.idle)
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
//	conversation.NewSummaryFunc(provider,
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

		resp, err := provider.Stream(ctx, agent.ModelRequest{
			System: systemPrompt,
			Messages: []agent.Message{{
				Role:    agent.RoleUser,
				Content: []agent.ContentBlock{agent.TextBlock{Text: sb.String()}},
			}},
		}, nil)
		if err != nil {
			return [2]agent.Message{}, fmt.Errorf("summary func: %w", err)
		}

		return [2]agent.Message{
			{
				Role:    agent.RoleUser,
				Content: []agent.ContentBlock{agent.TextBlock{Text: "[Automated summary of earlier messages in this conversation — not a new user message]\n\n" + resp.Text}},
			},
			{
				Role:    agent.RoleAssistant,
				Content: []agent.ContentBlock{agent.TextBlock{Text: "OK"}},
			},
		}, nil
	}
}

// DefaultSummaryFunc returns a SummaryFunc that uses the given Provider to
// condense messages into a single summary. This is the batteries-included
// default — pass it to NewSummary so you don't have to write your own.
func DefaultSummaryFunc(provider agent.Provider) SummaryFunc {
	return NewSummaryFunc(provider,
		"Summarize the following conversation into a single concise paragraph. "+
			"Preserve all key facts, names, and decisions.",
	)
}

// NewMediaSummaryFunc returns a MediaSummaryFunc that uses the given Provider
// and system prompt to summarize messages containing non-text content into
// text-only equivalents.
func NewMediaSummaryFunc(provider agent.Provider, systemPrompt string) MediaSummaryFunc {
	return func(ctx context.Context, msg agent.Message) (agent.Message, error) {
		resp, err := provider.Stream(ctx, agent.ModelRequest{
			System:   systemPrompt,
			Messages: []agent.Message{msg},
		}, nil)
		if err != nil {
			return agent.Message{}, fmt.Errorf("media summary: %w", err)
		}

		return agent.Message{
			Role:    msg.Role,
			Content: []agent.ContentBlock{agent.TextBlock{Text: resp.Text}},
		}, nil
	}
}

// DefaultMediaSummaryFunc returns a MediaSummaryFunc that uses the given
// Provider with a default system prompt to summarize non-text content.
func DefaultMediaSummaryFunc(provider agent.Provider) MediaSummaryFunc {
	return NewMediaSummaryFunc(provider,
		"Describe the non-text content in this message (images, documents) "+
			"as concise text. Preserve all key visual details, data, and context. "+
			"Combine with any existing text in the message into a single coherent summary.",
	)
}

// Load waits for summary work that was already accepted for this conversation,
// then returns the current state. Workers use inner.Load directly so they never
// wait on their own barrier.
func (s *Summary) Load(ctx context.Context, conversationID string) (agent.ConversationSnapshot, error) {
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

// triggerThreshold returns the number of summarizable messages (total minus
// preserved) at which summarization fires.
func (s *Summary) triggerThreshold() int {
	return (s.threshold * s.triggerPct) / 100
}

// Save delegates to the inner store, then checks whether background
// summarization should be triggered. The trigger compares the summarizable
// message count (total minus preserved) against the configured threshold
// percentage.
func (s *Summary) Save(ctx context.Context, conversationID string, msgs []agent.Message, expectedRevision uint64) (uint64, error) {
	revision, err := s.inner.Save(ctx, conversationID, msgs, expectedRevision)
	if err != nil {
		return 0, err
	}

	// Don't trigger summarization if closed.
	if s.ctx.Err() != nil {
		return revision, nil
	}

	// Approval and human-input checkpoints leave tool calls unresolved. Keep the
	// full transcript intact until every tool use has a corresponding result.
	if hasUnresolvedToolCalls(msgs) {
		return revision, nil
	}

	trigger := s.triggerThreshold()
	summarizable := len(msgs) - s.preserveRecent

	s.mu.Lock()
	if summarizable < trigger {
		delete(s.summarizedAt, conversationID)
		s.mu.Unlock()
		return revision, nil
	}

	if s.summarizing[conversationID] {
		s.mu.Unlock()
		return revision, nil
	}

	// Skip if the conversation hasn't grown since the last summarization.
	if lastCount, ok := s.summarizedAt[conversationID]; ok && len(msgs) <= lastCount {
		s.mu.Unlock()
		return revision, nil
	}
	s.summarizing[conversationID] = true
	cutoff := len(msgs)
	s.pendingSummary[conversationID] = &summaryState{cutoffIndex: cutoff}
	s.workDone[conversationID] = make(chan struct{})
	if s.pending == 0 {
		s.idle = make(chan struct{})
	}
	s.pending++
	s.mu.Unlock()

	go s.runSummarize(conversationID, cutoff)

	return revision, nil
}

// runSummarize performs background summarization for a conversation.
func (s *Summary) runSummarize(conversationID string, cutoff int) {
	ctx := s.ctx
	if s.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.timeout)
		defer cancel()
	}
	success := false
	reTriggered := false

	defer func() {
		s.mu.Lock()
		// A replacement worker inherits both the summarizing lock and the open
		// per-conversation barrier. Only the terminal worker releases them.
		if !reTriggered {
			delete(s.summarizing, conversationID)
			if done := s.workDone[conversationID]; done != nil {
				close(done)
				delete(s.workDone, conversationID)
			}
		}
		delete(s.pendingSummary, conversationID)
		if success && !reTriggered {
			s.summarizedAt[conversationID] = cutoff
		}
		s.pending--
		if s.pending == 0 {
			close(s.idle)
		}
		s.mu.Unlock()
	}()

	// Load the messages that existed when summarization was triggered.
	// We snapshot up to cutoff — anything beyond that arrived after the trigger.
	preSnapshot, err := s.inner.Load(ctx, conversationID)
	if err != nil {
		s.recordError(fmt.Errorf("summary: load %q: %w", conversationID, err))
		if s.logger != nil {
			s.logger.Printf("summary: failed to load messages for %s: %v", conversationID, err)
		}
		return
	}
	preSummarize := preSnapshot.Messages

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

	if summarizeUntil < len(preSummarize) && preSummarize[summarizeUntil].Role == agent.RoleAssistant {
		// Keep the tail at a complete user/assistant turn boundary instead of
		// dropping an assistant message during merge.
		summarizeUntil--
	}
	if summarizeUntil <= 0 {
		if s.logger != nil {
			s.logger.Printf("summary: skipping summarization for %s — no complete turn before preserve_recent window", conversationID)
		}
		return
	}

	// Preprocess media messages: summarize non-text content into text-only equivalents.
	toSummarize := preSummarize[:summarizeUntil]
	if s.mediaSummaryFunc != nil {
		toSummarize = s.preprocessMediaMessages(ctx, toSummarize)
	}

	// Call SummaryFunc on the messages up to summarizeUntil.
	// This may be slow (LLM call) — no locks held during this.
	summaryPair, err := s.summarize(ctx, toSummarize)
	if err != nil {
		s.recordError(fmt.Errorf("summary: summarize %q: %w", conversationID, err))
		if s.logger != nil {
			s.logger.Printf("summary: summarization failed for %s: %v", conversationID, err)
		}
		return
	}

	// Re-load immediately before each CAS attempt. If another writer wins after
	// the load, retry against its snapshot so no newly appended turn is lost.
	var newMsgs []agent.Message
	for attempt := 0; ; attempt++ {
		latest, loadErr := s.inner.Load(ctx, conversationID)
		if loadErr != nil {
			s.recordError(fmt.Errorf("summary: reload %q: %w", conversationID, loadErr))
			if s.logger != nil {
				s.logger.Printf("summary: failed to re-load messages for %s: %v", conversationID, loadErr)
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
					s.recordError(fmt.Errorf("summary: save %q after CAS retries: %w", conversationID, saveErr))
					return
				}
				continue
			}
			s.recordError(fmt.Errorf("summary: save %q: %w", conversationID, saveErr))
			if s.logger != nil {
				s.logger.Printf("summary: failed to save summarized messages for %s: %v", conversationID, saveErr)
			}
			return
		}
		break
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
	if summarizable >= trigger && summarizable > 2 && !hasUnresolvedToolCalls(newMsgs) {
		newCutoff := len(newMsgs)
		s.mu.Lock()
		reTriggered = true
		// summarizing[conv] stays true — the new goroutine inherits it.
		s.pendingSummary[conversationID] = &summaryState{cutoffIndex: newCutoff}
		s.pending++
		s.mu.Unlock()
		go s.runSummarize(conversationID, newCutoff)
	}
}

func mergeSummary(latest []agent.Message, summarizeUntil int, summaryPair [2]agent.Message) []agent.Message {
	preserveFrom := min(summarizeUntil, len(latest))
	tail := safeTruncate(latest, preserveFrom)
	newMsgs := make([]agent.Message, 0, 2+len(tail))
	newMsgs = append(newMsgs, summaryPair[0], summaryPair[1])
	return append(newMsgs, tail...)
}

// hasUnresolvedToolCalls reports whether any requested tool use lacks a later
// result with the same ID. Results only resolve earlier unmatched uses.
func hasUnresolvedToolCalls(msgs []agent.Message) bool {
	unmatched := make(map[string]int)
	unresolved := 0
	for _, msg := range msgs {
		for _, block := range msg.Content {
			switch block := block.(type) {
			case agent.ToolUseBlock:
				unmatched[block.ToolUseID]++
				unresolved++
			case agent.ToolResultBlock:
				if unmatched[block.ToolUseID] == 0 {
					continue
				}
				unmatched[block.ToolUseID]--
				unresolved--
				if unmatched[block.ToolUseID] == 0 {
					delete(unmatched, block.ToolUseID)
				}
			}
		}
	}
	return unresolved > 0
}

// hasNonTextContent reports whether msg contains any ImageBlock or DocumentBlock.
func hasNonTextContent(msg agent.Message) bool {
	for _, block := range msg.Content {
		switch block.(type) {
		case agent.ImageBlock, agent.DocumentBlock:
			return true
		}
	}
	return false
}

// stripNonTextBlocks returns a new message with the same Role containing only
// TextBlock content. If no text blocks exist, Content is nil.
func stripNonTextBlocks(msg agent.Message) agent.Message {
	var textBlocks []agent.ContentBlock
	for _, b := range msg.Content {
		if _, ok := b.(agent.TextBlock); ok {
			textBlocks = append(textBlocks, b)
		}
	}
	return agent.Message{
		Role:    msg.Role,
		Content: textBlocks,
	}
}

// preprocessMediaMessages summarizes messages containing non-text content
// (images, documents) into text-only equivalents using the configured
// MediaSummaryFunc. Concurrency is bounded by mediaSummaryConcurrency.
func (s *Summary) preprocessMediaMessages(ctx context.Context, msgs []agent.Message) []agent.Message {
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

	// Semaphore limits concurrent media summary calls.
	concurrency := s.mediaSummaryConcurrency
	if concurrency < 1 {
		concurrency = 3 // default
	}
	sem := make(chan struct{}, concurrency)

	// Run media summaries concurrently with bounded parallelism.
	results := make(chan indexedResult, len(needsSummary))
	for _, idx := range needsSummary {
		go func(i int) {
			sem <- struct{}{}        // acquire
			defer func() { <-sem }() // release
			summarized, err := s.mediaSummaryFunc(ctx, msgs[i])
			results <- indexedResult{idx: i, msg: summarized, err: err}
		}(idx)
	}

	// Collect results into a copy of the message slice.
	processed := make([]agent.Message, len(msgs))
	copy(processed, msgs)

	for range needsSummary {
		r := <-results
		if r.err != nil {
			// Fallback: strip non-text blocks, keep text
			if s.logger != nil {
				s.logger.Printf("summary: media summarization failed for message %d: %v", r.idx, r.err)
			}
			processed[r.idx] = stripNonTextBlocks(msgs[r.idx])
		} else {
			processed[r.idx] = r.msg
		}
	}

	return processed
}

// Close cancels in-flight summarization and waits for it to finish.
func (s *Summary) Close() {
	s.cancel()
	_ = s.Flush(context.Background())
}

// Flush waits for in-flight summarization and reports background or nested
// store failures. Context cancellation bounds the wait.
func (s *Summary) Flush(ctx context.Context) error {
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

func (s *Summary) recordError(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	s.flushErr = errors.Join(s.flushErr, err)
	s.mu.Unlock()
}

func (s *Summary) List(ctx context.Context) ([]string, error) {
	manager, ok := s.inner.(agent.ConversationManager)
	if !ok {
		return nil, fmt.Errorf("conversation: inner store does not support List")
	}
	return manager.List(ctx)
}

func (s *Summary) Delete(ctx context.Context, conversationID string) error {
	manager, ok := s.inner.(agent.ConversationManager)
	if !ok {
		return fmt.Errorf("conversation: inner store does not support Delete")
	}
	return manager.Delete(ctx, conversationID)
}
