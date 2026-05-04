package tracing

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/camilbinas/gude-agents/agent/graph"
)

// graphOtelHook implements graph.GraphTracingHook using OpenTelemetry spans.
type graphOtelHook struct {
	tracer trace.Tracer
}

// Compile-time check that graphOtelHook implements graph.GraphTracingHook.
var _ graph.GraphTracingHook = (*graphOtelHook)(nil)

// WithGraphTracing returns a graph.GraphOption that enables OpenTelemetry tracing
// for graph execution. If tp is nil, the global TracerProvider is used.
// Creates spans named "graph.run" and "graph.node.<node_name>" with appropriate attributes.
func WithGraphTracing(tp trace.TracerProvider) graph.GraphOption {
	return func(g graph.GraphConfigurator) error {
		if tp == nil {
			tp = otel.GetTracerProvider()
		}
		tracer := tp.Tracer(instrumentationName)
		g.SetGraphTracingHook(&graphOtelHook{tracer: tracer})
		return nil
	}
}

func (h *graphOtelHook) OnGraphRunStart(ctx context.Context) (context.Context, func(err error, iterations int)) {
	ctx, span := h.tracer.Start(ctx, "graph.run")
	return ctx, func(err error, iterations int) {
		span.SetAttributes(attribute.Int(AttrGraphIterations, iterations))
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		} else {
			span.SetStatus(codes.Ok, "")
		}
		span.End()
	}
}

func (h *graphOtelHook) OnNodeStart(ctx context.Context, nodeName string) (context.Context, func(err error)) {
	ctx, span := h.tracer.Start(ctx, fmt.Sprintf("graph.node.%s", nodeName))
	return ctx, func(err error) {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}
}

func (h *graphOtelHook) OnCheckpointSave(ctx context.Context, nodeName string, version int) func(err error) {
	_, span := h.tracer.Start(ctx, "graph.checkpoint.save")
	span.SetAttributes(
		attribute.String("graph.checkpoint.node", nodeName),
		attribute.Int("graph.checkpoint.version", version),
	)
	return func(err error) {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}
}

func (h *graphOtelHook) OnInterrupt(ctx context.Context, nodeName string, interruptType graph.InterruptType, version int) {
	_, span := h.tracer.Start(ctx, "graph.interrupt")
	span.SetAttributes(
		attribute.String("graph.interrupt.node", nodeName),
		attribute.String("graph.interrupt.type", string(interruptType)),
		attribute.Int("graph.interrupt.version", version),
	)
	span.End()
}

func (h *graphOtelHook) OnResume(ctx context.Context, threadID string, version int) {
	_, span := h.tracer.Start(ctx, "graph.resume")
	span.SetAttributes(
		attribute.String("graph.thread_id", threadID),
		attribute.Int("graph.resume.from_version", version),
	)
	span.End()
}

func (h *graphOtelHook) OnRewind(ctx context.Context, threadID string, targetVersion int) {
	_, span := h.tracer.Start(ctx, "graph.rewind")
	span.SetAttributes(
		attribute.String("graph.thread_id", threadID),
		attribute.Int("graph.rewind.target_version", targetVersion),
	)
	span.End()
}
