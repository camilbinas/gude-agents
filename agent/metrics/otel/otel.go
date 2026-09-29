package otel

import (
	"context"

	otelglobal "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	agent "github.com/camilbinas/gude-agents/agent"
)

const defaultMeterName = "github.com/camilbinas/gude-agents"

// llmBuckets defines histogram buckets tuned for LLM latencies.
var llmBuckets = []float64{0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0, 30.0, 60.0, 120.0}

// otelHook implements the observer capabilities needed for OpenTelemetry metrics.
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
	imagesAttachedTotal  metric.Int64Counter
	docsAttachedTotal    metric.Int64Counter
}

var (
	_ agent.InvokeObserver     = (*otelHook)(nil)
	_ agent.IterationObserver  = (*otelHook)(nil)
	_ agent.ModelObserver      = (*otelHook)(nil)
	_ agent.ToolObserver       = (*otelHook)(nil)
	_ agent.GuardrailObserver  = (*otelHook)(nil)
	_ agent.AttachmentObserver = (*otelHook)(nil)
)

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

	h.imagesAttachedTotal, err = h.meter.Int64Counter("agent.images.attached.total",
		metric.WithDescription("Total number of images attached to agent invocations via WithImages."))
	if err != nil {
		return err
	}

	h.docsAttachedTotal, err = h.meter.Int64Counter("agent.documents.attached.total",
		metric.WithDescription("Total number of documents attached to agent invocations via WithDocuments."))
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

func (h *otelHook) ObserveInvoke(ctx context.Context, record agent.InvokeRecord) context.Context {
	if record.Phase != agent.End {
		return ctx
	}
	h.invokeDuration.Record(ctx, record.Duration.Seconds(), metric.WithAttributes(h.baseAttrs()...))
	h.invokeTotal.Add(ctx, 1, metric.WithAttributes(append(h.baseAttrs(), statusAttr(record.Err))...))
	return ctx
}

func (h *otelHook) ObserveIteration(ctx context.Context, record agent.IterationRecord) context.Context {
	if record.Phase == agent.Start {
		h.iterationTotal.Add(ctx, 1, metric.WithAttributes(h.baseAttrs()...))
	}
	return ctx
}

func (h *otelHook) ObserveModel(ctx context.Context, record agent.ModelCallRecord) context.Context {
	if record.Phase != agent.End {
		return ctx
	}
	modelID := record.ModelID
	if modelID == "" {
		modelID = "unknown"
	}
	modelAttr := attribute.String("model_id", modelID)
	h.providerCallDuration.Record(ctx, record.Duration.Seconds(), metric.WithAttributes(h.baseAttrs()...))
	h.providerCallTotal.Add(ctx, 1,
		metric.WithAttributes(append(h.baseAttrs(), modelAttr, statusAttr(record.Err))...))
	if record.Err == nil {
		h.providerTokensTotal.Add(ctx, int64(record.Usage.InputTokens),
			metric.WithAttributes(append(h.baseAttrs(), modelAttr, attribute.String("direction", "input"))...))
		h.providerTokensTotal.Add(ctx, int64(record.Usage.OutputTokens),
			metric.WithAttributes(append(h.baseAttrs(), modelAttr, attribute.String("direction", "output"))...))
		if record.Usage.CacheReadTokens > 0 {
			h.providerTokensTotal.Add(ctx, int64(record.Usage.CacheReadTokens),
				metric.WithAttributes(append(h.baseAttrs(), modelAttr, attribute.String("direction", "cache_read"))...))
		}
		if record.Usage.CacheWriteTokens > 0 {
			h.providerTokensTotal.Add(ctx, int64(record.Usage.CacheWriteTokens),
				metric.WithAttributes(append(h.baseAttrs(), modelAttr, attribute.String("direction", "cache_write"))...))
		}
	}
	return ctx
}

func (h *otelHook) ObserveTool(ctx context.Context, record agent.ToolCallRecord) context.Context {
	if record.Phase != agent.End {
		return ctx
	}
	toolAttr := attribute.String("tool_name", record.Name)
	h.toolCallDuration.Record(ctx, record.Duration.Seconds(),
		metric.WithAttributes(append(h.baseAttrs(), toolAttr)...))
	h.toolCallTotal.Add(ctx, 1,
		metric.WithAttributes(append(h.baseAttrs(), toolAttr, statusAttr(record.Err))...))
	return ctx
}

func (h *otelHook) ObserveGuardrail(ctx context.Context, record agent.GuardrailRecord) context.Context {
	if record.Phase == agent.End && record.Blocked {
		h.guardrailBlockTotal.Add(ctx, 1,
			metric.WithAttributes(append(h.baseAttrs(), attribute.String("direction", record.Direction))...))
	}
	return ctx
}

func (h *otelHook) ObserveAttachment(ctx context.Context, record agent.AttachmentRecord) context.Context {
	if record.Phase != agent.End {
		return ctx
	}
	if record.ImageCount > 0 {
		h.imagesAttachedTotal.Add(ctx, int64(record.ImageCount), metric.WithAttributes(h.baseAttrs()...))
	}
	if record.DocumentCount > 0 {
		h.docsAttachedTotal.Add(ctx, int64(record.DocumentCount), metric.WithAttributes(h.baseAttrs()...))
	}
	return ctx
}

// baseAttrs returns the common attributes for all metrics.
// If an agent name is set, it's included as an attribute.
func (h *otelHook) baseAttrs() []attribute.KeyValue {
	if h.agentName != "" {
		return []attribute.KeyValue{attribute.String("agent_name", h.agentName)}
	}
	return nil
}

// Option configures the OTEL metrics observer.
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
		return agent.WithObserver(h)(a)
	}
}
