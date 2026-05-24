package agentcore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcore"
	"github.com/camilbinas/gude-agents/agent"
	gudea2a "github.com/camilbinas/gude-agents/agent/a2a"
	"github.com/camilbinas/gude-agents/agent/tracing"
)

// incomingEvent represents the JSON payload received from AgentCore.
type incomingEvent struct {
	EventID   string `json:"eventId"`
	SessionID string `json:"sessionId"`
	Message   string `json:"message"`
	Timestamp string `json:"timestamp,omitempty"`
}

// validateEvent checks that all required fields are non-empty.
func (e *incomingEvent) validate() bool {
	return e.EventID != "" && e.SessionID != "" && e.Message != ""
}

// Runtime bridges a gude-agents *Agent to the AgentCore worker protocol.
type Runtime struct {
	agent        *agent.Agent
	client       agentCoreClient
	cfg          runtimeConfig
	running      atomic.Bool
	workerID     string
	mu           sync.Mutex
	sessions     map[string]*sync.Mutex // per-session serialization
	inflight     sync.WaitGroup
	conversation *Conversation // non-nil when AutoConversation created the store
}

// NewRuntime creates an AgentCore runtime adapter. It validates the agent,
// resolves the agent name, loads AWS configuration if not provided, and
// constructs the AgentCore client.
func NewRuntime(a *agent.Agent, opts ...RuntimeOption) (*Runtime, error) {
	if a == nil {
		return nil, ErrAgentRequired
	}

	cfg := defaultRuntimeConfig()

	// Apply all options, returning the first error encountered.
	for _, opt := range opts {
		if err := opt(&cfg); err != nil {
			return nil, err
		}
	}

	// Resolve agent name: option → agent.Name() → error.
	if cfg.agentName == "" {
		cfg.agentName = a.Name()
	}
	if cfg.agentName == "" {
		return nil, ErrAgentNameRequired
	}

	// Load default AWS config if none was provided via options.
	if cfg.awsCfg == nil {
		awsCfg, err := config.LoadDefaultConfig(context.Background())
		if err != nil {
			return nil, fmt.Errorf("loading AWS config: %w", err)
		}
		cfg.awsCfg = &awsCfg
	}

	// Construct the AgentCore client from the AWS config.
	client := bedrockagentcore.NewFromConfig(*cfg.awsCfg)

	return &Runtime{
		agent:    a,
		client:   client,
		cfg:      cfg,
		sessions: make(map[string]*sync.Mutex),
	}, nil
}

// Run registers the worker, starts heartbeating, and enters the event
// processing loop. Blocks until ctx is cancelled or a fatal error occurs.
func (r *Runtime) Run(ctx context.Context) error {
	// Reject double-Run via atomic CAS.
	if !r.running.CompareAndSwap(false, true) {
		return ErrAlreadyRunning
	}
	defer r.running.Store(false)

	// Register the worker with AgentCore.
	if err := r.register(ctx); err != nil {
		return fmt.Errorf("worker registration failed: %w", err)
	}

	// Wire AutoConversation: create an AgentCore Conversation store using the
	// Runtime's AWS config unless the agent already has one configured.
	if r.cfg.autoConversation && !r.agent.HasConversation() {
		conv, err := NewConversation(WithConversationAWSConfig(*r.cfg.awsCfg))
		if err != nil {
			return fmt.Errorf("creating auto-conversation store: %w", err)
		}
		r.conversation = conv
		r.agent.SetConversation(conv)
		r.logf("agentcore:autoconv", "auto-conversation store created")
	}

	// Wire AgentCore-compatible OTEL tracing: if the agent doesn't already have
	// a tracing hook, set one using the AgentCore attribute scheme so traces
	// integrate with AgentCore's observability dashboards.
	r.autoWireTracing()

	// --- A2A Server Setup ---
	// When WithA2A is configured, start an HTTP server that serves A2A protocol
	// endpoints on the same port. The server shares the runtime's shutdown sequence.
	var httpServer *http.Server
	var a2aErrCh chan error
	if r.cfg.a2aCard != nil {
		// Use a CardOption that replaces the derived card with the user-provided one.
		userCard := *r.cfg.a2aCard
		cardOverride := gudea2a.CardOption(func(c *a2a.AgentCard) {
			*c = userCard
		})

		a2aSrv, err := gudea2a.NewServer(r.agent, []gudea2a.CardOption{cardOverride},
			gudea2a.WithGracefulTimeout(r.cfg.shutdownTimeout),
		)
		if err != nil {
			return fmt.Errorf("agentcore: creating A2A server: %w", err)
		}

		// The Server's Handler() creates a mux with /.well-known/agent.json
		// and JSON-RPC at /. Mount it directly as the HTTP handler.
		handler := a2aSrv.Handler()

		httpServer = &http.Server{
			Addr:    r.cfg.a2aAddr,
			Handler: handler,
		}

		a2aErrCh = make(chan error, 1)
		go func() {
			r.logf("agentcore:a2a", "A2A server starting on %s", r.cfg.a2aAddr)
			if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				a2aErrCh <- err
			}
			close(a2aErrCh)
		}()

		// Give the server a moment to bind or fail.
		select {
		case err := <-a2aErrCh:
			if err != nil {
				return fmt.Errorf("agentcore: A2A server failed to start: %w", err)
			}
		case <-time.After(100 * time.Millisecond):
			// Server is likely running — continue.
		}

		r.logf("agentcore:a2a", "A2A server started, serving AgentCard at /.well-known/agent.json")
	}

	// Create a separate cancellable context for event processing goroutines.
	// This allows us to force-cancel in-flight work if the shutdown timeout
	// is exceeded, without affecting the poll loop's own context.
	processCtx, processCancel := context.WithCancel(ctx)
	defer processCancel()

	// Start heartbeat goroutine.
	heartbeatCtx, heartbeatCancel := context.WithCancel(ctx)
	defer heartbeatCancel()

	var heartbeatWg sync.WaitGroup
	heartbeatWg.Add(1)
	go func() {
		defer heartbeatWg.Done()
		r.heartbeat(heartbeatCtx)
	}()

	// Run the poll loop — blocks until context is cancelled or a fatal error occurs.
	pollErr := r.pollLoop(ctx, processCtx)

	// --- Shutdown sequence ---
	// Requirement 13.1: Stop accepting new events (pollLoop already returned).
	// Requirement 13.2: Wait for in-flight goroutines up to shutdown timeout.

	heartbeatCancel()
	heartbeatWg.Wait()

	// Track the first error encountered during shutdown.
	var shutdownErr error
	if pollErr != nil {
		shutdownErr = pollErr
	}

	// Gracefully shut down the A2A HTTP server if it was started.
	if httpServer != nil {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), r.cfg.shutdownTimeout)
		r.logf("agentcore:a2a", "shutting down A2A server")
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			r.logf("agentcore:a2a", "A2A server shutdown error: %v", err)
			if shutdownErr == nil {
				shutdownErr = fmt.Errorf("agentcore: A2A server shutdown: %w", err)
			}
		}
		shutdownCancel()
	}

	// Wait for in-flight event processing to complete (up to shutdown timeout).
	done := make(chan struct{})
	go func() {
		r.inflight.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All in-flight work completed gracefully.
	case <-time.After(r.cfg.shutdownTimeout):
		// Requirement 13.3: Cancel processing contexts if timeout exceeded.
		r.logf("agentcore:shutdown", "shutdown timeout exceeded, cancelling in-flight processing")
		processCancel()
	}

	// Requirement 13.4, 13.5: Deregister the worker (best-effort).
	r.deregister(context.Background())

	// Requirement 13.6: Call Agent.Close() with timeout guard.
	closeDone := make(chan struct{})
	go func() {
		r.agent.Close()
		close(closeDone)
	}()

	select {
	case <-closeDone:
		// Agent.Close() completed.
	case <-time.After(r.cfg.shutdownTimeout):
		r.logf("agentcore:shutdown", "Agent.Close() timeout exceeded")
		if shutdownErr == nil {
			shutdownErr = fmt.Errorf("agent close timeout exceeded")
		}
	}

	// Requirement 13.7: Return nil on success or first failure error.
	return shutdownErr
}

// autoWireTracing installs an AgentCore-compatible OTEL tracing hook on the
// agent if none is already configured. It is a no-op when a hook is set,
// preserving any caller-supplied tracing configuration.
func (r *Runtime) autoWireTracing() {
	if r.agent.TracingHook() != nil {
		return
	}
	tracingOpt := tracing.WithTracing(nil, tracing.WithScheme(tracing.AgentCoreScheme()))
	_ = tracingOpt(r.agent)
	r.logf("agentcore:tracing", "auto-wired OTEL tracing with AgentCore attribute scheme")
}

// runtimePayload is the JSON envelope used for worker lifecycle operations
// (register, heartbeat, deregister) sent via InvokeAgentRuntime.
type runtimePayload struct {
	Action   string `json:"action"`
	Agent    string `json:"agent,omitempty"`
	WorkerID string `json:"workerId,omitempty"`
}

// runtimeResponse is the JSON envelope received from AgentCore for lifecycle operations.
type runtimeResponse struct {
	WorkerID string `json:"workerId,omitempty"`
	Status   string `json:"status,omitempty"`
}

// register calls AgentCore to register this worker and stores the worker ID.
func (r *Runtime) register(ctx context.Context) error {
	payload, err := json.Marshal(runtimePayload{
		Action: "register",
		Agent:  r.cfg.agentName,
	})
	if err != nil {
		return fmt.Errorf("marshaling register payload: %w", err)
	}

	out, err := r.client.InvokeAgentRuntime(ctx, &bedrockagentcore.InvokeAgentRuntimeInput{
		AgentRuntimeArn: aws.String(r.cfg.agentName),
		Payload:         payload,
		ContentType:     aws.String("application/json"),
		Accept:          aws.String("application/json"),
	})
	if err != nil {
		return err
	}

	resp, err := parseRuntimeResponse(out)
	if err != nil {
		return fmt.Errorf("parsing register response: %w", err)
	}

	r.mu.Lock()
	r.workerID = resp.WorkerID
	r.mu.Unlock()
	r.logf("agentcore:register", "worker registered: id=%s", resp.WorkerID)
	return nil
}

// heartbeat runs a ticker loop that sends heartbeat signals to AgentCore at the
// configured interval. On failure, it retries up to heartbeatBackoff.maxRetries
// times. If all retries fail, it logs the failure and resumes the schedule.
func (r *Runtime) heartbeat(ctx context.Context) {
	ticker := time.NewTicker(r.cfg.heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.sendHeartbeat(ctx)
		}
	}
}

// sendHeartbeat sends a single heartbeat to AgentCore with retry logic.
func (r *Runtime) sendHeartbeat(ctx context.Context) {
	payload, err := json.Marshal(runtimePayload{
		Action:   "heartbeat",
		WorkerID: r.workerID,
	})
	if err != nil {
		r.logf("agentcore:heartbeat", "failed to marshal heartbeat payload: %v", err)
		return
	}

	input := &bedrockagentcore.InvokeAgentRuntimeInput{
		AgentRuntimeArn: aws.String(r.cfg.agentName),
		Payload:         payload,
		ContentType:     aws.String("application/json"),
		Accept:          aws.String("application/json"),
	}

	for attempt := 0; attempt <= heartbeatBackoff.maxRetries; attempt++ {
		_, err = r.client.InvokeAgentRuntime(ctx, input)
		if err == nil {
			return
		}

		// If context is cancelled, stop retrying.
		if ctx.Err() != nil {
			return
		}

		// Log the retry attempt.
		r.logf("agentcore:heartbeat", "heartbeat failed (attempt %d/%d): %v",
			attempt+1, heartbeatBackoff.maxRetries+1, err)

		// If we've exhausted retries, log and resume schedule.
		if attempt >= heartbeatBackoff.maxRetries {
			r.logf("agentcore:heartbeat", "heartbeat retries exhausted, resuming schedule")
			return
		}

		// Wait with backoff before retrying.
		delay := heartbeatBackoff.delay(attempt)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

// deregister calls the AgentCore deregistration API with best-effort semantics.
// If deregistration fails, it logs the error and returns without error.
func (r *Runtime) deregister(ctx context.Context) {
	payload, err := json.Marshal(runtimePayload{
		Action:   "deregister",
		WorkerID: r.workerID,
	})
	if err != nil {
		r.logf("agentcore:deregister", "failed to marshal deregister payload: %v", err)
		return
	}

	_, err = r.client.InvokeAgentRuntime(ctx, &bedrockagentcore.InvokeAgentRuntimeInput{
		AgentRuntimeArn: aws.String(r.cfg.agentName),
		Payload:         payload,
		ContentType:     aws.String("application/json"),
		Accept:          aws.String("application/json"),
	})
	if err != nil {
		r.logf("agentcore:deregister", "deregistration failed (best-effort): %v", err)
		return
	}

	r.logf("agentcore:deregister", "worker deregistered: id=%s", r.workerID)
}

// parseRuntimeResponse reads and parses the JSON response from InvokeAgentRuntime.
func parseRuntimeResponse(out *bedrockagentcore.InvokeAgentRuntimeOutput) (runtimeResponse, error) {
	var resp runtimeResponse
	if out == nil || out.Response == nil {
		return resp, fmt.Errorf("nil response from AgentCore")
	}
	defer out.Response.Close()

	body, err := io.ReadAll(out.Response)
	if err != nil {
		return resp, fmt.Errorf("reading response body: %w", err)
	}

	if err := json.Unmarshal(body, &resp); err != nil {
		return resp, fmt.Errorf("unmarshaling response: %w", err)
	}

	return resp, nil
}

// logf logs a message via the agent's LoggingHook if available.
// It uses OnToolLog with the operation name as the tool name.
func (r *Runtime) logf(op string, format string, args ...any) {
	hook := r.agent.LoggingHook()
	if hook == nil {
		return
	}
	hook.OnToolLog(op, fmt.Sprintf(format, args...))
}

// sessionMutex returns the per-session mutex, creating one if needed.
func (r *Runtime) sessionMutex(sessionID string) *sync.Mutex {
	r.mu.Lock()
	defer r.mu.Unlock()
	mu, ok := r.sessions[sessionID]
	if !ok {
		mu = &sync.Mutex{}
		r.sessions[sessionID] = mu
	}
	return mu
}

// pollLoop long-polls AgentCore for events and dispatches them for processing.
// It retries transient errors with pollBackoff (infinite retries), re-registers
// on 404, and returns nil on context cancellation.
// The processCtx is a separate cancellable context passed to event processing
// goroutines, allowing the shutdown sequence to force-cancel in-flight work.
func (r *Runtime) pollLoop(ctx context.Context, processCtx context.Context) error {
	sem := make(chan struct{}, r.cfg.maxConcurrency)
	attempt := 0

	for {
		// Check for context cancellation before polling.
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		// Long-poll AgentCore for the next event.
		out, err := r.client.InvokeAgentRuntime(ctx, &bedrockagentcore.InvokeAgentRuntimeInput{})
		if err != nil {
			// Context cancelled — graceful shutdown.
			if ctx.Err() != nil {
				return nil
			}

			// 404 — worker deregistered, attempt re-registration once.
			if is404(err) {
				r.logf("poll", "received 404, attempting re-registration")
				if regErr := r.register(ctx); regErr != nil {
					return fmt.Errorf("re-registration after 404: %w", regErr)
				}
				r.logf("poll", "re-registration successful, resuming poll")
				attempt = 0
				continue
			}

			// Transient error — retry with backoff (infinite retries).
			if isTransient(err) {
				delay := pollBackoff.delay(attempt)
				r.logf("poll", "transient error (attempt %d): %v, retrying in %v", attempt+1, err, delay)
				attempt++
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(delay):
					continue
				}
			}

			// Non-transient, non-404 error — log and retry with backoff as well
			// (treat unknown errors as transient for resilience).
			delay := pollBackoff.delay(attempt)
			r.logf("poll", "poll error (attempt %d): %v, retrying in %v", attempt+1, err, delay)
			attempt++
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(delay):
				continue
			}
		}

		// Successful poll — reset backoff.
		attempt = 0

		// Read the event payload from the response body.
		var payload []byte
		if out != nil && out.Response != nil {
			payload, err = io.ReadAll(out.Response)
			out.Response.Close()
			if err != nil {
				r.logf("poll", "error reading response body: %v", err)
				continue
			}
		}

		// If no payload (empty poll / long-poll timeout), continue polling.
		if len(payload) == 0 {
			continue
		}

		var event incomingEvent
		if err := json.Unmarshal(payload, &event); err != nil {
			r.logf("poll", "malformed event payload: %v", err)
			continue
		}

		// Validate required fields.
		if !event.validate() {
			r.logf("poll", "discarding event with missing fields: eventId=%q sessionId=%q message=%q",
				event.EventID, event.SessionID, event.Message)
			continue
		}

		// Acquire semaphore slot for concurrency limiting.
		select {
		case <-ctx.Done():
			return nil
		case sem <- struct{}{}:
		}

		// Dispatch to per-session processing in a goroutine.
		r.inflight.Add(1)
		go func(ev incomingEvent) {
			defer func() {
				<-sem
				r.inflight.Done()
			}()

			// Per-session serialization: lock the session mutex.
			sessMu := r.sessionMutex(ev.SessionID)
			sessMu.Lock()
			defer sessMu.Unlock()

			r.processEvent(processCtx, ev)
		}(event)
	}
}

// eventResponse is the JSON payload submitted back to AgentCore.
type eventResponse struct {
	EventID  string `json:"eventId"`
	Response string `json:"response"`
	IsError  bool   `json:"isError,omitempty"`
}

// streamChunk is a partial response submitted during streaming.
type streamChunk struct {
	EventID string `json:"eventId"`
	Chunk   string `json:"chunk"`
	Index   int    `json:"index"`
	Final   bool   `json:"final"`
}

// processEvent processes a single validated event. It constructs an agent.Context
// with the session ID as conversation ID, invokes the agent, and submits the
// response (or error) back to AgentCore with retry logic.
func (r *Runtime) processEvent(ctx context.Context, ev incomingEvent) {
	r.logf("processEvent", "processing event=%s session=%s", ev.EventID, ev.SessionID)

	// Reject events with empty session ID when AutoConversation is enabled.
	if r.cfg.autoConversation && ev.SessionID == "" {
		r.logf("processEvent", "rejecting event=%s: empty session ID with AutoConversation enabled", ev.EventID)
		resp := eventResponse{
			EventID:  ev.EventID,
			Response: "missing session ID: AutoConversation requires a valid session ID",
			IsError:  true,
		}
		r.submitResponse(ctx, resp)
		return
	}

	// Start observability hooks.
	var tracingFinish func(err error, usage agent.TokenUsage, response string)
	if th := r.agent.TracingHook(); th != nil {
		_, tracingFinish = th.OnInvokeStart(ctx, agent.InvokeSpanParams{
			ConversationID: ev.EventID,
			AgentName:      ev.SessionID,
			UserMessage:    "",
		})
	}

	var metricsFinish func(err error, usage agent.TokenUsage)
	if mh := r.agent.MetricsHook(); mh != nil {
		metricsFinish = mh.OnInvokeStart()
	}

	// Construct the agent context with session ID as conversation ID.
	agentCtx := agent.NewContext(ctx)
	agentCtx.WithConversationID(ev.SessionID)

	// Propagate observability hooks to the agent context.
	if th := r.agent.TracingHook(); th != nil {
		agentCtx.WithTracingHook(th)
	}
	if mh := r.agent.MetricsHook(); mh != nil {
		agentCtx.WithMetricsHook(mh)
	}
	if lh := r.agent.LoggingHook(); lh != nil {
		agentCtx.WithLoggingHook(lh)
	}

	var result string
	var invokeErr error

	if r.cfg.streaming {
		// Streaming path: submit chunks incrementally to AgentCore.
		var mu sync.Mutex
		var chunks []string
		var chunkIndex int
		var failed bool

		cb := func(chunk string) {
			mu.Lock()
			defer mu.Unlock()

			// Always buffer the chunk for fallback.
			chunks = append(chunks, chunk)

			// If a previous chunk submission failed, only buffer (don't submit).
			if failed {
				chunkIndex++
				return
			}

			// Submit the chunk to AgentCore synchronously.
			sc := streamChunk{
				EventID: ev.EventID,
				Chunk:   chunk,
				Index:   chunkIndex,
				Final:   false,
			}
			if err := r.submitStreamChunk(ctx, sc); err != nil {
				r.logf("processEvent", "stream chunk submission failed for event=%s index=%d: %v", ev.EventID, chunkIndex, err)
				failed = true
			}
			chunkIndex++
		}

		invokeErr = r.agent.InvokeStream(agentCtx, ev.Message, cb)

		mu.Lock()
		streamFailed := failed
		allChunks := make([]string, len(chunks))
		copy(allChunks, chunks)
		finalIndex := chunkIndex
		mu.Unlock()

		if invokeErr == nil {
			if streamFailed {
				// Fallback: concatenate all buffered chunks and submit as complete response.
				var concatenated string
				for _, c := range allChunks {
					concatenated += c
				}
				resp := eventResponse{
					EventID:  ev.EventID,
					Response: concatenated,
				}
				r.submitResponse(ctx, resp)
			} else {
				// Submit final chunk marker.
				sc := streamChunk{
					EventID: ev.EventID,
					Chunk:   "",
					Index:   finalIndex,
					Final:   true,
				}
				if err := r.submitStreamChunk(ctx, sc); err != nil {
					// Final chunk submission failed — fallback to complete response.
					r.logf("processEvent", "final stream chunk submission failed for event=%s: %v", ev.EventID, err)
					var concatenated string
					for _, c := range allChunks {
						concatenated += c
					}
					resp := eventResponse{
						EventID:  ev.EventID,
						Response: concatenated,
					}
					r.submitResponse(ctx, resp)
				}
			}
		} else {
			// Agent invocation failed — submit error response.
			r.logf("processEvent", "agent invocation failed for event=%s: %v", ev.EventID, invokeErr)
			resp := eventResponse{
				EventID:  ev.EventID,
				Response: invokeErr.Error(),
				IsError:  true,
			}
			r.submitResponse(ctx, resp)
		}
	} else {
		// Non-streaming path: invoke and get the complete response.
		result, invokeErr = r.agent.Invoke(agentCtx, ev.Message)

		// Build the response payload.
		var resp eventResponse
		if invokeErr != nil {
			r.logf("processEvent", "agent invocation failed for event=%s: %v", ev.EventID, invokeErr)
			resp = eventResponse{
				EventID:  ev.EventID,
				Response: invokeErr.Error(),
				IsError:  true,
			}
		} else {
			resp = eventResponse{
				EventID:  ev.EventID,
				Response: result,
			}
		}

		// Submit the response to AgentCore with retry for transient errors.
		r.submitResponse(ctx, resp)
	}

	// Finish observability hooks with the invocation outcome.
	usage := agentCtx.Usage()
	if tracingFinish != nil {
		tracingFinish(invokeErr, usage, result)
	}
	if metricsFinish != nil {
		metricsFinish(invokeErr, usage)
	}
}

// submitResponse submits an eventResponse to AgentCore via InvokeAgentRuntime.
// It retries transient errors up to submitBackoff.maxRetries times and logs
// permanent failures without retrying.
func (r *Runtime) submitResponse(ctx context.Context, resp eventResponse) {
	payload, err := json.Marshal(resp)
	if err != nil {
		r.logf("processEvent", "failed to marshal response for event=%s: %v", resp.EventID, err)
		return
	}

	input := &bedrockagentcore.InvokeAgentRuntimeInput{
		AgentRuntimeArn: aws.String(r.cfg.agentName),
		Payload:         payload,
		ContentType:     aws.String("application/json"),
		Accept:          aws.String("application/json"),
	}

	for attempt := 0; attempt <= submitBackoff.maxRetries; attempt++ {
		_, err = r.client.InvokeAgentRuntime(ctx, input)
		if err == nil {
			return
		}

		// Context cancelled — stop retrying.
		if ctx.Err() != nil {
			r.logf("processEvent", "context cancelled during response submission for event=%s", resp.EventID)
			return
		}

		// Permanent error — do not retry.
		if isPermanent(err) {
			r.logf("processEvent", "permanent error submitting response for event=%s: %v", resp.EventID, err)
			return
		}

		// Log the retry attempt.
		r.logf("processEvent", "response submission failed for event=%s (attempt %d/%d): %v",
			resp.EventID, attempt+1, submitBackoff.maxRetries+1, err)

		// If retries exhausted, log and discard.
		if attempt >= submitBackoff.maxRetries {
			r.logf("processEvent", "response submission retries exhausted for event=%s, discarding", resp.EventID)
			return
		}

		// Wait with backoff before retrying.
		delay := submitBackoff.delay(attempt)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

// submitStreamChunk submits a single streamChunk to AgentCore via InvokeAgentRuntime.
// It returns an error if the submission fails (after retries for transient errors).
func (r *Runtime) submitStreamChunk(ctx context.Context, sc streamChunk) error {
	payload, err := json.Marshal(sc)
	if err != nil {
		return fmt.Errorf("marshaling stream chunk: %w", err)
	}

	input := &bedrockagentcore.InvokeAgentRuntimeInput{
		AgentRuntimeArn: aws.String(r.cfg.agentName),
		Payload:         payload,
		ContentType:     aws.String("application/json"),
		Accept:          aws.String("application/json"),
	}

	for attempt := 0; attempt <= submitBackoff.maxRetries; attempt++ {
		_, err = r.client.InvokeAgentRuntime(ctx, input)
		if err == nil {
			return nil
		}

		// Context cancelled — stop retrying.
		if ctx.Err() != nil {
			return err
		}

		// Permanent error — do not retry.
		if isPermanent(err) {
			return err
		}

		// If retries exhausted, return the error.
		if attempt >= submitBackoff.maxRetries {
			return err
		}

		// Wait with backoff before retrying.
		delay := submitBackoff.delay(attempt)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}

	return err
}
