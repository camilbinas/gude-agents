package slog

import (
	"context"
	"log/slog"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/graph"
)

// slogHook implements agent.LoggingHook, graph.GraphLoggingHook, and
// agent.SwarmLoggingHook using the standard library's log/slog package.
type slogHook struct {
	logger    *slog.Logger
	minLevel  slog.Level
	agentName string
}

// Compile-time interface checks.
var _ agent.LoggingHook = (*slogHook)(nil)
var _ graph.GraphLoggingHook = (*slogHook)(nil)
var _ agent.SwarmLoggingHook = (*slogHook)(nil)

// log emits a structured log entry if the level meets the minimum threshold.
func (h *slogHook) log(level slog.Level, msg string, attrs ...slog.Attr) {
	if level < h.minLevel {
		return
	}
	h.logger.LogAttrs(context.Background(), level, msg, attrs...)
}

// ---------------------------------------------------------------------------
// LoggingHook — agent lifecycle
// ---------------------------------------------------------------------------

func (h *slogHook) OnInvokeStart(params agent.InvokeSpanParams) {
	h.log(slog.LevelDebug, "invoke.start",
		slog.String("agent.name", h.agentName),
		slog.String("model.id", params.ModelID),
		slog.String("conversation_id", params.ConversationID),
		slog.Int("max_iterations", params.MaxIterations),
	)
}

func (h *slogHook) OnInvokeEnd(err error, usage agent.TokenUsage, duration time.Duration) {
	level := slog.LevelInfo
	attrs := []slog.Attr{
		slog.Float64("duration_ms", float64(duration.Milliseconds())),
		slog.Int("input_tokens", usage.InputTokens),
		slog.Int("output_tokens", usage.OutputTokens),
	}
	if err != nil {
		level = slog.LevelError
		attrs = append(attrs, slog.String("error", err.Error()))
	}
	h.log(level, "invoke.end", attrs...)
}

func (h *slogHook) OnIterationStart(iteration int) {
	h.log(slog.LevelDebug, "iteration.start",
		slog.Int("iteration", iteration),
	)
}

func (h *slogHook) OnProviderCallStart(modelID string) {
	h.log(slog.LevelDebug, "provider_call.start",
		slog.String("model.id", modelID),
	)
}

func (h *slogHook) OnProviderCallEnd(err error, usage agent.TokenUsage, toolCallCount int, duration time.Duration) {
	level := slog.LevelInfo
	attrs := []slog.Attr{
		slog.Float64("duration_ms", float64(duration.Milliseconds())),
		slog.Int("input_tokens", usage.InputTokens),
		slog.Int("output_tokens", usage.OutputTokens),
		slog.Int("tool_call_count", toolCallCount),
	}
	if err != nil {
		level = slog.LevelError
		attrs = append(attrs, slog.String("error", err.Error()))
	}
	h.log(level, "provider_call.end", attrs...)
}

func (h *slogHook) OnToolStart(toolName string) {
	h.log(slog.LevelDebug, "tool.start",
		slog.String("tool.name", toolName),
	)
}

func (h *slogHook) OnToolEnd(toolName string, err error, duration time.Duration) {
	level := slog.LevelInfo
	attrs := []slog.Attr{
		slog.String("tool.name", toolName),
		slog.Float64("duration_ms", float64(duration.Milliseconds())),
	}
	if err != nil {
		level = slog.LevelError
		attrs = append(attrs, slog.String("error", err.Error()))
	}
	h.log(level, "tool.end", attrs...)
}

func (h *slogHook) OnGuardrailComplete(direction string, blocked bool, err error) {
	level := slog.LevelDebug
	if blocked {
		level = slog.LevelWarn
	}
	attrs := []slog.Attr{
		slog.String("direction", direction),
		slog.Bool("blocked", blocked),
	}
	if err != nil {
		level = slog.LevelError
		attrs = append(attrs, slog.String("error", err.Error()))
	}
	h.log(level, "guardrail.complete", attrs...)
}

func (h *slogHook) OnMemoryStart(operation string, conversationID string) {
	h.log(slog.LevelDebug, "memory.start",
		slog.String("operation", operation),
		slog.String("conversation_id", conversationID),
	)
}

func (h *slogHook) OnMemoryEnd(operation string, conversationID string, err error, duration time.Duration) {
	level := slog.LevelInfo
	attrs := []slog.Attr{
		slog.String("operation", operation),
		slog.String("conversation_id", conversationID),
		slog.Float64("duration_ms", float64(duration.Milliseconds())),
	}
	if err != nil {
		level = slog.LevelError
		attrs = append(attrs, slog.String("error", err.Error()))
	}
	h.log(level, "memory.end", attrs...)
}

func (h *slogHook) OnRetrieverStart(query string) {
	h.log(slog.LevelDebug, "retriever.start",
		slog.String("query", query),
	)
}

func (h *slogHook) OnRetrieverEnd(err error, docCount int, duration time.Duration) {
	level := slog.LevelInfo
	attrs := []slog.Attr{
		slog.Int("doc_count", docCount),
		slog.Float64("duration_ms", float64(duration.Milliseconds())),
	}
	if err != nil {
		level = slog.LevelError
		attrs = append(attrs, slog.String("error", err.Error()))
	}
	h.log(level, "retriever.end", attrs...)
}

func (h *slogHook) OnMaxIterationsExceeded(limit int) {
	h.log(slog.LevelWarn, "max_iterations_exceeded",
		slog.Int("limit", limit),
	)
}

// ---------------------------------------------------------------------------
// GraphLoggingHook — graph lifecycle
// ---------------------------------------------------------------------------

func (h *slogHook) OnGraphRunStart() {
	h.log(slog.LevelDebug, "graph.run.start")
}

func (h *slogHook) OnGraphRunEnd(err error, iterations int, duration time.Duration) {
	level := slog.LevelInfo
	attrs := []slog.Attr{
		slog.Int("iterations", iterations),
		slog.Float64("duration_ms", float64(duration.Milliseconds())),
	}
	if err != nil {
		level = slog.LevelError
		attrs = append(attrs, slog.String("error", err.Error()))
	}
	h.log(level, "graph.run.end", attrs...)
}

func (h *slogHook) OnNodeStart(nodeName string) {
	h.log(slog.LevelDebug, "graph.node.start",
		slog.String("node.name", nodeName),
	)
}

func (h *slogHook) OnNodeEnd(nodeName string, err error, duration time.Duration) {
	level := slog.LevelInfo
	attrs := []slog.Attr{
		slog.String("node.name", nodeName),
		slog.Float64("duration_ms", float64(duration.Milliseconds())),
	}
	if err != nil {
		level = slog.LevelError
		attrs = append(attrs, slog.String("error", err.Error()))
	}
	h.log(level, "graph.node.end", attrs...)
}

// ---------------------------------------------------------------------------
// SwarmLoggingHook — swarm lifecycle
// ---------------------------------------------------------------------------

func (h *slogHook) OnSwarmRunStart(initialAgent string, memberCount int, maxHandoffs int) {
	h.log(slog.LevelDebug, "swarm.run.start",
		slog.String("initial_agent", initialAgent),
		slog.Int("member_count", memberCount),
		slog.Int("max_handoffs", maxHandoffs),
	)
}

func (h *slogHook) OnSwarmRunEnd(err error, result agent.SwarmResult, duration time.Duration) {
	level := slog.LevelInfo
	attrs := []slog.Attr{
		slog.String("final_agent", result.FinalAgent),
		slog.Int("handoff_count", len(result.HandoffHistory)),
		slog.Float64("duration_ms", float64(duration.Milliseconds())),
	}
	if err != nil {
		level = slog.LevelError
		attrs = append(attrs, slog.String("error", err.Error()))
	}
	h.log(level, "swarm.run.end", attrs...)
}

func (h *slogHook) OnSwarmAgentStart(agentName string) {
	h.log(slog.LevelDebug, "swarm.agent.start",
		slog.String("agent.name", agentName),
	)
}

func (h *slogHook) OnSwarmAgentEnd(agentName string, err error, duration time.Duration) {
	level := slog.LevelInfo
	attrs := []slog.Attr{
		slog.String("agent.name", agentName),
		slog.Float64("duration_ms", float64(duration.Milliseconds())),
	}
	if err != nil {
		level = slog.LevelError
		attrs = append(attrs, slog.String("error", err.Error()))
	}
	h.log(level, "swarm.agent.end", attrs...)
}

func (h *slogHook) OnSwarmHandoff(from, to string) {
	h.log(slog.LevelInfo, "swarm.handoff",
		slog.String("agent.from", from),
		slog.String("agent.to", to),
	)
}

// ---------------------------------------------------------------------------
// Option functions — wire the hook into agent, graph, and swarm
// ---------------------------------------------------------------------------

// newSlogHook creates a slogHook with defaults and applies the given options.
func newSlogHook(opts []Option) *slogHook {
	h := &slogHook{
		logger:   slog.Default(),
		minLevel: slog.LevelDebug,
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// WithLogging returns an agent.Option that installs the slog-based LoggingHook.
func WithLogging(opts ...Option) agent.Option {
	return func(a *agent.Agent) error {
		h := newSlogHook(opts)
		h.agentName = a.Name()
		a.SetLoggingHook(h)
		return nil
	}
}

// WithGraphLogging returns a graph.GraphOption that installs the slog-based GraphLoggingHook.
func WithGraphLogging(opts ...Option) graph.GraphOption {
	return func(g *graph.Graph) error {
		h := newSlogHook(opts)
		g.SetGraphLoggingHook(h)
		return nil
	}
}

// WithSwarmLogging returns an agent.SwarmOption that installs the slog-based SwarmLoggingHook.
func WithSwarmLogging(opts ...Option) agent.SwarmOption {
	return func(s *agent.Swarm) error {
		h := newSlogHook(opts)
		s.SetSwarmLoggingHook(h)
		return nil
	}
}
