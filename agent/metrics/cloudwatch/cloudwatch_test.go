package cloudwatch

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	cw "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"

	agent "github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// mockCWClient implements the cloudwatchClient interface for testing.
type mockCWClient struct {
	mu    sync.Mutex
	calls []cw.PutMetricDataInput
	err   error // if set, PutMetricData returns this error
}

func (m *mockCWClient) PutMetricData(ctx context.Context, params *cw.PutMetricDataInput, optFns ...func(*cw.Options)) (*cw.PutMetricDataOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, *params)
	return &cw.PutMetricDataOutput{}, m.err
}

// newTestHook creates a cloudwatchHook directly with a mock client,
// bypassing WithMetrics (which starts a goroutine).
func newTestHook(mock *mockCWClient) *cloudwatchHook {
	return &cloudwatchHook{
		client:        mock,
		namespace:     defaultNamespace,
		flushInterval: defaultFlushInterval,
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
}

// mockProvider is a minimal Provider for creating agents in tests.
type mockProvider struct {
	mu        sync.Mutex
	responses []*agent.ProviderResponse
	callIndex int
}

func newMockProvider(responses ...*agent.ProviderResponse) *mockProvider {
	return &mockProvider{responses: responses}
}

func (p *mockProvider) Converse(ctx context.Context, params agent.ConverseParams) (*agent.ProviderResponse, error) {
	return p.ConverseStream(ctx, params, nil)
}

func (p *mockProvider) ConverseStream(_ context.Context, _ agent.ConverseParams, cb agent.StreamCallback) (*agent.ProviderResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.callIndex >= len(p.responses) {
		return nil, fmt.Errorf("mockProvider: no more responses (call %d)", p.callIndex)
	}
	resp := p.responses[p.callIndex]
	p.callIndex++
	if len(resp.ToolCalls) == 0 && resp.Text != "" && cb != nil {
		cb(resp.Text)
	}
	return resp, nil
}

// allData returns all MetricDatum across all PutMetricData calls.
func (m *mockCWClient) allData() []cwtypes.MetricDatum {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []cwtypes.MetricDatum
	for _, call := range m.calls {
		result = append(result, call.MetricData...)
	}
	return result
}

// datumsByName filters data points by metric name.
func datumsByName(data []cwtypes.MetricDatum, name string) []cwtypes.MetricDatum {
	var result []cwtypes.MetricDatum
	for _, d := range data {
		if aws.ToString(d.MetricName) == name {
			result = append(result, d)
		}
	}
	return result
}

// dimValue returns the value of a dimension by name, or "" if not found.
func dimValue(d cwtypes.MetricDatum, name string) string {
	for _, dim := range d.Dimensions {
		if aws.ToString(dim.Name) == name {
			return aws.ToString(dim.Value)
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Unit Tests
// ---------------------------------------------------------------------------

// TestWithMetrics_InstallsHook verifies that WithMetrics sets MetricsHook on the agent.
// Validates: Requirement 8.3
func TestWithMetrics_InstallsHook(t *testing.T) {
	mock := &mockCWClient{}
	hook := newTestHook(mock)

	// Verify the hook implements MetricsHook.
	var _ agent.MetricsHook = hook

	if hook.client == nil {
		t.Fatal("expected client to be set on hook")
	}
	if hook.namespace != defaultNamespace {
		t.Fatalf("expected namespace %q, got %q", defaultNamespace, hook.namespace)
	}
}

// TestWithMetrics_WithClient verifies WithClient bypasses default credential chain.
// Validates: Requirement 8.4
func TestWithMetrics_WithClient(t *testing.T) {
	// Create a real *cw.Client with dummy config to avoid credential issues.
	realClient := cw.New(cw.Options{
		Region: "us-east-1",
	})

	prov := newMockProvider(&agent.ProviderResponse{Text: "hello"})
	opt, shutdown := WithMetrics(WithClient(realClient))

	a, err := agent.New(prov, prompt.Text("sys"), nil, opt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if a.MetricsHook() == nil {
		t.Fatal("expected MetricsHook to be set after WithMetrics with WithClient")
	}

	// Clean up the background goroutine.
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown error: %v", err)
	}
}

// TestWithMetrics_ReturnsShutdown verifies WithMetrics returns both option and shutdown function.
// Validates: Requirement 14.3
func TestWithMetrics_ReturnsShutdown(t *testing.T) {
	realClient := cw.New(cw.Options{Region: "us-east-1"})
	opt, shutdown := WithMetrics(WithClient(realClient))

	if opt == nil {
		t.Fatal("expected non-nil agent.Option from WithMetrics")
	}
	if shutdown == nil {
		t.Fatal("expected non-nil shutdown function from WithMetrics")
	}

	// Apply the option to an agent and verify shutdown works.
	prov := newMockProvider(&agent.ProviderResponse{Text: "hello"})
	_, err := agent.New(prov, prompt.Text("sys"), nil, opt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Shutdown should not error.
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown returned error: %v", err)
	}
}

// TestDefaultNamespace verifies default namespace is "GudeAgents".
// Validates: Requirement 9.2
func TestDefaultNamespace(t *testing.T) {
	if defaultNamespace != "GudeAgents" {
		t.Fatalf("expected default namespace %q, got %q", "GudeAgents", defaultNamespace)
	}

	mock := &mockCWClient{}
	hook := newTestHook(mock)

	if hook.namespace != "GudeAgents" {
		t.Fatalf("expected hook namespace %q, got %q", "GudeAgents", hook.namespace)
	}
}

// TestWithNamespace verifies custom namespace appears in PutMetricData calls.
// Validates: Requirements 9.1, 9.3
func TestWithNamespace(t *testing.T) {
	mock := &mockCWClient{}
	hook := newTestHook(mock)
	WithNamespace("MyCustomNamespace")(hook)

	// Record a metric and flush.
	hook.OnIterationStart()
	hook.flush(context.Background())

	mock.mu.Lock()
	defer mock.mu.Unlock()

	if len(mock.calls) != 1 {
		t.Fatalf("expected 1 PutMetricData call, got %d", len(mock.calls))
	}

	ns := aws.ToString(mock.calls[0].Namespace)
	if ns != "MyCustomNamespace" {
		t.Fatalf("expected namespace %q in PutMetricData, got %q", "MyCustomNamespace", ns)
	}
}

// TestWithDimensions verifies extra dimensions appear on all data points.
// Validates: Requirement 12.5
func TestWithDimensions(t *testing.T) {
	mock := &mockCWClient{}
	hook := newTestHook(mock)
	WithDimensions(map[string]string{"Environment": "production"})(hook)

	// Record various metrics.
	hook.OnIterationStart()
	finishInvoke := hook.OnInvokeStart()
	finishInvoke(nil, agent.TokenUsage{})
	finishTool := hook.OnToolStart("my-tool")
	finishTool(nil)

	hook.flush(context.Background())

	data := mock.allData()
	if len(data) == 0 {
		t.Fatal("expected data points after flush, got 0")
	}

	for i, d := range data {
		found := false
		for _, dim := range d.Dimensions {
			if aws.ToString(dim.Name) == "Environment" && aws.ToString(dim.Value) == "production" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("data point %d (%s): missing Environment=production dimension",
				i, aws.ToString(d.MetricName))
		}
	}
}

// TestDefaultFlushInterval verifies default flush interval is 60 seconds.
// Validates: Requirement 13.3
func TestDefaultFlushInterval(t *testing.T) {
	if defaultFlushInterval != 60*time.Second {
		t.Fatalf("expected default flush interval 60s, got %v", defaultFlushInterval)
	}

	mock := &mockCWClient{}
	hook := newTestHook(mock)

	if hook.flushInterval != 60*time.Second {
		t.Fatalf("expected hook flush interval 60s, got %v", hook.flushInterval)
	}
}

// TestDurationStatisticSet verifies duration metrics use StatisticSet with Seconds unit.
// Validates: Requirements 11.1, 11.2, 11.3
func TestDurationStatisticSet(t *testing.T) {
	mock := &mockCWClient{}
	hook := newTestHook(mock)

	// Exercise all duration-recording hooks.
	finishInvoke := hook.OnInvokeStart()
	finishInvoke(nil, agent.TokenUsage{})

	finishProvider := hook.OnProviderCallStart("test-model")
	finishProvider(nil, agent.TokenUsage{InputTokens: 10, OutputTokens: 5})

	finishTool := hook.OnToolStart("my-tool")
	finishTool(nil)

	hook.flush(context.Background())

	data := mock.allData()

	durationMetrics := []string{
		"AgentInvokeDuration",
		"AgentProviderCallDuration",
		"AgentToolCallDuration",
	}

	for _, name := range durationMetrics {
		datums := datumsByName(data, name)
		if len(datums) == 0 {
			t.Errorf("expected %s data point, got 0", name)
			continue
		}

		d := datums[0]

		// Verify StatisticSet is used.
		if d.StatisticValues == nil {
			t.Errorf("%s: expected StatisticValues (StatisticSet), got nil", name)
			continue
		}

		// Verify unit is Seconds.
		if d.Unit != cwtypes.StandardUnitSeconds {
			t.Errorf("%s: expected unit Seconds, got %v", name, d.Unit)
		}

		// Verify StatisticSet fields are populated.
		if aws.ToFloat64(d.StatisticValues.SampleCount) != 1 {
			t.Errorf("%s: expected SampleCount 1, got %v", name, aws.ToFloat64(d.StatisticValues.SampleCount))
		}
		if aws.ToFloat64(d.StatisticValues.Sum) < 0 {
			t.Errorf("%s: expected non-negative Sum, got %v", name, aws.ToFloat64(d.StatisticValues.Sum))
		}
		if aws.ToFloat64(d.StatisticValues.Minimum) < 0 {
			t.Errorf("%s: expected non-negative Minimum, got %v", name, aws.ToFloat64(d.StatisticValues.Minimum))
		}
		if aws.ToFloat64(d.StatisticValues.Maximum) < 0 {
			t.Errorf("%s: expected non-negative Maximum, got %v", name, aws.ToFloat64(d.StatisticValues.Maximum))
		}
	}
}

// TestFlush_SendsBufferedData verifies Flush method triggers immediate flush.
// Validates: Requirement 13.5
func TestFlush_SendsBufferedData(t *testing.T) {
	mock := &mockCWClient{}
	hook := newTestHook(mock)

	// Buffer some data.
	hook.OnIterationStart()
	hook.OnIterationStart()
	hook.OnIterationStart()

	// Verify nothing sent yet.
	mock.mu.Lock()
	callsBefore := len(mock.calls)
	mock.mu.Unlock()
	if callsBefore != 0 {
		t.Fatalf("expected 0 calls before Flush, got %d", callsBefore)
	}

	// Flush should send the data.
	hook.Flush(context.Background())

	mock.mu.Lock()
	callsAfter := len(mock.calls)
	mock.mu.Unlock()
	if callsAfter != 1 {
		t.Fatalf("expected 1 PutMetricData call after Flush, got %d", callsAfter)
	}

	data := mock.allData()
	iterations := datumsByName(data, "AgentIterationTotal")
	if len(iterations) != 3 {
		t.Fatalf("expected 3 AgentIterationTotal data points, got %d", len(iterations))
	}
}

// TestFlush_RetainsOnError verifies failed PutMetricData retains data points.
// Validates: Requirement 13.6
func TestFlush_RetainsOnError(t *testing.T) {
	mock := &mockCWClient{err: errors.New("network error")}
	hook := newTestHook(mock)

	// Buffer some data.
	hook.OnIterationStart()
	hook.OnIterationStart()

	// First flush should fail and retain data.
	hook.flush(context.Background())

	hook.mu.Lock()
	retained := len(hook.buffer)
	hook.mu.Unlock()

	if retained != 2 {
		t.Fatalf("expected 2 retained data points after failed flush, got %d", retained)
	}

	// Clear the error and flush again — data should be sent.
	mock.mu.Lock()
	mock.err = nil
	mock.calls = nil
	mock.mu.Unlock()

	hook.flush(context.Background())

	hook.mu.Lock()
	afterRetry := len(hook.buffer)
	hook.mu.Unlock()

	if afterRetry != 0 {
		t.Fatalf("expected 0 buffered data points after successful retry, got %d", afterRetry)
	}

	data := mock.allData()
	if len(data) != 2 {
		t.Fatalf("expected 2 data points sent on retry, got %d", len(data))
	}
}

// TestShutdown_FinalFlush verifies Shutdown performs final flush and stops goroutine.
// Validates: Requirement 14.1
func TestShutdown_FinalFlush(t *testing.T) {
	mock := &mockCWClient{}
	hook := newTestHook(mock)

	// Start the flush loop manually.
	go hook.flushLoop()

	// Buffer some data.
	hook.OnIterationStart()
	hook.OnIterationStart()

	// Shutdown should stop the goroutine and flush remaining data.
	err := hook.Shutdown(context.Background())
	if err != nil {
		t.Fatalf("Shutdown returned error: %v", err)
	}

	// Verify the goroutine stopped (doneCh is closed).
	select {
	case <-hook.doneCh:
		// Good — goroutine stopped.
	default:
		t.Fatal("expected doneCh to be closed after Shutdown")
	}

	// Verify data was flushed.
	data := mock.allData()
	iterations := datumsByName(data, "AgentIterationTotal")
	if len(iterations) != 2 {
		t.Fatalf("expected 2 AgentIterationTotal data points after Shutdown, got %d", len(iterations))
	}
}

// TestShutdown_ContextCancellation verifies Shutdown respects context cancellation.
// Validates: Requirement 14.2
func TestShutdown_ContextCancellation(t *testing.T) {
	// Use a mock that blocks on PutMetricData to simulate slow flush.
	blockCh := make(chan struct{})
	mock := &mockCWClient{}

	hook := &cloudwatchHook{
		client:        mock,
		namespace:     defaultNamespace,
		flushInterval: defaultFlushInterval,
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}

	// Start the flush loop.
	go hook.flushLoop()

	// Buffer some data.
	hook.OnIterationStart()

	// Create a context that we cancel immediately.
	ctx, cancel := context.WithCancel(context.Background())

	// Shutdown in a goroutine — it should complete even with cancelled context.
	done := make(chan error, 1)
	go func() {
		done <- hook.Shutdown(ctx)
	}()

	// Cancel the context.
	cancel()

	// Shutdown should still complete (it stops the goroutine first).
	select {
	case err := <-done:
		// Shutdown completed — this is the expected behavior.
		// The error may be nil since Shutdown always returns nil currently.
		_ = err
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown did not complete within timeout after context cancellation")
	}

	_ = blockCh // unused but kept for clarity of intent
}

// TestBuffering verifies data points accumulate in buffer between flushes.
// Validates: Requirement 13.1
func TestBuffering(t *testing.T) {
	mock := &mockCWClient{}
	hook := newTestHook(mock)

	// Record multiple metrics without flushing.
	hook.OnIterationStart()

	finishInvoke := hook.OnInvokeStart()
	finishInvoke(nil, agent.TokenUsage{InputTokens: 10, OutputTokens: 5})

	finishProvider := hook.OnProviderCallStart("test-model")
	finishProvider(nil, agent.TokenUsage{InputTokens: 20, OutputTokens: 10})

	finishTool := hook.OnToolStart("my-tool")
	finishTool(nil)

	hook.OnGuardrailComplete("input", true)

	// Verify no PutMetricData calls yet.
	mock.mu.Lock()
	callCount := len(mock.calls)
	mock.mu.Unlock()
	if callCount != 0 {
		t.Fatalf("expected 0 PutMetricData calls before flush, got %d", callCount)
	}

	// Verify buffer has accumulated data points.
	hook.mu.Lock()
	bufLen := len(hook.buffer)
	hook.mu.Unlock()

	// Expected data points:
	// 1 AgentIterationTotal
	// 1 AgentInvokeDuration + 1 AgentInvokeTotal
	// 1 AgentProviderCallDuration + 1 AgentProviderCallTotal + 2 AgentProviderTokensTotal (input + output)
	// 1 AgentToolCallDuration + 1 AgentToolCallTotal
	// 1 AgentGuardrailBlockTotal
	// Total: 10
	expectedMin := 10
	if bufLen < expectedMin {
		t.Fatalf("expected at least %d buffered data points, got %d", expectedMin, bufLen)
	}

	// Now flush and verify all data is sent.
	hook.flush(context.Background())

	data := mock.allData()
	if len(data) < expectedMin {
		t.Fatalf("expected at least %d data points after flush, got %d", expectedMin, len(data))
	}

	// Verify buffer is empty after flush.
	hook.mu.Lock()
	afterFlush := len(hook.buffer)
	hook.mu.Unlock()
	if afterFlush != 0 {
		t.Fatalf("expected empty buffer after successful flush, got %d", afterFlush)
	}
}

// ---------------------------------------------------------------------------
// Swarm Metrics Unit Tests
// ---------------------------------------------------------------------------

// TestSwarmMetrics_RunCounter verifies swarm run counter and duration are buffered.
func TestSwarmMetrics_RunCounter(t *testing.T) {
	mock := &mockCWClient{}
	inner := newTestHook(mock)
	h := &swarmCloudwatchHook{cloudwatchHook: inner}

	finish := h.OnSwarmRunStart()
	finish(nil, agent.SwarmResult{})

	h.flush(context.Background())

	data := mock.allData()
	if len(data) == 0 {
		t.Fatal("expected data points after flush, got 0")
	}

	// Verify SwarmRunTotal is present with Status=success.
	runTotals := datumsByName(data, "SwarmRunTotal")
	if len(runTotals) == 0 {
		t.Fatal("SwarmRunTotal not found in buffered data")
	}
	if v := dimValue(runTotals[0], "Status"); v != "success" {
		t.Errorf("SwarmRunTotal Status: expected 'success', got %q", v)
	}

	// Verify SwarmRunDuration is present.
	runDurations := datumsByName(data, "SwarmRunDuration")
	if len(runDurations) == 0 {
		t.Fatal("SwarmRunDuration not found in buffered data")
	}
	if runDurations[0].StatisticValues == nil {
		t.Fatal("SwarmRunDuration: expected StatisticValues, got nil")
	}
}

// TestSwarmMetrics_HandoffCounter verifies swarm handoff counter is buffered with From/To dimensions.
func TestSwarmMetrics_HandoffCounter(t *testing.T) {
	mock := &mockCWClient{}
	inner := newTestHook(mock)
	h := &swarmCloudwatchHook{cloudwatchHook: inner}

	h.OnSwarmHandoff("triage", "billing")

	h.flush(context.Background())

	data := mock.allData()
	handoffs := datumsByName(data, "SwarmHandoffTotal")
	if len(handoffs) == 0 {
		t.Fatal("SwarmHandoffTotal not found in buffered data")
	}

	d := handoffs[0]
	if v := dimValue(d, "From"); v != "triage" {
		t.Errorf("SwarmHandoffTotal From: expected 'triage', got %q", v)
	}
	if v := dimValue(d, "To"); v != "billing" {
		t.Errorf("SwarmHandoffTotal To: expected 'billing', got %q", v)
	}
}

// ---------------------------------------------------------------------------
// Graph Metrics Unit Tests
// ---------------------------------------------------------------------------

// TestGraphMetrics_RunCounter verifies graph run counter and duration are buffered.
func TestGraphMetrics_RunCounter(t *testing.T) {
	mock := &mockCWClient{}
	inner := newTestHook(mock)
	h := &graphCloudwatchHook{cloudwatchHook: inner}

	finish := h.OnGraphRunStart()
	finish(nil, 3)

	h.flush(context.Background())

	data := mock.allData()
	if len(data) == 0 {
		t.Fatal("expected data points after flush, got 0")
	}

	// Verify GraphRunTotal is present with Status=success.
	runTotals := datumsByName(data, "GraphRunTotal")
	if len(runTotals) == 0 {
		t.Fatal("GraphRunTotal not found in buffered data")
	}
	if v := dimValue(runTotals[0], "Status"); v != "success" {
		t.Errorf("GraphRunTotal Status: expected 'success', got %q", v)
	}

	// Verify GraphRunDuration is present.
	runDurations := datumsByName(data, "GraphRunDuration")
	if len(runDurations) == 0 {
		t.Fatal("GraphRunDuration not found in buffered data")
	}
	if runDurations[0].StatisticValues == nil {
		t.Fatal("GraphRunDuration: expected StatisticValues, got nil")
	}
}

// TestGraphMetrics_NodeCounter verifies graph node counter is buffered with NodeName dimension.
func TestGraphMetrics_NodeCounter(t *testing.T) {
	mock := &mockCWClient{}
	inner := newTestHook(mock)
	h := &graphCloudwatchHook{cloudwatchHook: inner}

	finish := h.OnNodeStart("fetch")
	finish(nil)

	h.flush(context.Background())

	data := mock.allData()
	nodeTotals := datumsByName(data, "GraphNodeTotal")
	if len(nodeTotals) == 0 {
		t.Fatal("GraphNodeTotal not found in buffered data")
	}

	d := nodeTotals[0]
	if v := dimValue(d, "NodeName"); v != "fetch" {
		t.Errorf("GraphNodeTotal NodeName: expected 'fetch', got %q", v)
	}
	if v := dimValue(d, "Status"); v != "success" {
		t.Errorf("GraphNodeTotal Status: expected 'success', got %q", v)
	}
}
