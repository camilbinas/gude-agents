package prometheus

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/camilbinas/gude-agents/agent/graph"
)

// graphPrometheusHook implements graph.GraphMetricsHook using Prometheus metrics.
type graphPrometheusHook struct {
	registerer prometheus.Registerer
	namespace  string

	graphRunTotal     *prometheus.CounterVec
	graphRunDuration  prometheus.Histogram
	graphNodeTotal    *prometheus.CounterVec
	graphNodeDuration *prometheus.HistogramVec
}

var _ graph.GraphMetricsHook = (*graphPrometheusHook)(nil)

func (h *graphPrometheusHook) register() {
	ns := h.namespace

	h.graphRunTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: ns, Name: "graph_run_total",
		Help: "Total number of graph runs.",
	}, []string{"status"})

	h.graphRunDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: ns, Name: "graph_run_duration_seconds",
		Help:    "Duration of graph runs in seconds.",
		Buckets: llmBuckets,
	})

	h.graphNodeTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: ns, Name: "graph_node_total",
		Help: "Total number of graph node executions.",
	}, []string{"node_name", "status"})

	h.graphNodeDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: ns, Name: "graph_node_duration_seconds",
		Help:    "Duration of graph node executions in seconds.",
		Buckets: llmBuckets,
	}, []string{"node_name"})

	for _, c := range []prometheus.Collector{
		h.graphRunTotal, h.graphRunDuration,
		h.graphNodeTotal, h.graphNodeDuration,
	} {
		h.registerer.MustRegister(c)
	}
}

func (h *graphPrometheusHook) OnGraphRunStart() func(err error, iterations int) {
	start := time.Now()
	return func(err error, iterations int) {
		h.graphRunDuration.Observe(time.Since(start).Seconds())
		h.graphRunTotal.WithLabelValues(statusLabel(err)).Inc()
	}
}

func (h *graphPrometheusHook) OnNodeStart(nodeName string) func(err error) {
	start := time.Now()
	return func(err error) {
		h.graphNodeDuration.WithLabelValues(nodeName).Observe(time.Since(start).Seconds())
		h.graphNodeTotal.WithLabelValues(nodeName, statusLabel(err)).Inc()
	}
}

// WithGraphMetrics returns a graph.GraphOption that enables Prometheus metrics
// for graph execution.
func WithGraphMetrics(opts ...Option) graph.GraphOption {
	return func(g *graph.Graph) error {
		h := &graphPrometheusHook{}
		// Apply options via a proxy to extract namespace and registerer.
		proxy := &prometheusHook{}
		for _, opt := range opts {
			opt(proxy)
		}
		h.namespace = proxy.namespace
		h.registerer = proxy.registerer
		if h.registerer == nil {
			h.registerer = prometheus.DefaultRegisterer
		}
		h.register()
		g.SetGraphMetricsHook(h)
		return nil
	}
}
