package agent

import (
	"context"
	"fmt"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// ToolLogger is an alias for tool.Logger for backward compatibility.
// Tools should prefer tool.LoggerFrom(ctx) directly.
type ToolLogger = tool.Logger

// ToolLoggerFrom extracts the ToolLogger from a context.
// Returns a no-op logger if none is set — safe to call unconditionally.
// This is a convenience re-export of tool.LoggerFrom.
func ToolLoggerFrom(ctx context.Context) ToolLogger {
	return tool.LoggerFrom(ctx)
}

// withToolLogger returns a new context with the given ToolLogger attached.
func withToolLogger(ctx context.Context, tl ToolLogger) context.Context {
	return tool.WithLogger(ctx, tl)
}

// hookToolLogger bridges the tool.Logger interface to LoggingHook.OnToolLog.
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
