package agentcore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/camilbinas/gude-agents/agent"
)

// SessionHeader is the HTTP header AgentCore uses to propagate the
// runtime session ID. The Server reads it on every /invocations request
// and threads the value through to the agent as the conversation ID.
const SessionHeader = "X-Amzn-Bedrock-AgentCore-Runtime-Session-Id"

// HealthStatus is the value returned by the /ping endpoint.
type HealthStatus string

const (
	// StatusHealthy reports that the server is ready for new work.
	StatusHealthy HealthStatus = "Healthy"
	// StatusHealthyBusy reports that the server is operational but
	// currently processing background work and should not receive new
	// invocations until the busy state clears.
	StatusHealthyBusy HealthStatus = "HealthyBusy"
)

// Server implements the AWS Bedrock AgentCore Runtime HTTP contract
// (POST /invocations, GET /ping). Session IDs from the request header are
// propagated as conversation IDs. Use Mux() to attach additional routes.
type Server struct {
	agent        *agent.Agent
	mux          *http.ServeMux
	addr         string
	readTO       time.Duration
	writeTO      time.Duration
	idleTO       time.Duration
	maxBody      int64
	drainTO      time.Duration
	lastUpdate   atomic.Int64
	busy         atomic.Bool
	logger       *log.Logger
	bundleClient *BundleClient
	bundleApply  BundleApplier
}

// ServerOption configures a Server.
type ServerOption func(*Server)

// WithAddr sets the listen address. The default is ":8080" which matches
// the AgentCore Runtime container contract.
func WithAddr(addr string) ServerOption {
	return func(s *Server) {
		if addr != "" {
			s.addr = addr
		}
	}
}

// WithMaxBody caps the request body size accepted by /invocations. A
// non-positive value disables the cap. The default is 1 MiB.
func WithMaxBody(n int64) ServerOption {
	return func(s *Server) {
		s.maxBody = n
	}
}

// WithReadTimeout sets the HTTP server read timeout. The default is 30s.
func WithReadTimeout(d time.Duration) ServerOption {
	return func(s *Server) {
		if d > 0 {
			s.readTO = d
		}
	}
}

// WithWriteTimeout sets the HTTP server write timeout. The default is 5
// minutes which accommodates long agent runs.
func WithWriteTimeout(d time.Duration) ServerOption {
	return func(s *Server) {
		if d > 0 {
			s.writeTO = d
		}
	}
}

// WithIdleTimeout sets the HTTP server idle timeout. The default is 2
// minutes.
func WithIdleTimeout(d time.Duration) ServerOption {
	return func(s *Server) {
		if d > 0 {
			s.idleTO = d
		}
	}
}

// WithDrainTimeout sets the maximum time ListenAndServe waits for
// in-flight requests to finish during graceful shutdown. The default is
// 30 seconds.
func WithDrainTimeout(d time.Duration) ServerOption {
	return func(s *Server) {
		if d > 0 {
			s.drainTO = d
		}
	}
}

// WithLogger sets the logger used for server errors and operational
// events. The default is the standard library logger.
func WithLogger(l *log.Logger) ServerOption {
	return func(s *Server) {
		if l != nil {
			s.logger = l
		}
	}
}

// BundleApplier maps a resolved AgentCore configuration bundle to the
// per-request *agent.Context. It runs after baggage parsing but before the
// agent is invoked, so any context mutations (system prompt override,
// inference config, etc.) take effect for that request only.
//
// The default applier (used when only WithBundleClient is set) reads the
// "system_prompt" key from the bundle and applies it via
// Context.WithSystemPromptOverride. Provide a custom applier when you want
// to map additional fields (model_id, temperature, ...) or use different
// keys.
type BundleApplier func(ctx *agent.Context, ref BundleRef, cfg BundleConfig)

// defaultBundleApplier reads "system_prompt" and sets the per-request prompt
// override.
func defaultBundleApplier(ctx *agent.Context, _ BundleRef, cfg BundleConfig) {
	if prompt := cfg.String("system_prompt"); prompt != "" {
		ctx.WithSystemPromptOverride(prompt)
	}
}

// WithBundleClient enables AgentCore configuration bundle integration on the
// Server. When set, Server parses the W3C baggage header on every
// /invocations request, resolves the bundle via the client, and applies the
// configuration to the request's *agent.Context using the configured
// BundleApplier (or the default if none is configured).
//
// Requests without baggage headers are unaffected — the agent uses its
// configured instructions as before.
func WithBundleClient(c *BundleClient) ServerOption {
	return func(s *Server) {
		s.bundleClient = c
	}
}

// WithBundleApplier overrides the default BundleApplier. Has effect only
// when WithBundleClient is also configured.
func WithBundleApplier(fn BundleApplier) ServerOption {
	return func(s *Server) {
		if fn != nil {
			s.bundleApply = fn
		}
	}
}

// NewServer constructs a Server bound to the given Agent. A non-nil
// agent is required.
func NewServer(a *agent.Agent, opts ...ServerOption) (*Server, error) {
	if a == nil {
		return nil, errors.New("agentcore server: agent is required")
	}
	s := &Server{
		agent:       a,
		mux:         http.NewServeMux(),
		addr:        ":8080",
		readTO:      30 * time.Second,
		writeTO:     5 * time.Minute,
		idleTO:      2 * time.Minute,
		maxBody:     1 << 20, // 1 MiB
		drainTO:     30 * time.Second,
		logger:      log.Default(),
		bundleApply: defaultBundleApplier,
	}
	for _, opt := range opts {
		opt(s)
	}
	s.lastUpdate.Store(time.Now().Unix())

	s.mux.HandleFunc("/invocations", s.handleInvocations)
	s.mux.HandleFunc("/ping", s.handlePing)

	return s, nil
}

// Mux returns the underlying ServeMux so callers can attach additional
// routes alongside the AgentCore contract endpoints.
func (s *Server) Mux() *http.ServeMux { return s.mux }

// Addr returns the configured listen address.
func (s *Server) Addr() string { return s.addr }

// SetBusy marks the server as busy with background work. While busy, the
// /ping endpoint reports HealthyBusy and refreshes the time_of_last_update.
func (s *Server) SetBusy(busy bool) {
	s.busy.Store(busy)
	s.lastUpdate.Store(time.Now().Unix())
}

// ListenAndServe starts the HTTP server and blocks until ctx is cancelled
// or the server fails. On ctx cancellation, it triggers a graceful
// shutdown bounded by the configured drain timeout.
func (s *Server) ListenAndServe(ctx context.Context) error {
	srv := &http.Server{
		Addr:         s.addr,
		Handler:      s.mux,
		ReadTimeout:  s.readTO,
		WriteTimeout: s.writeTO,
		IdleTimeout:  s.idleTO,
	}

	errCh := make(chan error, 1)
	go func() {
		s.logger.Printf("agentcore server: listening on %s", s.addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		s.logger.Printf("agentcore server: shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.drainTO)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("agentcore server: shutdown: %w", err)
		}
		return nil
	}
}

// invocationRequest is the JSON envelope accepted by /invocations.
// Only the prompt is required; the session id is normally provided via
// the SessionHeader and falls back to this field for clients that cannot
// set headers.
type invocationRequest struct {
	Prompt    string `json:"prompt"`
	SessionID string `json:"session_id,omitempty"`
	Stream    bool   `json:"stream,omitempty"`
}

// invocationResponse is the JSON envelope returned by /invocations for
// non-streaming responses. Matches the AgentCore HTTP contract example.
type invocationResponse struct {
	Response string `json:"response"`
	Status   string `json:"status"`
}

// errorResponse is returned for invocation failures.
type errorResponse struct {
	Error  string `json:"error"`
	Status string `json:"status"`
}

func (s *Server) handleInvocations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}

	if s.maxBody > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, s.maxBody)
	}
	defer r.Body.Close()

	var req invocationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Prompt == "" {
		writeJSONError(w, http.StatusBadRequest, "prompt is required")
		return
	}

	sessionID := r.Header.Get(SessionHeader)
	if sessionID == "" {
		sessionID = req.SessionID
	}

	ctx := agent.NewContext(r.Context())
	if sessionID != "" {
		ctx.WithConversationID(sessionID)
	}

	// Resolve and apply AgentCore configuration bundle when wired. Baggage
	// is the per-request hand-off from the AgentCore Gateway during A/B
	// testing.
	if s.bundleClient != nil {
		if ref := parseBundleRefFromBaggage(r.Header.Get("baggage")); !ref.IsZero() {
			cfg, err := s.bundleClient.Resolve(r.Context(), ref)
			if err != nil {
				s.logger.Printf("agentcore server: bundle resolve failed (continuing with defaults) version=%s err=%v",
					ref.VersionID, err)
			} else if s.bundleApply != nil {
				s.bundleApply(ctx, ref, cfg)
			}
		}
	}

	// Streaming is selected via Accept header (text/event-stream) or the
	// `stream` field in the JSON body. The agent's InvokeStream is used
	// in both modes; non-streaming simply collects chunks.
	wantsStream := req.Stream || acceptsSSE(r)

	s.lastUpdate.Store(time.Now().Unix())

	if wantsStream {
		s.streamResponse(w, r, ctx, req.Prompt)
		return
	}

	result, err := s.agent.Invoke(ctx, req.Prompt)
	if err != nil {
		s.logger.Printf("agentcore server: invoke error: %v", err)
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(invocationResponse{
		Response: result,
		Status:   "success",
	})
}

// streamResponse writes the agent's output as Server-Sent Events. Each
// chunk is emitted as a `data:` line carrying a JSON object with an
// `event` field, mirroring the example in the AgentCore HTTP contract.
func (s *Server) streamResponse(w http.ResponseWriter, r *http.Request, ctx *agent.Context, prompt string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "streaming not supported by transport")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	cb := func(chunk string) {
		if chunk == "" {
			return
		}
		payload, err := json.Marshal(map[string]string{"event": chunk})
		if err != nil {
			return
		}
		fmt.Fprintf(w, "data: %s\n\n", payload)
		flusher.Flush()
	}

	if err := s.agent.InvokeStream(ctx, prompt, cb); err != nil {
		s.logger.Printf("agentcore server: stream error: %v", err)
		// Best-effort error event for SSE clients.
		errPayload, _ := json.Marshal(map[string]string{"error": err.Error()})
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", errPayload)
		flusher.Flush()
		return
	}

	// Final sentinel allows clients to detect end-of-stream cleanly.
	fmt.Fprint(w, "event: done\ndata: {}\n\n")
	flusher.Flush()
}

// pingResponse matches the JSON shape required by the AgentCore Runtime.
type pingResponse struct {
	Status           HealthStatus `json:"status"`
	TimeOfLastUpdate int64        `json:"time_of_last_update"`
}

func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	status := StatusHealthy
	if s.busy.Load() {
		status = StatusHealthyBusy
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(pingResponse{
		Status:           status,
		TimeOfLastUpdate: s.lastUpdate.Load(),
	})
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{Error: msg, Status: "error"})
}

// acceptsSSE reports whether the request's Accept header expresses
// preference for text/event-stream.
func acceptsSSE(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	if accept == "" {
		return false
	}
	return strings.Contains(strings.ToLower(accept), "text/event-stream")
}
