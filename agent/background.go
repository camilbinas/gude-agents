// Background_Tools: In-Memory Lifecycle (v1)
//
// Pending Background_Dispatches live in process memory only. If the process
// exits while handlers are in flight, those handlers are abandoned and their
// results are lost.
//
// Durable persistence of pending Background_Dispatches across process restarts
// is an explicit non-goal for v1.
//
// Durable ExecutionStore records created for re-entry model turns are distinct
// child invocations. Dispatch/completion tracking and deduplication remain
// local to this process; they are not restart-durable in v1.
//
// Streaming chunks from a Re_Entry_Turn are not delivered to the Notify_Callback
// in v1. The callback receives only the complete final assistant text. Streaming-
// aware notification is a future extension.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// backgroundDispatch captures all metadata needed to execute a Background_Handler
// and later inject its result back into the originating conversation.
type backgroundDispatch struct {
	toolName       string
	toolUseID      string
	conversationID string
	cfg            invocationConfig // originating invocation config (identity, scopes, principal, ...)
	rawInput       json.RawMessage
	handler        func(ctx context.Context, input json.RawMessage) (string, error)
	ack            string
	dispatchedAt   time.Time
}

// completionResult holds the outcome of a Background_Handler invocation.
type completionResult struct {
	result string
	err    error
}

// toMessage converts the completion into a user Message suitable for appending
// to conversation history so the LLM can react to the background result.
//
// The message is a plain TextBlock (not a ToolResultBlock) because the preceding
// assistant message in the conversation is the originating turn's final text
// response — not a ToolUseBlock. Providers like Bedrock validate that
// ToolResultBlocks correspond to ToolUseBlocks in the immediately preceding
// assistant turn, so we use a text message that describes the completion.
//
//   - Success (err == nil): text describes the tool result.
//   - Error (err != nil): text describes the error.
func (c completionResult) toMessage(toolUseID string) Message {
	var text string
	if c.err != nil {
		text = fmt.Sprintf("[Background tool %s failed: %s]", toolUseID, c.err.Error())
	} else {
		text = fmt.Sprintf("[Background tool %s completed: %s]", toolUseID, c.result)
	}
	return Message{
		Role:    RoleUser,
		Content: []ContentBlock{TextBlock{Text: text}},
	}
}

// backgroundRegistry manages in-flight Background_Dispatches for a single *Agent.
// It serializes Re_Entry_Turns per Conversation_ID via a per-conversation mutex map
// and tracks all in-flight goroutines so Agent.Shutdown can wait for them.
type backgroundRegistry struct {
	agent *Agent

	// wg tracks both in-flight handler goroutines and in-flight Re_Entry_Turn
	// goroutines so Agent.Shutdown blocks until everything completes.
	wg sync.WaitGroup

	// stateMu guards closing. dispatch adds to wg under stateMu so no new
	// work is registered once shutdown started.
	stateMu sync.Mutex
	closing bool

	// mu guards the locks and completed maps. It is held only during map
	// lookup/insertion, never across a protected critical section.
	mu        sync.Mutex
	locks     map[string]*sync.Mutex
	completed map[string]struct{}

	// notify is the Notify_Callback registered via WithBackgroundNotify, or nil.
	notify func(conversationID, agentMessage string)

	// logger is the fallback for panic recovery and internal errors when no
	// ToolLogObserver is configured.
	logger Logger
}

// newBackgroundRegistry creates a backgroundRegistry for the given agent.
// The optional notify callback (variadic for ergonomics) is stored on the registry;
// if not provided or nil, notification is a no-op.
func newBackgroundRegistry(a *Agent, notify func(conversationID, agentMessage string), logger Logger) *backgroundRegistry {
	return &backgroundRegistry{
		agent:     a,
		locks:     make(map[string]*sync.Mutex),
		completed: make(map[string]struct{}),
		notify:    notify,
		logger:    logger,
	}
}

// lockFor returns the per-conversation mutex for the given conversationID,
// lazily allocating one if it does not yet exist. The meta-mutex r.mu is held
// only for the map lookup/insert, never across the caller's critical section.
// Entries are never deleted; memory growth is bounded by the set of distinct
// Conversation_IDs the agent serves over its lifetime.
func (r *backgroundRegistry) lockFor(conversationID string) *sync.Mutex {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.locks[conversationID]
	if !ok {
		m = &sync.Mutex{}
		r.locks[conversationID] = m
	}
	return m
}

// claimCompletion permits one local completion re-entry per originating
// conversation/tool-use pair. It deliberately scopes deduplication to this
// process, matching v1's in-memory dispatch lifecycle.
func (r *backgroundRegistry) claimCompletion(d backgroundDispatch) bool {
	key := d.conversationID + "\x00" + d.toolUseID
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.completed[key]; exists {
		return false
	}
	r.completed[key] = struct{}{}
	return true
}

// ErrAgentShuttingDown is returned for background dispatches after Shutdown started.
var ErrAgentShuttingDown = errors.New("agent is shutting down")

// dispatch spawns a detached goroutine to run the Background_Handler and emits
// the dispatch log entry. The registry's WaitGroup is incremented so that
// Agent.Shutdown waits until the handler (and its Re_Entry_Turn) complete.
func (r *backgroundRegistry) dispatch(d backgroundDispatch) error {
	r.stateMu.Lock()
	if r.closing {
		r.stateMu.Unlock()
		return ErrAgentShuttingDown
	}
	r.wg.Go(func() { r.runHandler(d) })
	r.stateMu.Unlock()
	r.logBackgroundDispatch(d)
	return nil
}

// shutdown rejects new dispatches and waits for in-flight handlers and
// Re_Entry_Turns, bounded by ctx.
func (r *backgroundRegistry) shutdown(ctx context.Context) error {
	r.stateMu.Lock()
	r.closing = true
	r.stateMu.Unlock()

	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// runHandler executes the Background_Handler on context.Background(), recovers
// panics, records duration, emits the completion log entry, and schedules the
// Re_Entry_Turn goroutine. wg.Go for the Re_Entry_Turn is called before the
// handler goroutine returns so the registry counter never crosses zero between
// phases.
func (r *backgroundRegistry) runHandler(d backgroundDispatch) {
	start := time.Now()

	var (
		result string
		err    error
	)

	// Execute the handler with panic recovery (Req 4.3).
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				err = fmt.Errorf("background tool %q panicked: %v", d.toolName, rec)
			}
		}()
		result, err = d.handler(context.Background(), d.rawInput)
	}()

	r.logBackgroundCompletion(d, err, time.Since(start))

	completion := completionResult{result: result, err: err}

	// Schedule the Re_Entry_Turn. wg.Go is called before this goroutine returns
	// so the registry counter never crosses zero between phases (Req 13.2).
	r.wg.Go(func() {
		r.agent.reEntryTurn(d, completion)
	})
}

// ---------------------------------------------------------------------------
// Background-specific log helpers (Requirements 6.5, 8.4, 8.5, 12.1, 12.2)
// ---------------------------------------------------------------------------

// logBackgroundDispatch emits a ToolLogRecord for a Background_Dispatch and
// falls back to the registry logger when no ToolLogObserver is configured.
func (r *backgroundRegistry) logBackgroundDispatch(d backgroundDispatch) {
	msg := fmt.Sprintf("background dispatch: tool=%s conv=%s toolUseID=%s",
		d.toolName, d.conversationID, d.toolUseID)
	if r.agent != nil && r.agent.observeBackgroundLog(d, msg, nil, time.Since(d.dispatchedAt)) {
		return
	}
	if r.logger != nil {
		r.logger.Printf(msg)
	}
}

// logBackgroundCompletion emits a ToolLogRecord for handler completion.
func (r *backgroundRegistry) logBackgroundCompletion(d backgroundDispatch, err error, duration time.Duration) {
	status := "success"
	if err != nil {
		status = "error"
	}
	msg := fmt.Sprintf("background completion: tool=%s conv=%s toolUseID=%s status=%s duration=%s",
		d.toolName, d.conversationID, d.toolUseID, status, duration)
	if r.agent != nil && r.agent.observeBackgroundLog(d, msg, err, duration) {
		return
	}
	if r.logger != nil {
		r.logger.Printf(msg)
	}
}

// reEntryTurn runs an agent iteration in response to a Background_Completion.
// It goes through the shared invocation lifecycle (which acquires the
// Conversation_Lock) with the originating invocation's config.
func (a *Agent) reEntryTurn(d backgroundDispatch, completion completionResult) {
	// Without a registry, conversation store, or non-empty conversation ID no
	// re-entry is possible (unit tests may exercise handler dispatch in isolation).
	if a.backgroundRegistry == nil || a.conversation == nil || d.conversationID == "" {
		return
	}
	if !a.backgroundRegistry.claimCompletion(d) {
		return
	}

	base := NewContext(context.Background())
	base.cfg = d.cfg
	// Defense in depth: a re-entry always creates a new execution, even when
	// constructed directly rather than through dispatchBackground.
	base.cfg.executionID, base.cfg.executionIDSet = "", false
	base.cfg.executionResume, base.cfg.executionVersion = false, 0
	base.cfg.conversationID = d.conversationID
	c := base.forInvocation(base, &invocationRuntime{})

	res, err := a.lifecycle(c, d.conversationID, "", func(r *run) (Result, error) {
		boundary := uint64(0)
		if a.contextManager != nil {
			var err error
			boundary, err = a.contextManager.HistoryBoundary(r.c, r.convID)
			if err != nil {
				return Result{}, fmt.Errorf("re-entry context history boundary: %w", err)
			}
		}
		loadC, cf := r.h.onConversationStart(r.c, ConversationRecord{Operation: "load_after", ConversationID: r.convID})
		snapshot, err := a.conversation.LoadAfter(loadC, r.convID, boundary)
		cf.finish(err, len(snapshot.Messages), snapshot.Revision)
		if err != nil {
			return Result{}, fmt.Errorf("re-entry load: %w", err)
		}
		r.revision = snapshot.Revision
		r.lastSequence = snapshot.LastSequence
		r.historyBoundary = boundary
		r.persistedCount = len(snapshot.Messages)
		// Append the synthesized completion and persist it before re-entry.
		history := append(snapshot.Messages, completion.toMessage(d.toolUseID))
		if _, err := r.saveConversation(history, TokenUsage{}); err != nil {
			return Result{}, fmt.Errorf("re-entry pre-save: %w", err)
		}
		// Context-state refreshes do not change canonical revision or history, so
		// no post-flush reload is necessary before the re-entry provider call.
		cfg, err := r.inferenceConfig()
		if err != nil {
			return Result{}, err
		}
		return r.loop(history, -1, cfg)
	})
	if err != nil {
		a.logBackgroundError(d, "re-entry", err)
		return
	}
	if res.StopReason == StopInterrupt {
		a.logBackgroundError(d, "re-entry", fmt.Errorf("re-entry turn interrupted (%s, execution=%s)", res.Interrupt.Type, res.Interrupt.ExecutionID))
		return
	}
	a.backgroundRegistry.notifySafely(d.conversationID, res.Text)
}

// logBackgroundError emits a structured ToolLogRecord and falls back to the
// registry logger when no ToolLogObserver is configured.
func (a *Agent) logBackgroundError(d backgroundDispatch, phase string, err error) {
	msg := fmt.Sprintf("background error [%s]: conv=%s tool=%s err=%v",
		phase, d.conversationID, d.toolName, err)
	if a.observeBackgroundLog(d, msg, err, 0) {
		return
	}
	if a.backgroundRegistry != nil && a.backgroundRegistry.logger != nil {
		a.backgroundRegistry.logger.Printf(msg)
	}
}

func (a *Agent) observeBackgroundLog(d backgroundDispatch, message string, err error, duration time.Duration) bool {
	base := NewContext(context.Background())
	base.cfg = d.cfg
	base.cfg.conversationID = d.conversationID
	h := a.hooks(base)
	if !h.hasToolLogObserver() {
		return false
	}
	principal, _ := base.Principal()
	h.onToolLog(base, ToolLogRecord{
		CallID: d.toolUseID, Name: d.toolName, Message: message,
		Principal: principal, ConversationID: d.conversationID, Err: err, Duration: duration,
	})
	return true
}

// notifySafely invokes the Notify_Callback if configured, recovering from panics.
// No-op if r.notify is nil (Req 8.3). Panics inside the callback are recovered
// and reported through ToolLogObserver (or the registry's fallback logger)
// without propagating to any other goroutine (Req 8.4).
func (r *backgroundRegistry) notifySafely(convID, text string) {
	if r.notify == nil {
		return // Req 8.3: no-op if not registered
	}
	defer func() {
		if rec := recover(); rec != nil {
			// Req 8.4: recover panic, log without propagating.
			r.logNotifyPanic(convID, rec)
		}
	}()
	r.notify(convID, text) // Req 8.2: exactly once
}

// logNotifyPanic reports a callback panic through ToolLogObserver when
// available, otherwise through the registry logger.
func (r *backgroundRegistry) logNotifyPanic(convID string, recovered any) {
	message := fmt.Sprintf("notify callback panic conv=%s: %v", convID, recovered)
	if r.agent != nil {
		base := Background().WithConversationID(convID)
		h := r.agent.hooks(base)
		if h.hasToolLogObserver() {
			h.onToolLog(base, ToolLogRecord{Name: "background:notify", Message: message, ConversationID: convID})
			return
		}
	}
	if r.logger != nil {
		r.logger.Printf("background notify callback panic conv=%s: %v", convID, recovered)
	}
}
