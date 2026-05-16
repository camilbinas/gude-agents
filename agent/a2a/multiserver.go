package a2a

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/camilbinas/gude-agents/agent"
	"google.golang.org/grpc"
)

// AgentRegistration pairs a path prefix with an agent and optional card overrides.
type AgentRegistration struct {
	Prefix   string       // e.g. "/agents/summarizer"
	Agent    *agent.Agent // the agent to host at this prefix
	CardOpts []CardOption // optional card overrides
}

// MultiServer hosts multiple agents on a single HTTP server, each at its own
// path prefix. Each agent gets an independent Agent Card and request handler.
type MultiServer struct {
	entries         []serverEntry
	logger          *slog.Logger
	gracefulTimeout time.Duration
}

type serverEntry struct {
	prefix string
	server *Server
}

// MultiServerOption configures a MultiServer.
type MultiServerOption func(*multiServerConfig)

type multiServerConfig struct {
	logger          *slog.Logger
	gracefulTimeout time.Duration
}

// WithMultiServerLogger sets the MultiServer's logger.
func WithMultiServerLogger(l *slog.Logger) MultiServerOption {
	return func(cfg *multiServerConfig) {
		cfg.logger = l
	}
}

// WithMultiServerGracefulTimeout sets the graceful shutdown timeout (default 30s).
func WithMultiServerGracefulTimeout(d time.Duration) MultiServerOption {
	return func(cfg *multiServerConfig) {
		cfg.gracefulTimeout = d
	}
}

// NewMultiServer creates a MultiServer from a list of agent registrations.
// Returns an error if any prefix is duplicated or if any agent is nil.
func NewMultiServer(registrations []AgentRegistration, opts ...MultiServerOption) (*MultiServer, error) {
	cfg := &multiServerConfig{
		logger:          slog.Default(),
		gracefulTimeout: 30 * time.Second,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	// Validate no duplicate prefixes.
	seen := make(map[string]struct{}, len(registrations))
	for _, reg := range registrations {
		if _, exists := seen[reg.Prefix]; exists {
			return nil, fmt.Errorf("a2a: duplicate prefix %q", reg.Prefix)
		}
		seen[reg.Prefix] = struct{}{}
	}

	entries := make([]serverEntry, 0, len(registrations))
	for _, reg := range registrations {
		if reg.Agent == nil {
			return nil, fmt.Errorf("a2a: agent is nil for prefix %q", reg.Prefix)
		}

		// Prepend WithCardURL(prefix) so the card's endpoint reflects the routing prefix.
		cardOpts := make([]CardOption, 0, len(reg.CardOpts)+1)
		cardOpts = append(cardOpts, WithCardURL(reg.Prefix))
		cardOpts = append(cardOpts, reg.CardOpts...)

		srv, err := NewServer(reg.Agent, cardOpts, WithLogger(cfg.logger), WithGracefulTimeout(cfg.gracefulTimeout))
		if err != nil {
			return nil, fmt.Errorf("a2a: creating server for prefix %q: %w", reg.Prefix, err)
		}

		entries = append(entries, serverEntry{
			prefix: reg.Prefix,
			server: srv,
		})
	}

	return &MultiServer{
		entries:         entries,
		logger:          cfg.logger,
		gracefulTimeout: cfg.gracefulTimeout,
	}, nil
}

// Handler returns an http.Handler that routes JSON-RPC requests by path prefix.
// Requests to {prefix}/.well-known/agent-card.json return the agent's card.
// Requests to {prefix}/ are handled by the agent's JSON-RPC handler.
// Unmatched prefixes return 404.
func (ms *MultiServer) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, entry := range ms.entries {
		cardPath := entry.prefix + "/.well-known/agent-card.json"
		rpcPath := entry.prefix + "/"
		mux.Handle(cardPath, a2asrv.NewStaticAgentCardHandler(entry.server.card))
		mux.Handle(rpcPath, a2asrv.NewJSONRPCHandler(entry.server.handler))
	}
	return mux
}

// RESTHandler returns an http.Handler that routes REST requests by path prefix.
func (ms *MultiServer) RESTHandler() http.Handler {
	mux := http.NewServeMux()
	for _, entry := range ms.entries {
		cardPath := entry.prefix + "/.well-known/agent-card.json"
		restPath := entry.prefix + "/"
		mux.Handle(cardPath, a2asrv.NewStaticAgentCardHandler(entry.server.card))
		mux.Handle(restPath, a2asrv.NewRESTHandler(entry.server.handler))
	}
	return mux
}

// RegisterGRPC registers each agent's gRPC service with the given server.
func (ms *MultiServer) RegisterGRPC(srv *grpc.Server) {
	for _, entry := range ms.entries {
		entry.server.RegisterGRPC(srv)
	}
}

// ListenAndServe starts an HTTP server on addr with the JSON-RPC handler.
// It performs graceful shutdown when ctx is canceled.
func (ms *MultiServer) ListenAndServe(ctx context.Context, addr string) error {
	srv := &http.Server{
		Addr:    addr,
		Handler: ms.Handler(),
	}

	errCh := make(chan error, 1)
	go func() {
		ms.logger.Info("a2a multiserver starting", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("a2a: multiserver error: %w", err)
	case <-ctx.Done():
	}

	ms.logger.Info("a2a multiserver shutting down", "timeout", ms.gracefulTimeout)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), ms.gracefulTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("a2a: multiserver shutdown error: %w", err)
	}
	return nil
}
