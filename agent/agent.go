package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// ErrTokenBudgetExceeded is returned when cumulative token usage exceeds the configured budget.
// Documented in docs/agent-api.md — update when changing error text or behavior.
var ErrTokenBudgetExceeded = fmt.Errorf("token budget exceeded")

// Agent orchestrates LLM calls and tool execution.
// Documented in docs/agent-api.md — update when changing fields, constructor, or loop behavior.
type Agent struct {
	provider         Provider
	toolsMu          sync.RWMutex // protects tools and toolSpecs for safe runtime registration (unlikely but possible)
	tools            map[string]tool.Tool
	toolSpecs        []tool.Spec
	instructions     string
	maxIterations    int
	parallelTools    bool
	memory           Memory
	conversationID   string
	middlewares      []Middleware
	inputGuardrails  []InputGuardrail
	outputGuardrails []OutputGuardrail
	logger           Logger
	tokenBudget      int              // 0 = no budget
	retriever        Retriever        // nil = no RAG
	contextFormatter ContextFormatter // nil = use DefaultContextFormatter
	thinkingCallback ThinkingCallback // nil = discard thinking chunks
	syncMemory       bool             // if true, call Wait() on memory after each Save
	normStrategy     *NormStrategy    // nil = use default (Merge); pointer to distinguish "not set" from "set to Merge"
	normDisabled     bool             // true = skip normalization entirely
	tracingHook      TracingHook      // nil = no tracing
	loggerSet        bool             // true if WithLogger was explicitly called
	providerTimeout  time.Duration    // 0 = no timeout (default)
	retryMax         int              // 0 = no retry (default)
	retryBaseDelay   time.Duration    // base delay for exponential backoff
}

// New creates a new Agent. Returns an error if tool validation fails or an option errors.
// Documented in docs/agent-api.md — update when changing signature or validation logic.
func New(provider Provider, instructions prompt.Instructions, tools []tool.Tool, opts ...Option) (*Agent, error) {
	a := &Agent{
		provider:      provider,
		tools:         make(map[string]tool.Tool),
		instructions:  instructions.String(),
		maxIterations: 10,
	}

	// Register and validate tools.
	for _, t := range tools {
		if t.Spec.Name == "" || t.Spec.Description == "" || t.Handler == nil {
			return nil, fmt.Errorf("tool %q: name, description, and handler are required", t.Spec.Name)
		}
		if _, exists := a.tools[t.Spec.Name]; exists {
			return nil, fmt.Errorf("duplicate tool name: %q", t.Spec.Name)
		}
		a.tools[t.Spec.Name] = t
		a.toolSpecs = append(a.toolSpecs, t.Spec)
	}

	// Apply options.
	for _, opt := range opts {
		if err := opt(a); err != nil {
			return nil, err
		}
	}

	// Warn if the provider advertises missing capabilities for what's being requested.
	if cr, ok := provider.(CapabilityReporter); ok {
		caps := cr.Capabilities()
		if len(a.toolSpecs) > 0 && !caps.ToolUse {
			a.logf("[agent] WARNING: provider does not support tool use — tools will be ignored by the model")
		}
		if a.tokenBudget > 0 && !caps.TokenUsage {
			a.logf("[agent] WARNING: provider does not report token usage — token budget enforcement will not work")
		}
	}

	return a, nil
}

// logf logs a formatted message if a logger is configured.
func (a *Agent) logf(format string, v ...any) {
	if a.logger != nil {
		a.logger.Printf(format, v...)
	}
}

// InvokeStream runs the agent loop, streaming the final text answer via cb.
// It returns cumulative TokenUsage and nil on success, or an error on failure.
// If the agent calls the handoff tool, it returns ErrHandoffRequested — use
// GetHandoffRequest to retrieve the request and Agent.Resume to continue.
// Documented in docs/agent-api.md — update when changing signature, loop steps, or error conditions.
func (a *Agent) InvokeStream(ctx context.Context, userMessage string, cb StreamCallback) (TokenUsage, error) {
	// Reset per-invocation accumulator.
	var cumulative TokenUsage

	// Create a new InvocationContext if one isn't already attached.
	if GetInvocationContext(ctx) == nil {
		ic := NewInvocationContext()
		ctx = WithInvocationContext(ctx, ic)
	}

	// Resolve conversation ID: per-invocation override or agent default.
	convID := ResolveConversationID(ctx, a.conversationID)

	// Start the invoke span if tracing is enabled.
	var finishInvoke func(error, TokenUsage, string)
	if a.tracingHook != nil {
		var modelID string
		if mi, ok := a.provider.(ModelIdentifier); ok {
			modelID = mi.ModelId()
		}
		ctx, finishInvoke = a.tracingHook.OnInvokeStart(ctx, InvokeSpanParams{
			MaxIterations:  a.maxIterations,
			ModelID:        modelID,
			ConversationID: convID,
			UserMessage:    userMessage,
			SystemPrompt:   a.instructions,
		})
	}

	usage, err := a.invokeStreamInner(ctx, userMessage, convID, cb)
	cumulative = usage

	if finishInvoke != nil {
		finishInvoke(err, cumulative, "")
	}

	return cumulative, err
}

// invokeStreamInner contains the core InvokeStream logic, separated so that
// the tracing finish function in InvokeStream can capture the final error and usage.
func (a *Agent) invokeStreamInner(ctx context.Context, userMessage string, convID string, cb StreamCallback) (TokenUsage, error) {
	var cumulative TokenUsage

	// Apply input guardrails.
	msg := userMessage
	for _, g := range a.inputGuardrails {
		// Trace each input guardrail.
		guardrailCtx := ctx
		var finishGuardrail func(error, string)
		if a.tracingHook != nil {
			guardrailCtx, finishGuardrail = a.tracingHook.OnGuardrailStart(ctx, "input", msg)
		}

		var err error
		msg, err = g(guardrailCtx, msg)

		if finishGuardrail != nil {
			finishGuardrail(err, msg)
		}
		if err != nil {
			return cumulative, &GuardrailError{Direction: "input", Cause: err}
		}
	}

	// Load conversation history if memory is configured.
	var messages []Message
	if a.memory != nil {
		// Trace memory load.
		memCtx := ctx
		var finishMemLoad func(error)
		if a.tracingHook != nil {
			memCtx, finishMemLoad = a.tracingHook.OnMemoryStart(ctx, "load", convID)
		}

		history, err := a.memory.Load(memCtx, convID)

		if finishMemLoad != nil {
			finishMemLoad(err)
		}
		if err != nil {
			return cumulative, fmt.Errorf("memory load: %w", err)
		}
		messages = history
	}

	// Append the user message, optionally preceded by a RAG context exchange.
	// Retrieved context is injected as a separate user/assistant turn rather than
	// prepended to the user message, keeping untrusted content clearly isolated
	// from the actual user query. A synthetic assistant acknowledgement is required
	// because most providers enforce strictly alternating user/assistant turns.
	// ragOffset tracks how many ephemeral RAG messages were prepended so they
	// can be stripped before saving to memory.
	ragOffset := 0
	if a.retriever != nil {
		// Trace retriever call.
		retCtx := ctx
		var finishRetriever func(error, int)
		if a.tracingHook != nil {
			retCtx, finishRetriever = a.tracingHook.OnRetrieverStart(ctx, msg)
		}

		docs, err := a.retriever.Retrieve(retCtx, msg)

		if finishRetriever != nil {
			finishRetriever(err, len(docs))
		}
		if err != nil {
			return cumulative, fmt.Errorf("retriever: %w", err)
		}
		if len(docs) > 0 {
			formatter := a.contextFormatter
			if formatter == nil {
				formatter = DefaultContextFormatter
			}
			if contextStr := formatter(docs); contextStr != "" {
				messages = append(messages,
					Message{Role: RoleUser, Content: []ContentBlock{TextBlock{Text: contextStr}}},
					Message{Role: RoleAssistant, Content: []ContentBlock{TextBlock{Text: "Understood. I will use this context to answer your question."}}},
				)
				ragOffset = 2
			}
		}
	}
	messages = append(messages, Message{
		Role:    RoleUser,
		Content: []ContentBlock{TextBlock{Text: msg}},
	})

	return a.runLoop(ctx, convID, messages, ragOffset, a.instructions, cb)
}

// runLoop is the core agent iteration loop shared by InvokeStream and Resume.
// ragOffset is the number of ephemeral RAG context messages prepended to messages
// that should be stripped before saving to memory.
func (a *Agent) runLoop(ctx context.Context, convID string, messages []Message, ragOffset int, systemPrompt string, cb StreamCallback) (TokenUsage, error) {
	var cumulative TokenUsage

	for iteration := range a.maxIterations {
		a.logf("[agent] iteration %d", iteration+1)

		// Start iteration span.
		iterCtx := ctx
		var finishIteration func(toolCount int, isFinal bool)
		if a.tracingHook != nil {
			iterCtx, finishIteration = a.tracingHook.OnIterationStart(ctx, iteration+1)
		}

		// Stream text to the caller in real-time.
		hasOutputGuardrails := len(a.outputGuardrails) > 0
		var bufferedChunks []string

		streamCB := func(chunk string) {
			if hasOutputGuardrails {
				bufferedChunks = append(bufferedChunks, chunk)
			} else if cb != nil {
				cb(chunk)
			}
		}

		// Normalize messages before sending to provider.
		converseMessages := messages
		if !a.normDisabled {
			strategy := NormMerge
			if a.normStrategy != nil {
				strategy = *a.normStrategy
			}
			converseMessages = NormalizeMessages(messages, strategy)
		}

		// Start provider call span.
		providerCtx := iterCtx
		var finishProvider func(err error, usage TokenUsage, toolCallCount int, responseText string)
		if a.tracingHook != nil {
			providerCtx, finishProvider = a.tracingHook.OnProviderCallStart(iterCtx, ProviderCallParams{
				System:       systemPrompt,
				MessageCount: len(converseMessages),
			})
		}

		a.toolsMu.RLock()
		currentToolSpecs := make([]tool.Spec, len(a.toolSpecs))
		copy(currentToolSpecs, a.toolSpecs)
		a.toolsMu.RUnlock()

		resp, err := a.callProviderWithRetry(providerCtx, ConverseParams{
			Messages:         converseMessages,
			System:           systemPrompt,
			ToolConfig:       currentToolSpecs,
			ThinkingCallback: a.thinkingCallback,
		}, streamCB)

		if err != nil {
			if finishProvider != nil {
				finishProvider(err, TokenUsage{}, 0, "")
			}
			if finishIteration != nil {
				finishIteration(0, false)
			}
			// Providers return *ProviderError directly; pass through as-is.
			var pe *ProviderError
			if errors.As(err, &pe) {
				return cumulative, err
			}
			return cumulative, &ProviderError{Cause: err}
		}

		// Finish provider call span with usage and tool call count.
		if finishProvider != nil {
			finishProvider(nil, resp.Usage, len(resp.ToolCalls), resp.Text)
		}

		// Accumulate token usage.
		cumulative.InputTokens += resp.Usage.InputTokens
		cumulative.OutputTokens += resp.Usage.OutputTokens

		// Check budget after each provider call.
		if a.tokenBudget > 0 && cumulative.Total() > a.tokenBudget {
			if finishIteration != nil {
				finishIteration(0, false)
			}
			return cumulative, ErrTokenBudgetExceeded
		}

		// If there are tool calls, execute them and loop.
		if len(resp.ToolCalls) > 0 {
			assistantContent := make([]ContentBlock, 0, len(resp.ToolCalls))
			if resp.Text != "" {
				assistantContent = append(assistantContent, TextBlock{Text: resp.Text})
			}
			for _, tc := range resp.ToolCalls {
				assistantContent = append(assistantContent, ToolUseBlock{
					ToolUseID: tc.ToolUseID,
					Name:      tc.Name,
					Input:     tc.Input,
				})
			}
			messages = append(messages, Message{
				Role:    RoleAssistant,
				Content: assistantContent,
			})

			results := a.executeTools(iterCtx, resp.ToolCalls)

			// Finish iteration span with tool count.
			if finishIteration != nil {
				finishIteration(len(resp.ToolCalls), false)
			}

			// Check for human handoff before appending results.
			if isHandoffResult(results) {
				// Replace the handoff sentinel with a descriptive message
				// so the conversation is in a valid state for the LLM
				// (every tool_use must have a matching tool_result).
				for i, r := range results {
					if r.Content == handoffSentinelHuman {
						results[i].Content = "Paused — waiting for human input."
					}
				}
				resultBlocks := make([]ContentBlock, len(results))
				for i, r := range results {
					resultBlocks[i] = r
				}
				messages = append(messages, Message{
					Role:    RoleUser,
					Content: resultBlocks,
				})

				// Stash the conversation state in the HandoffRequest.
				if ic := GetInvocationContext(ctx); ic != nil {
					if hr, ok := GetHandoffRequest(ic); ok {
						hr.Messages = messages
						hr.ConversationID = convID
					}
				}
				// Save to memory so HTTP callers can resume in a different request.
				if a.memory != nil {
					_ = a.memory.Save(ctx, convID, messages[ragOffset:])
					if a.syncMemory {
						if w, ok := a.memory.(MemoryWaiter); ok {
							w.Wait()
						}
					}
				}
				return cumulative, ErrHandoffRequested
			}

			resultBlocks := make([]ContentBlock, len(results))
			for i, r := range results {
				resultBlocks[i] = r
			}
			messages = append(messages, Message{
				Role:    RoleUser,
				Content: resultBlocks,
			})
			continue
		}

		// Apply output guardrails before streaming to the caller.
		finalText := resp.Text
		for _, g := range a.outputGuardrails {
			// Trace each output guardrail.
			guardrailCtx := iterCtx
			var finishGuardrail func(error, string)
			if a.tracingHook != nil {
				guardrailCtx, finishGuardrail = a.tracingHook.OnGuardrailStart(iterCtx, "output", finalText)
			}

			var gErr error
			finalText, gErr = g(guardrailCtx, finalText)

			if finishGuardrail != nil {
				finishGuardrail(gErr, finalText)
			}
			if gErr != nil {
				if finishIteration != nil {
					finishIteration(0, true)
				}
				return cumulative, &GuardrailError{Direction: "output", Cause: gErr}
			}
		}

		// If guardrails were active, stream the result now.
		if hasOutputGuardrails && cb != nil {
			if finalText == resp.Text {
				for _, chunk := range bufferedChunks {
					cb(chunk)
				}
			} else {
				cb(finalText)
			}
		}

		// Append assistant response to messages for memory.
		messages = append(messages, Message{
			Role:    RoleAssistant,
			Content: []ContentBlock{TextBlock{Text: resp.Text}},
		})

		// Finish iteration span — this is the final iteration (no tool calls).
		if finishIteration != nil {
			finishIteration(0, true)
		}

		// Save conversation history if memory is configured.
		if a.memory != nil {
			// Trace memory save.
			memCtx := ctx
			var finishMemSave func(error)
			if a.tracingHook != nil {
				memCtx, finishMemSave = a.tracingHook.OnMemoryStart(ctx, "save", convID)
			}

			err := a.memory.Save(memCtx, convID, messages[ragOffset:])

			if finishMemSave != nil {
				finishMemSave(err)
			}
			if err != nil {
				return cumulative, fmt.Errorf("memory save: %w", err)
			}
			if a.syncMemory {
				if w, ok := a.memory.(MemoryWaiter); ok {
					w.Wait()
				}
			}
		}

		return cumulative, nil
	}

	// Max iterations exceeded — record event before returning error.
	if a.tracingHook != nil {
		a.tracingHook.OnMaxIterationsExceeded(ctx, a.maxIterations)
	}

	return cumulative, fmt.Errorf("max iterations (%d) exceeded", a.maxIterations)
}

// callProviderWithRetry calls ConverseStream with optional timeout and retry.
// If providerTimeout is set, each attempt gets its own deadline.
// If retryMax > 0, transient errors are retried with exponential backoff.
func (a *Agent) callProviderWithRetry(ctx context.Context, params ConverseParams, cb StreamCallback) (*ProviderResponse, error) {
	maxAttempts := 1 + a.retryMax
	var lastErr error

	for attempt := range maxAttempts {
		// Apply per-call timeout if configured.
		callCtx := ctx
		var cancel context.CancelFunc
		if a.providerTimeout > 0 {
			callCtx, cancel = context.WithTimeout(ctx, a.providerTimeout)
		}

		resp, err := a.provider.ConverseStream(callCtx, params, cb)

		if cancel != nil {
			cancel()
		}

		if err == nil {
			return resp, nil
		}

		lastErr = err

		// Don't retry on context cancellation from the caller (not our timeout).
		if ctx.Err() != nil {
			return nil, lastErr
		}

		// Don't retry on the last attempt.
		if attempt >= maxAttempts-1 {
			break
		}

		// Exponential backoff: baseDelay * 2^attempt.
		delay := a.retryBaseDelay << uint(attempt)
		a.logf("[agent] provider call failed (attempt %d/%d), retrying in %s: %v", attempt+1, maxAttempts, delay, err)

		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	return nil, lastErr
}

// ValidateToolInput checks that the JSON payload satisfies the tool's declared schema.
// It verifies all required fields are present and all enum-constrained fields have valid values.
func ValidateToolInput(schema map[string]any, input json.RawMessage) error {
	var payload map[string]any
	if err := json.Unmarshal(input, &payload); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}

	// Check required fields.
	if required, ok := schema["required"].([]any); ok {
		for _, r := range required {
			field, _ := r.(string)
			if _, present := payload[field]; !present {
				return fmt.Errorf("missing required field %q", field)
			}
		}
	}

	// Check enum constraints.
	if props, ok := schema["properties"].(map[string]any); ok {
		for fieldName, propRaw := range props {
			prop, _ := propRaw.(map[string]any)
			enumVals, hasEnum := prop["enum"].([]any)
			if !hasEnum {
				continue
			}
			val, present := payload[fieldName]
			if !present {
				continue
			}
			valid := false
			for _, ev := range enumVals {
				if fmt.Sprintf("%v", ev) == fmt.Sprintf("%v", val) {
					valid = true
					break
				}
			}
			if !valid {
				return fmt.Errorf("field %q value %v not in enum %v", fieldName, val, enumVals)
			}
		}
	}
	return nil
}

// executeTools runs tool calls either sequentially or in parallel,
// returning ToolResultBlocks in the same order as the input calls.
func (a *Agent) executeTools(ctx context.Context, calls []tool.Call) []ToolResultBlock {
	results := make([]ToolResultBlock, len(calls))

	exec := func(i int, tc tool.Call) {
		a.logf("[tool] %s", tc.Name)

		a.toolsMu.RLock()
		t, ok := a.tools[tc.Name]
		a.toolsMu.RUnlock()
		if !ok {
			results[i] = ToolResultBlock{
				ToolUseID: tc.ToolUseID,
				Content:   fmt.Sprintf("unknown tool: %s", tc.Name),
				IsError:   true,
			}
			return
		}

		// Start tool span.
		toolCtx := ctx
		var finishTool func(error, string)
		if a.tracingHook != nil {
			toolCtx, finishTool = a.tracingHook.OnToolStart(ctx, tc.Name, tc.Input)
		}

		// Validate tool input against the declared schema.
		if err := ValidateToolInput(t.Spec.InputSchema, tc.Input); err != nil {
			toolErr := &ToolError{ToolName: tc.Name, Cause: err}
			a.logf("[tool] %s validation error: %v", tc.Name, toolErr)
			if finishTool != nil {
				finishTool(err, "")
			}
			results[i] = ToolResultBlock{
				ToolUseID: tc.ToolUseID,
				Content:   toolErr.Error(),
				IsError:   true,
			}
			return
		}

		// Build the handler — wrap with middleware if configured.
		// Pass the context with the tool span through the middleware chain.
		handler := ChainMiddleware(
			func(ctx context.Context, toolName string, input json.RawMessage) (string, error) {
				return t.Handler(ctx, input)
			},
			a.middlewares...,
		)

		out, err := handler(toolCtx, tc.Name, tc.Input)
		if err != nil {
			toolErr := &ToolError{ToolName: tc.Name, Cause: err}
			a.logf("[tool] %s error: %v", tc.Name, toolErr)
			if finishTool != nil {
				finishTool(err, "")
			}
			results[i] = ToolResultBlock{
				ToolUseID: tc.ToolUseID,
				Content:   toolErr.Error(),
				IsError:   true,
			}
			return
		}

		if finishTool != nil {
			finishTool(nil, out)
		}

		a.logf("[tool] %s done", tc.Name)
		results[i] = ToolResultBlock{
			ToolUseID: tc.ToolUseID,
			Content:   out,
		}
	}

	if a.parallelTools {
		var wg sync.WaitGroup
		for i, tc := range calls {
			wg.Add(1)
			go func(i int, tc tool.Call) {
				defer wg.Done()
				exec(i, tc)
			}(i, tc)
		}
		wg.Wait()
	} else {
		for i, tc := range calls {
			exec(i, tc)
		}
	}

	return results
}

// Invoke is a convenience wrapper over InvokeStream that collects all
// streamed chunks into a single string and returns it along with cumulative TokenUsage.
// Documented in docs/agent-api.md — update when changing signature or return values.
func (a *Agent) Invoke(ctx context.Context, userMessage string) (string, TokenUsage, error) {
	var sb strings.Builder
	usage, err := a.InvokeStream(ctx, userMessage, func(chunk string) {
		sb.WriteString(chunk)
	})
	if err != nil {
		return "", usage, err
	}
	return sb.String(), usage, nil
}

// ---------------------------------------------------------------------------
// Accessor methods — used by subpackages (graph, swarm) that need read access
// to agent internals without touching unexported fields.
// ---------------------------------------------------------------------------

// Instructions returns the agent's system prompt string.
func (a *Agent) Instructions() string { return a.instructions }

// MaxIterations returns the configured maximum iterations per invocation.
func (a *Agent) MaxIterations() int { return a.maxIterations }

// Provider returns the agent's LLM provider.
func (a *Agent) Provider() Provider { return a.provider }

// ToolSpecs returns a snapshot of the tool specifications registered on this agent.
func (a *Agent) ToolSpecs() []tool.Spec {
	a.toolsMu.RLock()
	defer a.toolsMu.RUnlock()
	cp := make([]tool.Spec, len(a.toolSpecs))
	copy(cp, a.toolSpecs)
	return cp
}

// Close performs graceful cleanup. If the agent's memory implements MemoryWaiter
// (e.g. the Summary strategy), Close blocks until all background work is complete.
// Safe to call multiple times. No-op if no cleanup is needed.
func (a *Agent) Close() {
	if a.memory == nil {
		return
	}
	if w, ok := a.memory.(MemoryWaiter); ok {
		w.Wait()
	}
}

// OutputGuardrails returns the agent's output guardrails.
func (a *Agent) OutputGuardrails() []OutputGuardrail { return a.outputGuardrails }

// TokenBudget returns the configured token budget (0 = no budget).
func (a *Agent) TokenBudget() int { return a.tokenBudget }

// ParallelTools returns whether parallel tool execution is enabled.
func (a *Agent) ParallelTools() bool { return a.parallelTools }

// Middlewares returns the agent's middleware chain.
func (a *Agent) Middlewares() []Middleware { return a.middlewares }

// HasTool reports whether a tool with the given name is registered.
func (a *Agent) HasTool(name string) bool {
	a.toolsMu.RLock()
	defer a.toolsMu.RUnlock()
	_, ok := a.tools[name]
	return ok
}

// LookupTool returns the tool with the given name and true, or a zero Tool and false.
func (a *Agent) LookupTool(name string) (tool.Tool, bool) {
	a.toolsMu.RLock()
	defer a.toolsMu.RUnlock()
	t, ok := a.tools[name]
	return t, ok
}

// RegisterTool adds a tool to the agent. Returns an error if a tool with the
// same name is already registered.
func (a *Agent) RegisterTool(t tool.Tool) error {
	a.toolsMu.Lock()
	defer a.toolsMu.Unlock()
	if _, exists := a.tools[t.Spec.Name]; exists {
		return fmt.Errorf("duplicate tool name: %q", t.Spec.Name)
	}
	a.tools[t.Spec.Name] = t
	a.toolSpecs = append(a.toolSpecs, t.Spec)
	return nil
}
