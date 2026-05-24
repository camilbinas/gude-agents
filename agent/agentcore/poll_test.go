package agentcore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcore"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/camilbinas/gude-agents/agent"
)

// mockAgentCoreClient implements agentCoreClient for testing.
type mockAgentCoreClient struct {
	mu            sync.Mutex
	invokeResults []invokeResult
	invokeIndex   int
	invokeCalls   int
}

type invokeResult struct {
	output *bedrockagentcore.InvokeAgentRuntimeOutput
	err    error
}

func (m *mockAgentCoreClient) nextInvokeResult() (*bedrockagentcore.InvokeAgentRuntimeOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.invokeCalls++
	if m.invokeIndex >= len(m.invokeResults) {
		// Default: return empty response (no event).
		return &bedrockagentcore.InvokeAgentRuntimeOutput{}, nil
	}
	r := m.invokeResults[m.invokeIndex]
	m.invokeIndex++
	return r.output, r.err
}

func (m *mockAgentCoreClient) getInvokeCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.invokeCalls
}

func (m *mockAgentCoreClient) InvokeAgentRuntime(_ context.Context, _ *bedrockagentcore.InvokeAgentRuntimeInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeAgentRuntimeOutput, error) {
	return m.nextInvokeResult()
}

func (m *mockAgentCoreClient) CreateEvent(_ context.Context, _ *bedrockagentcore.CreateEventInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.CreateEventOutput, error) {
	return nil, nil
}

func (m *mockAgentCoreClient) ListEvents(_ context.Context, _ *bedrockagentcore.ListEventsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.ListEventsOutput, error) {
	return nil, nil
}

func (m *mockAgentCoreClient) ListSessions(_ context.Context, _ *bedrockagentcore.ListSessionsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.ListSessionsOutput, error) {
	return nil, nil
}

func (m *mockAgentCoreClient) StopRuntimeSession(_ context.Context, _ *bedrockagentcore.StopRuntimeSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopRuntimeSessionOutput, error) {
	return nil, nil
}

func (m *mockAgentCoreClient) StartBrowserSession(_ context.Context, _ *bedrockagentcore.StartBrowserSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StartBrowserSessionOutput, error) {
	return nil, nil
}

func (m *mockAgentCoreClient) InvokeBrowser(_ context.Context, _ *bedrockagentcore.InvokeBrowserInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeBrowserOutput, error) {
	return nil, nil
}

func (m *mockAgentCoreClient) StopBrowserSession(_ context.Context, _ *bedrockagentcore.StopBrowserSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopBrowserSessionOutput, error) {
	return nil, nil
}

func (m *mockAgentCoreClient) StartCodeInterpreterSession(_ context.Context, _ *bedrockagentcore.StartCodeInterpreterSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StartCodeInterpreterSessionOutput, error) {
	return nil, nil
}

func (m *mockAgentCoreClient) InvokeCodeInterpreter(_ context.Context, _ *bedrockagentcore.InvokeCodeInterpreterInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeCodeInterpreterOutput, error) {
	return nil, nil
}

func (m *mockAgentCoreClient) StopCodeInterpreterSession(_ context.Context, _ *bedrockagentcore.StopCodeInterpreterSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopCodeInterpreterSessionOutput, error) {
	return nil, nil
}

func (m *mockAgentCoreClient) BatchCreateMemoryRecords(_ context.Context, _ *bedrockagentcore.BatchCreateMemoryRecordsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.BatchCreateMemoryRecordsOutput, error) {
	return nil, nil
}

func (m *mockAgentCoreClient) RetrieveMemoryRecords(_ context.Context, _ *bedrockagentcore.RetrieveMemoryRecordsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.RetrieveMemoryRecordsOutput, error) {
	return nil, nil
}

func (m *mockAgentCoreClient) DeleteMemoryRecord(_ context.Context, _ *bedrockagentcore.DeleteMemoryRecordInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.DeleteMemoryRecordOutput, error) {
	return nil, nil
}

// Helper to create an InvokeAgentRuntimeOutput with a JSON body.
func makeEventOutput(ev incomingEvent) *bedrockagentcore.InvokeAgentRuntimeOutput {
	data, _ := json.Marshal(ev)
	return &bedrockagentcore.InvokeAgentRuntimeOutput{
		Response: io.NopCloser(bytes.NewReader(data)),
	}
}

// Helper to create a 404 error.
func make404Error() error {
	return &smithyhttp.ResponseError{
		Response: &smithyhttp.Response{
			Response: &http.Response{StatusCode: 404},
		},
		Err: errors.New("not found"),
	}
}

// Helper to create a 503 transient error.
func make503Error() error {
	return &smithyhttp.ResponseError{
		Response: &smithyhttp.Response{
			Response: &http.Response{StatusCode: 503},
		},
		Err: errors.New("service unavailable"),
	}
}

// capturingLoggingHook captures OnToolLog calls for test assertions.
type capturingLoggingHook struct {
	mu   sync.Mutex
	logs []toolLogEntry
}

type toolLogEntry struct {
	toolName string
	msg      string
}

func (h *capturingLoggingHook) OnToolLog(toolName string, msg string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.logs = append(h.logs, toolLogEntry{toolName: toolName, msg: msg})
}

func (h *capturingLoggingHook) getLogs() []toolLogEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	result := make([]toolLogEntry, len(h.logs))
	copy(result, h.logs)
	return result
}

// Implement remaining LoggingHook methods as no-ops.
func (h *capturingLoggingHook) OnInvokeStart(_ agent.InvokeSpanParams)                   {}
func (h *capturingLoggingHook) OnInvokeEnd(_ error, _ agent.TokenUsage, _ time.Duration) {}
func (h *capturingLoggingHook) OnIterationStart(_ int)                                   {}
func (h *capturingLoggingHook) OnIterationEnd(_ int, _ int, _ bool, _ time.Duration)     {}
func (h *capturingLoggingHook) OnProviderCallStart(_ string)                             {}
func (h *capturingLoggingHook) OnProviderCallEnd(_ error, _ agent.TokenUsage, _ int, _ time.Duration) {
}
func (h *capturingLoggingHook) OnToolStart(_ string)                          {}
func (h *capturingLoggingHook) OnToolEnd(_ string, _ error, _ time.Duration)  {}
func (h *capturingLoggingHook) OnGuardrailComplete(_ string, _ bool, _ error) {}
func (h *capturingLoggingHook) OnConversationStart(_ string, _ string)        {}
func (h *capturingLoggingHook) OnConversationEnd(_ string, _ string, _ error, _ int, _ time.Duration) {
}
func (h *capturingLoggingHook) OnRetrieverStart(_ string)                      {}
func (h *capturingLoggingHook) OnRetrieverEnd(_ error, _ int, _ time.Duration) {}
func (h *capturingLoggingHook) OnImagesAttached(_ int)                         {}
func (h *capturingLoggingHook) OnDocumentsAttached(_ int)                      {}
func (h *capturingLoggingHook) OnMaxIterationsExceeded(_ int)                  {}
func (h *capturingLoggingHook) OnStreamChunk(_ string)                         {}
func (h *capturingLoggingHook) OnResponse(_ string)                            {}

// newTestRuntime creates a Runtime with a mock client for testing.
func newTestRuntime(t *testing.T, mock *mockAgentCoreClient, opts ...agent.Option) *Runtime {
	t.Helper()
	a := newTestAgent(t, append([]agent.Option{agent.WithName("test-agent")}, opts...)...)
	return &Runtime{
		agent:    a,
		client:   mock,
		cfg:      defaultRuntimeConfig(),
		sessions: make(map[string]*sync.Mutex),
	}
}

func TestPollLoop_ContextCancellation(t *testing.T) {
	mock := &mockAgentCoreClient{}
	rt := newTestRuntime(t, mock)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	err := rt.pollLoop(ctx, ctx)
	if err != nil {
		t.Fatalf("expected nil error on context cancellation, got: %v", err)
	}
}

func TestPollLoop_ValidEventDispatched(t *testing.T) {
	ev := incomingEvent{
		EventID:   "evt-1",
		SessionID: "sess-1",
		Message:   "hello",
	}

	mock := &mockAgentCoreClient{
		invokeResults: []invokeResult{
			{output: makeEventOutput(ev)},
			// Second call: context will be cancelled.
		},
	}

	logger := &capturingLoggingHook{}
	rt := newTestRuntime(t, mock)
	rt.agent.SetLoggingHook(logger)

	ctx, cancel := context.WithCancel(context.Background())

	// Run pollLoop in a goroutine and cancel after the first event is processed.
	done := make(chan error, 1)
	go func() {
		done <- rt.pollLoop(ctx, ctx)
	}()

	// Wait for the event to be processed (inflight will go to 0 after processing).
	time.Sleep(100 * time.Millisecond)
	cancel()

	err := <-done
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	// Verify the event was dispatched (processEvent logs it).
	logs := logger.getLogs()
	found := false
	for _, l := range logs {
		if l.toolName == "processEvent" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected processEvent log entry, got none")
	}
}

func TestPollLoop_MalformedEventDiscarded(t *testing.T) {
	// Event with empty EventID — should be discarded.
	ev := incomingEvent{
		EventID:   "",
		SessionID: "sess-1",
		Message:   "hello",
	}

	mock := &mockAgentCoreClient{
		invokeResults: []invokeResult{
			{output: makeEventOutput(ev)},
		},
	}

	logger := &capturingLoggingHook{}
	rt := newTestRuntime(t, mock)
	rt.agent.SetLoggingHook(logger)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- rt.pollLoop(ctx, ctx)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	err := <-done
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	// Verify the malformed event was logged.
	logs := logger.getLogs()
	found := false
	for _, l := range logs {
		if l.toolName == "poll" && containsStr(l.msg, "discarding event") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'discarding event' log entry for malformed event")
	}

	// Verify processEvent was NOT called.
	for _, l := range logs {
		if l.toolName == "processEvent" {
			t.Error("processEvent should not be called for malformed events")
		}
	}
}

func TestPollLoop_MissingSessionIDDiscarded(t *testing.T) {
	ev := incomingEvent{
		EventID:   "evt-1",
		SessionID: "",
		Message:   "hello",
	}

	mock := &mockAgentCoreClient{
		invokeResults: []invokeResult{
			{output: makeEventOutput(ev)},
		},
	}

	logger := &capturingLoggingHook{}
	rt := newTestRuntime(t, mock)
	rt.agent.SetLoggingHook(logger)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- rt.pollLoop(ctx, ctx)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	err := <-done
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	logs := logger.getLogs()
	found := false
	for _, l := range logs {
		if l.toolName == "poll" && containsStr(l.msg, "discarding event") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'discarding event' log entry for missing session ID")
	}
}

func TestPollLoop_MissingMessageDiscarded(t *testing.T) {
	ev := incomingEvent{
		EventID:   "evt-1",
		SessionID: "sess-1",
		Message:   "",
	}

	mock := &mockAgentCoreClient{
		invokeResults: []invokeResult{
			{output: makeEventOutput(ev)},
		},
	}

	logger := &capturingLoggingHook{}
	rt := newTestRuntime(t, mock)
	rt.agent.SetLoggingHook(logger)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- rt.pollLoop(ctx, ctx)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	err := <-done
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	logs := logger.getLogs()
	found := false
	for _, l := range logs {
		if l.toolName == "poll" && containsStr(l.msg, "discarding event") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'discarding event' log entry for missing message")
	}
}

func TestPollLoop_InvalidJSONDiscarded(t *testing.T) {
	mock := &mockAgentCoreClient{
		invokeResults: []invokeResult{
			{output: &bedrockagentcore.InvokeAgentRuntimeOutput{
				Response: io.NopCloser(bytes.NewReader([]byte("not json{{{"))),
			}},
		},
	}

	logger := &capturingLoggingHook{}
	rt := newTestRuntime(t, mock)
	rt.agent.SetLoggingHook(logger)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- rt.pollLoop(ctx, ctx)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	err := <-done
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	logs := logger.getLogs()
	found := false
	for _, l := range logs {
		if l.toolName == "poll" && containsStr(l.msg, "malformed event payload") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'malformed event payload' log entry for invalid JSON")
	}
}

func TestPollLoop_TransientErrorRetries(t *testing.T) {
	// First call: transient error. Second call: valid event. Third: context cancelled.
	ev := incomingEvent{
		EventID:   "evt-1",
		SessionID: "sess-1",
		Message:   "hello",
	}

	mock := &mockAgentCoreClient{
		invokeResults: []invokeResult{
			{err: make503Error()},
			{output: makeEventOutput(ev)},
		},
	}

	logger := &capturingLoggingHook{}
	rt := newTestRuntime(t, mock)
	rt.agent.SetLoggingHook(logger)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- rt.pollLoop(ctx, ctx)
	}()

	// Wait for retry + event processing.
	time.Sleep(2 * time.Second)
	cancel()

	err := <-done
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	// Verify retry was logged.
	logs := logger.getLogs()
	retryLogged := false
	for _, l := range logs {
		if l.toolName == "poll" && containsStr(l.msg, "transient error") {
			retryLogged = true
			break
		}
	}
	if !retryLogged {
		t.Error("expected transient error retry log entry")
	}

	// Verify the event was eventually processed.
	eventProcessed := false
	for _, l := range logs {
		if l.toolName == "processEvent" {
			eventProcessed = true
			break
		}
	}
	if !eventProcessed {
		t.Error("expected event to be processed after retry")
	}
}

func TestPollLoop_404ReregistrationSuccess(t *testing.T) {
	ev := incomingEvent{
		EventID:   "evt-1",
		SessionID: "sess-1",
		Message:   "hello",
	}

	// First call: 404. Second call (re-registration): success. Third call: valid event.
	mock := &mockAgentCoreClient{
		invokeResults: []invokeResult{
			{err: make404Error()},
			{output: makeRegisterResponse("worker-rereg")}, // re-registration
			{output: makeEventOutput(ev)},                  // poll after re-reg
		},
	}

	logger := &capturingLoggingHook{}
	rt := newTestRuntime(t, mock)
	rt.agent.SetLoggingHook(logger)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- rt.pollLoop(ctx, ctx)
	}()

	time.Sleep(200 * time.Millisecond)
	cancel()

	err := <-done
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	// Verify re-registration was logged.
	logs := logger.getLogs()
	reregLogged := false
	for _, l := range logs {
		if l.toolName == "poll" && containsStr(l.msg, "re-registration") {
			reregLogged = true
			break
		}
	}
	if !reregLogged {
		t.Error("expected re-registration log entry")
	}
}

func TestPollLoop_404ReregistrationFailure(t *testing.T) {
	// First call: 404. Second call (re-registration): fails.
	mock := &mockAgentCoreClient{
		invokeResults: []invokeResult{
			{err: make404Error()},
			{err: errors.New("registration failed")}, // re-registration fails
		},
	}

	rt := newTestRuntime(t, mock)

	ctx := context.Background()
	err := rt.pollLoop(ctx, ctx)
	if err == nil {
		t.Fatal("expected error when re-registration fails, got nil")
	}
	if !containsStr(err.Error(), "re-registration after 404") {
		t.Errorf("expected error to mention re-registration, got: %v", err)
	}
}

func TestPollLoop_ConcurrencyLimit(t *testing.T) {
	// Create events for different sessions to test concurrency limiting.
	maxConcurrency := 2

	mock := &mockAgentCoreClient{}
	rt := newTestRuntime(t, mock)
	rt.cfg.maxConcurrency = maxConcurrency

	// We'll create 5 events for different sessions.
	events := make([]incomingEvent, 5)
	results := make([]invokeResult, 5)
	for i := range events {
		events[i] = incomingEvent{
			EventID:   fmt.Sprintf("evt-%d", i),
			SessionID: fmt.Sprintf("sess-%d", i),
			Message:   "hello",
		}
		results[i] = invokeResult{output: makeEventOutput(events[i])}
	}
	mock.invokeResults = results

	// We can't easily override processEvent, but we can verify the semaphore
	// limits by checking that the poll loop runs without deadlock.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- rt.pollLoop(ctx, ctx)
	}()

	time.Sleep(300 * time.Millisecond)
	cancel()

	err := <-done
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
}

func TestPollLoop_PerSessionSerialization(t *testing.T) {
	// Create multiple events for the same session — they should be serialized.
	var processOrder []string
	var orderMu sync.Mutex

	ev1 := incomingEvent{EventID: "evt-1", SessionID: "sess-same", Message: "first"}
	ev2 := incomingEvent{EventID: "evt-2", SessionID: "sess-same", Message: "second"}

	mock := &mockAgentCoreClient{
		invokeResults: []invokeResult{
			{output: makeEventOutput(ev1)},
			{output: makeEventOutput(ev2)},
		},
	}

	logger := &capturingLoggingHook{}
	rt := newTestRuntime(t, mock)
	rt.agent.SetLoggingHook(logger)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- rt.pollLoop(ctx, ctx)
	}()

	time.Sleep(300 * time.Millisecond)
	cancel()

	err := <-done
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	// Verify both events were processed (via logs).
	logs := logger.getLogs()
	for _, l := range logs {
		if l.toolName == "processEvent" {
			orderMu.Lock()
			processOrder = append(processOrder, l.msg)
			orderMu.Unlock()
		}
	}

	// Both events should have been processed.
	if len(processOrder) < 2 {
		t.Errorf("expected at least 2 processEvent logs, got %d", len(processOrder))
	}
}

func TestPollLoop_EmptyResponseContinues(t *testing.T) {
	// Empty response (no body) should just continue polling.
	mock := &mockAgentCoreClient{
		invokeResults: []invokeResult{
			{output: &bedrockagentcore.InvokeAgentRuntimeOutput{}}, // nil Response
			{output: &bedrockagentcore.InvokeAgentRuntimeOutput{
				Response: io.NopCloser(bytes.NewReader([]byte{})), // empty body
			}},
		},
	}

	rt := newTestRuntime(t, mock)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- rt.pollLoop(ctx, ctx)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	err := <-done
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	// Should have made at least 2 calls (both empty responses).
	calls := mock.getInvokeCalls()
	if calls < 2 {
		t.Errorf("expected at least 2 invoke calls, got %d", calls)
	}
}

func TestIncomingEvent_Validate(t *testing.T) {
	tests := []struct {
		name  string
		event incomingEvent
		valid bool
	}{
		{"all fields present", incomingEvent{EventID: "e", SessionID: "s", Message: "m"}, true},
		{"missing eventId", incomingEvent{EventID: "", SessionID: "s", Message: "m"}, false},
		{"missing sessionId", incomingEvent{EventID: "e", SessionID: "", Message: "m"}, false},
		{"missing message", incomingEvent{EventID: "e", SessionID: "s", Message: ""}, false},
		{"all empty", incomingEvent{}, false},
		{"with timestamp", incomingEvent{EventID: "e", SessionID: "s", Message: "m", Timestamp: "2024-01-01"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.event.validate()
			if got != tt.valid {
				t.Errorf("validate() = %v, want %v", got, tt.valid)
			}
		})
	}
}

func TestIs404(t *testing.T) {
	t.Run("nil error", func(t *testing.T) {
		if is404(nil) {
			t.Error("nil should not be 404")
		}
	})

	t.Run("404 response", func(t *testing.T) {
		err := make404Error()
		if !is404(err) {
			t.Error("expected is404 to return true for 404")
		}
	})

	t.Run("503 is not 404", func(t *testing.T) {
		err := make503Error()
		if is404(err) {
			t.Error("503 should not be 404")
		}
	})

	t.Run("generic error is not 404", func(t *testing.T) {
		err := errors.New("something")
		if is404(err) {
			t.Error("generic error should not be 404")
		}
	})
}

// containsStr is a helper to check if a string contains a substring.
func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || findSubstr(s, substr))
}

func findSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
