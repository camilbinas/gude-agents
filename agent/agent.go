package agent

import (
	"fmt"
	"io"
	"time"

	"github.com/camilbinas/gude-agents/agent/rag"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// Runtime is the long-lived, configured agent object. It is built once by New
// (or its convenience forwarder Agent) and then runs work through Invoke,
// Stream, TextStream, Resume, ResumeStream and the execution recovery methods.
// Its configuration is immutable after construction. Its public inspection
// surface is deliberately small: Name, Instructions and ToolSpecs. Everything
// else is configuration owned by the engine.
//
// A configured tool.Registry is the sole intentionally dynamic component.
type Runtime struct {
	name         string
	provider     Provider
	instructions string
	toolRegistry *tool.Registry

	inferenceConfig *InferenceConfig
	maxIterations   int
	tokenBudget     int

	// toolExecution and maxConcurrentTools form the tool batch execution
	// policy; see WithToolExecution and WithMaxConcurrentTools. Zero
	// maxConcurrentTools means no limit on parallel handlers.
	toolExecution      ToolExecutionMode
	maxConcurrentTools int

	conversation   ConversationStore
	contextManager ContextManager
	executionStore ExecutionStore
	normStrategy   *NormStrategy
	normDisabled   bool

	retriever        rag.Retriever
	contextFormatter rag.ContextFormatter

	middlewares      []Middleware
	inputGuardrails  []InputGuardrail
	outputGuardrails []OutputGuardrail
	toolVisibility   []ToolVisibility

	rateLimiter  RateLimiter
	rateLimitKey RateLimitKeyFunc

	providerTimeout time.Duration

	observers []Observer

	// random is the entropy source for execution IDs. Nil means
	// crypto/rand.Reader; tests substitute failing readers.
	random io.Reader

	// conversationLocks serializes same-conversation work within this Runtime.
	conversationLocks conversationLocks
}

// New creates a Runtime with immutable instructions and construction options.
//
// A nil Option in opts is skipped rather than treated as an error, so callers
// can build an option slice with conditional entries (for example a helper
// that returns a nil Option to mean "no change") without filtering nils first.
func New(provider Provider, instructions string, opts ...Option) (*Runtime, error) {
	if provider == nil {
		return nil, fmt.Errorf("provider is required")
	}
	a := &Runtime{
		provider:      provider,
		instructions:  instructions,
		toolRegistry:  &tool.Registry{},
		maxIterations: 10,
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
	}
	if a.executionStore != nil && a.conversation == nil {
		return nil, fmt.Errorf("ExecutionStore requires ConversationStore")
	}
	if a.toolExecution == ToolExecutionSequential && a.maxConcurrentTools > 1 {
		return nil, fmt.Errorf("WithToolExecution(ToolExecutionSequential) conflicts with WithMaxConcurrentTools(%d): sequential execution runs one tool at a time", a.maxConcurrentTools)
	}
	if a.rateLimitKey != nil && a.rateLimiter == nil {
		return nil, fmt.Errorf("WithRateLimitKey requires WithRateLimiter")
	}
	return a, nil
}

// Agent creates a Runtime. It is equivalent to New, accepts the same arguments
// and options, and returns the same *Runtime and errors; it exists as the
// convenient constructor spelling agent.Agent(provider, instructions, opts...).
func Agent(provider Provider, instructions string, opts ...Option) (*Runtime, error) {
	return New(provider, instructions, opts...)
}

// RAGAgent creates a Runtime whose defining behavior is automatic retrieval.
// It requires a non-nil retriever (ErrRetrieverRequired otherwise) and is
// otherwise exactly equivalent to:
//
//	New(provider, instructions, append([]Option{WithRetriever(retriever)}, opts...)...)
//
// No other defaults are applied. Use New with WithRetriever when retrieval is
// one optional feature among many.
func RAGAgent(provider Provider, instructions string, retriever rag.Retriever, opts ...Option) (*Runtime, error) {
	if retriever == nil {
		return nil, ErrRetrieverRequired
	}
	options := make([]Option, 0, len(opts)+1)
	options = append(options, WithRetriever(retriever))
	options = append(options, opts...)
	return New(provider, instructions, options...)
}

// Name returns the configured agent name.
func (a *Runtime) Name() string { return a.name }

// Instructions returns the configured system instructions.
func (a *Runtime) Instructions() string { return a.instructions }

func (a *Runtime) instructionsFor(c *Context) string {
	if c != nil {
		if override := c.Instructions(); override != "" {
			return override
		}
	}
	return a.instructions
}

// ToolSpecs returns a current snapshot of provider-facing tool specifications.
func (a *Runtime) ToolSpecs() []tool.Spec {
	tools := a.toolRegistry.List()
	specs := make([]tool.Spec, len(tools))
	for i := range tools {
		specs[i] = tools[i].Spec
	}
	return specs
}
