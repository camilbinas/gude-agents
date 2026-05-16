package a2a

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	a2agrpc "github.com/a2aproject/a2a-go/v2/a2agrpc/v1"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/camilbinas/gude-agents/agent"
	"google.golang.org/grpc"
)

// Server wraps the a2a-go SDK components and provides a high-level API for
// exposing a gude-agents Agent over the A2A protocol.
type Server struct {
	handler         a2asrv.RequestHandler
	card            *a2a.AgentCard
	logger          *slog.Logger
	executor        *Executor
	gracefulTimeout time.Duration
}

// ServerOption configures a Server.
type ServerOption func(*serverConfig)

type serverConfig struct {
	logger          *slog.Logger
	cardOpts        []CardOption
	handlerOpts     []a2asrv.RequestHandlerOption
	gracefulTimeout time.Duration
}

// WithLogger sets the server's logger.
func WithLogger(l *slog.Logger) ServerOption {
	return func(cfg *serverConfig) {
		cfg.logger = l
	}
}

// WithHandlerOptions passes additional RequestHandlerOption to the SDK handler.
func WithHandlerOptions(opts ...a2asrv.RequestHandlerOption) ServerOption {
	return func(cfg *serverConfig) {
		cfg.handlerOpts = append(cfg.handlerOpts, opts...)
	}
}

// WithGracefulTimeout sets the graceful shutdown timeout (default 30s).
func WithGracefulTimeout(d time.Duration) ServerOption {
	return func(cfg *serverConfig) {
		cfg.gracefulTimeout = d
	}
}

// NewServer creates a new A2A server wrapping the given agent.
// CardOption values customize the auto-derived Agent Card.
// ServerOption values configure the server behavior.
func NewServer(a *agent.Agent, cardOpts []CardOption, serverOpts ...ServerOption) (*Server, error) {
	if a == nil {
		return nil, fmt.Errorf("a2a: agent is required")
	}

	cfg := &serverConfig{
		logger:          slog.Default(),
		gracefulTimeout: 30 * time.Second,
	}
	for _, opt := range serverOpts {
		opt(cfg)
	}

	executor := NewExecutor(a, cfg.logger)
	card := DeriveCard(a, cardOpts...)

	// Build SDK handler options.
	handlerOpts := []a2asrv.RequestHandlerOption{
		a2asrv.WithLogger(cfg.logger),
		a2asrv.WithCapabilityChecks(&card.Capabilities),
	}
	handlerOpts = append(handlerOpts, cfg.handlerOpts...)

	handler := a2asrv.NewHandler(executor, handlerOpts...)

	return &Server{
		handler:         handler,
		card:            card,
		logger:          cfg.logger,
		executor:        executor,
		gracefulTimeout: cfg.gracefulTimeout,
	}, nil
}

// Card returns the derived Agent Card.
func (s *Server) Card() *a2a.AgentCard {
	return s.card
}

// Handler returns an http.Handler implementing the A2A JSON-RPC transport
// plus the well-known agent card endpoint.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(s.card))
	mux.Handle("/", a2asrv.NewJSONRPCHandler(s.handler))
	return mux
}

// RESTHandler returns an http.Handler implementing the A2A REST transport
// plus the well-known agent card endpoint.
func (s *Server) RESTHandler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(s.card))
	mux.Handle("/", a2asrv.NewRESTHandler(s.handler))
	return mux
}

// RegisterGRPC registers the A2A gRPC service with the given gRPC server.
func (s *Server) RegisterGRPC(srv *grpc.Server) {
	grpcHandler := a2agrpc.NewHandler(s.handler)
	grpcHandler.RegisterWith(srv)
}

// ListenAndServe starts an HTTP server on addr with the JSON-RPC handler.
// It performs graceful shutdown when ctx is canceled.
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	srv := &http.Server{
		Addr:    addr,
		Handler: s.Handler(),
	}

	errCh := make(chan error, 1)
	go func() {
		s.logger.Info("a2a server starting", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("a2a: server error: %w", err)
	case <-ctx.Done():
	}

	s.logger.Info("a2a server shutting down", "timeout", s.gracefulTimeout)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.gracefulTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("a2a: shutdown error: %w", err)
	}
	return nil
}
