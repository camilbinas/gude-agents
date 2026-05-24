package agentcore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcore"
	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/testutil"
	"pgregory.net/rapid"
)

// Feature: agentcore-runtime, Property 13: Double-Run rejection

// TestProperty13_DoubleRunRejection verifies that for any Runtime instance where
// Run is already executing, a concurrent call to Run returns ErrAlreadyRunning
// immediately without attempting registration.
//
// **Validates: Requirements 2.7**
func TestProperty13_DoubleRunRejection(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Create a mock client that tracks whether InvokeAgentRuntime was called.
		mock := &doubleRunMock{}

		// Create a Runtime directly with the running flag — we don't need a full
		// agent since Run returns immediately when already running.
		rt := &Runtime{
			client:   mock,
			cfg:      defaultRuntimeConfig(),
			sessions: make(map[string]*sync.Mutex),
		}

		// Simulate that Run is already executing by setting the running flag.
		rt.running.Store(true)

		// Calling Run should return ErrAlreadyRunning immediately.
		err := rt.Run(context.Background())
		if !errors.Is(err, ErrAlreadyRunning) {
			t.Fatalf("expected ErrAlreadyRunning, got: %v", err)
		}

		// Verify no registration call was made (InvokeAgentRuntime should not be called).
		if calls := mock.invokeCalls.Load(); calls != 0 {
			t.Fatalf("expected 0 InvokeAgentRuntime calls when double-Run is rejected, got %d", calls)
		}
	})
}

// TestProperty13_ConcurrentRunAttempts verifies that when multiple goroutines
// call Run concurrently, exactly one succeeds (or proceeds to registration) and
// all others return ErrAlreadyRunning immediately without making client calls.
//
// **Validates: Requirements 2.7**
func TestProperty13_ConcurrentRunAttempts(t *testing.T) {
	// Create the agent outside the rapid callback since it needs *testing.T.
	prov := testutil.NewMockProvider(testutil.WithResponses(&agent.ProviderResponse{Text: "ok"}))
	a, err := agent.New(prov, prompt.Text("test system prompt"), nil, agent.WithName("test-agent"))
	if err != nil {
		t.Fatalf("failed to create test agent: %v", err)
	}

	rapid.Check(t, func(t *rapid.T) {
		// Generate a random number of concurrent Run attempts (2-5).
		numAttempts := rapid.IntRange(2, 5).Draw(t, "numAttempts")

		// Create a mock that blocks on the first InvokeAgentRuntime call (registration)
		// until we signal it, then returns a valid registration response followed by
		// context cancellation to end the Run loop.
		mock := &blockingDoubleRunMock{
			unblock: make(chan struct{}),
		}
		rt := &Runtime{
			agent:    a,
			client:   mock,
			cfg:      defaultRuntimeConfig(),
			sessions: make(map[string]*sync.Mutex),
		}

		// Launch all Run attempts concurrently.
		var wg sync.WaitGroup
		errs := make([]error, numAttempts)

		wg.Add(numAttempts)
		for i := 0; i < numAttempts; i++ {
			go func(idx int) {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
				defer cancel()
				errs[idx] = rt.Run(ctx)
			}(i)
		}

		// Give goroutines time to start and race on the CAS.
		time.Sleep(20 * time.Millisecond)

		// Unblock the mock so the winning goroutine can proceed through registration
		// and eventually return due to context timeout.
		close(mock.unblock)

		// Wait for all goroutines to finish.
		wg.Wait()

		// Count how many got ErrAlreadyRunning vs other results.
		alreadyRunningCount := 0
		otherCount := 0
		for _, err := range errs {
			if errors.Is(err, ErrAlreadyRunning) {
				alreadyRunningCount++
			} else {
				otherCount++
			}
		}

		// Exactly one goroutine should NOT get ErrAlreadyRunning (it won the CAS).
		if otherCount != 1 {
			t.Fatalf("expected exactly 1 goroutine to win the CAS, got %d (alreadyRunning=%d, other=%d)",
				otherCount, alreadyRunningCount, otherCount)
		}

		// All others must get ErrAlreadyRunning.
		expectedAlreadyRunning := numAttempts - 1
		if alreadyRunningCount != expectedAlreadyRunning {
			t.Fatalf("expected %d ErrAlreadyRunning errors, got %d",
				expectedAlreadyRunning, alreadyRunningCount)
		}

		// The mock should have received at least 1 InvokeAgentRuntime call
		// (registration from the winning goroutine).
		if calls := mock.invokeCalls.Load(); calls == 0 {
			t.Fatalf("expected at least 1 InvokeAgentRuntime call from the winning goroutine, got 0")
		}
	})
}

// doubleRunMock is a minimal mock that tracks InvokeAgentRuntime calls.
// It satisfies agentCoreClient and returns empty responses.
type doubleRunMock struct {
	invokeCalls atomic.Int32
}

func (m *doubleRunMock) InvokeAgentRuntime(_ context.Context, _ *bedrockagentcore.InvokeAgentRuntimeInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeAgentRuntimeOutput, error) {
	m.invokeCalls.Add(1)
	return &bedrockagentcore.InvokeAgentRuntimeOutput{}, nil
}

func (m *doubleRunMock) CreateEvent(_ context.Context, _ *bedrockagentcore.CreateEventInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.CreateEventOutput, error) {
	return nil, nil
}

func (m *doubleRunMock) ListEvents(_ context.Context, _ *bedrockagentcore.ListEventsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.ListEventsOutput, error) {
	return nil, nil
}

func (m *doubleRunMock) ListSessions(_ context.Context, _ *bedrockagentcore.ListSessionsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.ListSessionsOutput, error) {
	return nil, nil
}

func (m *doubleRunMock) StopRuntimeSession(_ context.Context, _ *bedrockagentcore.StopRuntimeSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopRuntimeSessionOutput, error) {
	return nil, nil
}

func (m *doubleRunMock) StartBrowserSession(_ context.Context, _ *bedrockagentcore.StartBrowserSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StartBrowserSessionOutput, error) {
	return nil, nil
}

func (m *doubleRunMock) InvokeBrowser(_ context.Context, _ *bedrockagentcore.InvokeBrowserInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeBrowserOutput, error) {
	return nil, nil
}

func (m *doubleRunMock) StopBrowserSession(_ context.Context, _ *bedrockagentcore.StopBrowserSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopBrowserSessionOutput, error) {
	return nil, nil
}

func (m *doubleRunMock) StartCodeInterpreterSession(_ context.Context, _ *bedrockagentcore.StartCodeInterpreterSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StartCodeInterpreterSessionOutput, error) {
	return nil, nil
}

func (m *doubleRunMock) InvokeCodeInterpreter(_ context.Context, _ *bedrockagentcore.InvokeCodeInterpreterInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeCodeInterpreterOutput, error) {
	return nil, nil
}

func (m *doubleRunMock) StopCodeInterpreterSession(_ context.Context, _ *bedrockagentcore.StopCodeInterpreterSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopCodeInterpreterSessionOutput, error) {
	return nil, nil
}

func (m *doubleRunMock) BatchCreateMemoryRecords(_ context.Context, _ *bedrockagentcore.BatchCreateMemoryRecordsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.BatchCreateMemoryRecordsOutput, error) {
	return nil, nil
}

func (m *doubleRunMock) RetrieveMemoryRecords(_ context.Context, _ *bedrockagentcore.RetrieveMemoryRecordsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.RetrieveMemoryRecordsOutput, error) {
	return nil, nil
}

func (m *doubleRunMock) DeleteMemoryRecord(_ context.Context, _ *bedrockagentcore.DeleteMemoryRecordInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.DeleteMemoryRecordOutput, error) {
	return nil, nil
}

// blockingDoubleRunMock blocks on the first InvokeAgentRuntime call until unblock
// is closed, then returns a valid registration response. Subsequent calls return
// empty responses (for heartbeat/deregister).
type blockingDoubleRunMock struct {
	invokeCalls atomic.Int32
	unblock     chan struct{}
	once        sync.Once
}

func (m *blockingDoubleRunMock) InvokeAgentRuntime(ctx context.Context, _ *bedrockagentcore.InvokeAgentRuntimeInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeAgentRuntimeOutput, error) {
	m.invokeCalls.Add(1)

	// Block on the first call (registration) until unblock is signaled.
	var blocked bool
	m.once.Do(func() {
		blocked = true
	})
	if blocked {
		select {
		case <-m.unblock:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		// Return a valid registration response.
		resp := runtimeResponse{WorkerID: "worker-pbt", Status: "registered"}
		data, _ := json.Marshal(resp)
		return &bedrockagentcore.InvokeAgentRuntimeOutput{
			Response: io.NopCloser(bytes.NewReader(data)),
		}, nil
	}

	// Subsequent calls (heartbeat, deregister) return empty response.
	return &bedrockagentcore.InvokeAgentRuntimeOutput{}, nil
}

func (m *blockingDoubleRunMock) CreateEvent(_ context.Context, _ *bedrockagentcore.CreateEventInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.CreateEventOutput, error) {
	return nil, nil
}

func (m *blockingDoubleRunMock) ListEvents(_ context.Context, _ *bedrockagentcore.ListEventsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.ListEventsOutput, error) {
	return nil, nil
}

func (m *blockingDoubleRunMock) ListSessions(_ context.Context, _ *bedrockagentcore.ListSessionsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.ListSessionsOutput, error) {
	return nil, nil
}

func (m *blockingDoubleRunMock) StopRuntimeSession(_ context.Context, _ *bedrockagentcore.StopRuntimeSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopRuntimeSessionOutput, error) {
	return nil, nil
}

func (m *blockingDoubleRunMock) StartBrowserSession(_ context.Context, _ *bedrockagentcore.StartBrowserSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StartBrowserSessionOutput, error) {
	return nil, nil
}

func (m *blockingDoubleRunMock) InvokeBrowser(_ context.Context, _ *bedrockagentcore.InvokeBrowserInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeBrowserOutput, error) {
	return nil, nil
}

func (m *blockingDoubleRunMock) StopBrowserSession(_ context.Context, _ *bedrockagentcore.StopBrowserSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopBrowserSessionOutput, error) {
	return nil, nil
}

func (m *blockingDoubleRunMock) StartCodeInterpreterSession(_ context.Context, _ *bedrockagentcore.StartCodeInterpreterSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StartCodeInterpreterSessionOutput, error) {
	return nil, nil
}

func (m *blockingDoubleRunMock) InvokeCodeInterpreter(_ context.Context, _ *bedrockagentcore.InvokeCodeInterpreterInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeCodeInterpreterOutput, error) {
	return nil, nil
}

func (m *blockingDoubleRunMock) StopCodeInterpreterSession(_ context.Context, _ *bedrockagentcore.StopCodeInterpreterSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopCodeInterpreterSessionOutput, error) {
	return nil, nil
}

func (m *blockingDoubleRunMock) BatchCreateMemoryRecords(_ context.Context, _ *bedrockagentcore.BatchCreateMemoryRecordsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.BatchCreateMemoryRecordsOutput, error) {
	return nil, nil
}

func (m *blockingDoubleRunMock) RetrieveMemoryRecords(_ context.Context, _ *bedrockagentcore.RetrieveMemoryRecordsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.RetrieveMemoryRecordsOutput, error) {
	return nil, nil
}

func (m *blockingDoubleRunMock) DeleteMemoryRecord(_ context.Context, _ *bedrockagentcore.DeleteMemoryRecordInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.DeleteMemoryRecordOutput, error) {
	return nil, nil
}

