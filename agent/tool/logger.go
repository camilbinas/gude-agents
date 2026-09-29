package tool

import (
	"context"
	"fmt"
)

// Logger allows tools to emit messages during execution. The agent injects it
// when a ToolLogObserver is configured; callers can retrieve it with LoggerFrom.
type Logger interface {
	// Log emits a message from within a tool execution.
	Log(msg string)

	// Logf emits a formatted message from within a tool execution.
	Logf(format string, args ...any)
}

type loggerKey struct{}

// WithLogger returns a new context with the given Logger attached.
// Called by the agent package to inject the logger before tool execution.
func WithLogger(ctx context.Context, l Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, l)
}

// LoggerFrom extracts the Logger from a context.
// Returns a no-op logger if none is set — safe to call unconditionally.
func LoggerFrom(ctx context.Context) Logger {
	if l, ok := ctx.Value(loggerKey{}).(Logger); ok && l != nil {
		return l
	}
	return nopLogger{}
}

// nopLogger is a no-op implementation for when no logger is configured.
type nopLogger struct{}

func (nopLogger) Log(string)          {}
func (nopLogger) Logf(string, ...any) {}

// FuncLogger creates a Logger from a simple function.
// Useful for bridging to other logging systems.
func FuncLogger(fn func(msg string)) Logger {
	return funcLogger{fn: fn}
}

type funcLogger struct {
	fn func(msg string)
}

func (l funcLogger) Log(msg string) {
	l.fn(msg)
}

func (l funcLogger) Logf(format string, args ...any) {
	l.fn(fmt.Sprintf(format, args...))
}
