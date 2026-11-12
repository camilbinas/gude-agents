package prometheus

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	agent "github.com/camilbinas/gude-agents/agent"
)

// Handler returns an http.Handler that serves Prometheus metrics
// in the standard exposition format from this hook's gatherer.
func (h *prometheusHook) Handler() http.Handler {
	return promhttp.HandlerFor(h.gatherer, promhttp.HandlerOpts{})
}

// NewHandler creates a Prometheus metrics hook and returns both the agent.Option
// and an http.Handler for scraping. This is a convenience for the common case
// where you need both the option (to pass to agent.New) and the handler (to
// mount on your HTTP server) without holding a reference to the hook.
//
// The hook is eagerly created so the handler is usable before agent.New is called.
// When agent.New applies the returned option, it installs the same hook on the agent.
//
// NewHandler is all-in-one: use it OR WithMetrics, not both. When no
// WithRegisterer option is supplied, NewHandler creates and scrapes from its
// own fresh *prometheus.Registry, whereas WithMetrics defaults to the global
// prometheus.DefaultRegisterer. Registering both on the same agent therefore
// installs two independent hooks on two different registries: the metrics the
// agent records go to one, while NewHandler's returned handler scrapes the
// other, so that handler reports empty or partial data. Pick one:
//   - handler + option from the same NewHandler call (recommended), or
//   - WithMetrics(...) plus a separate promhttp handler over the matching
//     registerer (pass the same WithRegisterer to both so they share a registry).
func NewHandler(opts ...Option) (agent.Option, http.Handler) {
	hook := &prometheusHook{}
	for _, o := range opts {
		o(hook)
	}
	if hook.registerer == nil {
		reg := prometheus.NewRegistry()
		hook.registerer = reg
		hook.gatherer = reg
	}
	hook.register()

	return agent.WithObserver(hook), hook.Handler()
}
