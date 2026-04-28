package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

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
type Swarm struct {
	mu             sync.Mutex // protects activeAgent
	members        map[string]*swarmEntry
	initialAgent   string // first member — used when no conversation history
	activeAgent    string // default active agent; overridden per-call via conversation or context
	maxHandoffs    int
	middlewares    []Middleware
	conversation   Conversation
	conversationID string
	tracingHook    SwarmTracingHook // nil = no tracing
	metricsHook    SwarmMetricsHook // nil = no metrics
	loggingHook    SwarmLoggingHook // nil = no structured logging
}

// swarmEntry holds a member plus the handoff tools injected into it.
type swarmEntry struct {
	member       SwarmMember
	handoffTools []tool.Tool
}

// SwarmOption configures the Swarm.
type SwarmOption func(*Swarm) error

// WithSwarmMaxHandoffs sets the maximum number of agent-to-agent handoffs per invocation.
// Defaults to 10. Returns an error if n is less than 1.
func WithSwarmMaxHandoffs(n int) SwarmOption {
	return func(s *Swarm) error {
		if n < 1 {
			return fmt.Errorf("swarm: max handoffs must be >= 1, got %d", n)
		}
		s.maxHandoffs = n
		return nil
	}
}

// WithSwarmMiddleware adds middleware applied to all tool executions across the swarm.
func WithSwarmMiddleware(mws ...Middleware) SwarmOption {
	return func(s *Swarm) error {
		s.middlewares = append(s.middlewares, mws...)
		return nil
	}
}

// WithSwarmConversation enables conversation so the swarm persists messages and
// the active agent across calls. Without this, each Run/Invoke is stateless.
func WithSwarmConversation(c Conversation, conversationID string) SwarmOption {
	return func(s *Swarm) error {
		s.conversation = c
		s.conversationID = conversationID
		return nil
	}
}

// handoffSentinel is a magic result string that signals a handoff occurred.
const handoffSentinel = "__swarm_handoff__"

// NewSwarm creates a Swarm from the given members. The first member is the initial active agent.
// Each agent receives transfer_to_<name> tools for every other agent in the swarm.
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
		if err := opt(s); err != nil {
			return nil, err
		}
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
			if err := entry.member.Agent.RegisterTool(handoffTool); err != nil {
				return nil, fmt.Errorf("swarm: register handoff tool %q on %q: %w", toolName, name, err)
			}
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

// Run executes the swarm starting from the active agent. When an agent hands off,
// the conversation context transfers to the new agent which continues the loop.
// If conversation is configured, conversation history and the active agent are persisted
// across calls.
func (s *Swarm) Run(ctx context.Context, userMessage string, cb StreamCallback) (SwarmResult, error) {
	var result SwarmResult
	result.HandoffHistory = make([]Handoff, 0)

	// Resolve conversation ID: per-invocation override or swarm default.
	convID := ResolveConversationID(ctx, s.conversationID)

	// Read the default active agent under lock.
	s.mu.Lock()
	currentAgent := s.activeAgent
	s.mu.Unlock()

	// Start swarm.run span if tracing is enabled.
	var finishSwarmRun func(error, SwarmResult)
	if s.tracingHook != nil {
		ctx, finishSwarmRun = s.tracingHook.OnSwarmRunStart(ctx, SwarmRunSpanParams{
			InitialAgent:   currentAgent,
			MemberCount:    len(s.members),
			MaxHandoffs:    s.maxHandoffs,
			ConversationID: convID,
			UserMessage:    userMessage,
		})
	}

	// Start swarm metrics tracking if metrics hook is enabled.
	var finishSwarmMetrics func(error, SwarmResult)
	if s.metricsHook != nil {
		finishSwarmMetrics = s.metricsHook.OnSwarmRunStart()
	}

	// Log swarm run start if logging hook is enabled.
	var swarmRunStart time.Time
	if s.loggingHook != nil {
		swarmRunStart = time.Now()
		s.loggingHook.OnSwarmRunStart(currentAgent, len(s.members), s.maxHandoffs)
	}

	// Load conversation history and active agent from conversation store.
	var messages []Message
	if s.conversation != nil {
		history, err := s.conversation.Load(ctx, convID)
		if err != nil {
			if finishSwarmRun != nil {
				finishSwarmRun(err, result)
			}
			if finishSwarmMetrics != nil {
				finishSwarmMetrics(err, result)
			}
			if s.loggingHook != nil {
				s.loggingHook.OnSwarmRunEnd(err, result, time.Since(swarmRunStart))
			}
			return result, fmt.Errorf("swarm conversation load: %w", err)
		}
		messages = history

		// Restore which agent was last active from the metadata conversation.
		// Uses a "meta:" prefix to separate internal metadata from user conversations,
		// and to allow future metadata keys without collision.
		agentHistory, err := s.conversation.Load(ctx, "meta:"+convID+":swarm_active")
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

		// Create a fresh invocation context for handoff detection.
		ic := NewInvocationContext()
		agentCtx := WithInvocationContext(ctx, ic)

		// Start agent span if tracing is enabled.
		var finishAgent func(error)
		if s.tracingHook != nil {
			agentCtx, finishAgent = s.tracingHook.OnSwarmAgentStart(agentCtx, currentAgent)
		}

		// Start agent metrics tracking if metrics hook is enabled.
		var finishAgentMetrics func(error)
		if s.metricsHook != nil {
			finishAgentMetrics = s.metricsHook.OnSwarmAgentStart(currentAgent)
		}

		// Log agent start if logging hook is enabled.
		var agentTurnStart time.Time
		if s.loggingHook != nil {
			agentTurnStart = time.Now()
			s.loggingHook.OnSwarmAgentStart(currentAgent)
		}

		// Run the agent's inner loop manually so we can intercept handoffs.
		usage, finalText, handedOff, err := s.runAgent(agentCtx, entry, messages, cb)
		result.Usage.InputTokens += usage.InputTokens
		result.Usage.OutputTokens += usage.OutputTokens

		if err != nil {
			if finishAgent != nil {
				finishAgent(err)
			}
			if finishAgentMetrics != nil {
				finishAgentMetrics(err)
			}
			if s.loggingHook != nil {
				s.loggingHook.OnSwarmAgentEnd(currentAgent, err, time.Since(agentTurnStart))
			}
			if finishSwarmRun != nil {
				finishSwarmRun(err, result)
			}
			if finishSwarmMetrics != nil {
				finishSwarmMetrics(err, result)
			}
			if s.loggingHook != nil {
				s.loggingHook.OnSwarmRunEnd(err, result, time.Since(swarmRunStart))
			}
			return result, fmt.Errorf("agent %q: %w", currentAgent, err)
		}

		// Check if a handoff was requested.
		if handedOff {
			targetRaw, _ := ic.Get(swarmActiveKey{})
			targetName, _ := targetRaw.(string)
			if targetName == "" {
				handoffErr := fmt.Errorf("agent %q triggered handoff but no target set", currentAgent)
				if finishAgent != nil {
					finishAgent(handoffErr)
				}
				if finishAgentMetrics != nil {
					finishAgentMetrics(handoffErr)
				}
				if s.loggingHook != nil {
					s.loggingHook.OnSwarmAgentEnd(currentAgent, handoffErr, time.Since(agentTurnStart))
				}
				if finishSwarmRun != nil {
					finishSwarmRun(handoffErr, result)
				}
				if finishSwarmMetrics != nil {
					finishSwarmMetrics(handoffErr, result)
				}
				if s.loggingHook != nil {
					s.loggingHook.OnSwarmRunEnd(handoffErr, result, time.Since(swarmRunStart))
				}
				return result, handoffErr
			}

			// Finish agent span before handoff.
			if finishAgent != nil {
				finishAgent(nil)
			}
			if finishAgentMetrics != nil {
				finishAgentMetrics(nil)
			}
			if s.loggingHook != nil {
				s.loggingHook.OnSwarmAgentEnd(currentAgent, nil, time.Since(agentTurnStart))
			}

			if s.loggingHook != nil {
				s.loggingHook.OnSwarmHandoff(currentAgent, targetName)
			}
			result.HandoffHistory = append(result.HandoffHistory, Handoff{
				From: currentAgent,
				To:   targetName,
			})

			// Record handoff event in trace.
			if s.tracingHook != nil {
				s.tracingHook.OnSwarmHandoff(ctx, currentAgent, targetName)
			}
			if s.metricsHook != nil {
				s.metricsHook.OnSwarmHandoff(currentAgent, targetName)
			}

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

		// Finish agent span.
		if finishAgent != nil {
			finishAgent(nil)
		}
		if finishAgentMetrics != nil {
			finishAgentMetrics(nil)
		}
		if s.loggingHook != nil {
			s.loggingHook.OnSwarmAgentEnd(currentAgent, nil, time.Since(agentTurnStart))
		}

		// Append assistant response to messages for conversation persistence.
		messages = append(messages, Message{
			Role:    RoleAssistant,
			Content: []ContentBlock{TextBlock{Text: finalText}},
		})

		// Persist conversation and active agent.
		if s.conversation != nil {
			saveCtx := WithTokenUsage(ctx, result.Usage)
			if err := s.conversation.Save(saveCtx, convID, messages); err != nil {
				if finishSwarmRun != nil {
					finishSwarmRun(err, result)
				}
				if finishSwarmMetrics != nil {
					finishSwarmMetrics(err, result)
				}
				if s.loggingHook != nil {
					s.loggingHook.OnSwarmRunEnd(err, result, time.Since(swarmRunStart))
				}
				return result, fmt.Errorf("swarm conversation save: %w", err)
			}
			// Store which agent is active so the next call resumes there.
			if err := s.conversation.Save(ctx, "meta:"+convID+":swarm_active", []Message{
				{Role: RoleAssistant, Content: []ContentBlock{TextBlock{Text: currentAgent}}},
			}); err != nil {
				// Non-critical: active agent metadata failed to save.
				_ = err
			}
		}
		s.mu.Lock()
		s.activeAgent = currentAgent
		s.mu.Unlock()

		// Finish swarm.run span.
		if finishSwarmRun != nil {
			finishSwarmRun(nil, result)
		}
		if finishSwarmMetrics != nil {
			finishSwarmMetrics(nil, result)
		}
		if s.loggingHook != nil {
			s.loggingHook.OnSwarmRunEnd(nil, result, time.Since(swarmRunStart))
		}

		return result, nil
	}

	maxErr := fmt.Errorf("max handoffs (%d) exceeded", s.maxHandoffs)
	if finishSwarmRun != nil {
		finishSwarmRun(maxErr, result)
	}
	if finishSwarmMetrics != nil {
		finishSwarmMetrics(maxErr, result)
	}
	if s.loggingHook != nil {
		s.loggingHook.OnSwarmRunEnd(maxErr, result, time.Since(swarmRunStart))
	}
	return result, maxErr
}

// runAgent executes a single agent's loop, returning whether a handoff was triggered.
func (s *Swarm) runAgent(ctx context.Context, entry *swarmEntry, messages []Message, cb StreamCallback) (TokenUsage, string, bool, error) {
	a := entry.member.Agent
	th := a.TracingHook() // may be nil
	var cumulative TokenUsage

	systemPrompt := a.Instructions()

	for iteration := range a.MaxIterations() {

		// Start iteration span.
		iterCtx := ctx
		var finishIteration func(toolCount int, isFinal bool)
		if th != nil {
			iterCtx, finishIteration = th.OnIterationStart(ctx, iteration+1)
		}

		// Log iteration start on the member agent's hook.
		lh := a.LoggingHook()
		if lh != nil {
			lh.OnIterationStart(iteration + 1)
		}

		var bufferedChunks []string
		hasOutputGuardrails := len(a.OutputGuardrails()) > 0

		streamCB := func(chunk string) {
			if hasOutputGuardrails {
				bufferedChunks = append(bufferedChunks, chunk)
			} else if cb != nil {
				cb(chunk)
			}
		}

		// Start provider call span.
		providerCtx := iterCtx
		var finishProvider func(err error, usage TokenUsage, toolCallCount int, responseText string)
		if th != nil {
			providerCtx, finishProvider = th.OnProviderCallStart(iterCtx, ProviderCallParams{
				System:       systemPrompt,
				MessageCount: len(messages),
			})
		}

		var providerModelID string
		if mi, ok := a.provider.(ModelIdentifier); ok {
			providerModelID = mi.ModelID()
		}
		if lh != nil {
			lh.OnProviderCallStart(providerModelID)
		}

		providerCallStart := time.Now()
		resp, err := a.CallProvider(providerCtx, ConverseParams{
			Messages:        messages,
			System:          systemPrompt,
			ToolConfig:      a.ToolSpecs(),
			InferenceConfig: a.InferenceConfig(),
		}, streamCB)
		if err != nil {
			providerDur := time.Since(providerCallStart)
			if finishProvider != nil {
				finishProvider(err, TokenUsage{}, 0, "")
			}
			if lh != nil {
				lh.OnProviderCallEnd(err, TokenUsage{}, 0, providerDur)
			}
			if finishIteration != nil {
				finishIteration(0, false)
			}
			return cumulative, "", false, err
		}

		// Finish provider call span.
		if finishProvider != nil {
			finishProvider(nil, resp.Usage, len(resp.ToolCalls), resp.Text)
		}
		if lh != nil {
			lh.OnProviderCallEnd(nil, resp.Usage, len(resp.ToolCalls), time.Since(providerCallStart))
		}

		cumulative.InputTokens += resp.Usage.InputTokens
		cumulative.OutputTokens += resp.Usage.OutputTokens

		if a.TokenBudget() > 0 && cumulative.Total() > a.TokenBudget() {
			if finishIteration != nil {
				finishIteration(0, false)
			}
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
			results, handedOff := s.executeToolsWithHandoff(iterCtx, a, resp.ToolCalls)

			// Finish iteration span.
			if finishIteration != nil {
				finishIteration(len(resp.ToolCalls), false)
			}

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
				if finishIteration != nil {
					finishIteration(0, true)
				}
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

		// Finish iteration span — final iteration.
		if finishIteration != nil {
			finishIteration(0, true)
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
	th := a.TracingHook() // may be nil

	exec := func(i int, tc tool.Call) {

		t, ok := a.LookupTool(tc.Name)
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
		if th != nil {
			toolCtx, finishTool = th.OnToolStart(ctx, tc.Name, tc.Input)
		}

		// Log tool start on the member agent's hook.
		lh := a.LoggingHook()
		if lh != nil {
			lh.OnToolStart(tc.Name)
		}

		toolStart := time.Now()

		// Validate tool input against the declared schema.
		if err := ValidateToolInput(t.Spec.InputSchema, tc.Input); err != nil {
			toolErr := &ToolError{ToolName: tc.Name, Cause: err}
			if finishTool != nil {
				finishTool(err, "")
			}
			if lh != nil {
				lh.OnToolEnd(tc.Name, err, time.Since(toolStart))
			}
			results[i] = ToolResultBlock{
				ToolUseID: tc.ToolUseID,
				Content:   toolErr.Error(),
				IsError:   true,
			}
			return
		}

		// Rich handlers (returning images) take precedence.
		if t.RichHandler != nil {
			richOut, err := t.RichHandler(toolCtx, tc.Input)
			if err != nil {
				if finishTool != nil {
					finishTool(err, "")
				}
				if lh != nil {
					lh.OnToolEnd(tc.Name, err, time.Since(toolStart))
				}
				results[i] = ToolResultBlock{
					ToolUseID: tc.ToolUseID,
					Content:   err.Error(),
					IsError:   true,
				}
				return
			}
			if finishTool != nil {
				finishTool(nil, richOut.Text)
			}
			if lh != nil {
				lh.OnToolEnd(tc.Name, nil, time.Since(toolStart))
			}
			result := ToolResultBlock{ToolUseID: tc.ToolUseID, Content: richOut.Text}
			for _, img := range richOut.Images {
				result.Images = append(result.Images, ImageBlock{
					Source: ImageSource{
						Data: img.Data, Base64: img.Base64,
						URL: img.URL, MIMEType: img.MIMEType,
					},
				})
			}
			results[i] = result
			return
		}

		// Apply both swarm-level and agent-level middleware.
		// Copy to avoid mutating s.middlewares when append has spare capacity.
		allMiddleware := make([]Middleware, 0, len(s.middlewares)+len(a.Middlewares()))
		allMiddleware = append(allMiddleware, s.middlewares...)
		allMiddleware = append(allMiddleware, a.Middlewares()...)
		handler := ChainMiddleware(
			func(ctx context.Context, toolName string, input json.RawMessage) (string, error) {
				return t.Handler(ctx, input)
			},
			allMiddleware...,
		)

		out, err := handler(toolCtx, tc.Name, tc.Input)
		if err != nil {
			if finishTool != nil {
				finishTool(err, "")
			}
			if lh != nil {
				lh.OnToolEnd(tc.Name, err, time.Since(toolStart))
			}
			results[i] = ToolResultBlock{
				ToolUseID: tc.ToolUseID,
				Content:   err.Error(),
				IsError:   true,
			}
			return
		}

		if out == handoffSentinel {
			if finishTool != nil {
				finishTool(nil, "handoff")
			}
			if lh != nil {
				lh.OnToolEnd(tc.Name, nil, time.Since(toolStart))
			}
			mu.Lock()
			handedOff = true
			mu.Unlock()
			results[i] = ToolResultBlock{
				ToolUseID: tc.ToolUseID,
				Content:   "Handoff accepted. Transferring conversation.",
			}
			return
		}

		if finishTool != nil {
			finishTool(nil, out)
		}
		if lh != nil {
			lh.OnToolEnd(tc.Name, nil, time.Since(toolStart))
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
