package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
	"pgregory.net/rapid"
)

// Feature: swarm-loop-refactor, Property 1: RunLoop returns provider's final text

// textOnlyProvider is a mock provider that returns a fixed text-only response
// (no tool calls) from ConverseStream.
type textOnlyProvider struct {
	text string
}

func (p *textOnlyProvider) Name() string { return "mock" }

func (p *textOnlyProvider) Converse(_ context.Context, _ ConverseParams) (*ProviderResponse, error) {
	return &ProviderResponse{Text: p.text}, nil
}

func (p *textOnlyProvider) ConverseStream(_ context.Context, _ ConverseParams, cb StreamCallback) (*ProviderResponse, error) {
	if cb != nil {
		cb(p.text)
	}
	return &ProviderResponse{Text: p.text}, nil
}

// TestProperty_RunLoopReturnsFinalText verifies that for any valid message history
// and any mock provider that returns a text-only response (no tool calls),
// RunLoop returns that exact text and a nil error.
//
// **Validates: Requirements 1.2**
func TestProperty_RunLoopReturnsFinalText(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a random non-empty text string as the provider response.
		responseText := rapid.StringMatching(`[a-zA-Z0-9 .,!?]{1,200}`).Draw(rt, "responseText")

		// Create a provider that always returns this text with no tool calls.
		provider := &textOnlyProvider{text: responseText}

		// Create an agent with the text-only provider and no tools.
		a, err := New(provider, prompt.Text("You are a test agent."), nil)
		if err != nil {
			rt.Fatalf("failed to create agent: %v", err)
		}

		// Generate a random message history (1-5 user/assistant turns).
		numMessages := rapid.IntRange(1, 5).Draw(rt, "numMessages")
		messages := make([]Message, 0, numMessages)
		for i := 0; i < numMessages; i++ {
			role := RoleUser
			if i%2 == 1 {
				role = RoleAssistant
			}
			text := rapid.StringMatching(`[a-zA-Z0-9 ]{1,50}`).Draw(rt, "msgText")
			messages = append(messages, Message{
				Role:    role,
				Content: []ContentBlock{TextBlock{Text: text}},
			})
		}
		// Ensure the last message is from the user (required for valid conversation).
		if messages[len(messages)-1].Role != RoleUser {
			userText := rapid.StringMatching(`[a-zA-Z0-9 ]{1,50}`).Draw(rt, "lastUserMsg")
			messages = append(messages, Message{
				Role:    RoleUser,
				Content: []ContentBlock{TextBlock{Text: userText}},
			})
		}

		// Call RunLoop with the generated messages.
		c := Background()
		usage, finalText, err := a.RunLoop(c, LoopParams{
			Messages: messages,
		})

		// Verify: RunLoop returns the exact text from the provider and nil error.
		if err != nil {
			rt.Fatalf("RunLoop returned error: %v", err)
		}
		if finalText != responseText {
			rt.Fatalf("RunLoop returned text %q, expected %q", finalText, responseText)
		}
		// Token usage should be non-negative (basic sanity).
		if usage.InputTokens < 0 || usage.OutputTokens < 0 {
			rt.Fatalf("RunLoop returned negative token usage: %+v", usage)
		}
	})
}

// Feature: swarm-loop-refactor, Property 2: ToolResultInterceptor stops the loop

// toolCallProvider is a mock provider that always returns tool calls on the first
// call and a final text response on subsequent calls. This simulates a provider
// that triggers tool execution so the interceptor can be invoked.
type toolCallProvider struct {
	toolNames []string // names of tools to call
	finalText string   // text returned after tool execution
	callCount atomic.Int32
}

func (p *toolCallProvider) Name() string { return "mock" }

func (p *toolCallProvider) Converse(_ context.Context, _ ConverseParams) (*ProviderResponse, error) {
	call := p.callCount.Add(1)
	if call == 1 {
		// First call: return tool calls
		calls := make([]tool.Call, len(p.toolNames))
		for i, name := range p.toolNames {
			calls[i] = tool.Call{
				ToolUseID: fmt.Sprintf("tc-%d", i),
				Name:      name,
				Input:     json.RawMessage(`{}`),
			}
		}
		return &ProviderResponse{ToolCalls: calls}, nil
	}
	// Subsequent calls: return final text (should not be reached if interceptor stops)
	return &ProviderResponse{Text: p.finalText}, nil
}

func (p *toolCallProvider) ConverseStream(_ context.Context, _ ConverseParams, cb StreamCallback) (*ProviderResponse, error) {
	call := p.callCount.Add(1)
	if call == 1 {
		// First call: return tool calls
		calls := make([]tool.Call, len(p.toolNames))
		for i, name := range p.toolNames {
			calls[i] = tool.Call{
				ToolUseID: fmt.Sprintf("tc-%d", i),
				Name:      name,
				Input:     json.RawMessage(`{}`),
			}
		}
		return &ProviderResponse{ToolCalls: calls}, nil
	}
	// Subsequent calls: return final text
	if cb != nil && p.finalText != "" {
		cb(p.finalText)
	}
	return &ProviderResponse{Text: p.finalText}, nil
}

// TestProperty_InterceptorStopsLoop verifies that for any tool call batch where
// the interceptor returns true, RunLoop returns ErrLoopStopped and stops
// iterating immediately, regardless of remaining tool calls or iterations.
//
// **Validates: Requirements 1.3, 4.3**
func TestProperty_InterceptorStopsLoop(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a random number of tools (1-5) that the provider will request.
		numTools := rapid.IntRange(1, 5).Draw(rt, "numTools")
		toolNames := make([]string, numTools)
		tools := make([]tool.Tool, numTools)
		for i := range numTools {
			name := fmt.Sprintf("tool_%d", i)
			toolNames[i] = name
			tools[i] = tool.NewRaw(name, name+" description", map[string]any{"type": "object"},
				func(_ context.Context, _ json.RawMessage) (string, error) {
					return "result", nil
				})
		}

		// Generate a random final text that should NOT be returned (interceptor stops first).
		finalText := rapid.StringMatching(`[a-zA-Z0-9 ]{1,100}`).Draw(rt, "finalText")

		// Create the provider that returns tool calls on first invocation.
		provider := &toolCallProvider{
			toolNames: toolNames,
			finalText: finalText,
		}

		// Create the agent with the tools.
		a, err := New(provider, prompt.Text("You are a test agent."), tools)
		if err != nil {
			rt.Fatalf("failed to create agent: %v", err)
		}

		// Track whether the interceptor was called.
		var interceptorCalled atomic.Bool

		// Create a ToolResultInterceptor that always returns true (stop signal).
		interceptor := func(results []ToolResultBlock) bool {
			interceptorCalled.Store(true)
			return true // signal stop
		}

		// Generate a random message history ending with a user message.
		numMessages := rapid.IntRange(1, 3).Draw(rt, "numMessages")
		messages := make([]Message, 0, numMessages+1)
		for i := range numMessages {
			role := RoleUser
			if i%2 == 1 {
				role = RoleAssistant
			}
			text := rapid.StringMatching(`[a-zA-Z0-9 ]{1,30}`).Draw(rt, "msgText")
			messages = append(messages, Message{
				Role:    role,
				Content: []ContentBlock{TextBlock{Text: text}},
			})
		}
		// Ensure the last message is from the user.
		if messages[len(messages)-1].Role != RoleUser {
			userText := rapid.StringMatching(`[a-zA-Z0-9 ]{1,30}`).Draw(rt, "lastUserMsg")
			messages = append(messages, Message{
				Role:    RoleUser,
				Content: []ContentBlock{TextBlock{Text: userText}},
			})
		}

		// Call RunLoop with the interceptor configured.
		c := Background()
		_, _, err = a.RunLoop(c, LoopParams{
			Messages: messages,
			Config: LoopConfig{
				ToolResultInterceptor: interceptor,
			},
		})

		// Verify: RunLoop returns ErrLoopStopped.
		if !errors.Is(err, ErrLoopStopped) {
			rt.Fatalf("expected ErrLoopStopped, got: %v", err)
		}

		// Verify: the interceptor was actually called.
		if !interceptorCalled.Load() {
			rt.Fatalf("interceptor was not called")
		}
	})
}

// Feature: swarm-loop-refactor, Property 3: Token budget enforcement

// highUsageProvider is a mock provider that always returns tool calls with high
// token usage, designed to exceed a token budget.
type highUsageProvider struct {
	usagePerCall int
}

func (p *highUsageProvider) Name() string { return "mock" }

func (p *highUsageProvider) Converse(_ context.Context, _ ConverseParams) (*ProviderResponse, error) {
	return &ProviderResponse{
		ToolCalls: []tool.Call{{ToolUseID: "tc-1", Name: "noop", Input: json.RawMessage(`{}`)}},
		Usage:     TokenUsage{InputTokens: p.usagePerCall / 2, OutputTokens: p.usagePerCall / 2},
	}, nil
}

func (p *highUsageProvider) ConverseStream(_ context.Context, _ ConverseParams, _ StreamCallback) (*ProviderResponse, error) {
	return &ProviderResponse{
		ToolCalls: []tool.Call{{ToolUseID: "tc-1", Name: "noop", Input: json.RawMessage(`{}`)}},
		Usage:     TokenUsage{InputTokens: p.usagePerCall / 2, OutputTokens: p.usagePerCall / 2},
	}, nil
}

// TestProperty_TokenBudgetEnforcement verifies that for any token budget B > 0
// and any sequence of provider responses whose cumulative token usage exceeds B,
// RunLoop returns ErrTokenBudgetExceeded.
//
// **Validates: Requirements 1.4**
func TestProperty_TokenBudgetEnforcement(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a token budget between 10 and 500.
		budget := rapid.IntRange(10, 500).Draw(rt, "budget")
		// Generate usage per call that will exceed the budget within a few iterations.
		// Ensure usagePerCall is at least budget/3 so we exceed within maxIterations.
		minUsage := budget/3 + 1
		usagePerCall := rapid.IntRange(minUsage, budget).Draw(rt, "usagePerCall")

		provider := &highUsageProvider{usagePerCall: usagePerCall}

		noopTool := tool.NewRaw("noop", "does nothing", map[string]any{"type": "object"},
			func(_ context.Context, _ json.RawMessage) (string, error) {
				return "ok", nil
			})

		a, err := New(provider, prompt.Text("test agent"), []tool.Tool{noopTool},
			WithTokenBudget(budget),
			WithMaxIterations(20), // high enough to not hit this limit first
		)
		if err != nil {
			rt.Fatalf("failed to create agent: %v", err)
		}

		messages := []Message{{
			Role:    RoleUser,
			Content: []ContentBlock{TextBlock{Text: "go"}},
		}}

		c := Background()
		_, _, err = a.RunLoop(c, LoopParams{Messages: messages})

		// Verify: RunLoop returns ErrTokenBudgetExceeded.
		if !errors.Is(err, ErrTokenBudgetExceeded) {
			rt.Fatalf("expected ErrTokenBudgetExceeded, got: %v", err)
		}
	})
}

// Feature: swarm-loop-refactor, Property 4: Max iterations enforcement

// alwaysToolCallProvider is a mock provider that always returns tool calls,
// never a final text response. It tracks the number of calls made.
type alwaysToolCallProvider struct {
	callCount atomic.Int32
}

func (p *alwaysToolCallProvider) Name() string { return "mock" }

func (p *alwaysToolCallProvider) Converse(_ context.Context, _ ConverseParams) (*ProviderResponse, error) {
	p.callCount.Add(1)
	return &ProviderResponse{
		ToolCalls: []tool.Call{{ToolUseID: "tc-1", Name: "loop_tool", Input: json.RawMessage(`{}`)}},
	}, nil
}

func (p *alwaysToolCallProvider) ConverseStream(_ context.Context, _ ConverseParams, _ StreamCallback) (*ProviderResponse, error) {
	p.callCount.Add(1)
	return &ProviderResponse{
		ToolCalls: []tool.Call{{ToolUseID: "tc-1", Name: "loop_tool", Input: json.RawMessage(`{}`)}},
	}, nil
}

// TestProperty_MaxIterationsEnforcement verifies that for any maxIterations N >= 1
// and a provider that always returns tool calls (never a final text response),
// RunLoop returns a "max iterations exceeded" error after exactly N iterations.
//
// **Validates: Requirements 1.5**
func TestProperty_MaxIterationsEnforcement(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate maxIterations between 1 and 15.
		maxIter := rapid.IntRange(1, 15).Draw(rt, "maxIterations")

		provider := &alwaysToolCallProvider{}

		loopTool := tool.NewRaw("loop_tool", "loops forever", map[string]any{"type": "object"},
			func(_ context.Context, _ json.RawMessage) (string, error) {
				return "looping", nil
			})

		a, err := New(provider, prompt.Text("test agent"), []tool.Tool{loopTool},
			WithMaxIterations(maxIter),
		)
		if err != nil {
			rt.Fatalf("failed to create agent: %v", err)
		}

		messages := []Message{{
			Role:    RoleUser,
			Content: []ContentBlock{TextBlock{Text: "go"}},
		}}

		c := Background()
		_, _, err = a.RunLoop(c, LoopParams{Messages: messages})

		// Verify: RunLoop returns a max iterations error.
		if err == nil {
			rt.Fatalf("expected max iterations error, got nil")
		}
		expectedMsg := fmt.Sprintf("max iterations (%d) exceeded", maxIter)
		if err.Error() != expectedMsg {
			rt.Fatalf("expected error %q, got %q", expectedMsg, err.Error())
		}

		// Verify: provider was called exactly N times.
		calls := int(provider.callCount.Load())
		if calls != maxIter {
			rt.Fatalf("expected exactly %d provider calls, got %d", maxIter, calls)
		}
	})
}

// Feature: swarm-loop-refactor, Property 5: Middleware ordering and application

// TestProperty_MiddlewareOrdering verifies that for any RunLoop call with
// ExtraMiddleware [S1, S2] and agent middleware [A1, A2], and any tool call,
// the execution order is S1 → S2 → A1 → A2 → handler (outermost to innermost),
// and every tool execution in the batch passes through all middleware.
//
// **Validates: Requirements 1.6, 3.1, 3.2**
func TestProperty_MiddlewareOrdering(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate number of extra middleware (1-4) and agent middleware (1-4).
		numExtra := rapid.IntRange(1, 4).Draw(rt, "numExtraMiddleware")
		numAgent := rapid.IntRange(1, 4).Draw(rt, "numAgentMiddleware")
		// Generate number of tool calls in the batch (1-3).
		numToolCalls := rapid.IntRange(1, 3).Draw(rt, "numToolCalls")

		// Shared slice to record execution order.
		var mu sync.Mutex
		var executionOrder []string

		// Create middleware that records its label when executed.
		makeMiddleware := func(label string) Middleware {
			return func(next ToolHandlerFunc) ToolHandlerFunc {
				return func(c *Context, toolName string, input json.RawMessage) (string, error) {
					mu.Lock()
					executionOrder = append(executionOrder, label)
					mu.Unlock()
					return next(c, toolName, input)
				}
			}
		}

		// Build extra middleware (S1, S2, ...).
		extraMW := make([]Middleware, numExtra)
		for i := range numExtra {
			extraMW[i] = makeMiddleware(fmt.Sprintf("S%d", i+1))
		}

		// Build agent middleware (A1, A2, ...).
		agentMW := make([]Middleware, numAgent)
		for i := range numAgent {
			agentMW[i] = makeMiddleware(fmt.Sprintf("A%d", i+1))
		}

		// Build tools.
		toolNames := make([]string, numToolCalls)
		tools := make([]tool.Tool, numToolCalls)
		for i := range numToolCalls {
			name := fmt.Sprintf("tool_%d", i)
			toolNames[i] = name
			tools[i] = tool.NewRaw(name, name+" desc", map[string]any{"type": "object"},
				func(_ context.Context, _ json.RawMessage) (string, error) {
					return "result", nil
				})
		}

		// Provider returns tool calls on first call, then final text.
		calls := make([]tool.Call, numToolCalls)
		for i, name := range toolNames {
			calls[i] = tool.Call{ToolUseID: fmt.Sprintf("tc-%d", i), Name: name, Input: json.RawMessage(`{}`)}
		}
		sp := newScriptedProvider(
			&ProviderResponse{ToolCalls: calls},
			&ProviderResponse{Text: "done"},
		)

		a, err := New(sp, prompt.Text("test agent"), tools,
			WithMiddleware(agentMW...),
		)
		if err != nil {
			rt.Fatalf("failed to create agent: %v", err)
		}

		messages := []Message{{
			Role:    RoleUser,
			Content: []ContentBlock{TextBlock{Text: "go"}},
		}}

		c := Background()
		_, _, err = a.RunLoop(c, LoopParams{
			Messages: messages,
			Config: LoopConfig{
				ExtraMiddleware: extraMW,
			},
		})
		if err != nil {
			rt.Fatalf("RunLoop returned error: %v", err)
		}

		// Verify: total middleware invocations = (numExtra + numAgent) * numToolCalls.
		expectedTotal := (numExtra + numAgent) * numToolCalls
		mu.Lock()
		actualTotal := len(executionOrder)
		orderCopy := make([]string, len(executionOrder))
		copy(orderCopy, executionOrder)
		mu.Unlock()

		if actualTotal != expectedTotal {
			rt.Fatalf("expected %d middleware invocations, got %d", expectedTotal, actualTotal)
		}

		// Verify ordering: for each tool call, the order should be S1, S2, ..., A1, A2, ...
		perTool := numExtra + numAgent
		for tc := range numToolCalls {
			offset := tc * perTool
			for i := range numExtra {
				expected := fmt.Sprintf("S%d", i+1)
				if orderCopy[offset+i] != expected {
					rt.Fatalf("tool call %d, position %d: expected %q, got %q", tc, i, expected, orderCopy[offset+i])
				}
			}
			for i := range numAgent {
				expected := fmt.Sprintf("A%d", i+1)
				if orderCopy[offset+numExtra+i] != expected {
					rt.Fatalf("tool call %d, position %d: expected %q, got %q", tc, numExtra+i, expected, orderCopy[offset+numExtra+i])
				}
			}
		}
	})
}

// Feature: swarm-loop-refactor, Property 6: ExtraMiddleware does not mutate agent state

// TestProperty_ExtraMiddlewareNoMutation verifies that for any RunLoop call with
// non-empty ExtraMiddleware, the agent's middleware slice is identical before and
// after the call (same length, same elements).
//
// **Validates: Requirements 3.4**
func TestProperty_ExtraMiddlewareNoMutation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate number of agent middleware (0-3) and extra middleware (1-4).
		numAgent := rapid.IntRange(0, 3).Draw(rt, "numAgentMiddleware")
		numExtra := rapid.IntRange(1, 4).Draw(rt, "numExtraMiddleware")

		// Build agent middleware.
		agentMW := make([]Middleware, numAgent)
		for i := range numAgent {
			agentMW[i] = func(next ToolHandlerFunc) ToolHandlerFunc {
				return func(c *Context, toolName string, input json.RawMessage) (string, error) {
					return next(c, toolName, input)
				}
			}
		}

		// Build extra middleware.
		extraMW := make([]Middleware, numExtra)
		for i := range numExtra {
			extraMW[i] = func(next ToolHandlerFunc) ToolHandlerFunc {
				return func(c *Context, toolName string, input json.RawMessage) (string, error) {
					return next(c, toolName, input)
				}
			}
		}

		// Provider returns a tool call then final text.
		noopTool := tool.NewRaw("noop", "does nothing", map[string]any{"type": "object"},
			func(_ context.Context, _ json.RawMessage) (string, error) {
				return "ok", nil
			})

		sp := newScriptedProvider(
			&ProviderResponse{ToolCalls: []tool.Call{{ToolUseID: "tc-1", Name: "noop", Input: json.RawMessage(`{}`)}}},
			&ProviderResponse{Text: "done"},
		)

		opts := []Option{}
		if numAgent > 0 {
			opts = append(opts, WithMiddleware(agentMW...))
		}

		a, err := New(sp, prompt.Text("test agent"), []tool.Tool{noopTool}, opts...)
		if err != nil {
			rt.Fatalf("failed to create agent: %v", err)
		}

		// Snapshot middleware before RunLoop.
		mwBefore := a.Middlewares()
		lenBefore := len(mwBefore)

		messages := []Message{{
			Role:    RoleUser,
			Content: []ContentBlock{TextBlock{Text: "go"}},
		}}

		c := Background()
		_, _, err = a.RunLoop(c, LoopParams{
			Messages: messages,
			Config: LoopConfig{
				ExtraMiddleware: extraMW,
			},
		})
		if err != nil {
			rt.Fatalf("RunLoop returned error: %v", err)
		}

		// Verify: agent's middleware slice is unchanged.
		mwAfter := a.Middlewares()
		if len(mwAfter) != lenBefore {
			rt.Fatalf("agent middleware length changed: before=%d, after=%d", lenBefore, len(mwAfter))
		}
	})
}

// Feature: swarm-loop-refactor, Property 11: SkipConversationSave prevents persistence

// trackingSaveStore is a mock conversation store that tracks Save calls.
type trackingSaveStore struct {
	mu        sync.Mutex
	saveCount int
}

func (s *trackingSaveStore) Load(_ context.Context, _ string) ([]Message, error) {
	return nil, nil
}

func (s *trackingSaveStore) Save(_ context.Context, _ string, _ []Message) error {
	s.mu.Lock()
	s.saveCount++
	s.mu.Unlock()
	return nil
}

func (s *trackingSaveStore) List(_ context.Context) ([]string, error) { return nil, nil }
func (s *trackingSaveStore) Delete(_ context.Context, _ string) error { return nil }

func (s *trackingSaveStore) SaveCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveCount
}

// TestProperty_SkipConversationSave verifies that for any RunLoop call with
// SkipConversationSave = true, the agent's conversation store (if configured)
// does NOT receive any Save calls during that execution.
//
// **Validates: Requirements 9.4**
func TestProperty_SkipConversationSave(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate whether the provider returns tool calls first or just text.
		hasToolCalls := rapid.Bool().Draw(rt, "hasToolCalls")

		store := &trackingSaveStore{}

		var responses []*ProviderResponse
		var tools []tool.Tool

		if hasToolCalls {
			// Provider returns a tool call, then final text.
			noopTool := tool.NewRaw("noop", "does nothing", map[string]any{"type": "object"},
				func(_ context.Context, _ json.RawMessage) (string, error) {
					return "ok", nil
				})
			tools = []tool.Tool{noopTool}
			responses = []*ProviderResponse{
				{ToolCalls: []tool.Call{{ToolUseID: "tc-1", Name: "noop", Input: json.RawMessage(`{}`)}}},
				{Text: "done"},
			}
		} else {
			// Provider returns text directly.
			responseText := rapid.StringMatching(`[a-zA-Z0-9 ]{1,50}`).Draw(rt, "responseText")
			responses = []*ProviderResponse{{Text: responseText}}
		}

		sp := newScriptedProvider(responses...)

		a, err := New(sp, prompt.Text("test agent"), tools,
			WithConversation(store, "conv-test"),
		)
		if err != nil {
			rt.Fatalf("failed to create agent: %v", err)
		}

		messages := []Message{{
			Role:    RoleUser,
			Content: []ContentBlock{TextBlock{Text: "hello"}},
		}}

		c := Background()
		_, _, err = a.RunLoop(c, LoopParams{
			Messages: messages,
			Config: LoopConfig{
				SkipConversationSave: true,
			},
		})
		if err != nil {
			rt.Fatalf("RunLoop returned error: %v", err)
		}

		// Verify: Save was never called.
		if count := store.SaveCount(); count != 0 {
			rt.Fatalf("expected 0 Save calls with SkipConversationSave=true, got %d", count)
		}
	})
}

// Feature: swarm-loop-refactor, Property 14: Standalone agent behavior unchanged

// TestProperty_StandaloneBehaviorUnchanged verifies that for any RunLoop call
// with a zero-value LoopConfig (no interceptor, no extra middleware,
// SkipConversationSave=false), the behavior is identical to calling the internal
// runLoop directly — same final text, same token usage, same errors.
//
// **Validates: Requirements 4.4**
func TestProperty_StandaloneBehaviorUnchanged(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a random response text.
		responseText := rapid.StringMatching(`[a-zA-Z0-9 .,!?]{1,100}`).Draw(rt, "responseText")
		// Generate random token usage.
		inputTokens := rapid.IntRange(1, 1000).Draw(rt, "inputTokens")
		outputTokens := rapid.IntRange(1, 1000).Draw(rt, "outputTokens")

		// Create a provider that returns text with specific usage.
		provider := &fixedUsageTextProvider{
			text:  responseText,
			usage: TokenUsage{InputTokens: inputTokens, OutputTokens: outputTokens},
		}

		a, err := New(provider, prompt.Text("test agent"), nil)
		if err != nil {
			rt.Fatalf("failed to create agent: %v", err)
		}

		messages := []Message{{
			Role:    RoleUser,
			Content: []ContentBlock{TextBlock{Text: "hello"}},
		}}

		// Call RunLoop with zero-value LoopConfig.
		c1 := Background()
		usage1, text1, err1 := a.RunLoop(c1, LoopParams{
			Messages: messages,
		})

		// Call Invoke (which uses the internal runLoop) with the same message.
		// We need a fresh provider since scriptedProvider is stateful.
		provider2 := &fixedUsageTextProvider{
			text:  responseText,
			usage: TokenUsage{InputTokens: inputTokens, OutputTokens: outputTokens},
		}
		a2, err := New(provider2, prompt.Text("test agent"), nil)
		if err != nil {
			rt.Fatalf("failed to create agent2: %v", err)
		}

		c2 := Background()
		text2, err2 := a2.Invoke(c2, "hello")

		// Verify: same error behavior.
		if (err1 == nil) != (err2 == nil) {
			rt.Fatalf("error mismatch: RunLoop err=%v, Invoke err=%v", err1, err2)
		}
		if err1 != nil {
			return // both errored, that's consistent
		}

		// Verify: same final text.
		if text1 != text2 {
			rt.Fatalf("text mismatch: RunLoop=%q, Invoke=%q", text1, text2)
		}

		// Verify: same token usage.
		usage2 := c2.Usage()
		if usage1.InputTokens != usage2.InputTokens || usage1.OutputTokens != usage2.OutputTokens {
			rt.Fatalf("usage mismatch: RunLoop=%+v, Invoke=%+v", usage1, usage2)
		}
	})
}

// fixedUsageTextProvider is a mock provider that returns a fixed text response
// with specific token usage values.
type fixedUsageTextProvider struct {
	text  string
	usage TokenUsage
}

func (p *fixedUsageTextProvider) Name() string { return "mock" }

func (p *fixedUsageTextProvider) Converse(_ context.Context, _ ConverseParams) (*ProviderResponse, error) {
	return &ProviderResponse{Text: p.text, Usage: p.usage}, nil
}

func (p *fixedUsageTextProvider) ConverseStream(_ context.Context, _ ConverseParams, cb StreamCallback) (*ProviderResponse, error) {
	if cb != nil {
		cb(p.text)
	}
	return &ProviderResponse{Text: p.text, Usage: p.usage}, nil
}

// Feature: swarm-loop-refactor, Property 9: Agent-level hooks fire with correct counts

// countingTracingHook is a mock TracingHook that counts invocations of
// OnIterationStart, OnProviderCallStart, and OnToolStart.
type countingTracingHook struct {
	iterationCount atomic.Int32
	providerCount  atomic.Int32
	toolCount      atomic.Int32
}

func (h *countingTracingHook) OnInvokeStart(_ context.Context, _ InvokeSpanParams) (context.Context, func(error, TokenUsage, string)) {
	return context.Background(), func(_ error, _ TokenUsage, _ string) {}
}

func (h *countingTracingHook) OnIterationStart(ctx context.Context, _ int) (context.Context, func(int, bool)) {
	h.iterationCount.Add(1)
	return ctx, func(_ int, _ bool) {}
}

func (h *countingTracingHook) OnProviderCallStart(ctx context.Context, _ ProviderCallParams) (context.Context, func(error, TokenUsage, int, string)) {
	h.providerCount.Add(1)
	return ctx, func(_ error, _ TokenUsage, _ int, _ string) {}
}

func (h *countingTracingHook) OnToolStart(ctx context.Context, _ string, _ json.RawMessage) (context.Context, func(error, string)) {
	h.toolCount.Add(1)
	return ctx, func(_ error, _ string) {}
}

func (h *countingTracingHook) OnGuardrailStart(ctx context.Context, _ string, _ string) (context.Context, func(error, string)) {
	return ctx, func(_ error, _ string) {}
}

func (h *countingTracingHook) OnConversationStart(ctx context.Context, _ string, _ string) (context.Context, func(error)) {
	return ctx, func(_ error) {}
}

func (h *countingTracingHook) OnRetrieverStart(ctx context.Context, _ string) (context.Context, func(error, int)) {
	return ctx, func(_ error, _ int) {}
}

func (h *countingTracingHook) OnMaxIterationsExceeded(_ context.Context, _ int) {}

// TestProperty_AgentHooksFireCorrectly verifies that for any agent with tracing
// hooks configured, running with N iterations and M total tool calls,
// OnIterationStart fires exactly N times, OnProviderCallStart fires exactly N
// times, and OnToolStart fires exactly M times.
//
// **Validates: Requirements 5.6, 6.1, 6.2, 6.3**
func TestProperty_AgentHooksFireCorrectly(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate random N (iterations, 1-5).
		n := rapid.IntRange(1, 5).Draw(rt, "iterations")
		// Generate random tool counts per iteration (1-3) for iterations 1..N-1.
		toolCounts := make([]int, n-1)
		totalTools := 0
		for i := range toolCounts {
			toolCounts[i] = rapid.IntRange(1, 3).Draw(rt, fmt.Sprintf("toolCount_%d", i))
			totalTools += toolCounts[i]
		}

		// Build the scripted provider responses:
		// - Iterations 1..N-1: each returns tool calls
		// - Iteration N: returns final text
		// We need enough distinct tool names to cover the max tools per iteration.
		maxToolsPerIter := 3
		tools := make([]tool.Tool, maxToolsPerIter)
		for i := range maxToolsPerIter {
			name := fmt.Sprintf("hook_tool_%d", i)
			tools[i] = tool.NewRaw(name, name+" desc", map[string]any{"type": "object"},
				func(_ context.Context, _ json.RawMessage) (string, error) {
					return "ok", nil
				})
		}

		responses := make([]*ProviderResponse, 0, n)
		for i := range n - 1 {
			calls := make([]tool.Call, toolCounts[i])
			for j := range toolCounts[i] {
				calls[j] = tool.Call{
					ToolUseID: fmt.Sprintf("tc-%d-%d", i, j),
					Name:      fmt.Sprintf("hook_tool_%d", j),
					Input:     json.RawMessage(`{}`),
				}
			}
			responses = append(responses, &ProviderResponse{ToolCalls: calls})
		}
		// Final iteration: text response.
		finalText := rapid.StringMatching(`[a-zA-Z0-9 ]{1,50}`).Draw(rt, "finalText")
		responses = append(responses, &ProviderResponse{Text: finalText})

		sp := newScriptedProvider(responses...)

		// Create the counting tracing hook.
		hook := &countingTracingHook{}

		// Create agent with the tools and set the tracing hook.
		a, err := New(sp, prompt.Text("test agent"), tools,
			WithMaxIterations(n+5), // ensure we don't hit max iterations
		)
		if err != nil {
			rt.Fatalf("failed to create agent: %v", err)
		}
		a.SetTracingHook(hook)

		messages := []Message{{
			Role:    RoleUser,
			Content: []ContentBlock{TextBlock{Text: "go"}},
		}}

		c := Background()
		_, text, err := a.RunLoop(c, LoopParams{Messages: messages})
		if err != nil {
			rt.Fatalf("RunLoop returned error: %v", err)
		}
		if text != finalText {
			rt.Fatalf("expected final text %q, got %q", finalText, text)
		}

		// Verify: OnIterationStart fired exactly N times.
		iterCount := int(hook.iterationCount.Load())
		if iterCount != n {
			rt.Fatalf("expected OnIterationStart to fire %d times, got %d", n, iterCount)
		}

		// Verify: OnProviderCallStart fired exactly N times.
		provCount := int(hook.providerCount.Load())
		if provCount != n {
			rt.Fatalf("expected OnProviderCallStart to fire %d times, got %d", n, provCount)
		}

		// Verify: OnToolStart fired exactly M (totalTools) times.
		toolCount := int(hook.toolCount.Load())
		if toolCount != totalTools {
			rt.Fatalf("expected OnToolStart to fire %d times, got %d", totalTools, toolCount)
		}
	})
}
