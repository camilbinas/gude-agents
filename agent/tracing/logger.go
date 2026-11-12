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
// trace/span ID correlation when a span is active.
//
// Scope: a Logger is single-invocation scoped. It captures the context passed
// to NewLogger and reads the active span from that captured context on every
// Printf call. Do not share one Logger across invocations or requests:
// because the context (and therefore the trace/span IDs) is fixed at
// construction, a reused Logger would stamp every later entry with the first
// invocation's trace and span IDs. Create a fresh Logger per invocation with
// that invocation's context.
//
// A zero-value Logger{} (or one whose Output is nil) silently discards every
// entry; see the Output field.
type Logger struct {
	// Output receives each structured entry: the captured context, the
	// formatted message, and a fields map that includes trace_id and span_id
	// when a span is active. If Output is nil, log entries are discarded
	// (no output, no error) — set it to emit anywhere.
	Output func(ctx context.Context, msg string, fields map[string]any)
	// ctx is stored to access the current span for trace correlation. It is
	// fixed at construction time (see the type doc on single-invocation scope).
	ctx context.Context
}

// NewLogger creates a structured Logger scoped to a single invocation. The
// context is captured and used to extract the active span for trace/span ID
// correlation on every Printf call, so pass the invocation's context and do
// not reuse the returned Logger across invocations (see Logger).
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
