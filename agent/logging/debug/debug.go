// Package debug provides a human-readable colored logging observer for local
// development. It implements the relevant agent observer capabilities with a
// trace-style output designed to be readable while an agent is running.
//
// Not intended for production use — use agent/logging/slog with a JSON handler
// for structured log aggregation.
//
// Usage:
//
//	import agentdebug "github.com/camilbinas/gude-agents/agent/logging/debug"
//
//	a, err := agent.New(provider, instructions,
//		agentdebug.WithLogging(),
//	)
package debug

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/camilbinas/gude-agents/agent"
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
	mu  sync.Mutex
	out io.Writer
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
var (
	_ agent.InvokeObserver       = (*debugHook)(nil)
	_ agent.IterationObserver    = (*debugHook)(nil)
	_ agent.ModelObserver        = (*debugHook)(nil)
	_ agent.ToolObserver         = (*debugHook)(nil)
	_ agent.GuardrailObserver    = (*debugHook)(nil)
	_ agent.ConversationObserver = (*debugHook)(nil)
	_ agent.RetrievalObserver    = (*debugHook)(nil)
	_ agent.AttachmentObserver   = (*debugHook)(nil)
	_ agent.LimitObserver        = (*debugHook)(nil)
	_ agent.ToolLogObserver      = (*debugHook)(nil)
)

// ---------------------------------------------------------------------------
// Observer methods — agent lifecycle
// ---------------------------------------------------------------------------

func (h *debugHook) ObserveInvoke(ctx context.Context, record agent.InvokeRecord) context.Context {
	switch record.Phase {
	case agent.Start:
		name := record.AgentName
		if name == "" {
			name = "agent"
		}
		h.p("\n%s▸ invoke%s  %s%s%s  %s%s%s  max_iter=%d%s\n",
			bold+blue, reset,
			bold, name, reset,
			purple, record.ModelID, reset,
			record.MaxIterations, dim+reset,
		)
	case agent.End:
		if record.Err != nil {
			h.p("\n%s✗ invoke%s  %s  %s\n%s\n", bold+red, reset, fmtDur(record.Duration), fmtErr(record.Err), divider)
			return ctx
		}
		h.p("\n%s✓ invoke%s  %s  %s↑%d ↓%d%s\n%s\n",
			bold+green, reset,
			fmtDur(record.Duration),
			dim, record.Usage.InputTokens, record.Usage.OutputTokens, reset,
			divider,
		)
	}
	return ctx
}

func (h *debugHook) ObserveIteration(ctx context.Context, record agent.IterationRecord) context.Context {
	switch record.Phase {
	case agent.Start:
		h.p("\n%s◉ iteration %d%s\n", cyan, record.Iteration, reset)
	case agent.End:
		if record.IsFinal {
			h.p("%s◉ iteration %d done%s  %s  %sfinal%s\n", dim, record.Iteration, reset, fmtDur(record.Duration), green, reset)
		} else {
			h.p("%s◉ iteration %d done%s  %s  %s%d tool(s)%s\n", dim, record.Iteration, reset, fmtDur(record.Duration), dim, record.ToolCount, reset)
		}
	}
	return ctx
}

func (h *debugHook) ObserveModel(ctx context.Context, record agent.ModelCallRecord) context.Context {
	if record.Phase != agent.End {
		return ctx
	}
	if record.Err != nil {
		h.p("\n%s⚡provider  %s  %s%s\n", dim, fmtDur(record.Duration), fmtErr(record.Err), reset)
		return ctx
	}
	tools := ""
	if record.ToolCallCount > 0 {
		tools = fmt.Sprintf("  %d tool(s)", record.ToolCallCount)
	}
	h.p("\n%s⚡provider  %s  %s↑%d ↓%d  cache_w=%d cache_r=%d%s%s\n",
		dim, fmtDur(record.Duration),
		dim, record.Usage.InputTokens, record.Usage.OutputTokens, record.Usage.CacheWriteTokens, record.Usage.CacheReadTokens, tools, reset,
	)
	return ctx
}

func (h *debugHook) ObserveTool(ctx context.Context, record agent.ToolCallRecord) context.Context {
	switch record.Phase {
	case agent.Start:
		h.p("    %s⚙ %s%s …\n", cyan, record.Name, reset)
	case agent.End:
		if record.Err != nil {
			h.p("    %s✗ %s  %s  %s%s\n", red, record.Name, fmtDur(record.Duration), fmtErr(record.Err), reset)
			return ctx
		}
		h.p("    %s✓ %s  %s%s\n", green, record.Name, fmtDur(record.Duration), reset)
	}
	return ctx
}

func (h *debugHook) ObserveToolLog(ctx context.Context, record agent.ToolLogRecord) context.Context {
	h.p("      %s→ %s%s\n", dim, record.Message, reset)
	return ctx
}

func (h *debugHook) ObserveGuardrail(ctx context.Context, record agent.GuardrailRecord) context.Context {
	if record.Phase != agent.End {
		return ctx
	}
	if record.Err != nil {
		h.p("    %s✗ guardrail(%s)  %s%s\n", red, record.Direction, fmtErr(record.Err), reset)
		return ctx
	}
	if record.Blocked {
		h.p("    %s⚠ guardrail(%s) blocked%s\n", yellow, record.Direction, reset)
	}
	return ctx
}

func (h *debugHook) ObserveConversation(ctx context.Context, record agent.ConversationRecord) context.Context {
	switch record.Phase {
	case agent.Start:
		h.p("%s⟳ conversation %s %s", dim, record.Operation, reset)
	case agent.End:
		if record.Err != nil {
			h.p("%s✗ %s  %s%s\n", red, fmtDur(record.Duration), fmtErr(record.Err), reset)
			return ctx
		}
		h.p("%s✓ %d msgs %s %s\n", dim, record.MessageCount, fmtDur(record.Duration), reset)
	}
	return ctx
}

func (h *debugHook) ObserveRetrieval(ctx context.Context, record agent.RetrievalRecord) context.Context {
	switch record.Phase {
	case agent.Start:
		h.p("%s⟳ retriever%s", dim, reset)
	case agent.End:
		if record.Err != nil {
			h.p(" %s✗ %s  %s%s\n", red, fmtDur(record.Duration), fmtErr(record.Err), reset)
			return ctx
		}
		h.p(" %s✓ %s  %d docs%s\n", dim, fmtDur(record.Duration), record.DocumentCount, reset)
	}
	return ctx
}

func (h *debugHook) ObserveAttachment(ctx context.Context, record agent.AttachmentRecord) context.Context {
	if record.Phase != agent.End {
		return ctx
	}
	if record.ImageCount > 0 {
		h.p("%s◈ images  %s%d image(s) attached%s\n", dim, dim, record.ImageCount, reset)
	}
	if record.DocumentCount > 0 {
		h.p("%s◈ documents  %s%d document(s) attached%s\n", dim, dim, record.DocumentCount, reset)
	}
	return ctx
}

func (h *debugHook) ObserveLimit(ctx context.Context, record agent.LimitRecord) context.Context {
	if record.Phase == agent.End && record.Name == "max_iterations" {
		h.p("    %s⚠ max iterations (%d) exceeded%s\n", yellow, record.Limit, reset)
	}
	return ctx
}

// ---------------------------------------------------------------------------
// Option functions
// ---------------------------------------------------------------------------

// WithLogging returns an agent.Option that installs the colored debug logging
// observer. Logs are written to stdout.
func WithLogging() agent.Option {
	return agent.WithObserver(newDebugHook(os.Stdout))
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
