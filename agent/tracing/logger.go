package tracing

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/trace"

	agent "github.com/camilbinas/gude-agents/agent"
)

// Compile-time check that Logger implements agent.Logger.
var _ agent.Logger = (*Logger)(nil)

// Logger implements agent.Logger with structured key-value output and
// trace/span ID correlation when a span is active. It is single-invocation
// scoped: the context (and its trace/span IDs) is fixed at construction, so
// create a fresh Logger per invocation rather than reusing one.
type Logger struct {
	// Output receives each structured entry. If nil, entries are discarded.
	Output func(ctx context.Context, msg string, fields map[string]any)
	// ctx is fixed at construction and used to read the active span.
	ctx context.Context
}

// NewLogger creates a structured Logger scoped to a single invocation; pass
// that invocation's context and do not reuse the Logger across invocations.
func NewLogger(ctx context.Context) *Logger {
	return &Logger{ctx: ctx}
}

// Printf formats a message and emits it as a structured log entry.
// If a span is active in the stored context, trace_id and span_id fields
// are attached to the entry. When Output is nil the entry is discarded.
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
