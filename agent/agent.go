package agent

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// Agent orchestrates LLM calls and tool execution.
type Agent struct {
	name     string
	provider Provider

	// instructions is the agent's system prompt. Stored behind an atomic
	// pointer so SetInstructions can update it concurrently with in-flight
	// invocations without locking the hot path. Always non-nil after New.
	instructions atomic.Pointer[string]

	// Tools
	toolsMu   sync.RWMutex // protects tools and toolSpecs for runtime registration
	tools     map[string]tool.Tool
	toolSpecs []tool.Spec

	// Inference
	inferenceConfig *InferenceConfig // nil = use provider defaults
	maxIterations   int
	parallelTools   bool
	tokenBudget     int // 0 = no budget

	// Conversation
	conversation     Conversation
	conversationID   string
	syncConversation bool          // call Wait() on conversation after each Save
	normStrategy     *NormStrategy // nil = default (Merge); pointer distinguishes "not set" from "set to Merge"
	normDisabled     bool

	// RAG
	retriever        Retriever        // nil = no RAG
	contextFormatter ContextFormatter // nil = use DefaultContextFormatter

	// Pipeline
	middlewares      []Middleware
	inputGuardrails  []InputGuardrail
	outputGuardrails []OutputGuardrail
	toolFilters      []ToolFilter

	// Rate limiting
	rateLimiter *RateLimiter // nil = no rate limiting

	// Resilience
	providerTimeout time.Duration // 0 = no timeout
	retryMax        int           // 0 = no retry
	retryBaseDelay  time.Duration

	// Observability
	tracingHook TracingHook // nil = no tracing
	metricsHook MetricsHook // nil = no metrics
	loggingHook LoggingHook // nil = no logging

	// Handoff
	handoffStore HandoffStore // nil = caller manages HandoffRequest persistence

	// Background tools
	backgroundRegistry *backgroundRegistry                       // nil until a Background_Tool is registered; manages dispatch, locks, and shutdown
	bgNotify           func(conversationID, agentMessage string) // Notify_Callback set via WithBackgroundNotify; wired onto the registry at construction
}

// New creates a new Agent. Returns an error if tool validation fails or an option errors.
func New(provider Provider, instructions prompt.Instructions, tools []tool.Tool, opts ...Option) (*Agent, error) {
	if provider == nil {
		return nil, fmt.Errorf("provider is required")
	}
	a := &Agent{
		provider:      provider,
		tools:         make(map[string]tool.Tool),
		maxIterations: 10,
	}
	initial := instructions.String()
	a.instructions.Store(&initial)

	// Register and validate tools.
	for _, t := range tools {
		if t.Spec.Name == "" || t.Spec.Description == "" || (t.Handler == nil && t.RichHandler == nil) {
			return nil, fmt.Errorf("tool %q: name, description, and handler are required", t.Spec.Name)
		}
		if t.IsBackground() {
			if t.Ack() == "" {
				return nil, fmt.Errorf("tool %q: background tools require a non-empty ack string", t.Spec.Name)
			}
			if t.Handler == nil {
				return nil, fmt.Errorf("tool %q: background tools require a handler", t.Spec.Name)
			}
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

	// Validate that Background_Tools have a conversation store configured.
	// This check runs after options are applied because WithConversation /
	// WithSharedConversation set a.conversation.
	for _, t := range a.tools {
		if t.IsBackground() && a.conversation == nil {
			return nil, fmt.Errorf("tool %q: background tools require a conversation store; use WithConversation or WithSharedConversation", t.Spec.Name)
		}
	}

	// Construct the backgroundRegistry whenever a conversation store is
	// configured. We do this eagerly even when no Background_Tool is registered
	// yet so that tools added later via RegisterTool can dispatch without
	// introducing a data race on a.backgroundRegistry (loop.go reads the field
	// without holding toolsMu). The registry is also the holder of the
	// per-conversation lock map, which serializes same-conversation user turns
	// even without Background_Tools — a desirable invariant for any agent
	// configured with a conversation store. When no conversation store is
	// configured, no Background_Tool can be registered (validated here and in
	// RegisterTool), so the registry is not needed.
	if a.conversation != nil {
		a.backgroundRegistry = newBackgroundRegistry(a, a.bgNotify, nil)
	}

	return a, nil
}

// ---------------------------------------------------------------------------
// Accessor methods — used by subpackages (graph) that need read access
// to agent internals without touching unexported fields.
// ---------------------------------------------------------------------------

// Name returns the agent's name, or empty if not set.
func (a *Agent) Name() string { return a.name }

// Provider returns the agent's LLM provider.
func (a *Agent) Provider() Provider { return a.provider }

// CallProvider calls the agent's provider with timeout and retry applied.
// Useful for subpackages that need to invoke the provider with the agent's
// retry and timeout settings without duplicating the logic.
func (a *Agent) CallProvider(ctx context.Context, params ConverseParams, cb StreamCallback) (*ProviderResponse, error) {
	return a.callProviderWithRetry(ctx, "", params, cb)
}

// Instructions returns the agent's system prompt string.
func (a *Agent) Instructions() string {
	if p := a.instructions.Load(); p != nil {
		return *p
	}
	return ""
}

// instructionsFor returns the system prompt to use for the given invocation
// context. If the context carries a non-empty system prompt override (set via
// Context.WithSystemPromptOverride), that takes precedence over the agent's
// configured instructions. This is the entry point used by the agent loop and
// supports per-request prompt selection for A/B testing.
func (a *Agent) instructionsFor(c *Context) string {
	if c != nil {
		if override := c.SystemPromptOverride(); override != "" {
			return override
		}
	}
	return a.Instructions()
}

// SetInstructions atomically updates the agent's system prompt. Subsequent
// invocations use the new value; in-flight invocations continue with the
// value they read at start. Intended for hot-reload scenarios such as
// AgentCore configuration bundle updates. Safe for concurrent use.
func (a *Agent) SetInstructions(s string) {
	a.instructions.Store(&s)
}

// Close performs graceful cleanup. If the agent has in-flight Background_Handlers or
// Re_Entry_Turns, Close blocks until they all complete. Then, if the agent's conversation
// implements ConversationWaiter (e.g. the Summary strategy), Close blocks until all
// background summarisation work is complete.
// Safe to call multiple times. No-op if no cleanup is needed.
func (a *Agent) Close() {
	if a.backgroundRegistry != nil {
		a.backgroundRegistry.wg.Wait()
	}
	if a.conversation != nil {
		if w, ok := a.conversation.(ConversationWaiter); ok {
			w.Wait()
		}
	}
}

// ToolSpecs returns a snapshot of the tool specifications registered on this agent.
func (a *Agent) ToolSpecs() []tool.Spec {
	a.toolsMu.RLock()
	defer a.toolsMu.RUnlock()
	cp := make([]tool.Spec, len(a.toolSpecs))
	copy(cp, a.toolSpecs)
	return cp
}

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
// same name is already registered or if Background_Tool prerequisites are not met.
func (a *Agent) RegisterTool(t tool.Tool) error {
	// Validate Background_Tool prerequisites before acquiring the lock or mutating state.
	if t.IsBackground() {
		if t.Ack() == "" {
			return fmt.Errorf("tool %q: background tools require a non-empty ack string", t.Spec.Name)
		}
		if t.Handler == nil {
			return fmt.Errorf("tool %q: background tools require a handler", t.Spec.Name)
		}
		if a.conversation == nil {
			return fmt.Errorf("tool %q: background tools require a conversation store; use WithConversation or WithSharedConversation", t.Spec.Name)
		}
	}

	a.toolsMu.Lock()
	defer a.toolsMu.Unlock()
	if _, exists := a.tools[t.Spec.Name]; exists {
		return fmt.Errorf("duplicate tool name: %q", t.Spec.Name)
	}
	a.tools[t.Spec.Name] = t
	a.toolSpecs = append(a.toolSpecs, t.Spec)
	return nil
}

// HasConversation reports whether the agent has a conversation store configured.
func (a *Agent) HasConversation() bool { return a.conversation != nil }

// SetConversation sets the agent's conversation store. This is intended for use
// by runtime adapters (e.g. agentcore) that need to wire a conversation store
// after construction. It operates as a shared conversation (no default ID).
func (a *Agent) SetConversation(c Conversation) {
	a.conversation = c
}

// InferenceConfig returns the agent's inference config, or nil if none is set.
func (a *Agent) InferenceConfig() *InferenceConfig { return a.inferenceConfig }

// MaxIterations returns the configured maximum iterations per invocation.
func (a *Agent) MaxIterations() int { return a.maxIterations }

// ParallelTools returns whether parallel tool execution is enabled.
func (a *Agent) ParallelTools() bool { return a.parallelTools }

// TokenBudget returns the configured token budget (0 = no budget).
func (a *Agent) TokenBudget() int { return a.tokenBudget }

// Middlewares returns the agent's middleware chain.
func (a *Agent) Middlewares() []Middleware { return a.middlewares }

// InputGuardrails returns the agent's input guardrails.
func (a *Agent) InputGuardrails() []InputGuardrail { return a.inputGuardrails }

// OutputGuardrails returns the agent's output guardrails.
func (a *Agent) OutputGuardrails() []OutputGuardrail { return a.outputGuardrails }
