// Background_Tools: In-Memory Lifecycle (v1)
//
// Pending Background_Dispatches live in process memory only. If the process
// exits while handlers are in flight, those handlers are abandoned and their
// results are lost.
//
// Durable persistence of pending Background_Dispatches across process restarts
// is an explicit non-goal for v1.
//
// Streaming chunks from a Re_Entry_Turn are not delivered to the Notify_Callback
// in v1. The callback receives only the complete final assistant text. Streaming-
// aware notification is a future extension.
package agent

import (
	"context"
	"encoding/json"
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
	identifier     string
	scopes         map[string]string
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
// and tracks all in-flight goroutines so Agent.Close can wait for them.
type backgroundRegistry struct {
	agent *Agent

	// wg tracks both in-flight handler goroutines and in-flight Re_Entry_Turn
	// goroutines so Agent.Close blocks until everything completes.
	wg sync.WaitGroup

	// mu guards the locks map. It is held only during map lookup/insertion,
	// never across the protected critical section.
	mu    sync.Mutex
	locks map[string]*sync.Mutex

	// notify is the Notify_Callback registered via WithBackgroundNotify, or nil.
	notify func(conversationID, agentMessage string)

	// logger is used for panic recovery and error messages when no LoggingHook is set.
	logger Logger
}

// newBackgroundRegistry creates a backgroundRegistry for the given agent.
// The optional notify callback (variadic for ergonomics) is stored on the registry;
// if not provided or nil, notification is a no-op.
func newBackgroundRegistry(a *Agent, notify func(conversationID, agentMessage string), logger Logger) *backgroundRegistry {
	return &backgroundRegistry{
		agent:  a,
		locks:  make(map[string]*sync.Mutex),
		notify: notify,
		logger: logger,
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

// dispatch spawns a detached goroutine to run the Background_Handler and emits
// the dispatch log entry. The registry's WaitGroup is incremented so that
// Agent.Close blocks until the handler (and its subsequent Re_Entry_Turn) complete.
func (r *backgroundRegistry) dispatch(d backgroundDispatch) {
	r.wg.Go(func() { r.runHandler(d) })
	r.logBackgroundDispatch(d)
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

// logBackgroundDispatch emits a logging entry for a Background_Dispatch.
// If a LoggingHook is configured on the agent, it uses OnToolLog; otherwise it
// falls back to the registry's logger (mirroring tool.NewAsync's fallback).
func (r *backgroundRegistry) logBackgroundDispatch(d backgroundDispatch) {
	msg := fmt.Sprintf("background dispatch: tool=%s conv=%s toolUseID=%s",
		d.toolName, d.conversationID, d.toolUseID)
	if r.agent != nil && r.agent.loggingHook != nil {
		r.agent.loggingHook.OnToolLog(d.toolName, msg)
		return
	}
	if r.logger != nil {
		r.logger.Printf(msg)
	}
}

// logBackgroundCompletion emits a logging entry for a Background_Handler completion.
// If a LoggingHook is configured on the agent, it uses OnToolLog; otherwise it
// falls back to the registry's logger (mirroring tool.NewAsync's fallback).
func (r *backgroundRegistry) logBackgroundCompletion(d backgroundDispatch, err error, duration time.Duration) {
	status := "success"
	if err != nil {
		status = "error"
	}
	msg := fmt.Sprintf("background completion: tool=%s conv=%s toolUseID=%s status=%s duration=%s",
		d.toolName, d.conversationID, d.toolUseID, status, duration)
	if r.agent != nil && r.agent.loggingHook != nil {
		r.agent.loggingHook.OnToolLog(d.toolName, msg)
		return
	}
	if r.logger != nil {
		r.logger.Printf(msg)
	}
}

// reEntryTurn runs an agent iteration in response to a Background_Completion.
// Caller must NOT hold the Conversation_Lock; reEntryTurn acquires it itself.
func (a *Agent) reEntryTurn(d backgroundDispatch, completion completionResult) {
	// Guard: if the registry or conversation store is not wired, we cannot
	// perform a re-entry turn. This can happen in unit tests that exercise
	// handler dispatch in isolation without a Conversation_Store.
	if a.backgroundRegistry == nil || a.conversation == nil {
		return
	}

	// Build a fresh *Context from context.Background() with the captured identifier
	// and scopes so memory subsystems see the same scoping identity as the
	// originating turn (Req 3.3, 6.6).
	ctx := NewContext(context.Background()).
		WithConversationID(d.conversationID).
		WithIdentifier(d.identifier)
	for k, v := range d.scopes {
		ctx.SetScope(k, v)
	}

	h := a.hooks(ctx)

	// Acquire the Conversation_Lock for the originating Conversation_ID (Req 7.1, 7.2, 7.3).
	m := a.backgroundRegistry.lockFor(d.conversationID)
	m.Lock()
	defer m.Unlock()

	// Tracing: distinguish a Re_Entry_Turn (Req 12.3).
	invokeParams := a.invokeParams(d.conversationID, "", ctx)
	invokeParams.UserMessage = ""
	ctx, finishInvoke := h.onInvokeStart(ctx, invokeParams)

	// Load existing history.
	loadC, cf := h.onConversationStart(ctx, "load", d.conversationID)
	history, err := a.conversation.Load(loadC, d.conversationID)
	cf.finish(err, len(history))
	if err != nil {
		a.logBackgroundError(d, "re-entry load", err)
		finishInvoke.finish(err, TokenUsage{})
		return
	}

	// Append the synthesized tool result for the Background_Completion (Req 5.1–5.5).
	history = append(history, completion.toMessage(d.toolUseID))

	// Persist before re-entry (Req 5.4).
	if err := a.saveConversation(ctx, d.conversationID, history, TokenUsage{}, &h); err != nil {
		a.logBackgroundError(d, "re-entry pre-save", err)
		finishInvoke.finish(err, TokenUsage{})
		return
	}

	// Reuse the same iteration loop the user-initiated turn uses (Req 6.1, 6.2, 6.4).
	mergedCfg := mergeInferenceConfig(a.inferenceConfig, ctx.InferenceConfig())
	cb := func(string) {} // streaming chunks are accumulated, not streamed (Req 11.1)
	usage, finalText, err := a.runLoop(
		ctx, d.conversationID, history, 0,
		a.Instructions(), mergedCfg, cb, &h, nil, a.cachingEnabled,
	)
	finishInvoke.finish(err, usage)

	if err != nil {
		// Req 8.5: on failure, log and skip Notify_Callback.
		a.logBackgroundError(d, "re-entry runLoop", err)
		return
	}

	// Req 8.2 / 8.3: invoke Notify_Callback if configured.
	a.backgroundRegistry.notifySafely(d.conversationID, finalText)
}

// logBackgroundError emits a logging entry for a background error tagged with
// the failure phase and the affected Conversation_ID. If a LoggingHook is
// configured on the agent, it uses OnToolLog; otherwise it falls back to the
// registry's logger (mirroring tool.NewAsync's fallback).
func (a *Agent) logBackgroundError(d backgroundDispatch, phase string, err error) {
	msg := fmt.Sprintf("background error [%s]: conv=%s tool=%s err=%v",
		phase, d.conversationID, d.toolName, err)
	if a.loggingHook != nil {
		a.loggingHook.OnToolLog(d.toolName, msg)
		return
	}
	if a.backgroundRegistry != nil && a.backgroundRegistry.logger != nil {
		a.backgroundRegistry.logger.Printf(msg)
	}
}

// notifySafely invokes the Notify_Callback if configured, recovering from panics.
// No-op if r.notify is nil (Req 8.3). Panics inside the callback are recovered
// and logged via the agent's LoggingHook (or the registry's fallback logger)
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

// logNotifyPanic logs a Notify_Callback panic via the agent's LoggingHook if
// available, otherwise falls back to the registry's logger.
func (r *backgroundRegistry) logNotifyPanic(convID string, recovered any) {
	if r.agent != nil && r.agent.loggingHook != nil {
		// LoggingHook is set — use OnToolLog as the closest ad-hoc log channel.
		r.agent.loggingHook.OnToolLog("background:notify",
			fmt.Sprintf("notify callback panic conv=%s: %v", convID, recovered))
		return
	}
	if r.logger != nil {
		r.logger.Printf("background notify callback panic conv=%s: %v", convID, recovered)
	}
}
