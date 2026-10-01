package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/camilbinas/gude-agents/agent/rag"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// Agent is long-lived, immutable invocation configuration. A configured
// tool.Registry is the sole intentionally dynamic component.
type Agent struct {
	name         string
	provider     Provider
	instructions string
	toolRegistry *tool.Registry

	inferenceConfig *InferenceConfig
	maxIterations   int
	parallelTools   bool
	tokenBudget     int

	conversation     ConversationStore
	syncConversation bool
	normStrategy     *NormStrategy
	normDisabled     bool

	retriever        rag.Retriever
	contextFormatter rag.ContextFormatter

	middlewares      []Middleware
	inputGuardrails  []InputGuardrail
	outputGuardrails []OutputGuardrail
	toolFilters      []ToolFilter

	rateLimiter *RateLimiter

	providerTimeout time.Duration
	retryMax        int
	retryBaseDelay  time.Duration

	cachingEnabled bool

	observers []Observer

	interruptStore           InterruptStore
	interruptStoreConfigured bool

	backgroundRegistry *backgroundRegistry
	bgNotify           func(conversationID, agentMessage string)
}

// New creates an Agent with immutable instructions and construction options.
func New(provider Provider, instructions string, opts ...Option) (*Agent, error) {
	if provider == nil {
		return nil, fmt.Errorf("provider is required")
	}
	a := &Agent{
		provider:       provider,
		instructions:   instructions,
		toolRegistry:   &tool.Registry{},
		maxIterations:  10,
		parallelTools:  true,
		interruptStore: newMemoryInterruptStore(),
	}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(a); err != nil {
			return nil, err
		}
	}
	if a.toolRegistry == nil {
		return nil, fmt.Errorf("tool registry must not be nil")
	}
	for _, t := range a.toolRegistry.List() {
		if err := t.Validate(); err != nil {
			return nil, err
		}
		if t.IsBackground() && a.conversation == nil {
			return nil, fmt.Errorf("tool %q: background and detached tools require a conversation store; use WithConversationStore", t.Spec.Name)
		}
	}
	if a.conversation != nil {
		a.backgroundRegistry = newBackgroundRegistry(a, a.bgNotify, nil)
	}
	return a, nil
}

// RAGAgent creates an Agent whose defining behavior is automatic retrieval.
// It requires a non-nil retriever (ErrRetrieverRequired otherwise) and is
// otherwise exactly equivalent to:
//
//	New(provider, instructions, append([]Option{WithRetriever(retriever)}, opts...)...)
//
// No other defaults are applied. Use New with WithRetriever when retrieval is
// one optional feature among many.
func RAGAgent(provider Provider, instructions string, retriever rag.Retriever, opts ...Option) (*Agent, error) {
	if retriever == nil {
		return nil, ErrRetrieverRequired
	}
	options := make([]Option, 0, len(opts)+1)
	options = append(options, WithRetriever(retriever))
	options = append(options, opts...)
	return New(provider, instructions, options...)
}

// Name returns the configured agent name.
func (a *Agent) Name() string { return a.name }

// Provider returns the configured model provider.
func (a *Agent) Provider() Provider { return a.provider }

// CallProvider calls the provider with the Agent's timeout and retry policy.
func (a *Agent) CallProvider(ctx context.Context, req ModelRequest, emit func(ModelEvent)) (*ModelResponse, error) {
	return a.callProviderWithRetry(ctx, "", req, emit)
}

// Instructions returns the configured system instructions.
func (a *Agent) Instructions() string { return a.instructions }

func (a *Agent) instructionsFor(c *Context) string {
	if c != nil {
		if override := c.Instructions(); override != "" {
			return override
		}
	}
	return a.instructions
}

// Shutdown waits for background tools, re-entry turns, and conversation flushes.
func (a *Agent) Shutdown(ctx context.Context) error {
	var err error
	if a.backgroundRegistry != nil {
		err = errors.Join(err, a.backgroundRegistry.shutdown(ctx))
	}
	if flusher, ok := a.conversation.(Flusher); ok {
		err = errors.Join(err, flusher.Flush(ctx))
	}
	return err
}

// ToolSpecs returns a current snapshot of provider-facing tool specifications.
func (a *Agent) ToolSpecs() []tool.Spec {
	tools := a.toolRegistry.List()
	specs := make([]tool.Spec, len(tools))
	for i := range tools {
		specs[i] = tools[i].Spec
	}
	return specs
}

// HasTool reports whether the current registry contains name.
func (a *Agent) HasTool(name string) bool {
	_, ok := a.toolRegistry.Lookup(name)
	return ok
}

// LookupTool returns a tool from the current registry snapshot.
func (a *Agent) LookupTool(name string) (tool.Tool, bool) { return a.toolRegistry.Lookup(name) }

// HasConversation reports whether a conversation store is configured.
func (a *Agent) HasConversation() bool { return a.conversation != nil }

func (a *Agent) InferenceConfig() *InferenceConfig { return a.inferenceConfig }
func (a *Agent) MaxIterations() int                { return a.maxIterations }
func (a *Agent) ParallelTools() bool               { return a.parallelTools }
func (a *Agent) TokenBudget() int                  { return a.tokenBudget }
func (a *Agent) Middlewares() []Middleware         { return append([]Middleware(nil), a.middlewares...) }
func (a *Agent) InputGuardrails() []InputGuardrail {
	return append([]InputGuardrail(nil), a.inputGuardrails...)
}
func (a *Agent) OutputGuardrails() []OutputGuardrail {
	return append([]OutputGuardrail(nil), a.outputGuardrails...)
}
