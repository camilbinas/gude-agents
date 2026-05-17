// Package debug provides a human-readable colored logging hook for local
// development. It implements agent.LoggingHook and graph.GraphLoggingHook
// with a trace-style output designed to be readable
// while an agent is running.
//
// Not intended for production use — use agent/logging/slog with a JSON handler
// for structured log aggregation.
//
// Usage:
//
//	import agentdebug "github.com/camilbinas/gude-agents/agent/logging/debug"
//
//	a, err := agent.Default(provider, instructions, tools,
//	    agentdebug.WithLogging(),
//	)
package debug

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/graph"
)

// ANSI codes.
const (
	reset  = "\033[0m"
	bold   = "\033[1m"
	dim    = "\033[2m"
	green  = "\033[32m"
	yellow = "\033[33m"
	red    = "\033[31m"
	cyan   = "\033[36m"
	blue   = "\033[34m"
	purple = "\033[35m"
)

const divider = dim + "────────────────────────────────────────────────" + reset

type debugHook struct {
	mu        sync.Mutex
	out       io.Writer
	agentName string
}

func newDebugHook(out io.Writer) *debugHook {
	return &debugHook{out: out}
}

func (h *debugHook) p(format string, args ...any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	fmt.Fprintf(h.out, format, args...)
}

// Compile-time interface checks.
var _ agent.LoggingHook = (*debugHook)(nil)
var _ graph.GraphLoggingHook = (*debugHook)(nil)

// ---------------------------------------------------------------------------
// LoggingHook — agent lifecycle
// ---------------------------------------------------------------------------

func (h *debugHook) OnInvokeStart(params agent.InvokeSpanParams) {
	name := params.AgentName
	if name == "" {
		name = h.agentName
	}
	if name == "" {
		name = "agent"
	}
	h.p("\n%s▸ invoke%s  %s%s%s  %s%s%s  max_iter=%d%s\n",
		bold+blue, reset,
		bold, name, reset,
		purple, params.ModelID, reset,
		params.MaxIterations, dim+reset,
	)
}

func (h *debugHook) OnInvokeEnd(err error, usage agent.TokenUsage, duration time.Duration) {
	if err != nil {
		h.p("\n%s✗ invoke%s  %s  %s\n%s\n", bold+red, reset, fmtDur(duration), fmtErr(err), divider)
		return
	}
	h.p("\n%s✓ invoke%s  %s  %s↑%d ↓%d%s\n%s\n",
		bold+green, reset,
		fmtDur(duration),
		dim, usage.InputTokens, usage.OutputTokens, reset,
		divider,
	)
}

func (h *debugHook) OnIterationStart(iteration int) {
	h.p("\n%s◉ iteration %d%s\n", cyan, iteration, reset)
}

func (h *debugHook) OnIterationEnd(iteration int, toolCount int, isFinal bool, duration time.Duration) {
	if isFinal {
		h.p("%s◉ iteration %d done%s  %s  %sfinal%s\n", dim, iteration, reset, fmtDur(duration), green, reset)
	} else {
		h.p("%s◉ iteration %d done%s  %s  %s%d tool(s)%s\n", dim, iteration, reset, fmtDur(duration), dim, toolCount, reset)
	}
}

func (h *debugHook) OnProviderCallStart(_ string) {}

func (h *debugHook) OnProviderCallEnd(err error, usage agent.TokenUsage, toolCallCount int, duration time.Duration) {
	if err != nil {
		h.p("\n%s⚡provider  %s  %s%s\n", dim, fmtDur(duration), fmtErr(err), reset)
		return
	}
	tools := ""
	if toolCallCount > 0 {
		tools = fmt.Sprintf("  %d tool(s)", toolCallCount)
	}
	h.p("\n%s⚡provider  %s  %s↑%d ↓%d%s%s\n",
		dim, fmtDur(duration),
		dim, usage.InputTokens, usage.OutputTokens, tools, reset,
	)
}

func (h *debugHook) OnToolStart(toolName string) {
	h.p("    %s⚙ %s%s …\n", cyan, toolName, reset)
}

func (h *debugHook) OnToolEnd(toolName string, err error, duration time.Duration) {
	if err != nil {
		h.p("    %s✗ %s  %s  %s%s\n", red, toolName, fmtDur(duration), fmtErr(err), reset)
		return
	}
	h.p("    %s✓ %s  %s%s\n", green, toolName, fmtDur(duration), reset)
}

func (h *debugHook) OnToolLog(toolName string, msg string) {
	h.p("      %s→ %s%s\n", dim, msg, reset)
}

func (h *debugHook) OnGuardrailComplete(direction string, blocked bool, err error) {
	if err != nil {
		h.p("    %s✗ guardrail(%s)  %s%s\n", red, direction, fmtErr(err), reset)
		return
	}
	if blocked {
		h.p("    %s⚠ guardrail(%s) blocked%s\n", yellow, direction, reset)
	}
}

func (h *debugHook) OnConversationStart(operation, _ string) {
	h.p("%s⟳ conversation %s %s", dim, operation, reset)
}

func (h *debugHook) OnConversationEnd(operation, _ string, err error, messageCount int, duration time.Duration) {
	if err != nil {
		h.p("%s✗ %s  %s%s\n", red, fmtDur(duration), fmtErr(err), reset)
		return
	}
	h.p("%s✓ %d msgs %s %s\n", dim, messageCount, fmtDur(duration), reset)
}

func (h *debugHook) OnRetrieverStart(_ string) {
	h.p("%s⟳ retriever%s", dim, reset)
}

func (h *debugHook) OnRetrieverEnd(err error, docCount int, duration time.Duration) {
	if err != nil {
		h.p(" %s✗ %s  %s%s\n", red, fmtDur(duration), fmtErr(err), reset)
		return
	}
	h.p(" %s✓ %s  %d docs%s\n", dim, fmtDur(duration), docCount, reset)
}

func (h *debugHook) OnImagesAttached(imageCount int) {
	h.p("%s◈ images  %s%d image(s) attached%s\n", dim, dim, imageCount, reset)
}

func (h *debugHook) OnDocumentsAttached(docCount int) {
	h.p("%s◈ documents  %s%d document(s) attached%s\n", dim, dim, docCount, reset)
}

func (h *debugHook) OnMaxIterationsExceeded(limit int) {
	h.p("    %s⚠ max iterations (%d) exceeded%s\n", yellow, limit, reset)
}

func (h *debugHook) OnStreamChunk(text string) {}

func (h *debugHook) OnResponse(text string) {}

// ---------------------------------------------------------------------------
// GraphLoggingHook — graph lifecycle
// ---------------------------------------------------------------------------

func (h *debugHook) OnGraphRunStart() {
	h.p("\n%s▸ graph run%s\n\n", bold+blue, reset)
}

func (h *debugHook) OnGraphRunEnd(err error, iterations int, usage agent.TokenUsage, duration time.Duration) {
	if err != nil {
		h.p("\n%s✗ graph run%s  %s  %s\n%s\n\n", bold+red, reset, fmtDur(duration), fmtErr(err), divider)
		return
	}
	h.p("\n%s✓ graph run%s  %s  %s%d nodes  ↑%d ↓%d%s\n%s\n\n",
		bold+green, reset,
		fmtDur(duration),
		dim, iterations, usage.InputTokens, usage.OutputTokens, reset,
		divider,
	)
}

func (h *debugHook) OnNodeStart(nodeName string) {
	h.p("  %s⚙ node: %s%s", cyan, nodeName, reset)
}

func (h *debugHook) OnNodeEnd(nodeName string, err error, duration time.Duration) {
	if err != nil {
		h.p("  %s✗ %s  %s  %s%s\n", red, nodeName, fmtDur(duration), fmtErr(err), reset)
		return
	}
	h.p("  %s✓ %s%s\n", green, fmtDur(duration), reset)
}

// ---------------------------------------------------------------------------
// Option functions
// ---------------------------------------------------------------------------

// WithLogging returns an agent.Option that installs the colored debug
// logging hook. Logs are written to stdout.
func WithLogging() agent.Option {
	return func(a *agent.Agent) error {
		h := newDebugHook(os.Stdout)
		h.agentName = a.Name()
		a.SetLoggingHook(h)
		return nil
	}
}

// WithGraphLogging returns a graph.GraphOption that installs the colored
// debug logging hook on a graph.
func WithGraphLogging() graph.GraphOption {
	return func(g graph.GraphConfigurator) error {
		h := newDebugHook(os.Stdout)
		g.SetGraphLoggingHook(h)
		return nil
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func fmtDur(d time.Duration) string {
	if d < time.Millisecond {
		return fmt.Sprintf("%s<1ms%s", dim, reset)
	}
	if d < time.Second {
		return fmt.Sprintf("%s%dms%s", dim, d.Milliseconds(), reset)
	}
	return fmt.Sprintf("%s%.1fs%s", dim, d.Seconds(), reset)
}

func fmtErr(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%s%s%s", red, err.Error(), reset)
}
