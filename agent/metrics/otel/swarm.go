package otel

import (
	"context"
	"time"

	otelglobal "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	agent "github.com/camilbinas/gude-agents/agent"
)

// swarmOtelHook implements agent.SwarmMetricsHook using OpenTelemetry metric instruments.
type swarmOtelHook struct {
	meter     metric.Meter
	meterName string

	swarmRunTotal          metric.Int64Counter
	swarmRunDuration       metric.Float64Histogram
	swarmAgentTurnTotal    metric.Int64Counter
	swarmAgentTurnDuration metric.Float64Histogram
	swarmHandoffTotal      metric.Int64Counter
}

var _ agent.SwarmMetricsHook = (*swarmOtelHook)(nil)

func (h *swarmOtelHook) register() error {
	var err error

	h.swarmRunTotal, err = h.meter.Int64Counter("swarm.run.total",
		metric.WithDescription("Total number of swarm runs."))
	if err != nil {
		return err
	}

	h.swarmRunDuration, err = h.meter.Float64Histogram("swarm.run.duration",
		metric.WithDescription("Duration of swarm runs in seconds."),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(llmBuckets...))
	if err != nil {
		return err
	}

	h.swarmAgentTurnTotal, err = h.meter.Int64Counter("swarm.agent.turn.total",
		metric.WithDescription("Total number of swarm agent turns."))
	if err != nil {
		return err
	}

	h.swarmAgentTurnDuration, err = h.meter.Float64Histogram("swarm.agent.turn.duration",
		metric.WithDescription("Duration of swarm agent turns in seconds."),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(llmBuckets...))
	if err != nil {
		return err
	}

	h.swarmHandoffTotal, err = h.meter.Int64Counter("swarm.handoff.total",
		metric.WithDescription("Total number of swarm handoffs."))
	if err != nil {
		return err
	}

	return nil
}

func (h *swarmOtelHook) OnSwarmRunStart() func(err error, result agent.SwarmResult) {
	start := time.Now()
	return func(err error, result agent.SwarmResult) {
		h.swarmRunDuration.Record(context.Background(), time.Since(start).Seconds())
		h.swarmRunTotal.Add(context.Background(), 1, metric.WithAttributes(statusAttr(err)))
	}
}

func (h *swarmOtelHook) OnSwarmAgentStart(agentName string) func(err error) {
	start := time.Now()
	return func(err error) {
		nameAttr := attribute.String("agent_name", agentName)
		h.swarmAgentTurnDuration.Record(context.Background(), time.Since(start).Seconds(),
			metric.WithAttributes(nameAttr))
		h.swarmAgentTurnTotal.Add(context.Background(), 1,
			metric.WithAttributes(nameAttr, statusAttr(err)))
	}
}

func (h *swarmOtelHook) OnSwarmHandoff(from, to string) {
	h.swarmHandoffTotal.Add(context.Background(), 1,
		metric.WithAttributes(
			attribute.String("from", from),
			attribute.String("to", to),
		))
}

// WithSwarmMetrics returns an agent.SwarmOption that enables OTEL metrics for swarm execution.
// If mp is nil, the global MeterProvider is used.
func WithSwarmMetrics(mp metric.MeterProvider, opts ...Option) agent.SwarmOption {
	return func(s *agent.Swarm) error {
		if mp == nil {
			mp = otelglobal.GetMeterProvider()
		}
		h := &swarmOtelHook{}
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
		s.SetSwarmMetricsHook(h)
		return nil
	}
}
