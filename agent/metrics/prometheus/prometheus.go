package prometheus

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"

	agent "github.com/camilbinas/gude-agents/agent"
)

// llmBuckets defines histogram buckets tuned for LLM latencies.
var llmBuckets = []float64{0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0, 30.0, 60.0, 120.0}

// Option configures the Prometheus metrics observer.
type Option func(*prometheusHook)

// WithNamespace sets a prefix for all metric names.
func WithNamespace(ns string) Option {
	return func(h *prometheusHook) { h.namespace = ns }
}

// WithRegisterer sets a custom Prometheus registerer.
// When nil, the default prometheus.DefaultRegisterer is used.
func WithRegisterer(r prometheus.Registerer) Option {
	return func(h *prometheusHook) {
		h.registerer = r
		if g, ok := r.(prometheus.Gatherer); ok {
			h.gatherer = g
		}
	}
}

// prometheusHook implements the observer capabilities needed for Prometheus metrics.
type prometheusHook struct {
	namespace   string
	registerer  prometheus.Registerer
	gatherer    prometheus.Gatherer
	constLabels prometheus.Labels // includes agent_name when set

	invokeTotal          *prometheus.CounterVec
	invokeDuration       prometheus.Histogram
	providerCallTotal    *prometheus.CounterVec
	providerCallDuration prometheus.Histogram
	providerTokensTotal  *prometheus.CounterVec
	toolCallTotal        *prometheus.CounterVec
	toolCallDuration     *prometheus.HistogramVec
	guardrailBlockTotal  *prometheus.CounterVec
	iterationTotal       prometheus.Counter
	imagesAttachedTotal  prometheus.Counter
	docsAttachedTotal    prometheus.Counter
}

var (
	_ agent.InvokeObserver     = (*prometheusHook)(nil)
	_ agent.IterationObserver  = (*prometheusHook)(nil)
	_ agent.ModelObserver      = (*prometheusHook)(nil)
	_ agent.ToolObserver       = (*prometheusHook)(nil)
	_ agent.GuardrailObserver  = (*prometheusHook)(nil)
	_ agent.AttachmentObserver = (*prometheusHook)(nil)
)

// register creates and registers all 11 Prometheus metrics with the registerer.
func (h *prometheusHook) register() {
	ns := h.namespace
	cl := h.constLabels

	h.invokeTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: ns, Name: "agent_invoke_total",
		Help: "Total number of agent invocations.", ConstLabels: cl,
	}, []string{"status"})

	h.invokeDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: ns, Name: "agent_invoke_duration_seconds",
		Help: "Duration of agent invocations in seconds.", ConstLabels: cl,
		Buckets: llmBuckets,
	})

	h.providerCallTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: ns, Name: "agent_provider_call_total",
		Help: "Total number of provider calls.", ConstLabels: cl,
	}, []string{"model_id", "status"})

	h.providerCallDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: ns, Name: "agent_provider_call_duration_seconds",
		Help: "Duration of provider calls in seconds.", ConstLabels: cl,
		Buckets: llmBuckets,
	})

	h.providerTokensTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: ns, Name: "agent_provider_tokens_total",
		Help: "Total tokens consumed by provider calls.", ConstLabels: cl,
	}, []string{"model_id", "direction"})

	h.toolCallTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: ns, Name: "agent_tool_call_total",
		Help: "Total number of tool executions.", ConstLabels: cl,
	}, []string{"tool_name", "status"})

	h.toolCallDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: ns, Name: "agent_tool_call_duration_seconds",
		Help: "Duration of tool executions in seconds.", ConstLabels: cl,
		Buckets: llmBuckets,
	}, []string{"tool_name"})

	h.guardrailBlockTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: ns, Name: "agent_guardrail_block_total",
		Help: "Total number of guardrail rejections.", ConstLabels: cl,
	}, []string{"direction"})

	h.iterationTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: ns, Name: "agent_iteration_total",
		Help: "Total number of agent loop iterations.", ConstLabels: cl,
	})

	h.imagesAttachedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: ns, Name: "agent_images_attached_total",
		Help: "Total number of images attached to agent invocations via WithImages.", ConstLabels: cl,
	})

	h.docsAttachedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: ns, Name: "agent_documents_attached_total",
		Help: "Total number of documents attached to agent invocations via WithDocuments.", ConstLabels: cl,
	})

	// Register all collectors.
	for _, c := range []prometheus.Collector{
		h.invokeTotal, h.invokeDuration,
		h.providerCallTotal, h.providerCallDuration, h.providerTokensTotal,
		h.toolCallTotal, h.toolCallDuration,
		h.guardrailBlockTotal, h.iterationTotal, h.imagesAttachedTotal, h.docsAttachedTotal,
	} {
		h.registerer.MustRegister(c)
	}
}

// statusLabel returns "success" when err is nil, "error" otherwise.
func statusLabel(err error) string {
	if err != nil {
		return "error"
	}
	return "success"
}

// ObserveInvoke records invocation duration and status from a normalized end record.
func (h *prometheusHook) ObserveInvoke(ctx context.Context, record agent.InvokeRecord) context.Context {
	if record.Phase != agent.End {
		return ctx
	}
	h.invokeDuration.Observe(record.Duration.Seconds())
	h.invokeTotal.WithLabelValues(statusLabel(record.Err)).Inc()
	return ctx
}

func (h *prometheusHook) ObserveIteration(ctx context.Context, record agent.IterationRecord) context.Context {
	if record.Phase == agent.Start {
		h.iterationTotal.Inc()
	}
	return ctx
}

func (h *prometheusHook) ObserveModel(ctx context.Context, record agent.ModelCallRecord) context.Context {
	if record.Phase != agent.End {
		return ctx
	}
	modelID := record.ModelID
	if modelID == "" {
		modelID = "unknown"
	}
	h.providerCallDuration.Observe(record.Duration.Seconds())
	h.providerCallTotal.WithLabelValues(modelID, statusLabel(record.Err)).Inc()
	if record.Err == nil {
		h.providerTokensTotal.WithLabelValues(modelID, "input").Add(float64(record.Usage.InputTokens))
		h.providerTokensTotal.WithLabelValues(modelID, "output").Add(float64(record.Usage.OutputTokens))
		if record.Usage.CacheReadTokens > 0 {
			h.providerTokensTotal.WithLabelValues(modelID, "cache_read").Add(float64(record.Usage.CacheReadTokens))
		}
		if record.Usage.CacheWriteTokens > 0 {
			h.providerTokensTotal.WithLabelValues(modelID, "cache_write").Add(float64(record.Usage.CacheWriteTokens))
		}
	}
	return ctx
}

func (h *prometheusHook) ObserveTool(ctx context.Context, record agent.ToolCallRecord) context.Context {
	if record.Phase != agent.End {
		return ctx
	}
	h.toolCallDuration.WithLabelValues(record.Name).Observe(record.Duration.Seconds())
	h.toolCallTotal.WithLabelValues(record.Name, statusLabel(record.Err)).Inc()
	return ctx
}

func (h *prometheusHook) ObserveGuardrail(ctx context.Context, record agent.GuardrailRecord) context.Context {
	if record.Phase == agent.End && record.Blocked {
		h.guardrailBlockTotal.WithLabelValues(record.Direction).Inc()
	}
	return ctx
}

func (h *prometheusHook) ObserveAttachment(ctx context.Context, record agent.AttachmentRecord) context.Context {
	if record.Phase != agent.End {
		return ctx
	}
	if record.ImageCount > 0 {
		h.imagesAttachedTotal.Add(float64(record.ImageCount))
	}
	if record.DocumentCount > 0 {
		h.docsAttachedTotal.Add(float64(record.DocumentCount))
	}
	return ctx
}

// WithMetrics returns an agent.Option that enables Prometheus metrics.
func WithMetrics(opts ...Option) agent.Option {
	return func(a *agent.Agent) error {
		h := &prometheusHook{}
		for _, opt := range opts {
			opt(h)
		}
		if h.registerer == nil {
			h.registerer = prometheus.DefaultRegisterer
		}
		if name := a.Name(); name != "" {
			h.constLabels = prometheus.Labels{"agent_name": name}
		}
		h.register()
		return agent.WithObserver(h)(a)
	}
}
