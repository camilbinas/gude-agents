package agent

import (
	"context"
	"fmt"
)

// ToolLogger allows tools to emit log messages during execution.
// Messages appear in the debug logger indented under the tool's line.
// The logger is injected into the tool's context automatically when a
// LoggingHook is configured on the agent.
type ToolLogger interface {
	// Log emits a message from within a tool execution.
	Log(msg string)

	// Logf emits a formatted message from within a tool execution.
	Logf(format string, args ...any)
}

type toolLoggerKey struct{}

// withToolLogger returns a new context with the given ToolLogger attached.
func withToolLogger(ctx context.Context, tl ToolLogger) context.Context {
	return context.WithValue(ctx, toolLoggerKey{}, tl)
}

// ToolLoggerFrom extracts the ToolLogger from a context.
// Returns a no-op logger if none is set — safe to call unconditionally.
func ToolLoggerFrom(ctx context.Context) ToolLogger {
	if tl, ok := ctx.Value(toolLoggerKey{}).(ToolLogger); ok && tl != nil {
		return tl
	}
	return nopToolLogger{}
}

// nopToolLogger is a no-op implementation for when no logger is configured.
type nopToolLogger struct{}

func (nopToolLogger) Log(string)          {}
func (nopToolLogger) Logf(string, ...any) {}

// hookToolLogger bridges the ToolLogger interface to LoggingHook.OnToolLog.
type hookToolLogger struct {
	hook     LoggingHook
	toolName string
}

func (l *hookToolLogger) Log(msg string) {
	l.hook.OnToolLog(l.toolName, msg)
}

func (l *hookToolLogger) Logf(format string, args ...any) {
	l.hook.OnToolLog(l.toolName, fmt.Sprintf(format, args...))
}
