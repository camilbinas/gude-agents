package agent

import (
	"context"
	"fmt"

	"github.com/camilbinas/gude-agents/agent/tool"
)

func withToolLogger(ctx context.Context, logger tool.Logger) context.Context {
	return tool.WithLogger(ctx, logger)
}

// observerToolLogger routes tool-originated messages through ToolLogObserver.
type observerToolLogger struct {
	hooks *hooks
	ctx   context.Context
	base  ToolLogRecord
}

func (l *observerToolLogger) Log(message string) {
	record := l.base
	record.Message = message
	l.hooks.onToolLog(l.ctx, record)
}

func (l *observerToolLogger) Logf(format string, args ...any) {
	l.Log(fmt.Sprintf(format, args...))
}
