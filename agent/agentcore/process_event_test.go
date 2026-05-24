package agentcore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcore"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/testutil"
)

// capturingClient wraps mockAgentCoreClient and captures the payloads sent to InvokeAgentRuntime.
type capturingClient struct {
	mu       sync.Mutex
	results  []invokeResult
	index    int
	payloads [][]byte
}

func (c *capturingClient) nextResult() (*bedrockagentcore.InvokeAgentRuntimeOutput, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.index >= len(c.results) {
		return &bedrockagentcore.InvokeAgentRuntimeOutput{}, nil
	}
	r := c.results[c.index]
	c.index++
	return r.output, r.err
}

func (c *capturingClient) getPayloads() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := make([][]byte, len(c.payloads))
	copy(cp, c.payloads)
	return cp
}

func (c *capturingClient) InvokeAgentRuntime(_ context.Context, input *bedrockagentcore.InvokeAgentRuntimeInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeAgentRuntimeOutput, error) {
	c.mu.Lock()
	if input != nil && input.Payload != nil {
		c.payloads = append(c.payloads, input.Payload)
	}
	c.mu.Unlock()
	return c.nextResult()
}

func (c *capturingClient) CreateEvent(_ context.Context, _ *bedrockagentcore.CreateEventInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.CreateEventOutput, error) {
	return nil, nil
}
func (c *capturingClient) ListEvents(_ context.Context, _ *bedrockagentcore.ListEventsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.ListEventsOutput, error) {
	return nil, nil
}
func (c *capturingClient) ListSessions(_ context.Context, _ *bedrockagentcore.ListSessionsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.ListSessionsOutput, error) {
	return nil, nil
}
func (c *capturingClient) StopRuntimeSession(_ context.Context, _ *bedrockagentcore.StopRuntimeSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopRuntimeSessionOutput, error) {
	return nil, nil
}
func (c *capturingClient) StartBrowserSession(_ context.Context, _ *bedrockagentcore.StartBrowserSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StartBrowserSessionOutput, error) {
	return nil, nil
}
func (c *capturingClient) InvokeBrowser(_ context.Context, _ *bedrockagentcore.InvokeBrowserInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeBrowserOutput, error) {
	return nil, nil
}
func (c *capturingClient) StopBrowserSession(_ context.Context, _ *bedrockagentcore.StopBrowserSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopBrowserSessionOutput, error) {
	return nil, nil
}
func (c *capturingClient) StartCodeInterpreterSession(_ context.Context, _ *bedrockagentcore.StartCodeInterpreterSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StartCodeInterpreterSessionOutput, error) {
	return nil, nil
}
func (c *capturingClient) InvokeCodeInterpreter(_ context.Context, _ *bedrockagentcore.InvokeCodeInterpreterInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeCodeInterpreterOutput, error) {
	return nil, nil
}
func (c *capturingClient) StopCodeInterpreterSession(_ context.Context, _ *bedrockagentcore.StopCodeInterpreterSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopCodeInterpreterSessionOutput, error) {
	return nil, nil
}

func (c *capturingClient) BatchCreateMemoryRecords(_ context.Context, _ *bedrockagentcore.BatchCreateMemoryRecordsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.BatchCreateMemoryRecordsOutput, error) {
	return nil, nil
}

func (c *capturingClient) RetrieveMemoryRecords(_ context.Context, _ *bedrockagentcore.RetrieveMemoryRecordsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.RetrieveMemoryRecordsOutput, error) {
	return nil, nil
}

func (c *capturingClient) DeleteMemoryRecord(_ context.Context, _ *bedrockagentcore.DeleteMemoryRecordInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.DeleteMemoryRecordOutput, error) {
	return nil, nil
}

func (c *capturingClient) GetAgentCard(_ context.Context, _ *bedrockagentcore.GetAgentCardInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.GetAgentCardOutput, error) {
	return nil, nil
}

func (c *capturingClient) SearchRegistryRecords(_ context.Context, _ *bedrockagentcore.SearchRegistryRecordsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.SearchRegistryRecordsOutput, error) {
	return nil, nil
}

// --- processEvent tests ---

func TestProcessEvent_NonStreamingSuccess(t *testing.T) {
	// Agent returns "ok" — processEvent should submit a success response.
	prov := testutil.NewMockProvider(testutil.WithResponses(&agent.ProviderResponse{Text: "hello world"}))
	a, err := agent.New(prov, prompt.Text("system"), nil, agent.WithName("test-agent"))
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	client := &capturingClient{
		results: []invokeResult{
			{output: &bedrockagentcore.InvokeAgentRuntimeOutput{}}, // submission succeeds
		},
	}

	rt := &Runtime{
		agent:    a,
		client:   client,
		cfg:      defaultRuntimeConfig(),
		sessions: make(map[string]*sync.Mutex),
	}
	rt.cfg.streaming = false

	ev := incomingEvent{
		EventID:   "evt-100",
		SessionID: "sess-200",
		Message:   "hi there",
	}

	rt.processEvent(context.Background(), ev)

	// Verify the submitted payload.
	payloads := client.getPayloads()
	if len(payloads) == 0 {
		t.Fatal("expected at least one payload submitted")
	}

	var resp eventResponse
	if err := json.Unmarshal(payloads[0], &resp); err != nil {
		t.Fatalf("failed to unmarshal submitted payload: %v", err)
	}

	if resp.EventID != "evt-100" {
		t.Errorf("expected EventID 'evt-100', got %q", resp.EventID)
	}
	if resp.Response != "hello world" {
		t.Errorf("expected Response 'hello world', got %q", resp.Response)
	}
	if resp.IsError {
		t.Error("expected IsError to be false")
	}
}

func TestProcessEvent_AgentError_SubmitsErrorResponse(t *testing.T) {
	// Agent returns an error — processEvent should submit an error response.
	prov := testutil.NewMockProvider(testutil.WithError(errors.New("model unavailable")))
	a, err := agent.New(prov, prompt.Text("system"), nil, agent.WithName("test-agent"))
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	client := &capturingClient{
		results: []invokeResult{
			{output: &bedrockagentcore.InvokeAgentRuntimeOutput{}},
		},
	}

	rt := &Runtime{
		agent:    a,
		client:   client,
		cfg:      defaultRuntimeConfig(),
		sessions: make(map[string]*sync.Mutex),
	}
	rt.cfg.streaming = false

	ev := incomingEvent{
		EventID:   "evt-err",
		SessionID: "sess-err",
		Message:   "hello",
	}

	rt.processEvent(context.Background(), ev)

	payloads := client.getPayloads()
	if len(payloads) == 0 {
		t.Fatal("expected at least one payload submitted")
	}

	var resp eventResponse
	if err := json.Unmarshal(payloads[0], &resp); err != nil {
		t.Fatalf("failed to unmarshal submitted payload: %v", err)
	}

	if resp.EventID != "evt-err" {
		t.Errorf("expected EventID 'evt-err', got %q", resp.EventID)
	}
	if !resp.IsError {
		t.Error("expected IsError to be true for agent error")
	}
	if resp.Response == "" {
		t.Error("expected non-empty error response")
	}
}

func TestProcessEvent_SubmissionRetryOnTransientError(t *testing.T) {
	// Agent succeeds, but first submission attempt fails with transient error.
	prov := testutil.NewMockProvider(testutil.WithResponses(&agent.ProviderResponse{Text: "result"}))
	a, err := agent.New(prov, prompt.Text("system"), nil, agent.WithName("test-agent"))
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	transientErr := &smithyhttp.ResponseError{
		Response: &smithyhttp.Response{
			Response: &http.Response{StatusCode: 503},
		},
		Err: errors.New("service unavailable"),
	}

	client := &capturingClient{
		results: []invokeResult{
			{err: transientErr}, // first attempt fails
			{output: &bedrockagentcore.InvokeAgentRuntimeOutput{}}, // retry succeeds
		},
	}

	logger := &capturingLoggingHook{}
	rt := &Runtime{
		agent:    a,
		client:   client,
		cfg:      defaultRuntimeConfig(),
		sessions: make(map[string]*sync.Mutex),
	}
	rt.cfg.streaming = false
	a.SetLoggingHook(logger)

	ev := incomingEvent{
		EventID:   "evt-retry",
		SessionID: "sess-retry",
		Message:   "hello",
	}

	rt.processEvent(context.Background(), ev)

	// Should have submitted twice (first failed, second succeeded).
	payloads := client.getPayloads()
	if len(payloads) < 2 {
		t.Errorf("expected at least 2 submission attempts, got %d", len(payloads))
	}

	// Verify retry was logged.
	logs := logger.getLogs()
	retryLogged := false
	for _, l := range logs {
		if containsStr(l.msg, "response submission failed") {
			retryLogged = true
			break
		}
	}
	if !retryLogged {
		t.Error("expected retry log message")
	}
}

func TestProcessEvent_NoRetryOnPermanentError(t *testing.T) {
	// Agent succeeds, but submission fails with a permanent 400 error.
	prov := testutil.NewMockProvider(testutil.WithResponses(&agent.ProviderResponse{Text: "result"}))
	a, err := agent.New(prov, prompt.Text("system"), nil, agent.WithName("test-agent"))
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	permanentErr := &smithyhttp.ResponseError{
		Response: &smithyhttp.Response{
			Response: &http.Response{StatusCode: 400},
		},
		Err: errors.New("bad request"),
	}

	client := &capturingClient{
		results: []invokeResult{
			{err: permanentErr}, // permanent error — no retry
		},
	}

	logger := &capturingLoggingHook{}
	rt := &Runtime{
		agent:    a,
		client:   client,
		cfg:      defaultRuntimeConfig(),
		sessions: make(map[string]*sync.Mutex),
	}
	rt.cfg.streaming = false
	a.SetLoggingHook(logger)

	ev := incomingEvent{
		EventID:   "evt-perm",
		SessionID: "sess-perm",
		Message:   "hello",
	}

	rt.processEvent(context.Background(), ev)

	// Should have submitted only once (no retry for permanent errors).
	payloads := client.getPayloads()
	if len(payloads) != 1 {
		t.Errorf("expected exactly 1 submission attempt for permanent error, got %d", len(payloads))
	}

	// Verify permanent error was logged.
	logs := logger.getLogs()
	permLogged := false
	for _, l := range logs {
		if containsStr(l.msg, "permanent error") {
			permLogged = true
			break
		}
	}
	if !permLogged {
		t.Error("expected permanent error log message")
	}
}

func TestProcessEvent_StreamingSuccess(t *testing.T) {
	// Agent streams "hello world" as two chunks — processEvent should submit each
	// chunk incrementally as streamChunk payloads, followed by a final chunk.
	prov := testutil.NewMockProvider(
		testutil.WithResponses(&agent.ProviderResponse{Text: "hello world"}),
		testutil.WithStreamWords(),
	)
	a, err := agent.New(prov, prompt.Text("system"), nil, agent.WithName("test-agent"))
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	client := &capturingClient{
		results: []invokeResult{
			{output: &bedrockagentcore.InvokeAgentRuntimeOutput{}}, // chunk 0
			{output: &bedrockagentcore.InvokeAgentRuntimeOutput{}}, // chunk 1
			{output: &bedrockagentcore.InvokeAgentRuntimeOutput{}}, // final chunk
		},
	}

	rt := &Runtime{
		agent:    a,
		client:   client,
		cfg:      defaultRuntimeConfig(),
		sessions: make(map[string]*sync.Mutex),
	}
	rt.cfg.streaming = true

	ev := incomingEvent{
		EventID:   "evt-stream",
		SessionID: "sess-stream",
		Message:   "hi",
	}

	rt.processEvent(context.Background(), ev)

	payloads := client.getPayloads()
	if len(payloads) < 2 {
		t.Fatalf("expected at least 2 payloads (chunks + final), got %d", len(payloads))
	}

	// Verify chunks are submitted in order with correct indices.
	var chunks []streamChunk
	for _, p := range payloads {
		var sc streamChunk
		if json.Unmarshal(p, &sc) == nil && sc.EventID == "evt-stream" {
			chunks = append(chunks, sc)
		}
	}

	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 stream chunks, got %d", len(chunks))
	}

	// Verify ordering: indices should be sequential.
	for i, sc := range chunks {
		if sc.Index != i {
			t.Errorf("chunk %d: expected Index=%d, got %d", i, i, sc.Index)
		}
		if sc.EventID != "evt-stream" {
			t.Errorf("chunk %d: expected EventID 'evt-stream', got %q", i, sc.EventID)
		}
	}

	// The last chunk should be the final marker.
	lastChunk := chunks[len(chunks)-1]
	if !lastChunk.Final {
		t.Error("expected last chunk to have Final=true")
	}
	if lastChunk.Chunk != "" {
		t.Errorf("expected final chunk to have empty Chunk, got %q", lastChunk.Chunk)
	}

	// Non-final chunks should have content and Final=false.
	for i := 0; i < len(chunks)-1; i++ {
		if chunks[i].Final {
			t.Errorf("chunk %d: expected Final=false for non-final chunk", i)
		}
		if chunks[i].Chunk == "" {
			t.Errorf("chunk %d: expected non-empty Chunk content", i)
		}
	}

	// Verify the concatenation of non-final chunks equals the full response.
	var concatenated string
	for i := 0; i < len(chunks)-1; i++ {
		concatenated += chunks[i].Chunk
	}
	if concatenated != "hello world" {
		t.Errorf("expected concatenated chunks to be 'hello world', got %q", concatenated)
	}
}

func TestProcessEvent_StreamingFallbackOnChunkFailure(t *testing.T) {
	// Agent streams "hello world" as two chunks. The first chunk submission fails,
	// triggering the fallback path that concatenates all chunks and submits as a
	// complete eventResponse.
	prov := testutil.NewMockProvider(
		testutil.WithResponses(&agent.ProviderResponse{Text: "hello world"}),
		testutil.WithStreamWords(),
	)
	a, err := agent.New(prov, prompt.Text("system"), nil, agent.WithName("test-agent"))
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	transientErr := &smithyhttp.ResponseError{
		Response: &smithyhttp.Response{
			Response: &http.Response{StatusCode: 503},
		},
		Err: errors.New("service unavailable"),
	}

	client := &capturingClient{
		results: []invokeResult{
			// First chunk submission: all retries fail (initial + 3 retries = 4 attempts).
			{err: transientErr},
			{err: transientErr},
			{err: transientErr},
			{err: transientErr},
			// Fallback complete response submission succeeds.
			{output: &bedrockagentcore.InvokeAgentRuntimeOutput{}},
		},
	}

	logger := &capturingLoggingHook{}
	rt := &Runtime{
		agent:    a,
		client:   client,
		cfg:      defaultRuntimeConfig(),
		sessions: make(map[string]*sync.Mutex),
	}
	rt.cfg.streaming = true
	a.SetLoggingHook(logger)

	ev := incomingEvent{
		EventID:   "evt-fallback",
		SessionID: "sess-fallback",
		Message:   "hi",
	}

	rt.processEvent(context.Background(), ev)

	payloads := client.getPayloads()

	// Find the fallback eventResponse payload.
	var foundFallback bool
	for _, p := range payloads {
		var resp eventResponse
		if json.Unmarshal(p, &resp) == nil && resp.EventID == "evt-fallback" && resp.Response != "" {
			foundFallback = true
			if resp.Response != "hello world" {
				t.Errorf("expected fallback response 'hello world', got %q", resp.Response)
			}
			if resp.IsError {
				t.Error("expected IsError to be false for fallback response")
			}
			break
		}
	}
	if !foundFallback {
		t.Error("expected to find fallback eventResponse in captured payloads")
	}

	// Verify the chunk failure was logged.
	logs := logger.getLogs()
	failLogged := false
	for _, l := range logs {
		if containsStr(l.msg, "stream chunk submission failed") {
			failLogged = true
			break
		}
	}
	if !failLogged {
		t.Error("expected 'stream chunk submission failed' log message")
	}
}

func TestProcessEvent_SubmissionRetriesExhausted(t *testing.T) {
	// Agent succeeds, but all submission attempts fail with transient errors.
	prov := testutil.NewMockProvider(testutil.WithResponses(&agent.ProviderResponse{Text: "result"}))
	a, err := agent.New(prov, prompt.Text("system"), nil, agent.WithName("test-agent"))
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	transientErr := &smithyhttp.ResponseError{
		Response: &smithyhttp.Response{
			Response: &http.Response{StatusCode: 503},
		},
		Err: errors.New("service unavailable"),
	}

	// All 4 attempts (initial + 3 retries) fail.
	client := &capturingClient{
		results: []invokeResult{
			{err: transientErr},
			{err: transientErr},
			{err: transientErr},
			{err: transientErr},
		},
	}

	logger := &capturingLoggingHook{}
	rt := &Runtime{
		agent:    a,
		client:   client,
		cfg:      defaultRuntimeConfig(),
		sessions: make(map[string]*sync.Mutex),
	}
	rt.cfg.streaming = false
	a.SetLoggingHook(logger)

	ev := incomingEvent{
		EventID:   "evt-exhaust",
		SessionID: "sess-exhaust",
		Message:   "hello",
	}

	rt.processEvent(context.Background(), ev)

	// Should have attempted 4 times (initial + 3 retries).
	payloads := client.getPayloads()
	if len(payloads) != 4 {
		t.Errorf("expected 4 submission attempts, got %d", len(payloads))
	}

	// Verify exhaustion was logged.
	logs := logger.getLogs()
	exhaustLogged := false
	for _, l := range logs {
		if containsStr(l.msg, "retries exhausted") {
			exhaustLogged = true
			break
		}
	}
	if !exhaustLogged {
		t.Error("expected 'retries exhausted' log message")
	}
}

func TestProcessEvent_ContextCancelledDuringSubmission(t *testing.T) {
	// Agent succeeds, but context is cancelled before submission can retry.
	prov := testutil.NewMockProvider(testutil.WithResponses(&agent.ProviderResponse{Text: "result"}))
	a, err := agent.New(prov, prompt.Text("system"), nil, agent.WithName("test-agent"))
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	transientErr := &smithyhttp.ResponseError{
		Response: &smithyhttp.Response{
			Response: &http.Response{StatusCode: 503},
		},
		Err: errors.New("service unavailable"),
	}

	client := &capturingClient{
		results: []invokeResult{
			{err: transientErr},
			{err: transientErr},
		},
	}

	rt := &Runtime{
		agent:    a,
		client:   client,
		cfg:      defaultRuntimeConfig(),
		sessions: make(map[string]*sync.Mutex),
	}
	rt.cfg.streaming = false

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	ev := incomingEvent{
		EventID:   "evt-cancel",
		SessionID: "sess-cancel",
		Message:   "hello",
	}

	// Should not panic or hang.
	rt.processEvent(ctx, ev)
}

// TestProcessEvent_IntegrationWithPollLoop verifies that processEvent is correctly
// called from pollLoop with a real agent invocation and response submission.
func TestProcessEvent_IntegrationWithPollLoop(t *testing.T) {
	prov := testutil.NewMockProvider(testutil.WithResponses(&agent.ProviderResponse{Text: "agent reply"}))
	a, err := agent.New(prov, prompt.Text("system"), nil, agent.WithName("test-agent"))
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	ev := incomingEvent{
		EventID:   "evt-int",
		SessionID: "sess-int",
		Message:   "user message",
	}
	evData, _ := json.Marshal(ev)

	// First call returns the event, second call returns empty (poll continues), then context cancelled.
	client := &capturingClient{
		results: []invokeResult{
			{output: &bedrockagentcore.InvokeAgentRuntimeOutput{
				Response: io.NopCloser(bytes.NewReader(evData)),
			}},
			// processEvent will call InvokeAgentRuntime to submit response
			{output: &bedrockagentcore.InvokeAgentRuntimeOutput{}},
		},
	}

	rt := &Runtime{
		agent:    a,
		client:   client,
		cfg:      defaultRuntimeConfig(),
		sessions: make(map[string]*sync.Mutex),
	}
	rt.cfg.streaming = false

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- rt.pollLoop(ctx, ctx)
	}()

	// Wait for event processing.
	// Give enough time for the poll to receive the event and process it.
	<-waitForPayloads(client, 2, 2000)
	cancel()

	err = <-done
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	// Verify the response was submitted.
	payloads := client.getPayloads()
	// First payload is the poll request (no payload), second is the response submission.
	// Actually, the poll uses InvokeAgentRuntime too, so we need to find the response payload.
	var foundResponse bool
	for _, p := range payloads {
		var resp eventResponse
		if json.Unmarshal(p, &resp) == nil && resp.EventID == "evt-int" {
			foundResponse = true
			if resp.Response != "agent reply" {
				t.Errorf("expected response 'agent reply', got %q", resp.Response)
			}
			if resp.IsError {
				t.Error("expected IsError to be false")
			}
			break
		}
	}
	if !foundResponse {
		t.Error("expected to find response submission in captured payloads")
	}
}

// waitForPayloads waits until the client has at least n payloads or timeout (ms).
func waitForPayloads(c *capturingClient, n int, timeoutMs int) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
		for time.Now().Before(deadline) {
			c.mu.Lock()
			count := len(c.payloads)
			c.mu.Unlock()
			if count >= n {
				close(ch)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		close(ch)
	}()
	return ch
}
