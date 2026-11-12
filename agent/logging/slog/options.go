package slog

import (
	"log/slog"
)

// Option configures the slog logging observer.
type Option func(*slogHook)

// WithHandler sets a custom slog.Handler for log output.
// When not set, the observer uses slog.Default().
func WithHandler(h slog.Handler) Option {
	return func(s *slogHook) {
		s.logger = slog.New(h)
	}
}

// WithContentLogging enables logging of user and model content: the final
// response text ("response.text" entry) and the retrieval query
// ("retriever.start" entry). It is off by default so that enabling the
// observer for timing and token logs never ships prompts or answers to a log
// pipeline. This mirrors tracing.WithContentCapture.
//
// Enable it only where logs are allowed to hold user data.
func WithContentLogging() Option {
	return func(s *slogHook) {
		s.content = true
	}
}

// WithMinLevel sets the minimum log level filter.
// Log entries below this level are not emitted.
func WithMinLevel(level slog.Level) Option {
	return func(s *slogHook) {
		s.minLevel = level
	}
}
