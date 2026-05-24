package agentcore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcore"
)

// TestShutdown_GracefulNoInflight verifies that when the context is cancelled
// with no in-flight events, Run returns nil quickly.
// Validates: Requirements 13.1, 13.4, 13.7
func TestShutdown_GracefulNoInflight(t *testing.T) {
	// Mock: registration succeeds, then all subsequent calls return empty (no events).
	mock := &mockAgentCoreClient{
		invokeResults: []invokeResult{
			{output: makeRegisterResponse("worker-shutdown-1")},
		},
	}
	logger := &capturingLoggingHook{}
	rt := newTestRuntime(t, mock)
	rt.agent.SetLoggingHook(logger)
	rt.cfg.shutdownTimeout = 2 * time.Second

	ctx, cancel := context.WithCancel(context.Background())

	runErr := make(chan error, 1)
	go func() {
		runErr <- rt.Run(ctx)
	}()

	// Let Run register and enter the poll loop.
	time.Sleep(50 * time.Millisecond)

	// Cancel context to trigger shutdown.
	cancel()

	// Run should return nil quickly (well within shutdown timeout).
	select {
	case err := <-runErr:
		if err != nil {
			t.Errorf("expected nil error from graceful shutdown, got: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return within expected time after context cancellation")
	}

	// Verify deregistration was attempted (logged).
	logs := logger.getLogs()
	deregLogged := false
	for _, l := range logs {
		if l.toolName == "agentcore:deregister" {
			deregLogged = true
			break
		}
	}
	if !deregLogged {
		t.Error("expected deregistration log entry during shutdown")
	}
}

// TestShutdown_TimeoutWithSlowInflight verifies that when in-flight processing
// takes longer than the shutdown timeout, Run still returns within a reasonable
// time (doesn't hang forever). We simulate this by directly manipulating the
// Runtime's inflight WaitGroup to represent a stuck goroutine.
// Validates: Requirements 13.1, 13.4, 13.7
func TestShutdown_TimeoutWithSlowInflight(t *testing.T) {
	// Use the deregisterFailMock (which handles action-based routing) but with
	// no deregister error — we just need registration + empty polls.
	mock := &deregisterFailMock{
		registerResp: makeRegisterResponse("worker-slow"),
		deregErr:     nil, // deregister succeeds
	}

	logger := &capturingLoggingHook{}
	rt := newTestRuntime(t, &mockAgentCoreClient{})
	rt.client = mock
	rt.agent.SetLoggingHook(logger)
	// Set a very short shutdown timeout so the test doesn't take long.
	rt.cfg.shutdownTimeout = 200 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())

	runErr := make(chan error, 1)
	go func() {
		runErr <- rt.Run(ctx)
	}()

	// Let Run register and enter the poll loop.
	time.Sleep(50 * time.Millisecond)

	// Simulate a stuck in-flight goroutine by adding to the WaitGroup.
	// This represents an event processing goroutine that won't finish in time.
	rt.inflight.Add(1)
	slowDone := make(chan struct{})
	go func() {
		defer rt.inflight.Done()
		// Block until test signals completion (simulates slow processing).
		<-slowDone
	}()

	start := time.Now()

	// Cancel context to trigger shutdown.
	cancel()

	// Run should return within shutdown timeout + buffer, not hang forever.
	select {
	case err := <-runErr:
		elapsed := time.Since(start)
		// Should complete within a reasonable time (shutdown timeout + Agent.Close timeout + buffer).
		if elapsed > 3*time.Second {
			t.Errorf("Run took too long to return: %v", elapsed)
		}
		// The error should be nil (shutdown timeout is not a fatal error per req 13.7).
		if err != nil {
			t.Errorf("expected nil error from shutdown (timeout is not fatal), got: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run hung forever — shutdown timeout did not work")
	}

	// Release the slow goroutine so it doesn't leak.
	close(slowDone)

	// Verify the timeout was logged.
	logs := logger.getLogs()
	timeoutLogged := false
	for _, l := range logs {
		if containsStr(l.msg, "shutdown timeout exceeded") {
			timeoutLogged = true
			break
		}
	}
	if !timeoutLogged {
		t.Error("expected 'shutdown timeout exceeded' log entry")
	}
}

// TestShutdown_DeregistrationFailureLogged verifies that when deregistration
// fails, the error is logged but Run still returns nil.
// Validates: Requirements 13.4, 13.5, 13.7
func TestShutdown_DeregistrationFailureLogged(t *testing.T) {
	// Mock: registration succeeds, then deregistration fails.
	// The mock returns results in order: first is registration, subsequent calls
	// (heartbeats, polls) return empty, and the deregister call will fail.
	deregMock := &deregisterFailMock{
		registerResp: makeRegisterResponse("worker-dereg-fail"),
		deregErr:     errors.New("service unavailable during deregister"),
	}

	logger := &capturingLoggingHook{}
	rt := newTestRuntime(t, &mockAgentCoreClient{})
	rt.client = deregMock
	rt.agent.SetLoggingHook(logger)
	rt.cfg.shutdownTimeout = 2 * time.Second

	ctx, cancel := context.WithCancel(context.Background())

	runErr := make(chan error, 1)
	go func() {
		runErr <- rt.Run(ctx)
	}()

	// Let Run register and enter the poll loop.
	time.Sleep(50 * time.Millisecond)

	// Cancel to trigger shutdown.
	cancel()

	select {
	case err := <-runErr:
		// Run should return nil even though deregistration failed (best-effort).
		if err != nil {
			t.Errorf("expected nil error when deregistration fails, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within expected time")
	}

	// Verify the deregistration failure was logged.
	logs := logger.getLogs()
	deregFailLogged := false
	for _, l := range logs {
		if l.toolName == "agentcore:deregister" && containsStr(l.msg, "deregistration failed") {
			deregFailLogged = true
			break
		}
	}
	if !deregFailLogged {
		t.Error("expected deregistration failure to be logged")
	}
}

// --- Custom mocks for shutdown tests ---

// deregisterFailMock simulates a scenario where registration succeeds but
// deregistration fails. It tracks calls to distinguish registration from
// deregistration based on the payload action field.
type deregisterFailMock struct {
	mu           sync.Mutex
	registered   bool
	registerResp *bedrockagentcore.InvokeAgentRuntimeOutput
	deregErr     error
}

func (m *deregisterFailMock) InvokeAgentRuntime(ctx context.Context, input *bedrockagentcore.InvokeAgentRuntimeInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeAgentRuntimeOutput, error) {
	// Parse the payload to determine the action.
	if input != nil && len(input.Payload) > 0 {
		var p runtimePayload
		if err := json.Unmarshal(input.Payload, &p); err == nil {
			switch p.Action {
			case "register":
				m.mu.Lock()
				m.registered = true
				m.mu.Unlock()
				return m.registerResp, nil
			case "deregister":
				return nil, m.deregErr
			case "heartbeat":
				return &bedrockagentcore.InvokeAgentRuntimeOutput{}, nil
			}
		}
	}

	// Default: return empty response (poll with no events).
	// Check context to allow graceful exit.
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return &bedrockagentcore.InvokeAgentRuntimeOutput{
		Response: io.NopCloser(bytes.NewReader([]byte{})),
	}, nil
}

func (m *deregisterFailMock) CreateEvent(_ context.Context, _ *bedrockagentcore.CreateEventInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.CreateEventOutput, error) {
	return nil, nil
}
func (m *deregisterFailMock) ListEvents(_ context.Context, _ *bedrockagentcore.ListEventsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.ListEventsOutput, error) {
	return nil, nil
}
func (m *deregisterFailMock) ListSessions(_ context.Context, _ *bedrockagentcore.ListSessionsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.ListSessionsOutput, error) {
	return nil, nil
}
func (m *deregisterFailMock) StopRuntimeSession(_ context.Context, _ *bedrockagentcore.StopRuntimeSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopRuntimeSessionOutput, error) {
	return nil, nil
}
func (m *deregisterFailMock) StartBrowserSession(_ context.Context, _ *bedrockagentcore.StartBrowserSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StartBrowserSessionOutput, error) {
	return nil, nil
}
func (m *deregisterFailMock) InvokeBrowser(_ context.Context, _ *bedrockagentcore.InvokeBrowserInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeBrowserOutput, error) {
	return nil, nil
}
func (m *deregisterFailMock) StopBrowserSession(_ context.Context, _ *bedrockagentcore.StopBrowserSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopBrowserSessionOutput, error) {
	return nil, nil
}
func (m *deregisterFailMock) StartCodeInterpreterSession(_ context.Context, _ *bedrockagentcore.StartCodeInterpreterSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StartCodeInterpreterSessionOutput, error) {
	return nil, nil
}
func (m *deregisterFailMock) InvokeCodeInterpreter(_ context.Context, _ *bedrockagentcore.InvokeCodeInterpreterInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeCodeInterpreterOutput, error) {
	return nil, nil
}
func (m *deregisterFailMock) StopCodeInterpreterSession(_ context.Context, _ *bedrockagentcore.StopCodeInterpreterSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopCodeInterpreterSessionOutput, error) {
	return nil, nil
}

func (m *deregisterFailMock) BatchCreateMemoryRecords(_ context.Context, _ *bedrockagentcore.BatchCreateMemoryRecordsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.BatchCreateMemoryRecordsOutput, error) {
	return nil, nil
}

func (m *deregisterFailMock) RetrieveMemoryRecords(_ context.Context, _ *bedrockagentcore.RetrieveMemoryRecordsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.RetrieveMemoryRecordsOutput, error) {
	return nil, nil
}

func (m *deregisterFailMock) DeleteMemoryRecord(_ context.Context, _ *bedrockagentcore.DeleteMemoryRecordInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.DeleteMemoryRecordOutput, error) {
	return nil, nil
}

