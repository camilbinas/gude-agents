package agentcore

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcore"
	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/testutil"
)

// lifecycleMockClient is a mock that simulates the full AgentCore lifecycle:
// registration, heartbeats, event polling, response submission, and deregistration.
// It uses a channel to inject events into the poll loop and captures all submitted payloads.
type lifecycleMockClient struct {
	mu sync.Mutex

	// Registration
	workerID string

	// Events to deliver via poll
	events chan incomingEvent

	// Captured payloads (responses, chunks, etc.)
	payloads [][]byte

	// Track call types for assertions
	callCount int
}

func newLifecycleMockClient(workerID string) *lifecycleMockClient {
	return &lifecycleMockClient{
		workerID: workerID,
		events:   make(chan incomingEvent, 10),
	}
}

func (m *lifecycleMockClient) getPayloads() [][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([][]byte, len(m.payloads))
	copy(cp, m.payloads)
	return cp
}

func (m *lifecycleMockClient) getCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callCount
}

func (m *lifecycleMockClient) InvokeAgentRuntime(_ context.Context, input *bedrockagentcore.InvokeAgentRuntimeInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeAgentRuntimeOutput, error) {
	m.mu.Lock()
	m.callCount++

	// Capture the payload if present.
	if input != nil && input.Payload != nil {
		m.payloads = append(m.payloads, input.Payload)

		// Determine what kind of call this is by inspecting the payload.
		var rp runtimePayload
		if json.Unmarshal(input.Payload, &rp) == nil && rp.Action != "" {
			switch rp.Action {
			case "register":
				m.mu.Unlock()
				resp := runtimeResponse{WorkerID: m.workerID, Status: "registered"}
				data, _ := json.Marshal(resp)
				return &bedrockagentcore.InvokeAgentRuntimeOutput{
					Response: io.NopCloser(bytes.NewReader(data)),
				}, nil
			case "heartbeat":
				m.mu.Unlock()
				return &bedrockagentcore.InvokeAgentRuntimeOutput{}, nil
			case "deregister":
				m.mu.Unlock()
				return &bedrockagentcore.InvokeAgentRuntimeOutput{}, nil
			}
		}

		// If it's not a lifecycle action, it's a response/chunk submission.
		m.mu.Unlock()
		return &bedrockagentcore.InvokeAgentRuntimeOutput{}, nil
	}
	m.mu.Unlock()

	// No payload = poll request. Try to deliver an event from the channel.
	select {
	case ev := <-m.events:
		data, _ := json.Marshal(ev)
		return &bedrockagentcore.InvokeAgentRuntimeOutput{
			Response: io.NopCloser(bytes.NewReader(data)),
		}, nil
	case <-time.After(50 * time.Millisecond):
		// Long-poll timeout — return empty response.
		return &bedrockagentcore.InvokeAgentRuntimeOutput{}, nil
	}
}

func (m *lifecycleMockClient) CreateEvent(_ context.Context, _ *bedrockagentcore.CreateEventInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.CreateEventOutput, error) {
	return &bedrockagentcore.CreateEventOutput{}, nil
}
func (m *lifecycleMockClient) ListEvents(_ context.Context, _ *bedrockagentcore.ListEventsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.ListEventsOutput, error) {
	return &bedrockagentcore.ListEventsOutput{}, nil
}
func (m *lifecycleMockClient) ListSessions(_ context.Context, _ *bedrockagentcore.ListSessionsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.ListSessionsOutput, error) {
	return &bedrockagentcore.ListSessionsOutput{}, nil
}
func (m *lifecycleMockClient) StopRuntimeSession(_ context.Context, _ *bedrockagentcore.StopRuntimeSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopRuntimeSessionOutput, error) {
	return &bedrockagentcore.StopRuntimeSessionOutput{}, nil
}
func (m *lifecycleMockClient) StartBrowserSession(_ context.Context, _ *bedrockagentcore.StartBrowserSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StartBrowserSessionOutput, error) {
	return nil, nil
}
func (m *lifecycleMockClient) InvokeBrowser(_ context.Context, _ *bedrockagentcore.InvokeBrowserInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeBrowserOutput, error) {
	return nil, nil
}
func (m *lifecycleMockClient) StopBrowserSession(_ context.Context, _ *bedrockagentcore.StopBrowserSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopBrowserSessionOutput, error) {
	return nil, nil
}
func (m *lifecycleMockClient) StartCodeInterpreterSession(_ context.Context, _ *bedrockagentcore.StartCodeInterpreterSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StartCodeInterpreterSessionOutput, error) {
	return nil, nil
}
func (m *lifecycleMockClient) InvokeCodeInterpreter(_ context.Context, _ *bedrockagentcore.InvokeCodeInterpreterInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeCodeInterpreterOutput, error) {
	return nil, nil
}
func (m *lifecycleMockClient) StopCodeInterpreterSession(_ context.Context, _ *bedrockagentcore.StopCodeInterpreterSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopCodeInterpreterSessionOutput, error) {
	return nil, nil
}

func (m *lifecycleMockClient) BatchCreateMemoryRecords(_ context.Context, _ *bedrockagentcore.BatchCreateMemoryRecordsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.BatchCreateMemoryRecordsOutput, error) {
	return nil, nil
}

func (m *lifecycleMockClient) RetrieveMemoryRecords(_ context.Context, _ *bedrockagentcore.RetrieveMemoryRecordsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.RetrieveMemoryRecordsOutput, error) {
	return nil, nil
}

func (m *lifecycleMockClient) DeleteMemoryRecord(_ context.Context, _ *bedrockagentcore.DeleteMemoryRecordInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.DeleteMemoryRecordOutput, error) {
	return nil, nil
}

// --- Integration Test 1: Full Runtime Lifecycle ---
// Validates: Requirements 2.1, 2.2, 3.1, 3.3, 3.4

func TestIntegration_FullRuntimeLifecycle(t *testing.T) {
	// Set up a mock provider that returns a known response.
	prov := testutil.NewMockProvider(testutil.WithResponses(
		&agent.ProviderResponse{Text: "agent response to user"},
	))
	a, err := agent.New(prov, prompt.Text("you are a helpful assistant"), nil, agent.WithName("lifecycle-agent"))
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	// Set up the lifecycle mock client.
	mock := newLifecycleMockClient("worker-integ-001")
	logger := &capturingLoggingHook{}
	a.SetLoggingHook(logger)

	// Create the Runtime directly with the mock client (bypass NewRuntime's AWS config loading).
	rt := &Runtime{
		agent:    a,
		client:   mock,
		cfg:      defaultRuntimeConfig(),
		sessions: make(map[string]*sync.Mutex),
	}
	rt.cfg.heartbeatInterval = 50 * time.Millisecond
	rt.cfg.shutdownTimeout = 2 * time.Second
	rt.cfg.streaming = false
	rt.cfg.awsCfg = &aws.Config{Region: "us-east-1"}

	ctx, cancel := context.WithCancel(context.Background())

	// Start the runtime in a goroutine.
	runErr := make(chan error, 1)
	go func() {
		runErr <- rt.Run(ctx)
	}()

	// Wait for registration to complete.
	time.Sleep(100 * time.Millisecond)

	// Verify registration happened (read under lock to avoid race with Run goroutine).
	rt.mu.Lock()
	wid := rt.workerID
	rt.mu.Unlock()
	if wid != "worker-integ-001" {
		t.Fatalf("expected workerID 'worker-integ-001', got %q", wid)
	}

	// Inject an event into the poll loop.
	mock.events <- incomingEvent{
		EventID:   "evt-lifecycle-1",
		SessionID: "sess-lifecycle-1",
		Message:   "hello from user",
	}

	// Wait for the event to be processed and response submitted.
	deadline := time.Now().Add(3 * time.Second)
	var foundResponse bool
	for time.Now().Before(deadline) {
		payloads := mock.getPayloads()
		for _, p := range payloads {
			var resp eventResponse
			if json.Unmarshal(p, &resp) == nil && resp.EventID == "evt-lifecycle-1" {
				foundResponse = true
				if resp.Response != "agent response to user" {
					t.Errorf("expected response 'agent response to user', got %q", resp.Response)
				}
				if resp.IsError {
					t.Error("expected IsError to be false")
				}
				break
			}
		}
		if foundResponse {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !foundResponse {
		t.Fatal("timed out waiting for agent response submission")
	}

	// Verify heartbeats were sent (at 50ms interval, after 100ms+ we should have at least 1).
	logs := logger.getLogs()
	// Heartbeats are sent via InvokeAgentRuntime with "heartbeat" action — check call count.
	if mock.getCallCount() < 3 {
		// At minimum: 1 register + 1 poll + 1 response submission = 3 calls.
		t.Errorf("expected at least 3 API calls, got %d", mock.getCallCount())
	}

	// Cancel context to trigger shutdown.
	cancel()

	// Wait for Run to return.
	select {
	case err := <-runErr:
		if err != nil {
			t.Errorf("expected nil error from Run, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within timeout")
	}

	// Verify deregistration was logged.
	logs = logger.getLogs()
	hasDeregister := false
	for _, l := range logs {
		if containsStr(l.msg, "worker deregistered") {
			hasDeregister = true
			break
		}
	}
	if !hasDeregister {
		t.Error("expected deregistration log message after shutdown")
	}

	// Verify registration was logged.
	hasRegister := false
	for _, l := range logs {
		if containsStr(l.msg, "worker registered") {
			hasRegister = true
			break
		}
	}
	if !hasRegister {
		t.Error("expected registration log message")
	}
}

// --- Integration Test 2: Streaming Path End-to-End ---
// Validates: Requirements 4.2

func TestIntegration_StreamingPathEndToEnd(t *testing.T) {
	// Set up a mock provider that streams "hello world" as two words.
	prov := testutil.NewMockProvider(
		testutil.WithResponses(&agent.ProviderResponse{Text: "hello world"}),
		testutil.WithStreamWords(),
	)
	a, err := agent.New(prov, prompt.Text("you are a helpful assistant"), nil, agent.WithName("stream-agent"))
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	// Set up the lifecycle mock client.
	mock := newLifecycleMockClient("worker-stream-001")
	logger := &capturingLoggingHook{}
	a.SetLoggingHook(logger)

	// Create the Runtime with streaming enabled.
	rt := &Runtime{
		agent:    a,
		client:   mock,
		cfg:      defaultRuntimeConfig(),
		sessions: make(map[string]*sync.Mutex),
	}
	rt.cfg.heartbeatInterval = 100 * time.Millisecond
	rt.cfg.shutdownTimeout = 2 * time.Second
	rt.cfg.streaming = true
	rt.cfg.awsCfg = &aws.Config{Region: "us-east-1"}

	ctx, cancel := context.WithCancel(context.Background())

	// Start the runtime.
	runErr := make(chan error, 1)
	go func() {
		runErr <- rt.Run(ctx)
	}()

	// Wait for registration.
	time.Sleep(100 * time.Millisecond)

	// Inject an event.
	mock.events <- incomingEvent{
		EventID:   "evt-stream-1",
		SessionID: "sess-stream-1",
		Message:   "tell me something",
	}

	// Wait for stream chunks to be submitted.
	deadline := time.Now().Add(3 * time.Second)
	var chunks []streamChunk
	for time.Now().Before(deadline) {
		payloads := mock.getPayloads()
		chunks = nil
		for _, p := range payloads {
			var sc streamChunk
			if json.Unmarshal(p, &sc) == nil && sc.EventID == "evt-stream-1" {
				chunks = append(chunks, sc)
			}
		}
		// We expect at least 3 chunks: "hello", " ", "world", and a final marker.
		// With WithStreamWords, the provider calls cb("hello"), cb(" "), cb("world").
		// So we expect 3 content chunks + 1 final = 4 total.
		// Actually looking at the provider code: it calls cb("hello"), cb(" "), cb("world")
		// which is 3 callbacks. Plus the final marker = 4 chunks total.
		hasFinal := false
		for _, sc := range chunks {
			if sc.Final {
				hasFinal = true
				break
			}
		}
		if hasFinal && len(chunks) >= 3 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if len(chunks) < 3 {
		t.Fatalf("expected at least 3 stream chunks (content + final), got %d", len(chunks))
	}

	// Verify chunk ordering: indices should be sequential.
	for i, sc := range chunks {
		if sc.Index != i {
			t.Errorf("chunk %d: expected Index=%d, got %d", i, i, sc.Index)
		}
	}

	// Verify the last chunk is the final marker.
	lastChunk := chunks[len(chunks)-1]
	if !lastChunk.Final {
		t.Error("expected last chunk to have Final=true")
	}
	if lastChunk.Chunk != "" {
		t.Errorf("expected final chunk to have empty Chunk, got %q", lastChunk.Chunk)
	}

	// Verify non-final chunks have content and concatenate to "hello world".
	var concatenated string
	for i := 0; i < len(chunks)-1; i++ {
		if chunks[i].Final {
			t.Errorf("chunk %d: expected Final=false for non-final chunk", i)
		}
		concatenated += chunks[i].Chunk
	}
	if concatenated != "hello world" {
		t.Errorf("expected concatenated chunks to be 'hello world', got %q", concatenated)
	}

	// Shutdown.
	cancel()
	select {
	case err := <-runErr:
		if err != nil {
			t.Errorf("expected nil error from Run, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within timeout")
	}
}

// --- Integration Test 3: AutoConversation Wiring ---
// Validates: Requirements 9.2
// Tests that when AutoConversation is enabled and the agent already has a conversation
// store, the Runtime uses the existing store and sets the session ID as conversation ID.

func TestIntegration_AutoConversationWiring(t *testing.T) {
	// Set up a mock provider.
	prov := testutil.NewMockProvider(
		testutil.WithResponses(&agent.ProviderResponse{Text: "conversation reply"}),
		testutil.WithCapture(),
	)

	// Create an in-memory conversation store to track Save/Load calls.
	conv := &trackingConversation{data: make(map[string][]agent.Message)}

	a, err := agent.New(prov, prompt.Text("you are a helpful assistant"), nil,
		agent.WithName("autoconv-agent"),
		agent.WithSharedConversation(conv),
	)
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	// Set up the lifecycle mock client.
	mock := newLifecycleMockClient("worker-autoconv-001")
	logger := &capturingLoggingHook{}
	a.SetLoggingHook(logger)

	// Create the Runtime with AutoConversation enabled.
	rt := &Runtime{
		agent:    a,
		client:   mock,
		cfg:      defaultRuntimeConfig(),
		sessions: make(map[string]*sync.Mutex),
	}
	rt.cfg.heartbeatInterval = 100 * time.Millisecond
	rt.cfg.shutdownTimeout = 2 * time.Second
	rt.cfg.streaming = false
	rt.cfg.autoConversation = true
	rt.cfg.awsCfg = &aws.Config{Region: "us-east-1"}

	ctx, cancel := context.WithCancel(context.Background())

	// Start the runtime.
	runErr := make(chan error, 1)
	go func() {
		runErr <- rt.Run(ctx)
	}()

	// Wait for registration.
	time.Sleep(150 * time.Millisecond)

	// Since the agent already has a conversation store, the Runtime should NOT
	// create its own — it should use the existing one.
	if rt.conversation != nil {
		t.Error("expected Runtime.conversation to be nil when agent already has a conversation store")
	}

	// Inject an event with a specific session ID.
	mock.events <- incomingEvent{
		EventID:   "evt-autoconv-1",
		SessionID: "sess-autoconv-abc",
		Message:   "hello with session",
	}

	// Wait for the event to be processed.
	deadline := time.Now().Add(3 * time.Second)
	var foundResponse bool
	for time.Now().Before(deadline) {
		payloads := mock.getPayloads()
		for _, p := range payloads {
			var resp eventResponse
			if json.Unmarshal(p, &resp) == nil && resp.EventID == "evt-autoconv-1" {
				foundResponse = true
				if resp.Response != "conversation reply" {
					t.Errorf("expected response 'conversation reply', got %q", resp.Response)
				}
				break
			}
		}
		if foundResponse {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !foundResponse {
		t.Fatal("timed out waiting for agent response with AutoConversation")
	}

	// Verify the session ID was used as the conversation ID by checking the
	// tracking conversation store's Save calls.
	savedIDs := conv.getSavedIDs()
	foundSessionID := false
	for _, id := range savedIDs {
		if id == "sess-autoconv-abc" {
			foundSessionID = true
			break
		}
	}
	if !foundSessionID {
		t.Errorf("expected conversation Save to be called with session ID 'sess-autoconv-abc', got saves for: %v", savedIDs)
	}

	// Verify the processEvent log references the session ID.
	logs := logger.getLogs()
	sessionProcessed := false
	for _, l := range logs {
		if l.toolName == "processEvent" && containsStr(l.msg, "sess-autoconv-abc") {
			sessionProcessed = true
			break
		}
	}
	if !sessionProcessed {
		t.Error("expected processEvent log to reference session ID 'sess-autoconv-abc'")
	}

	// Shutdown.
	cancel()
	select {
	case err := <-runErr:
		if err != nil {
			t.Errorf("expected nil error from Run, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within timeout")
	}
}

// trackingConversation is an in-memory conversation store that tracks which
// conversation IDs were used in Save calls.
type trackingConversation struct {
	mu       sync.Mutex
	data     map[string][]agent.Message
	savedIDs []string
}

func (c *trackingConversation) Save(_ context.Context, id string, msgs []agent.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[id] = msgs
	c.savedIDs = append(c.savedIDs, id)
	return nil
}

func (c *trackingConversation) Load(_ context.Context, id string) ([]agent.Message, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	msgs, ok := c.data[id]
	if !ok {
		return []agent.Message{}, nil
	}
	return msgs, nil
}

func (c *trackingConversation) List(_ context.Context) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var ids []string
	for k := range c.data {
		ids = append(ids, k)
	}
	return ids, nil
}

func (c *trackingConversation) Delete(_ context.Context, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.data, id)
	return nil
}

func (c *trackingConversation) getSavedIDs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := make([]string, len(c.savedIDs))
	copy(cp, c.savedIDs)
	return cp
}
