// Package auto provides environment-aware logging that selects the appropriate
// backend based on the ENV environment variable. In production (ENV=production
// or ENV=prod), it uses structured slog logging. In all other cases, it uses
// the colored debug logger for local development.
//
// This prevents accidentally shipping verbose debug output to production.
package auto

import (
	"os"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/graph"
	"github.com/camilbinas/gude-agents/agent/logging/debug"
	agentslog "github.com/camilbinas/gude-agents/agent/logging/slog"
)

// WithLogging returns an agent.Option that installs the appropriate logging
// hook based on the environment. Checks APP_ENV, ENV, and ENVIRONMENT
// (first non-empty wins).
//
//   - development, dev, or local: colored debug output to stdout
//   - anything else / unset: structured slog
func WithLogging() agent.Option {
	if isDev() {
		return debug.WithLogging()
	}
	return agentslog.WithLogging()
}

// WithGraphLogging returns a graph.GraphOption that installs the appropriate
// logging hook based on the ENV environment variable.
func WithGraphLogging() graph.GraphOption {
	if isDev() {
		return debug.WithGraphLogging()
	}
	return agentslog.WithGraphLogging()
}

// WithSwarmLogging returns an agent.SwarmOption that installs the appropriate
// logging hook based on the ENV environment variable.
func WithSwarmLogging() agent.SwarmOption {
	if isDev() {
		return debug.WithSwarmLogging()
	}
	return agentslog.WithSwarmLogging()
}

func isDev() bool {
	for _, key := range []string{"APP_ENV", "ENV", "ENVIRONMENT"} {
		if env := strings.ToLower(os.Getenv(key)); env != "" {
			return env == "development" || env == "dev" || env == "local"
		}
	}
	return false
}
