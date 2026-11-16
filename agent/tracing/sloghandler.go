package tracing

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

// slogTraceHandler wraps a slog.Handler and stamps trace_id and span_id onto
// every record whose context carries a valid OTel span.
type slogTraceHandler struct {
	inner slog.Handler
}

// SlogHandler wraps inner so that every record carrying an active OTel span in
// its context gains trace_id and span_id attributes, correlating logs with the
// spans from WithTracing. Use it with logging/slog.WithHandler; the slog
// observer already logs with the span-carrying context, so pass this handler
// and register tracing before the slog observer.
func SlogHandler(inner slog.Handler) slog.Handler {
	if inner == nil {
		inner = slog.Default().Handler()
	}
	return &slogTraceHandler{inner: inner}
}

func (h *slogTraceHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *slogTraceHandler) Handle(ctx context.Context, r slog.Record) error {
	if span := trace.SpanFromContext(ctx); span.SpanContext().IsValid() {
		sc := span.SpanContext()
		r = r.Clone()
		r.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}
	return h.inner.Handle(ctx, r)
}

func (h *slogTraceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &slogTraceHandler{inner: h.inner.WithAttrs(attrs)}
}

func (h *slogTraceHandler) WithGroup(name string) slog.Handler {
	return &slogTraceHandler{inner: h.inner.WithGroup(name)}
}
