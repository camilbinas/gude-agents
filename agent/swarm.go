package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// swarmActiveKey is the context key for tracking the active agent in a swarm.
type swarmActiveKey struct{}

// SwarmMember registers an agent with a name and description inside a Swarm.
type SwarmMember struct {
	// Name is the unique identifier used in handoff tool names (e.g. "researcher" → "transfer_to_researcher").
	Name string
	// Description explains what this agent does — shown to other agents so they know when to hand off.
	Description string
	// Agent is the fully configured agent instance.
	Agent *Agent
}

// Swarm coordinates multiple agents with handoff support.
// The active agent runs until it either produces a final response or hands off
// to another agent via a transfer_to_<name> tool. Handoffs carry the full
// conversation context so the receiving agent can continue seamlessly.
//
// Documented in docs/swarm.md — update when changing fields or behavior.
type Swarm struct {
	mu             sync.Mutex // protects activeAgent
	members        map[string]*swarmEntry
	initialAgent   string // first member — used when no memory or fresh conversation
	activeAgent    string // default active agent; overridden per-call via memory or context
	maxHandoffs    int
	logger         Logger
	middlewares    []Middleware
	memory         Memory
	conversationID string
}

// swarmEntry holds a member plus the handoff tools injected into it.
type swarmEntry struct {
	member       SwarmMember
	handoffTools []tool.Tool
}

// SwarmOption configures the Swarm.
type SwarmOption func(*Swarm)

// WithSwarmMaxHandoffs sets the maximum number of agent-to-agent handoffs per invocation.
// Defaults to 10.
func WithSwarmMaxHandoffs(n int) SwarmOption {
	return func(s *Swarm) { s.maxHandoffs = n }
}

// WithSwarmLogger sets a logger for swarm-level events.
func WithSwarmLogger(l Logger) SwarmOption {
	return func(s *Swarm) { s.logger = l }
}

// WithSwarmMiddleware adds middleware applied to all tool executions across the swarm.
func WithSwarmMiddleware(mws ...Middleware) SwarmOption {
	return func(s *Swarm) { s.middlewares = append(s.middlewares, mws...) }
}

// WithSwarmMemory enables conversation memory so the swarm persists messages and
// the active agent across calls. Without this, each Run/Invoke is stateless.
func WithSwarmMemory(m Memory, conversationID string) SwarmOption {
	return func(s *Swarm) {
		s.memory = m
		s.conversationID = conversationID
	}
}

// handoffSentinel is a magic result string that signals a handoff occurred.
const handoffSentinel = "__swarm_handoff__"

// NewSwarm creates a Swarm from the given members. The first member is the initial active agent.
// Each agent receives transfer_to_<name> tools for every other agent in the swarm.
// Documented in docs/swarm.md — update when changing signature or validation.
func NewSwarm(members []SwarmMember, opts ...SwarmOption) (*Swarm, error) {
	if len(members) < 2 {
		return nil, fmt.Errorf("swarm requires at least 2 members, got %d", len(members))
	}

	s := &Swarm{
		members:      make(map[string]*swarmEntry, len(members)),
		initialAgent: members[0].Name,
		activeAgent:  members[0].Name,
		maxHandoffs:  10,
	}
	for _, opt := range opts {
		opt(s)
	}

	// Validate unique names.
	for _, m := range members {
		if m.Name == "" {
			return nil, fmt.Errorf("swarm member name cannot be empty")
		}
		if m.Agent == nil {
			return nil, fmt.Errorf("swarm member %q has nil Agent", m.Name)
		}
		if _, exists := s.members[m.Name]; exists {
			return nil, fmt.Errorf("duplicate swarm member name: %q", m.Name)
		}
		s.members[m.Name] = &swarmEntry{member: m}
	}

	// Inject handoff tools into each agent.
	for name, entry := range s.members {
		for otherName, otherEntry := range s.members {
			if otherName == name {
				continue
			}
			toolName := "transfer_to_" + otherName
			if entry.member.Agent.HasTool(toolName) {
				continue // already registered, skip
			}
			handoffTool := s.makeHandoffTool(otherName, otherEntry.member.Description)
			entry.handoffTools = append(entry.handoffTools, handoffTool)

			// Register the tool on the agent.
			entry.member.Agent.RegisterTool(handoffTool)
		}
	}

	return s, nil
}

// makeHandoffTool creates a transfer_to_<name> tool that triggers a handoff.
func (s *Swarm) makeHandoffTool(targetName, targetDescription string) tool.Tool {
	toolName := "transfer_to_" + targetName
	return tool.NewRaw(
		toolName,
		fmt.Sprintf("Hand off the conversation to %s. %s", targetName, targetDescription),
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"summary": map[string]any{
					"type":        "string",
					"description": "Brief context about why you are handing off and what the user needs",
				},
			},
			"required": []string{"summary"},
		},
		func(ctx context.Context, input json.RawMessage) (string, error) {
			// Store the target in context so the swarm loop can detect it.
			if ic := GetInvocationContext(ctx); ic != nil {
				ic.Set(swarmActiveKey{}, targetName)
			}
			return handoffSentinel, nil
		},
	)
}

// logf logs if a logger is configured.
func (s *Swarm) logf(format string, v ...any) {
	if s.logger != nil {
		s.logger.Printf(format, v...)
	}
}

// Run executes the swarm starting from the active agent. When an agent hands off,
// the conversation context transfers to the new agent which continues the loop.
// If memory is configured, conversation history and the active agent are persisted
// across calls.
// Documented in docs/swarm.md — update when changing loop behavior.
func (s *Swarm) Run(ctx context.Context, userMessage string, cb StreamCallback) (SwarmResult, error) {
	var result SwarmResult
	result.HandoffHistory = make([]Handoff, 0)

	// Resolve conversation ID: per-invocation override or swarm default.
	convID := ResolveConversationID(ctx, s.conversationID)

	// Read the default active agent under lock.
	s.mu.Lock()
	currentAgent := s.activeAgent
	s.mu.Unlock()

	// Load conversation history and active agent from memory.
	var messages []Message
	if s.memory != nil {
		history, err := s.memory.Load(ctx, convID)
		if err != nil {
			return result, fmt.Errorf("swarm memory load: %w", err)
		}
		messages = history

		// Restore which agent was last active from the metadata conversation.
		agentHistory, err := s.memory.Load(ctx, convID+"::swarm_active")
		if err == nil && len(agentHistory) > 0 {
			last := agentHistory[len(agentHistory)-1]
			if len(last.Content) > 0 {
				if tb, ok := last.Content[0].(TextBlock); ok && tb.Text != "" {
					if _, exists := s.members[tb.Text]; exists {
						currentAgent = tb.Text
					}
				}
			}
		}
	}

	// Append the new user message.
	messages = append(messages, Message{
		Role:    RoleUser,
		Content: []ContentBlock{TextBlock{Text: userMessage}},
	})

	for handoff := 0; handoff <= s.maxHandoffs; handoff++ {
		entry, ok := s.members[currentAgent]
		if !ok {
			return result, fmt.Errorf("unknown swarm member: %q", currentAgent)
		}

		s.logf("[swarm] active agent: %s (handoff %d)", currentAgent, handoff)

		// Create a fresh invocation context for handoff detection.
		ic := NewInvocationContext()
		agentCtx := WithInvocationContext(ctx, ic)

		// Run the agent's inner loop manually so we can intercept handoffs.
		usage, finalText, handedOff, err := s.runAgent(agentCtx, entry, messages, cb)
		result.Usage.InputTokens += usage.InputTokens
		result.Usage.OutputTokens += usage.OutputTokens

		if err != nil {
			return result, fmt.Errorf("agent %q: %w", currentAgent, err)
		}

		// Check if a handoff was requested.
		if handedOff {
			targetRaw, _ := ic.Get(swarmActiveKey{})
			targetName, _ := targetRaw.(string)
			if targetName == "" {
				return result, fmt.Errorf("agent %q triggered handoff but no target set", currentAgent)
			}

			s.logf("[swarm] handoff: %s → %s", currentAgent, targetName)
			result.HandoffHistory = append(result.HandoffHistory, Handoff{
				From: currentAgent,
				To:   targetName,
			})

			// Detect ping-pong: if the target already appeared in recent handoff history,
			// tell it to handle the request directly instead of bouncing again.
			loopDetected := false
			for _, h := range result.HandoffHistory[:len(result.HandoffHistory)-1] {
				if h.From == targetName {
					loopDetected = true
					break
				}
			}

			if loopDetected {
				s.logf("[swarm] loop detected: %s was already active — injecting handle-it instruction", targetName)
				messages = append(messages, Message{
					Role: RoleUser,
					Content: []ContentBlock{TextBlock{
						Text: fmt.Sprintf("[Conversation transferred from %s to %s. IMPORTANT: You have already been consulted in this conversation. Do NOT transfer again — handle the request directly with the information available, or tell the user what specific information you still need.]", currentAgent, targetName),
					}},
				})
			} else {
				messages = append(messages, Message{
					Role: RoleUser,
					Content: []ContentBlock{TextBlock{
						Text: fmt.Sprintf("[Conversation transferred from %s to %s]", currentAgent, targetName),
					}},
				})
			}

			currentAgent = targetName
			continue
		}

		// No handoff — agent produced a final response.
		result.FinalAgent = currentAgent
		result.Response = finalText

		// Append assistant response to messages for memory.
		messages = append(messages, Message{
			Role:    RoleAssistant,
			Content: []ContentBlock{TextBlock{Text: finalText}},
		})

		// Persist conversation and active agent.
		if s.memory != nil {
			if err := s.memory.Save(ctx, convID, messages); err != nil {
				return result, fmt.Errorf("swarm memory save: %w", err)
			}
			// Store which agent is active so the next call resumes there.
			_ = s.memory.Save(ctx, convID+"::swarm_active", []Message{
				{Role: RoleAssistant, Content: []ContentBlock{TextBlock{Text: currentAgent}}},
			})
		}
		s.mu.Lock()
		s.activeAgent = currentAgent
		s.mu.Unlock()

		return result, nil
	}

	return result, fmt.Errorf("max handoffs (%d) exceeded", s.maxHandoffs)
}

// runAgent executes a single agent's loop, returning whether a handoff was triggered.
func (s *Swarm) runAgent(ctx context.Context, entry *swarmEntry, messages []Message, cb StreamCallback) (TokenUsage, string, bool, error) {
	a := entry.member.Agent
	var cumulative TokenUsage

	systemPrompt := a.Instructions()

	for iteration := range a.MaxIterations() {
		s.logf("[swarm/%s] iteration %d", entry.member.Name, iteration+1)

		var bufferedChunks []string
		hasOutputGuardrails := len(a.OutputGuardrails()) > 0

		streamCB := func(chunk string) {
			if hasOutputGuardrails {
				bufferedChunks = append(bufferedChunks, chunk)
			} else if cb != nil {
				cb(chunk)
			}
		}

		resp, err := a.Provider().ConverseStream(ctx, ConverseParams{
			Messages:   messages,
			System:     systemPrompt,
			ToolConfig: a.ToolSpecs(),
		}, streamCB)
		if err != nil {
			return cumulative, "", false, err
		}

		cumulative.InputTokens += resp.Usage.InputTokens
		cumulative.OutputTokens += resp.Usage.OutputTokens

		if a.TokenBudget() > 0 && cumulative.Total() > a.TokenBudget() {
			return cumulative, "", false, ErrTokenBudgetExceeded
		}

		if len(resp.ToolCalls) > 0 {
			// Build assistant message with tool calls.
			assistantContent := make([]ContentBlock, 0, len(resp.ToolCalls)+1)
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
			messages = append(messages, Message{Role: RoleAssistant, Content: assistantContent})

			// Execute tools and check for handoff.
			results, handedOff := s.executeToolsWithHandoff(ctx, a, resp.ToolCalls)

			resultBlocks := make([]ContentBlock, len(results))
			for i, r := range results {
				resultBlocks[i] = r
			}
			messages = append(messages, Message{Role: RoleUser, Content: resultBlocks})

			if handedOff {
				return cumulative, "", true, nil
			}
			continue
		}

		// No tool calls — final response.
		finalText := resp.Text
		for _, g := range a.OutputGuardrails() {
			finalText, err = g(ctx, finalText)
			if err != nil {
				return cumulative, "", false, fmt.Errorf("output guardrail: %w", err)
			}
		}

		if hasOutputGuardrails && cb != nil {
			if finalText == resp.Text {
				for _, chunk := range bufferedChunks {
					cb(chunk)
				}
			} else {
				cb(finalText)
			}
		}

		return cumulative, finalText, false, nil
	}

	return cumulative, "", false, fmt.Errorf("max iterations (%d) exceeded", a.MaxIterations())
}

// executeToolsWithHandoff runs tool calls and detects if any was a handoff.
func (s *Swarm) executeToolsWithHandoff(ctx context.Context, a *Agent, calls []tool.Call) ([]ToolResultBlock, bool) {
	results := make([]ToolResultBlock, len(calls))
	handedOff := false
	var mu sync.Mutex

	exec := func(i int, tc tool.Call) {
		s.logf("[swarm/tool] %s", tc.Name)

		t, ok := a.LookupTool(tc.Name)
		if !ok {
			results[i] = ToolResultBlock{
				ToolUseID: tc.ToolUseID,
				Content:   fmt.Sprintf("unknown tool: %s", tc.Name),
				IsError:   true,
			}
			return
		}

		// Validate tool input against the declared schema.
		if err := ValidateToolInput(t.Spec.InputSchema, tc.Input); err != nil {
			toolErr := &ToolError{ToolName: tc.Name, Cause: err}
			results[i] = ToolResultBlock{
				ToolUseID: tc.ToolUseID,
				Content:   toolErr.Error(),
				IsError:   true,
			}
			return
		}

		// Apply both swarm-level and agent-level middleware.
		allMiddleware := append(s.middlewares, a.Middlewares()...)
		handler := ChainMiddleware(
			func(ctx context.Context, toolName string, input json.RawMessage) (string, error) {
				return t.Handler(ctx, input)
			},
			allMiddleware...,
		)

		out, err := handler(ctx, tc.Name, tc.Input)
		if err != nil {
			results[i] = ToolResultBlock{
				ToolUseID: tc.ToolUseID,
				Content:   err.Error(),
				IsError:   true,
			}
			return
		}

		if out == handoffSentinel {
			mu.Lock()
			handedOff = true
			mu.Unlock()
			results[i] = ToolResultBlock{
				ToolUseID: tc.ToolUseID,
				Content:   "Handoff accepted. Transferring conversation.",
			}
			return
		}

		results[i] = ToolResultBlock{
			ToolUseID: tc.ToolUseID,
			Content:   out,
		}
	}

	if a.ParallelTools() {
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
			if handedOff {
				// Fill remaining results with cancellation notices.
				for j := i + 1; j < len(calls); j++ {
					results[j] = ToolResultBlock{
						ToolUseID: calls[j].ToolUseID,
						Content:   "Skipped — handoff in progress.",
					}
				}
				break
			}
		}
	}

	return results, handedOff
}

// Invoke is a convenience wrapper that collects streamed output into a string.
func (s *Swarm) Invoke(ctx context.Context, userMessage string) (SwarmResult, error) {
	var sb strings.Builder
	result, err := s.Run(ctx, userMessage, func(chunk string) {
		sb.WriteString(chunk)
	})
	if err != nil {
		return result, err
	}
	if result.Response == "" {
		result.Response = sb.String()
	}
	return result, nil
}

// SwarmResult holds the outcome of a swarm invocation.
type SwarmResult struct {
	// Response is the final text output.
	Response string
	// FinalAgent is the name of the agent that produced the final response.
	FinalAgent string
	// Usage is the cumulative token usage across all agents.
	Usage TokenUsage
	// HandoffHistory records each agent-to-agent transfer that occurred.
	HandoffHistory []Handoff
}

// Handoff records a single agent-to-agent transfer.
type Handoff struct {
	From string
	To   string
}
