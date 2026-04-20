package otel

import (
	"context"
	"time"

	otelglobal "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	agent "github.com/camilbinas/gude-agents/agent"
)

const defaultMeterName = "github.com/camilbinas/gude-agents"

// llmBuckets defines histogram buckets tuned for LLM latencies.
var llmBuckets = []float64{0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0, 30.0, 60.0, 120.0}

// otelHook implements agent.MetricsHook using OpenTelemetry metric instruments.
type otelHook struct {
	meter     metric.Meter
	meterName string
	agentName string

	invokeTotal          metric.Int64Counter
	invokeDuration       metric.Float64Histogram
	providerCallTotal    metric.Int64Counter
	providerCallDuration metric.Float64Histogram
	providerTokensTotal  metric.Int64Counter
	toolCallTotal        metric.Int64Counter
	toolCallDuration     metric.Float64Histogram
	guardrailBlockTotal  metric.Int64Counter
	iterationTotal       metric.Int64Counter
}

var _ agent.MetricsHook = (*otelHook)(nil)

func (h *otelHook) register() error {
	var err error

	h.invokeTotal, err = h.meter.Int64Counter("agent.invoke.total",
		metric.WithDescription("Total number of agent invocations."))
	if err != nil {
		return err
	}

	h.invokeDuration, err = h.meter.Float64Histogram("agent.invoke.duration",
		metric.WithDescription("Duration of agent invocations in seconds."),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(llmBuckets...))
	if err != nil {
		return err
	}

	h.providerCallTotal, err = h.meter.Int64Counter("agent.provider.call.total",
		metric.WithDescription("Total number of provider calls."))
	if err != nil {
		return err
	}

	h.providerCallDuration, err = h.meter.Float64Histogram("agent.provider.call.duration",
		metric.WithDescription("Duration of provider calls in seconds."),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(llmBuckets...))
	if err != nil {
		return err
	}

	h.providerTokensTotal, err = h.meter.Int64Counter("agent.provider.tokens.total",
		metric.WithDescription("Total tokens consumed by provider calls."))
	if err != nil {
		return err
	}

	h.toolCallTotal, err = h.meter.Int64Counter("agent.tool.call.total",
		metric.WithDescription("Total number of tool executions."))
	if err != nil {
		return err
	}

	h.toolCallDuration, err = h.meter.Float64Histogram("agent.tool.call.duration",
		metric.WithDescription("Duration of tool executions in seconds."),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(llmBuckets...))
	if err != nil {
		return err
	}

	h.guardrailBlockTotal, err = h.meter.Int64Counter("agent.guardrail.block.total",
		metric.WithDescription("Total number of guardrail rejections."))
	if err != nil {
		return err
	}

	h.iterationTotal, err = h.meter.Int64Counter("agent.iteration.total",
		metric.WithDescription("Total number of agent loop iterations."))
	if err != nil {
		return err
	}

	return nil
}

func statusAttr(err error) attribute.KeyValue {
	if err != nil {
		return attribute.String("status", "error")
	}
	return attribute.String("status", "success")
}

func (h *otelHook) OnInvokeStart() func(err error, usage agent.TokenUsage) {
	start := time.Now()
	return func(err error, usage agent.TokenUsage) {
		h.invokeDuration.Record(context.Background(), time.Since(start).Seconds(), metric.WithAttributes(h.baseAttrs()...))
		h.invokeTotal.Add(context.Background(), 1, metric.WithAttributes(append(h.baseAttrs(), statusAttr(err))...))
	}
}

func (h *otelHook) OnIterationStart() {
	h.iterationTotal.Add(context.Background(), 1, metric.WithAttributes(h.baseAttrs()...))
}

func (h *otelHook) OnProviderCallStart(modelID string) func(err error, usage agent.TokenUsage) {
	if modelID == "" {
		modelID = "unknown"
	}
	start := time.Now()
	return func(err error, usage agent.TokenUsage) {
		modelAttr := attribute.String("model_id", modelID)
		h.providerCallDuration.Record(context.Background(), time.Since(start).Seconds(), metric.WithAttributes(h.baseAttrs()...))
		h.providerCallTotal.Add(context.Background(), 1,
			metric.WithAttributes(append(h.baseAttrs(), modelAttr, statusAttr(err))...))
		if err == nil {
			h.providerTokensTotal.Add(context.Background(), int64(usage.InputTokens),
				metric.WithAttributes(append(h.baseAttrs(), modelAttr, attribute.String("direction", "input"))...))
			h.providerTokensTotal.Add(context.Background(), int64(usage.OutputTokens),
				metric.WithAttributes(append(h.baseAttrs(), modelAttr, attribute.String("direction", "output"))...))
		}
	}
}

func (h *otelHook) OnToolStart(toolName string) func(err error) {
	start := time.Now()
	return func(err error) {
		toolAttr := attribute.String("tool_name", toolName)
		h.toolCallDuration.Record(context.Background(), time.Since(start).Seconds(),
			metric.WithAttributes(append(h.baseAttrs(), toolAttr)...))
		h.toolCallTotal.Add(context.Background(), 1,
			metric.WithAttributes(append(h.baseAttrs(), toolAttr, statusAttr(err))...))
	}
}

func (h *otelHook) OnGuardrailComplete(direction string, blocked bool) {
	if blocked {
		h.guardrailBlockTotal.Add(context.Background(), 1,
			metric.WithAttributes(append(h.baseAttrs(), attribute.String("direction", direction))...))
	}
}

// baseAttrs returns the common attributes for all metrics.
// If an agent name is set, it's included as an attribute.
func (h *otelHook) baseAttrs() []attribute.KeyValue {
	if h.agentName != "" {
		return []attribute.KeyValue{attribute.String("agent_name", h.agentName)}
	}
	return nil
}

// Option configures the OTEL metrics hook.
type Option func(*otelHook)

// WithNamespace sets the meter instrumentation scope name.
// When empty, the default "github.com/camilbinas/gude-agents" is used.
func WithNamespace(ns string) Option {
	return func(h *otelHook) { h.meterName = ns }
}

// WithMetrics returns an agent.Option that enables OTEL metrics.
// If mp is nil, the global MeterProvider is used.
func WithMetrics(mp metric.MeterProvider, opts ...Option) agent.Option {
	return func(a *agent.Agent) error {
		if mp == nil {
			mp = otelglobal.GetMeterProvider()
		}
		h := &otelHook{}
		for _, opt := range opts {
			opt(h)
		}
		meterName := defaultMeterName
		if h.meterName != "" {
			meterName = h.meterName
		}
		h.meter = mp.Meter(meterName)
		if err := h.register(); err != nil {
			return err
		}
		h.agentName = a.Name()
		a.SetMetricsHook(h)
		return nil
	}
}
