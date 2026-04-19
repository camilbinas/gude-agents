package agent

import (
	"context"
	"fmt"
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
	if provider == nil {
		return nil, fmt.Errorf("provider is required")
	}
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

// CallProvider calls the agent's provider with timeout and retry applied.
// Used by the swarm to ensure swarm agents benefit from WithTimeout and
// WithRetry without duplicating the retry logic.
func (a *Agent) CallProvider(ctx context.Context, params ConverseParams, cb StreamCallback) (*ProviderResponse, error) {
	return a.callProviderWithRetry(ctx, params, cb)
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
