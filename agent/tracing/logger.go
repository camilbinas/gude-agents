package tracing

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/trace"

	agent "github.com/camilbinas/gude-agents/agent"
)

// Compile-time check that Logger implements agent.Logger.
var _ agent.Logger = (*Logger)(nil)

// Logger implements agent.Logger with structured key-value output
// and trace/span ID correlation when a span is active.
type Logger struct {
	// Output function for structured entries. If nil, log entries are discarded.
	Output func(ctx context.Context, msg string, fields map[string]any)
	// ctx is stored to access the current span for trace correlation.
	ctx context.Context
}

// NewLogger creates a structured Logger. The context is used to extract
// the active span for trace ID correlation.
func NewLogger(ctx context.Context) *Logger {
	return &Logger{ctx: ctx}
}

// Printf formats a message and emits it as a structured log entry.
// If a span is active in the stored context, trace_id and span_id fields
// are attached to the entry.
func (l *Logger) Printf(format string, v ...any) {
	fields := make(map[string]any)
	if span := trace.SpanFromContext(l.ctx); span.SpanContext().IsValid() {
		fields["trace_id"] = span.SpanContext().TraceID().String()
		fields["span_id"] = span.SpanContext().SpanID().String()
	}
	fields["msg"] = fmt.Sprintf(format, v...)
	if l.Output != nil {
		l.Output(l.ctx, fields["msg"].(string), fields)
	}
}
