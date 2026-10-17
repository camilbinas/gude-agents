// Package debug provides a human-readable colored logging observer for local
// development. It implements the relevant agent observer capabilities with a
// trace-style output designed to be readable while an agent is running.
//
// Not intended for production use — use agent/logging/slog with a JSON handler
// for structured log aggregation.
//
// By default the trace goes to stdout with ANSI colors. WithWriter redirects it
// (a file, a test buffer, a TUI pane) and WithColor(false) removes every ANSI
// escape sequence:
//
//	import agentdebug "github.com/camilbinas/gude-agents/agent/logging/debug"
//
//	a, err := agent.New(provider, instructions,
//		agentdebug.WithLogging(),
//	)
//
//	var trace bytes.Buffer
//	a, err = agent.New(provider, instructions,
//		agentdebug.WithLogging(agentdebug.WithWriter(&trace), agentdebug.WithColor(false)),
//	)
package debug

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sync"
	"time"

	"github.com/camilbinas/gude-agents/agent"
)

// palette holds the ANSI sequences used by the trace. Every field is empty
// when color is disabled.
type palette struct {
	reset, bold, dim, green, yellow, red, cyan, blue, purple string
	divider                                                  string
}

var colorPalette = func() palette {
	p := palette{
		reset:  "\033[0m",
		bold:   "\033[1m",
		dim:    "\033[2m",
		green:  "\033[32m",
		yellow: "\033[33m",
		red:    "\033[31m",
		cyan:   "\033[36m",
		blue:   "\033[34m",
		purple: "\033[35m",
	}
	p.divider = p.dim + dividerRule + p.reset
	return p
}()

// plainPalette is the zero palette plus an uncolored divider.
var plainPalette = palette{divider: dividerRule}

const dividerRule = "────────────────────────────────────────────────"

// ansiSequence matches CSI escape sequences. Plain output is scrubbed with it
// so that text supplied by tools, models or errors cannot smuggle colors in.
var ansiSequence = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]")

// config holds the validated logging options.
type config struct {
	out   io.Writer
	color bool
}

// Option configures the debug logger. Options are validated when the agent is
// constructed; an invalid option makes agent.New fail.
type Option func(*config) error

// WithWriter sends the trace to w instead of stdout. A nil writer is a
// construction error. Writes are serialized, so concurrent events never
// interleave within a line.
func WithWriter(w io.Writer) Option {
	return func(c *config) error {
		if w == nil {
			return errors.New("debug: WithWriter: writer must not be nil")
		}
		c.out = w
		return nil
	}
}

// WithColor turns ANSI colors on or off. They are on by default. With
// WithColor(false) no escape sequence is written anywhere, dividers and error
// lines included.
func WithColor(enabled bool) Option {
	return func(c *config) error {
		c.color = enabled
		return nil
	}
}

type debugHook struct {
	mu    sync.Mutex
	out   io.Writer
	color bool
	c     palette
}

func newDebugHook(out io.Writer, color bool) *debugHook {
	h := &debugHook{out: out, color: color, c: plainPalette}
	if color {
		h.c = colorPalette
	}
	return h
}

func (h *debugHook) p(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	if !h.color {
		line = ansiSequence.ReplaceAllString(line, "")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	_, _ = io.WriteString(h.out, line)
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
			h.c.bold+h.c.blue, h.c.reset,
			h.c.bold, name, h.c.reset,
			h.c.purple, record.ModelID, h.c.reset,
			record.MaxIterations, h.c.dim+h.c.reset,
		)
	case agent.End:
		if record.Err != nil {
			h.p("\n%s✗ invoke%s  %s  %s\n%s\n", h.c.bold+h.c.red, h.c.reset, h.fmtDur(record.Duration), h.fmtErr(record.Err), h.c.divider)
			return ctx
		}
		h.p("\n%s✓ invoke%s  %s  %s↑%d ↓%d%s\n%s\n",
			h.c.bold+h.c.green, h.c.reset,
			h.fmtDur(record.Duration),
			h.c.dim, record.Usage.InputTokens, record.Usage.OutputTokens, h.c.reset,
			h.c.divider,
		)
	}
	return ctx
}

func (h *debugHook) ObserveIteration(ctx context.Context, record agent.IterationRecord) context.Context {
	switch record.Phase {
	case agent.Start:
		h.p("\n%s◉ iteration %d%s\n", h.c.cyan, record.Iteration, h.c.reset)
	case agent.End:
		if record.IsFinal {
			h.p("%s◉ iteration %d done%s  %s  %sfinal%s\n", h.c.dim, record.Iteration, h.c.reset, h.fmtDur(record.Duration), h.c.green, h.c.reset)
		} else {
			h.p("%s◉ iteration %d done%s  %s  %s%d tool(s)%s\n", h.c.dim, record.Iteration, h.c.reset, h.fmtDur(record.Duration), h.c.dim, record.ToolCount, h.c.reset)
		}
	}
	return ctx
}

func (h *debugHook) ObserveModel(ctx context.Context, record agent.ModelCallRecord) context.Context {
	if record.Phase != agent.End {
		return ctx
	}
	if record.Err != nil {
		h.p("\n%s⚡provider  %s  %s%s\n", h.c.dim, h.fmtDur(record.Duration), h.fmtErr(record.Err), h.c.reset)
		return ctx
	}
	tools := ""
	if record.ToolCallCount > 0 {
		tools = fmt.Sprintf("  %d tool(s)", record.ToolCallCount)
	}
	h.p("\n%s⚡provider  %s  %s↑%d ↓%d  cache_w=%d cache_r=%d%s%s\n",
		h.c.dim, h.fmtDur(record.Duration),
		h.c.dim, record.Usage.InputTokens, record.Usage.OutputTokens, record.Usage.CacheWriteTokens, record.Usage.CacheReadTokens, tools, h.c.reset,
	)
	return ctx
}

func (h *debugHook) ObserveTool(ctx context.Context, record agent.ToolCallRecord) context.Context {
	switch record.Phase {
	case agent.Start:
		h.p("    %s⚙ %s%s …\n", h.c.cyan, record.Name, h.c.reset)
	case agent.End:
		if record.Err != nil {
			h.p("    %s✗ %s  %s  %s%s\n", h.c.red, record.Name, h.fmtDur(record.Duration), h.fmtErr(record.Err), h.c.reset)
			return ctx
		}
		h.p("    %s✓ %s  %s%s\n", h.c.green, record.Name, h.fmtDur(record.Duration), h.c.reset)
	}
	return ctx
}

func (h *debugHook) ObserveToolLog(ctx context.Context, record agent.ToolLogRecord) context.Context {
	h.p("      %s→ %s%s\n", h.c.dim, record.Message, h.c.reset)
	return ctx
}

func (h *debugHook) ObserveGuardrail(ctx context.Context, record agent.GuardrailRecord) context.Context {
	if record.Phase != agent.End {
		return ctx
	}
	if record.Err != nil {
		h.p("    %s✗ guardrail(%s)  %s%s\n", h.c.red, record.Direction, h.fmtErr(record.Err), h.c.reset)
		return ctx
	}
	if record.Blocked {
		h.p("    %s⚠ guardrail(%s) blocked%s\n", h.c.yellow, record.Direction, h.c.reset)
	}
	return ctx
}

func (h *debugHook) ObserveConversation(ctx context.Context, record agent.ConversationRecord) context.Context {
	switch record.Phase {
	case agent.Start:
		h.p("%s⟳ conversation %s %s", h.c.dim, record.Operation, h.c.reset)
	case agent.End:
		if record.Err != nil {
			h.p("%s✗ %s  %s%s\n", h.c.red, h.fmtDur(record.Duration), h.fmtErr(record.Err), h.c.reset)
			return ctx
		}
		h.p("%s✓ %d msgs %s %s\n", h.c.dim, record.MessageCount, h.fmtDur(record.Duration), h.c.reset)
	}
	return ctx
}

func (h *debugHook) ObserveRetrieval(ctx context.Context, record agent.RetrievalRecord) context.Context {
	switch record.Phase {
	case agent.Start:
		h.p("%s⟳ retriever%s", h.c.dim, h.c.reset)
	case agent.End:
		if record.Err != nil {
			h.p(" %s✗ %s  %s%s\n", h.c.red, h.fmtDur(record.Duration), h.fmtErr(record.Err), h.c.reset)
			return ctx
		}
		h.p(" %s✓ %s  %d docs%s\n", h.c.dim, h.fmtDur(record.Duration), record.DocumentCount, h.c.reset)
	}
	return ctx
}

func (h *debugHook) ObserveAttachment(ctx context.Context, record agent.AttachmentRecord) context.Context {
	if record.Phase != agent.End {
		return ctx
	}
	if record.ImageCount > 0 {
		h.p("%s◈ images  %s%d image(s) attached%s\n", h.c.dim, h.c.dim, record.ImageCount, h.c.reset)
	}
	if record.DocumentCount > 0 {
		h.p("%s◈ documents  %s%d document(s) attached%s\n", h.c.dim, h.c.dim, record.DocumentCount, h.c.reset)
	}
	return ctx
}

func (h *debugHook) ObserveLimit(ctx context.Context, record agent.LimitRecord) context.Context {
	if record.Phase == agent.End && record.Name == "max_iterations" {
		h.p("    %s⚠ max iterations (%d) exceeded%s\n", h.c.yellow, record.Limit, h.c.reset)
	}
	return ctx
}

// ---------------------------------------------------------------------------
// Option functions
// ---------------------------------------------------------------------------

// WithLogging returns an agent.Option that installs the human-readable debug
// logging observer. Defaults: stdout with ANSI colors. An invalid option, such
// as a nil writer, makes agent.New return an error.
func WithLogging(opts ...Option) agent.Option {
	cfg := config{out: os.Stdout, color: true}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(&cfg); err != nil {
			return func(*agent.Agent) error { return err }
		}
	}
	return agent.WithObserver(newDebugHook(cfg.out, cfg.color))
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (h *debugHook) fmtDur(d time.Duration) string {
	if d < time.Millisecond {
		return fmt.Sprintf("%s<1ms%s", h.c.dim, h.c.reset)
	}
	if d < time.Second {
		return fmt.Sprintf("%s%dms%s", h.c.dim, d.Milliseconds(), h.c.reset)
	}
	return fmt.Sprintf("%s%.1fs%s", h.c.dim, d.Seconds(), h.c.reset)
}

func (h *debugHook) fmtErr(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%s%s%s", h.c.red, err.Error(), h.c.reset)
}
