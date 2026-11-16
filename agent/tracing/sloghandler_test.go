package tracing

import (
	"context"
	"log/slog"
	"sync"
	"testing"
)

// slogCapture records every slog.Record and its context for assertion.
type slogCapture struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *slogCapture) Enabled(context.Context, slog.Level) bool { return true }

func (h *slogCapture) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r)
	return nil
}

func (h *slogCapture) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *slogCapture) WithGroup(string) slog.Handler      { return h }

func slogAttrs(r slog.Record) map[string]string {
	m := make(map[string]string)
	r.Attrs(func(a slog.Attr) bool {
		m[a.Key] = a.Value.String()
		return true
	})
	return m
}

// TestSlogHandler_StampsTraceAndSpanID verifies that a record logged with a
// span-carrying context gains matching trace_id and span_id attributes.
func TestSlogHandler_StampsTraceAndSpanID(t *testing.T) {
	_, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	tracer := tp.Tracer("test")
	ctx, span := tracer.Start(context.Background(), "test-span")
	defer span.End()

	cap := &slogCapture{}
	logger := slog.New(SlogHandler(cap))
	logger.InfoContext(ctx, "invoke.end")

	if len(cap.records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(cap.records))
	}
	attrs := slogAttrs(cap.records[0])
	sc := span.SpanContext()
	if attrs["trace_id"] != sc.TraceID().String() {
		t.Errorf("trace_id: expected %s, got %q", sc.TraceID(), attrs["trace_id"])
	}
	if attrs["span_id"] != sc.SpanID().String() {
		t.Errorf("span_id: expected %s, got %q", sc.SpanID(), attrs["span_id"])
	}
}

// TestSlogHandler_NoSpan_NoTraceFields verifies that without an active span no
// trace_id/span_id attributes are added.
func TestSlogHandler_NoSpan_NoTraceFields(t *testing.T) {
	cap := &slogCapture{}
	logger := slog.New(SlogHandler(cap))
	logger.InfoContext(context.Background(), "no span")

	if len(cap.records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(cap.records))
	}
	attrs := slogAttrs(cap.records[0])
	if _, ok := attrs["trace_id"]; ok {
		t.Error("expected no trace_id without an active span")
	}
	if _, ok := attrs["span_id"]; ok {
		t.Error("expected no span_id without an active span")
	}
}
