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
	return func(g *graph.Graph) error {
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
