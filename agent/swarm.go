package agent

import (
	"context"
	"encoding/json"
	"errors"
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
			if c := FromContext(ctx); c != nil {
				c.Set(swarmActiveKey{}, targetName)
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

	// Resolve conversation ID: prefer per-request override from *Context, fall back to swarm default.
	convID := s.conversationID
	if ac := FromContext(ctx); ac != nil && ac.ConversationID() != "" {
		convID = ac.ConversationID()
	}

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

		// Create a fresh *Context for handoff detection.
		agentCtx := NewContext(ctx)

		// Start agent span if tracing is enabled.
		var finishAgent func(error)
		if s.tracingHook != nil {
			newCtx, finish := s.tracingHook.OnSwarmAgentStart(agentCtx, currentAgent)
			finishAgent = finish
			if newCtx != context.Context(agentCtx) {
				agentCtx = agentCtx.withContext(newCtx)
			}
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
			targetRaw, _ := agentCtx.Get(swarmActiveKey{})
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
func (s *Swarm) runAgent(c *Context, entry *swarmEntry, messages []Message, cb StreamCallback) (TokenUsage, string, bool, error) {
	a := entry.member.Agent

	interceptor := func(results []ToolResultBlock) bool {
		for _, r := range results {
			if r.Content == handoffSentinel {
				return true // stop the loop
			}
		}
		return false
	}

	usage, text, err := a.RunLoop(c, LoopParams{
		Messages:       messages,
		StreamCallback: cb,
		Config: LoopConfig{
			ExtraMiddleware:       s.middlewares,
			ToolResultInterceptor: interceptor,
			SkipConversationSave:  true,
		},
	})

	if errors.Is(err, ErrLoopStopped) {
		return usage, "", true, nil
	}
	return usage, text, false, err
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
