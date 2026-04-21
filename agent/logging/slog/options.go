package slog

import (
	"log/slog"
)

// Option configures the slog logging hook.
type Option func(*slogHook)

// WithHandler sets a custom slog.Handler for log output.
// When not set, the hook uses slog.Default().
func WithHandler(h slog.Handler) Option {
	return func(s *slogHook) {
		s.logger = slog.New(h)
	}
}

// WithMinLevel sets the minimum log level filter.
// Log entries below this level are not emitted.
func WithMinLevel(level slog.Level) Option {
	return func(s *slogHook) {
		s.minLevel = level
	}
}
