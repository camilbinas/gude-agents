package agentcore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcore"
	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/testutil"
	"pgregory.net/rapid"
)

// Feature: agentcore-runtime, Property 14: Retry log completeness

// TestProperty14_RetryLogCompleteness verifies that for any retry of an AgentCore
// API call, the log message emitted via LoggingHook contains the operation name,
// the attempt number (starting at 1), and the error description.
//
// **Validates: Requirements 10.5**
func TestProperty14_RetryLogCompleteness(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate a random error message (non-empty, printable).
		errMsg := rapid.StringMatching(`[a-zA-Z0-9 _\-\.]{1,50}`).Draw(t, "errorMessage")

		// Create a mock client that always fails with the generated error.
		simulatedErr := errors.New(errMsg)
		mock := &retryLogMock{err: simulatedErr}

		// Create a test agent with a capturing logging hook.
		prov := testutil.NewMockProvider(testutil.WithResponses(&agent.ProviderResponse{Text: "ok"}))
		a, err := agent.New(prov, prompt.Text("test"), nil, agent.WithName("retry-test-agent"))
		if err != nil {
			t.Fatalf("failed to create agent: %v", err)
		}

		logger := &retryLogCapturingHook{}
		a.SetLoggingHook(logger)

		// Create a Runtime directly with the mock.
		rt := &Runtime{
			agent:    a,
			client:   mock,
			cfg:      defaultRuntimeConfig(),
			sessions: make(map[string]*sync.Mutex),
			workerID: "worker-test",
		}

		// Override heartbeat backoff to use very short delays for testing.
		origBackoff := heartbeatBackoff
		heartbeatBackoff = backoffConfig{
			baseDelay:  1 * time.Millisecond,
			maxDelay:   5 * time.Millisecond,
			maxRetries: 3,
			jitterPct:  0.25,
		}
		defer func() { heartbeatBackoff = origBackoff }()

		// Call sendHeartbeat which will fail and retry, logging each attempt.
		rt.sendHeartbeat(context.Background())

		// Verify the captured logs.
		logs := logger.getLogs()

		// sendHeartbeat retries up to heartbeatBackoff.maxRetries times (3).
		// Each attempt (including the initial one) that fails logs a message.
		// Total attempts = maxRetries + 1 = 4, so we expect 4 log entries for failures
		// plus 1 "retries exhausted" entry = 5 total.
		expectedAttempts := heartbeatBackoff.maxRetries + 1

		// Filter to only retry log entries (exclude "retries exhausted" message).
		var retryLogs []retryLogEntry
		for _, entry := range logs {
			if strings.Contains(entry.msg, "attempt") {
				retryLogs = append(retryLogs, entry)
			}
		}

		if len(retryLogs) != expectedAttempts {
			t.Fatalf("expected %d retry log entries, got %d (total logs: %d)", expectedAttempts, len(retryLogs), len(logs))
		}

		// Verify each retry log entry contains:
		// 1. The operation name (toolName = "agentcore:heartbeat")
		// 2. The attempt number (starting at 1)
		// 3. The error description
		for i := 0; i < expectedAttempts; i++ {
			entry := retryLogs[i]

			// Check operation name is present as the toolName.
			if entry.toolName != "agentcore:heartbeat" {
				t.Fatalf("attempt %d: expected toolName 'agentcore:heartbeat', got %q", i+1, entry.toolName)
			}

			// Check attempt number (starting at 1) is in the message.
			attemptStr := fmt.Sprintf("attempt %d/", i+1)
			if !strings.Contains(entry.msg, attemptStr) {
				t.Fatalf("attempt %d: expected log message to contain %q, got %q", i+1, attemptStr, entry.msg)
			}

			// Check error description is in the message.
			if !strings.Contains(entry.msg, errMsg) {
				t.Fatalf("attempt %d: expected log message to contain error %q, got %q", i+1, errMsg, entry.msg)
			}
		}
	})
}

// retryLogMock is a mock client that always returns an error for InvokeAgentRuntime.
type retryLogMock struct {
	err error
}

func (m *retryLogMock) InvokeAgentRuntime(_ context.Context, _ *bedrockagentcore.InvokeAgentRuntimeInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeAgentRuntimeOutput, error) {
	return nil, m.err
}

func (m *retryLogMock) CreateEvent(_ context.Context, _ *bedrockagentcore.CreateEventInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.CreateEventOutput, error) {
	return nil, nil
}

func (m *retryLogMock) ListEvents(_ context.Context, _ *bedrockagentcore.ListEventsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.ListEventsOutput, error) {
	return nil, nil
}

func (m *retryLogMock) ListSessions(_ context.Context, _ *bedrockagentcore.ListSessionsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.ListSessionsOutput, error) {
	return nil, nil
}

func (m *retryLogMock) StopRuntimeSession(_ context.Context, _ *bedrockagentcore.StopRuntimeSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopRuntimeSessionOutput, error) {
	return nil, nil
}

func (m *retryLogMock) StartBrowserSession(_ context.Context, _ *bedrockagentcore.StartBrowserSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StartBrowserSessionOutput, error) {
	return nil, nil
}

func (m *retryLogMock) InvokeBrowser(_ context.Context, _ *bedrockagentcore.InvokeBrowserInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeBrowserOutput, error) {
	return nil, nil
}

func (m *retryLogMock) StopBrowserSession(_ context.Context, _ *bedrockagentcore.StopBrowserSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopBrowserSessionOutput, error) {
	return nil, nil
}

func (m *retryLogMock) StartCodeInterpreterSession(_ context.Context, _ *bedrockagentcore.StartCodeInterpreterSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StartCodeInterpreterSessionOutput, error) {
	return nil, nil
}

func (m *retryLogMock) InvokeCodeInterpreter(_ context.Context, _ *bedrockagentcore.InvokeCodeInterpreterInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeCodeInterpreterOutput, error) {
	return nil, nil
}

func (m *retryLogMock) StopCodeInterpreterSession(_ context.Context, _ *bedrockagentcore.StopCodeInterpreterSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopCodeInterpreterSessionOutput, error) {
	return nil, nil
}

func (m *retryLogMock) BatchCreateMemoryRecords(_ context.Context, _ *bedrockagentcore.BatchCreateMemoryRecordsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.BatchCreateMemoryRecordsOutput, error) {
	return nil, nil
}

func (m *retryLogMock) RetrieveMemoryRecords(_ context.Context, _ *bedrockagentcore.RetrieveMemoryRecordsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.RetrieveMemoryRecordsOutput, error) {
	return nil, nil
}

func (m *retryLogMock) DeleteMemoryRecord(_ context.Context, _ *bedrockagentcore.DeleteMemoryRecordInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.DeleteMemoryRecordOutput, error) {
	return nil, nil
}

// retryLogCapturingHook captures OnToolLog calls for Property 14 assertions.
type retryLogCapturingHook struct {
	mu   sync.Mutex
	logs []retryLogEntry
}

type retryLogEntry struct {
	toolName string
	msg      string
}

func (h *retryLogCapturingHook) OnToolLog(toolName string, msg string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.logs = append(h.logs, retryLogEntry{toolName: toolName, msg: msg})
}

func (h *retryLogCapturingHook) getLogs() []retryLogEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	result := make([]retryLogEntry, len(h.logs))
	copy(result, h.logs)
	return result
}

// Implement remaining LoggingHook methods as no-ops.
func (h *retryLogCapturingHook) OnInvokeStart(_ agent.InvokeSpanParams)                   {}
func (h *retryLogCapturingHook) OnInvokeEnd(_ error, _ agent.TokenUsage, _ time.Duration) {}
func (h *retryLogCapturingHook) OnIterationStart(_ int)                                   {}
func (h *retryLogCapturingHook) OnIterationEnd(_ int, _ int, _ bool, _ time.Duration)     {}
func (h *retryLogCapturingHook) OnProviderCallStart(_ string)                             {}
func (h *retryLogCapturingHook) OnProviderCallEnd(_ error, _ agent.TokenUsage, _ int, _ time.Duration) {
}
func (h *retryLogCapturingHook) OnToolStart(_ string)                          {}
func (h *retryLogCapturingHook) OnToolEnd(_ string, _ error, _ time.Duration)  {}
func (h *retryLogCapturingHook) OnGuardrailComplete(_ string, _ bool, _ error) {}
func (h *retryLogCapturingHook) OnConversationStart(_ string, _ string)        {}
func (h *retryLogCapturingHook) OnConversationEnd(_ string, _ string, _ error, _ int, _ time.Duration) {
}
func (h *retryLogCapturingHook) OnRetrieverStart(_ string)                      {}
func (h *retryLogCapturingHook) OnRetrieverEnd(_ error, _ int, _ time.Duration) {}
func (h *retryLogCapturingHook) OnImagesAttached(_ int)                         {}
func (h *retryLogCapturingHook) OnDocumentsAttached(_ int)                      {}
func (h *retryLogCapturingHook) OnMaxIterationsExceeded(_ int)                  {}
func (h *retryLogCapturingHook) OnStreamChunk(_ string)                         {}
func (h *retryLogCapturingHook) OnResponse(_ string)                            {}
