package otel

import (
	"context"
	"time"

	otelglobal "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/camilbinas/gude-agents/agent/graph"
)

// graphOtelHook implements graph.GraphMetricsHook using OpenTelemetry metric instruments.
type graphOtelHook struct {
	meter     metric.Meter
	meterName string

	graphRunTotal     metric.Int64Counter
	graphRunDuration  metric.Float64Histogram
	graphNodeTotal    metric.Int64Counter
	graphNodeDuration metric.Float64Histogram
}

var _ graph.GraphMetricsHook = (*graphOtelHook)(nil)

func (h *graphOtelHook) register() error {
	var err error

	h.graphRunTotal, err = h.meter.Int64Counter("graph.run.total",
		metric.WithDescription("Total number of graph runs."))
	if err != nil {
		return err
	}

	h.graphRunDuration, err = h.meter.Float64Histogram("graph.run.duration",
		metric.WithDescription("Duration of graph runs in seconds."),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(llmBuckets...))
	if err != nil {
		return err
	}

	h.graphNodeTotal, err = h.meter.Int64Counter("graph.node.total",
		metric.WithDescription("Total number of graph node executions."))
	if err != nil {
		return err
	}

	h.graphNodeDuration, err = h.meter.Float64Histogram("graph.node.duration",
		metric.WithDescription("Duration of graph node executions in seconds."),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(llmBuckets...))
	if err != nil {
		return err
	}

	return nil
}

func (h *graphOtelHook) OnGraphRunStart() func(err error, iterations int) {
	start := time.Now()
	return func(err error, iterations int) {
		h.graphRunDuration.Record(context.Background(), time.Since(start).Seconds())
		h.graphRunTotal.Add(context.Background(), 1, metric.WithAttributes(statusAttr(err)))
	}
}

func (h *graphOtelHook) OnNodeStart(nodeName string) func(err error) {
	start := time.Now()
	return func(err error) {
		nameAttr := attribute.String("node_name", nodeName)
		h.graphNodeDuration.Record(context.Background(), time.Since(start).Seconds(),
			metric.WithAttributes(nameAttr))
		h.graphNodeTotal.Add(context.Background(), 1,
			metric.WithAttributes(nameAttr, statusAttr(err)))
	}
}

// WithGraphMetrics returns a graph.GraphOption that enables OTEL metrics for graph execution.
// If mp is nil, the global MeterProvider is used.
func WithGraphMetrics(mp metric.MeterProvider, opts ...Option) graph.GraphOption {
	return func(g *graph.Graph) error {
		if mp == nil {
			mp = otelglobal.GetMeterProvider()
		}
		h := &graphOtelHook{}
		// Apply options to read meterName.
		proxy := &otelHook{}
		for _, opt := range opts {
			opt(proxy)
		}
		h.meterName = proxy.meterName

		meterName := defaultMeterName
		if h.meterName != "" {
			meterName = h.meterName
		}
		h.meter = mp.Meter(meterName)
		if err := h.register(); err != nil {
			return err
		}
		g.SetGraphMetricsHook(h)
		return nil
	}
}
