package prometheus

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"

	agent "github.com/camilbinas/gude-agents/agent"
)

// llmBuckets defines histogram buckets tuned for LLM latencies.
var llmBuckets = []float64{0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0, 30.0, 60.0, 120.0}

// Option configures the Prometheus metrics hook.
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

// prometheusHook implements agent.MetricsHook using Prometheus metrics.
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
}

var _ agent.MetricsHook = (*prometheusHook)(nil)

// register creates and registers all 9 Prometheus metrics with the registerer.
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

	// Register all collectors.
	for _, c := range []prometheus.Collector{
		h.invokeTotal, h.invokeDuration,
		h.providerCallTotal, h.providerCallDuration, h.providerTokensTotal,
		h.toolCallTotal, h.toolCallDuration,
		h.guardrailBlockTotal, h.iterationTotal,
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

// OnInvokeStart is called at the beginning of an invocation.
// It returns a finish function that records duration and status.
func (h *prometheusHook) OnInvokeStart() func(err error, usage agent.TokenUsage) {
	start := time.Now()
	return func(err error, usage agent.TokenUsage) {
		h.invokeDuration.Observe(time.Since(start).Seconds())
		h.invokeTotal.WithLabelValues(statusLabel(err)).Inc()
	}
}

// OnIterationStart is called at the beginning of each agent loop iteration.
func (h *prometheusHook) OnIterationStart() {
	h.iterationTotal.Inc()
}

// OnProviderCallStart is called before each provider call.
// It returns a finish function that records duration, status, and token counts.
func (h *prometheusHook) OnProviderCallStart(modelID string) func(err error, usage agent.TokenUsage) {
	if modelID == "" {
		modelID = "unknown"
	}
	start := time.Now()
	return func(err error, usage agent.TokenUsage) {
		h.providerCallDuration.Observe(time.Since(start).Seconds())
		h.providerCallTotal.WithLabelValues(modelID, statusLabel(err)).Inc()
		if err == nil {
			h.providerTokensTotal.WithLabelValues(modelID, "input").Add(float64(usage.InputTokens))
			h.providerTokensTotal.WithLabelValues(modelID, "output").Add(float64(usage.OutputTokens))
		}
	}
}

// OnToolStart is called before each tool execution.
// It returns a finish function that records duration and status.
func (h *prometheusHook) OnToolStart(toolName string) func(err error) {
	start := time.Now()
	return func(err error) {
		h.toolCallDuration.WithLabelValues(toolName).Observe(time.Since(start).Seconds())
		h.toolCallTotal.WithLabelValues(toolName, statusLabel(err)).Inc()
	}
}

// OnGuardrailComplete is called after a guardrail evaluation.
// It increments the block counter only when blocked is true.
func (h *prometheusHook) OnGuardrailComplete(direction string, blocked bool) {
	if blocked {
		h.guardrailBlockTotal.WithLabelValues(direction).Inc()
	}
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
		a.SetMetricsHook(h)
		return nil
	}
}
