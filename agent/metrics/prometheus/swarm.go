package prometheus

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"

	agent "github.com/camilbinas/gude-agents/agent"
)

// swarmPrometheusHook implements agent.SwarmMetricsHook using Prometheus metrics.
type swarmPrometheusHook struct {
	registerer prometheus.Registerer
	namespace  string

	swarmRunTotal          *prometheus.CounterVec
	swarmRunDuration       prometheus.Histogram
	swarmAgentTurnTotal    *prometheus.CounterVec
	swarmAgentTurnDuration *prometheus.HistogramVec
	swarmHandoffTotal      *prometheus.CounterVec
}

var _ agent.SwarmMetricsHook = (*swarmPrometheusHook)(nil)

func (h *swarmPrometheusHook) register() {
	ns := h.namespace

	h.swarmRunTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: ns, Name: "swarm_run_total",
		Help: "Total number of swarm runs.",
	}, []string{"status"})

	h.swarmRunDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: ns, Name: "swarm_run_duration_seconds",
		Help:    "Duration of swarm runs in seconds.",
		Buckets: llmBuckets,
	})

	h.swarmAgentTurnTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: ns, Name: "swarm_agent_turn_total",
		Help: "Total number of swarm agent turns.",
	}, []string{"agent_name", "status"})

	h.swarmAgentTurnDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: ns, Name: "swarm_agent_turn_duration_seconds",
		Help:    "Duration of swarm agent turns in seconds.",
		Buckets: llmBuckets,
	}, []string{"agent_name"})

	h.swarmHandoffTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: ns, Name: "swarm_handoff_total",
		Help: "Total number of swarm handoffs.",
	}, []string{"from", "to"})

	for _, c := range []prometheus.Collector{
		h.swarmRunTotal, h.swarmRunDuration,
		h.swarmAgentTurnTotal, h.swarmAgentTurnDuration,
		h.swarmHandoffTotal,
	} {
		h.registerer.MustRegister(c)
	}
}

func (h *swarmPrometheusHook) OnSwarmRunStart() func(err error, result agent.SwarmResult) {
	start := time.Now()
	return func(err error, result agent.SwarmResult) {
		h.swarmRunDuration.Observe(time.Since(start).Seconds())
		h.swarmRunTotal.WithLabelValues(statusLabel(err)).Inc()
	}
}

func (h *swarmPrometheusHook) OnSwarmAgentStart(agentName string) func(err error) {
	start := time.Now()
	return func(err error) {
		h.swarmAgentTurnDuration.WithLabelValues(agentName).Observe(time.Since(start).Seconds())
		h.swarmAgentTurnTotal.WithLabelValues(agentName, statusLabel(err)).Inc()
	}
}

func (h *swarmPrometheusHook) OnSwarmHandoff(from, to string) {
	h.swarmHandoffTotal.WithLabelValues(from, to).Inc()
}

// WithSwarmMetrics returns an agent.SwarmOption that enables Prometheus metrics
// for swarm execution.
func WithSwarmMetrics(opts ...Option) agent.SwarmOption {
	return func(s *agent.Swarm) error {
		h := &swarmPrometheusHook{}
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
		s.SetSwarmMetricsHook(h)
		return nil
	}
}
