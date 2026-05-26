package agentcore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcore"
	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/testutil"
)

func newTestAgent(t *testing.T, opts ...agent.Option) *agent.Agent {
	t.Helper()
	prov := testutil.NewMockProvider(testutil.WithResponses(&agent.ProviderResponse{Text: "ok"}))
	a, err := agent.New(prov, prompt.Text("test system prompt"), nil, opts...)
	if err != nil {
		t.Fatalf("failed to create test agent: %v", err)
	}
	return a
}

func TestNewRuntime_NilAgent(t *testing.T) {
	_, err := NewRuntime(nil, WithAWSConfig(aws.Config{Region: "us-east-1"}))
	if err == nil {
		t.Fatal("expected error for nil agent, got nil")
	}
	if !errors.Is(err, ErrAgentRequired) {
		t.Errorf("expected ErrAgentRequired, got: %v", err)
	}
}

func TestNewRuntime_MissingAgentName(t *testing.T) {
	// Create an agent without a name.
	a := newTestAgent(t)

	// Don't pass WithAgentName — agent.Name() returns "" since no name was set.
	_, err := NewRuntime(a, WithAWSConfig(aws.Config{Region: "us-east-1"}))
	if err == nil {
		t.Fatal("expected error for missing agent name, got nil")
	}
	if !errors.Is(err, ErrAgentNameRequired) {
		t.Errorf("expected ErrAgentNameRequired, got: %v", err)
	}
}

func TestNewRuntime_ValidWithExplicitAWSConfig(t *testing.T) {
	a := newTestAgent(t, agent.WithName("test-agent"))

	rt, err := NewRuntime(a, WithAWSConfig(aws.Config{Region: "us-west-2"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rt == nil {
		t.Fatal("expected non-nil Runtime")
	}
	if rt.agent != a {
		t.Error("expected Runtime.agent to be the provided agent")
	}
	if rt.cfg.agentName != "test-agent" {
		t.Errorf("expected agentName 'test-agent', got %q", rt.cfg.agentName)
	}
	if rt.cfg.awsCfg == nil {
		t.Fatal("expected awsCfg to be set")
	}
	if rt.cfg.awsCfg.Region != "us-west-2" {
		t.Errorf("expected region 'us-west-2', got %q", rt.cfg.awsCfg.Region)
	}
	if rt.sessions == nil {
		t.Error("expected sessions map to be initialized")
	}
}

func TestNewRuntime_AgentNameFallback(t *testing.T) {
	// Create an agent with a name set via WithName.
	a := newTestAgent(t, agent.WithName("fallback-name"))

	// Don't pass WithAgentName — should fall back to agent.Name().
	rt, err := NewRuntime(a, WithAWSConfig(aws.Config{Region: "us-east-1"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rt.cfg.agentName != "fallback-name" {
		t.Errorf("expected agentName 'fallback-name' from agent.Name() fallback, got %q", rt.cfg.agentName)
	}
}

// --- Tests for Run lifecycle: register, heartbeat, deregister, double-Run ---

func TestRun_DoubleRunRejection(t *testing.T) {
	// Create a mock that succeeds on registration (returns a worker ID).
	mock := &mockAgentCoreClient{
		invokeResults: []invokeResult{
			{output: makeRegisterResponse("worker-123")},
		},
	}
	rt := newTestRuntime(t, mock)

	ctx, cancel := context.WithCancel(context.Background())

	// Start Run in a goroutine.
	runErr := make(chan error, 1)
	go func() {
		runErr <- rt.Run(ctx)
	}()

	// Give Run time to start and register.
	time.Sleep(50 * time.Millisecond)

	// Second call to Run should return ErrAlreadyRunning immediately.
	err := rt.Run(ctx)
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Errorf("expected ErrAlreadyRunning, got: %v", err)
	}

	// Cancel context to stop the first Run.
	cancel()

	// Wait for the first Run to finish.
	select {
	case err := <-runErr:
		if err != nil {
			t.Errorf("expected nil error from first Run, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first Run did not return within timeout")
	}

	// After Run exits, running should be false — a new Run should be possible.
	if rt.running.Load() {
		t.Error("expected running to be false after Run exits")
	}
}

func TestRun_RegisterFailure(t *testing.T) {
	// Mock that fails on registration.
	mock := &mockAgentCoreClient{
		invokeResults: []invokeResult{
			{err: errors.New("registration denied")},
		},
	}
	rt := newTestRuntime(t, mock)

	err := rt.Run(context.Background())
	if err == nil {
		t.Fatal("expected error from Run when registration fails")
	}
	if !containsStr(err.Error(), "worker registration failed") {
		t.Errorf("expected error to contain 'worker registration failed', got: %v", err)
	}

	// After failed registration, running should be false.
	if rt.running.Load() {
		t.Error("expected running to be false after failed registration")
	}
}

func TestRegister_Success(t *testing.T) {
	mock := &mockAgentCoreClient{
		invokeResults: []invokeResult{
			{output: makeRegisterResponse("worker-abc")},
		},
	}
	rt := newTestRuntime(t, mock)

	err := rt.register(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rt.workerID != "worker-abc" {
		t.Errorf("expected workerID 'worker-abc', got %q", rt.workerID)
	}
}

func TestRegister_APIError(t *testing.T) {
	mock := &mockAgentCoreClient{
		invokeResults: []invokeResult{
			{err: errors.New("connection refused")},
		},
	}
	rt := newTestRuntime(t, mock)

	err := rt.register(context.Background())
	if err == nil {
		t.Fatal("expected error from register")
	}
}

func TestSendHeartbeat_Success(t *testing.T) {
	mock := &mockAgentCoreClient{
		invokeResults: []invokeResult{
			{output: &bedrockagentcore.InvokeAgentRuntimeOutput{}},
		},
	}
	rt := newTestRuntime(t, mock)
	rt.workerID = "worker-123"

	rt.sendHeartbeat(context.Background())

	// Should have made exactly 1 call (no retries needed).
	if calls := mock.getInvokeCalls(); calls != 1 {
		t.Errorf("expected 1 invoke call, got %d", calls)
	}
}

func TestSendHeartbeat_RetriesOnFailure(t *testing.T) {
	// Fail 3 times then succeed on 4th attempt (but maxRetries is 3, so it should
	// try 4 times total: initial + 3 retries).
	mock := &mockAgentCoreClient{
		invokeResults: []invokeResult{
			{err: errors.New("timeout")},
			{err: errors.New("timeout")},
			{err: errors.New("timeout")},
			{output: &bedrockagentcore.InvokeAgentRuntimeOutput{}},
		},
	}
	logger := &capturingLoggingHook{}
	rt := newTestRuntime(t, mock)
	rt.agent.SetLoggingHook(logger)
	rt.workerID = "worker-123"

	rt.sendHeartbeat(context.Background())

	// Should have made 4 calls (initial + 3 retries).
	if calls := mock.getInvokeCalls(); calls != 4 {
		t.Errorf("expected 4 invoke calls, got %d", calls)
	}
}

func TestSendHeartbeat_AllRetriesExhausted(t *testing.T) {
	// All attempts fail.
	mock := &mockAgentCoreClient{
		invokeResults: []invokeResult{
			{err: errors.New("timeout")},
			{err: errors.New("timeout")},
			{err: errors.New("timeout")},
			{err: errors.New("timeout")},
		},
	}
	logger := &capturingLoggingHook{}
	rt := newTestRuntime(t, mock)
	rt.agent.SetLoggingHook(logger)
	rt.workerID = "worker-123"

	rt.sendHeartbeat(context.Background())

	// Should have made 4 calls (initial + 3 retries).
	if calls := mock.getInvokeCalls(); calls != 4 {
		t.Errorf("expected 4 invoke calls, got %d", calls)
	}

	// Should have logged the exhaustion.
	logs := logger.getLogs()
	found := false
	for _, l := range logs {
		if containsStr(l.msg, "retries exhausted") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected log message about retries exhausted")
	}
}

func TestSendHeartbeat_ContextCancelled(t *testing.T) {
	// First attempt fails, then context is cancelled before retry.
	mock := &mockAgentCoreClient{
		invokeResults: []invokeResult{
			{err: errors.New("timeout")},
			{err: errors.New("timeout")},
		},
	}
	rt := newTestRuntime(t, mock)
	rt.workerID = "worker-123"

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	rt.sendHeartbeat(ctx)

	// Should have made at most 1 call (context cancelled before retry).
	if calls := mock.getInvokeCalls(); calls > 1 {
		t.Errorf("expected at most 1 invoke call with cancelled context, got %d", calls)
	}
}

func TestDeregister_Success(t *testing.T) {
	mock := &mockAgentCoreClient{
		invokeResults: []invokeResult{
			{output: &bedrockagentcore.InvokeAgentRuntimeOutput{}},
		},
	}
	logger := &capturingLoggingHook{}
	rt := newTestRuntime(t, mock)
	rt.agent.SetLoggingHook(logger)
	rt.workerID = "worker-123"

	rt.deregister(context.Background())

	// Should have logged successful deregistration.
	logs := logger.getLogs()
	found := false
	for _, l := range logs {
		if containsStr(l.msg, "worker deregistered") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected log message about successful deregistration")
	}
}

func TestDeregister_FailureIsBestEffort(t *testing.T) {
	mock := &mockAgentCoreClient{
		invokeResults: []invokeResult{
			{err: errors.New("service unavailable")},
		},
	}
	logger := &capturingLoggingHook{}
	rt := newTestRuntime(t, mock)
	rt.agent.SetLoggingHook(logger)
	rt.workerID = "worker-123"

	// deregister should not panic or return error — it's best-effort.
	rt.deregister(context.Background())

	// Should have logged the failure.
	logs := logger.getLogs()
	found := false
	for _, l := range logs {
		if containsStr(l.msg, "deregistration failed") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected log message about deregistration failure")
	}
}

func TestHeartbeat_StopsOnContextCancel(t *testing.T) {
	mock := &mockAgentCoreClient{}
	rt := newTestRuntime(t, mock)
	rt.cfg.heartbeatInterval = 10 * time.Millisecond
	rt.workerID = "worker-123"

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		rt.heartbeat(ctx)
		close(done)
	}()

	// Let a few heartbeats fire.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// Heartbeat goroutine exited.
	case <-time.After(2 * time.Second):
		t.Fatal("heartbeat goroutine did not exit after context cancel")
	}

	// Should have made at least 1 heartbeat call.
	if calls := mock.getInvokeCalls(); calls < 1 {
		t.Errorf("expected at least 1 heartbeat call, got %d", calls)
	}
}

func TestRun_FullLifecycle(t *testing.T) {
	// Mock: first call is registration, subsequent calls are heartbeats/deregister.
	mock := &mockAgentCoreClient{
		invokeResults: []invokeResult{
			{output: makeRegisterResponse("worker-lifecycle")},
		},
	}
	logger := &capturingLoggingHook{}
	rt := newTestRuntime(t, mock)
	rt.agent.SetLoggingHook(logger)
	rt.cfg.heartbeatInterval = 20 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())

	runErr := make(chan error, 1)
	go func() {
		runErr <- rt.Run(ctx)
	}()

	// Let it run for a bit (register + some heartbeats).
	time.Sleep(100 * time.Millisecond)

	// Cancel to trigger shutdown.
	cancel()

	select {
	case err := <-runErr:
		if err != nil {
			t.Errorf("expected nil error from Run, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within timeout")
	}

	// Verify registration happened.
	if rt.workerID != "worker-lifecycle" {
		t.Errorf("expected workerID 'worker-lifecycle', got %q", rt.workerID)
	}

	// Verify some logging happened (registration + deregistration).
	logs := logger.getLogs()
	hasRegister := false
	for _, l := range logs {
		if containsStr(l.msg, "worker registered") {
			hasRegister = true
		}
	}
	if !hasRegister {
		t.Error("expected registration log message")
	}
}

// --- Helpers ---

func makeRegisterResponse(workerID string) *bedrockagentcore.InvokeAgentRuntimeOutput {
	resp := runtimeResponse{WorkerID: workerID, Status: "registered"}
	data, _ := json.Marshal(resp)
	return &bedrockagentcore.InvokeAgentRuntimeOutput{
		Response: io.NopCloser(bytes.NewReader(data)),
	}
}

// --- AutoConversation tests ---

// inMemoryConversation is a minimal agent.Conversation for testing.
type inMemoryConversation struct {
	data map[string][]agent.Message
}

func (c *inMemoryConversation) Save(_ context.Context, id string, msgs []agent.Message) error {
	c.data[id] = msgs
	return nil
}
func (c *inMemoryConversation) Load(_ context.Context, id string) ([]agent.Message, error) {
	return c.data[id], nil
}
func (c *inMemoryConversation) List(_ context.Context) ([]string, error) {
	var ids []string
	for k := range c.data {
		ids = append(ids, k)
	}
	return ids, nil
}
func (c *inMemoryConversation) Delete(_ context.Context, id string) error {
	delete(c.data, id)
	return nil
}

func TestAutoConversation_CreatesStoreWhenAgentHasNone(t *testing.T) {
	// Agent without a conversation store.
	mock := &mockAgentCoreClient{
		invokeResults: []invokeResult{
			{output: makeRegisterResponse("worker-ac")},
		},
	}
	logger := &capturingLoggingHook{}
	rt := newTestRuntime(t, mock)
	rt.agent.SetLoggingHook(logger)
	rt.cfg.autoConversation = true
	rt.cfg.awsCfg = &aws.Config{Region: "us-east-1"}

	// Verify agent has no conversation initially.
	if rt.agent.HasConversation() {
		t.Fatal("expected agent to not have a conversation store initially")
	}

	ctx, cancel := context.WithCancel(context.Background())

	runErr := make(chan error, 1)
	go func() {
		runErr <- rt.Run(ctx)
	}()

	// Give Run time to register and set up auto-conversation.
	time.Sleep(100 * time.Millisecond)

	// Verify the conversation was created and set on the agent.
	if !rt.agent.HasConversation() {
		t.Error("expected agent to have a conversation store after Run with AutoConversation")
	}
	if rt.conversation == nil {
		t.Error("expected Runtime.conversation to be set")
	}

	// Verify the log message.
	logs := logger.getLogs()
	found := false
	for _, l := range logs {
		if containsStr(l.msg, "auto-conversation store created") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'auto-conversation store created' log message")
	}

	cancel()
	<-runErr
}

func TestAutoConversation_SkipsWhenAgentAlreadyHasConversation(t *testing.T) {
	// Agent with an existing conversation store.
	conv := &inMemoryConversation{data: make(map[string][]agent.Message)}

	mock := &mockAgentCoreClient{
		invokeResults: []invokeResult{
			{output: makeRegisterResponse("worker-ac2")},
		},
	}
	logger := &capturingLoggingHook{}
	rt := newTestRuntime(t, mock, agent.WithSharedConversation(conv))
	rt.agent.SetLoggingHook(logger)
	rt.cfg.autoConversation = true
	rt.cfg.awsCfg = &aws.Config{Region: "us-east-1"}

	ctx, cancel := context.WithCancel(context.Background())

	runErr := make(chan error, 1)
	go func() {
		runErr <- rt.Run(ctx)
	}()

	time.Sleep(100 * time.Millisecond)

	// Verify the Runtime did NOT create its own conversation store.
	if rt.conversation != nil {
		t.Error("expected Runtime.conversation to be nil when agent already has one")
	}

	// Verify no auto-conversation log message.
	logs := logger.getLogs()
	for _, l := range logs {
		if containsStr(l.msg, "auto-conversation store created") {
			t.Error("should not log 'auto-conversation store created' when agent already has a conversation")
		}
	}

	cancel()
	<-runErr
}

func TestAutoConversation_RejectsEmptySessionID(t *testing.T) {
	// Event with empty session ID should be rejected when AutoConversation is enabled.
	mock := &mockAgentCoreClient{
		invokeResults: []invokeResult{
			// Response submission for the error response.
			{output: &bedrockagentcore.InvokeAgentRuntimeOutput{}},
		},
	}
	logger := &capturingLoggingHook{}
	rt := newTestRuntime(t, mock)
	rt.agent.SetLoggingHook(logger)
	rt.cfg.autoConversation = true

	ev := incomingEvent{
		EventID:   "evt-empty-session",
		SessionID: "",
		Message:   "hello",
	}

	rt.processEvent(context.Background(), ev)

	// Verify the rejection was logged.
	logs := logger.getLogs()
	rejectionLogged := false
	for _, l := range logs {
		if containsStr(l.msg, "rejecting event") && containsStr(l.msg, "empty session ID") {
			rejectionLogged = true
			break
		}
	}
	if !rejectionLogged {
		t.Error("expected rejection log for empty session ID with AutoConversation")
	}

	// Verify an error response was submitted.
	if calls := mock.getInvokeCalls(); calls != 1 {
		t.Errorf("expected 1 invoke call (error response submission), got %d", calls)
	}
}

func TestAutoConversation_AllowsNonEmptySessionID(t *testing.T) {
	// Event with non-empty session ID should NOT be rejected.
	mock := &mockAgentCoreClient{
		invokeResults: []invokeResult{
			// Response submission for the agent response.
			{output: &bedrockagentcore.InvokeAgentRuntimeOutput{}},
		},
	}
	logger := &capturingLoggingHook{}
	rt := newTestRuntime(t, mock)
	rt.agent.SetLoggingHook(logger)
	rt.cfg.autoConversation = true

	ev := incomingEvent{
		EventID:   "evt-valid-session",
		SessionID: "sess-123",
		Message:   "hello",
	}

	rt.processEvent(context.Background(), ev)

	// Verify no rejection log.
	logs := logger.getLogs()
	for _, l := range logs {
		if containsStr(l.msg, "rejecting event") {
			t.Error("should not reject event with valid session ID")
		}
	}
}

func TestAutoConversation_NoRejectionWhenDisabled(t *testing.T) {
	// When AutoConversation is NOT enabled, empty session ID should NOT be rejected
	// (it's handled by the normal validate() check in pollLoop, but processEvent
	// itself should not reject it).
	mock := &mockAgentCoreClient{
		invokeResults: []invokeResult{
			{output: &bedrockagentcore.InvokeAgentRuntimeOutput{}},
		},
	}
	logger := &capturingLoggingHook{}
	rt := newTestRuntime(t, mock)
	rt.agent.SetLoggingHook(logger)
	rt.cfg.autoConversation = false

	ev := incomingEvent{
		EventID:   "evt-no-autoconv",
		SessionID: "",
		Message:   "hello",
	}

	rt.processEvent(context.Background(), ev)

	// Verify no rejection log.
	logs := logger.getLogs()
	for _, l := range logs {
		if containsStr(l.msg, "rejecting event") {
			t.Error("should not reject event when AutoConversation is disabled")
		}
	}
}

func TestHasConversation_ReturnsFalseWhenNone(t *testing.T) {
	a := newTestAgent(t)
	if a.HasConversation() {
		t.Error("expected HasConversation() to return false for agent without conversation")
	}
}

func TestHasConversation_ReturnsTrueWhenSet(t *testing.T) {
	conv := &inMemoryConversation{data: make(map[string][]agent.Message)}
	a := newTestAgent(t, agent.WithSharedConversation(conv))
	if !a.HasConversation() {
		t.Error("expected HasConversation() to return true for agent with conversation")
	}
}

func TestSetConversation_SetsConversation(t *testing.T) {
	a := newTestAgent(t)
	if a.HasConversation() {
		t.Fatal("precondition: agent should not have conversation")
	}

	conv := &inMemoryConversation{data: make(map[string][]agent.Message)}
	a.SetConversation(conv)

	if !a.HasConversation() {
		t.Error("expected HasConversation() to return true after SetConversation")
	}
}
