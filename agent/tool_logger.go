package agent

import (
	"context"
	"fmt"

	"github.com/camilbinas/gude-agents/agent/tool"
)

func withToolLogger(ctx context.Context, logger tool.Logger) context.Context {
	return tool.WithLogger(ctx, logger)
}

// hookToolLogger routes tool-originated messages through Hooks.ToolLog.
type hookToolLogger struct {
	hooks *hooks
	ctx   context.Context
	base  ToolLogRecord
}

func (l *hookToolLogger) Log(message string) {
	record := l.base
	record.Message = message
	l.hooks.onToolLog(l.ctx, record)
}

func (l *hookToolLogger) Logf(format string, args ...any) {
	l.Log(fmt.Sprintf(format, args...))
}
