package cloudwatch

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"

	agent "github.com/camilbinas/gude-agents/agent"
	"pgregory.net/rapid"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// bufferSnapshot returns a copy of the hook's internal buffer under the lock.
// This allows PBT tests to inspect buffered data points without flushing.
func bufferSnapshot(h *cloudwatchHook) []cwtypes.MetricDatum {
	h.mu.Lock()
	defer h.mu.Unlock()
	cp := make([]cwtypes.MetricDatum, len(h.buffer))
	copy(cp, h.buffer)
	return cp
}

// ---------------------------------------------------------------------------
// Property 6: CloudWatch invoke counter correctness with status mapping
// ---------------------------------------------------------------------------

// Feature: metrics-exporters, Property 6: CloudWatch invoke counter correctness with status mapping
//
// TestProperty_CWInvokeCounterCorrectness verifies that for any sequence of
// N invocations with random success/error outcomes, the CloudWatch buffer
// contains exactly N AgentInvokeTotal counter data points, and the Status
// dimension on each is "success" when the error was nil and "error" when
// non-nil.
//
// **Validates: Requirements 10.1, 12.3**
func TestProperty_CWInvokeCounterCorrectness(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		mock := &mockCWClient{}
		hook := newTestHook(mock)

		n := rapid.IntRange(1, 50).Draw(rt, "n")

		// Track expected counts per status.
		var expectedSuccess, expectedError int

		// Record the expected status for each invocation in order.
		expectedStatuses := make([]string, 0, n)

		for i := range n {
			isErr := rapid.Bool().Draw(rt, fmt.Sprintf("err_%d", i))
			finish := hook.OnInvokeStart()

			var err error
			if isErr {
				err = errors.New("fail")
				expectedError++
				expectedStatuses = append(expectedStatuses, "error")
			} else {
				expectedSuccess++
				expectedStatuses = append(expectedStatuses, "success")
			}
			finish(err, agent.TokenUsage{})
		}

		// Inspect the buffer directly (no flush).
		buf := bufferSnapshot(hook)

		// Filter for AgentInvokeTotal data points.
		invokeTotals := datumsByName(buf, "AgentInvokeTotal")

		// Verify count: exactly N AgentInvokeTotal data points.
		if len(invokeTotals) != n {
			rt.Fatalf("AgentInvokeTotal count: got %d, want %d", len(invokeTotals), n)
		}

		// Verify each data point has the correct Status dimension and value.
		var gotSuccess, gotError int
		for i, d := range invokeTotals {
			status := dimValue(d, "Status")
			wantStatus := expectedStatuses[i]
			if status != wantStatus {
				rt.Fatalf("AgentInvokeTotal[%d] Status: got %q, want %q", i, status, wantStatus)
			}

			// Each counter datum should have Value == 1.
			if aws.ToFloat64(d.Value) != 1 {
				rt.Fatalf("AgentInvokeTotal[%d] Value: got %v, want 1", i, aws.ToFloat64(d.Value))
			}

			switch status {
			case "success":
				gotSuccess++
			case "error":
				gotError++
			default:
				rt.Fatalf("AgentInvokeTotal[%d] unexpected Status: %q", i, status)
			}
		}

		// Verify aggregate counts match expected.
		if gotSuccess != expectedSuccess {
			rt.Fatalf("success count: got %d, want %d", gotSuccess, expectedSuccess)
		}
		if gotError != expectedError {
			rt.Fatalf("error count: got %d, want %d", gotError, expectedError)
		}
		if gotSuccess+gotError != n {
			rt.Fatalf("total count: got %d, want %d", gotSuccess+gotError, n)
		}
	})
}

// ---------------------------------------------------------------------------
// Property 7: CloudWatch provider metrics with model ID fallback
// ---------------------------------------------------------------------------

// Feature: metrics-exporters, Property 7: CloudWatch provider metrics with model ID fallback
//
// TestProperty_CWProviderMetricsModelIDFallback verifies that for any sequence
// of provider call completions with random TokenUsage values (non-negative
// InputTokens and OutputTokens), random model ID strings (including empty),
// and random success/error outcomes:
//   - The buffer contains the correct number of AgentProviderCallTotal data points
//     with correct ModelId and Status dimension values
//   - The buffer contains the correct number of AgentProviderTokensTotal data points
//     with correct ModelId and Direction dimension values (only for successful calls)
//   - The ModelId dimension equals the input string when non-empty and "unknown"
//     when the input is empty
//
// **Validates: Requirements 10.2, 10.3, 12.1**
func TestProperty_CWProviderMetricsModelIDFallback(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		mock := &mockCWClient{}
		hook := newTestHook(mock)

		n := rapid.IntRange(1, 30).Draw(rt, "n")

		// Track expected call counter per (effectiveModelID, status).
		type callKey struct {
			modelID string
			status  string
		}
		expectedCalls := make(map[callKey]int)

		// Track expected token sums per (effectiveModelID, direction).
		type tokenKey struct {
			modelID   string
			direction string
		}
		expectedTokens := make(map[tokenKey]float64)

		// Track the ordered sequence of expected effective model IDs and statuses
		// so we can verify each data point in order.
		type callRecord struct {
			effectiveModelID string
			status           string
			isErr            bool
			inputTokens      int
			outputTokens     int
		}
		records := make([]callRecord, 0, n)

		for i := range n {
			modelID := rapid.OneOf(
				rapid.Just(""),
				rapid.StringMatching("[a-zA-Z][a-zA-Z0-9_-]*"),
			).Draw(rt, fmt.Sprintf("model_%d", i))

			inputTokens := rapid.IntRange(0, 1000).Draw(rt, fmt.Sprintf("input_tokens_%d", i))
			outputTokens := rapid.IntRange(0, 1000).Draw(rt, fmt.Sprintf("output_tokens_%d", i))
			isErr := rapid.Bool().Draw(rt, fmt.Sprintf("err_%d", i))

			finish := hook.OnProviderCallStart(modelID)

			effectiveModelID := modelID
			if effectiveModelID == "" {
				effectiveModelID = "unknown"
			}

			var err error
			status := "success"
			if isErr {
				err = errors.New("fail")
				status = "error"
			}

			usage := agent.TokenUsage{
				InputTokens:  inputTokens,
				OutputTokens: outputTokens,
			}
			finish(err, usage)

			expectedCalls[callKey{effectiveModelID, status}]++

			if err == nil {
				expectedTokens[tokenKey{effectiveModelID, "input"}] += float64(inputTokens)
				expectedTokens[tokenKey{effectiveModelID, "output"}] += float64(outputTokens)
			}

			records = append(records, callRecord{
				effectiveModelID: effectiveModelID,
				status:           status,
				isErr:            isErr,
				inputTokens:      inputTokens,
				outputTokens:     outputTokens,
			})
		}

		// Inspect the buffer directly (no flush).
		buf := bufferSnapshot(hook)

		// --- Verify AgentProviderCallTotal data points ---
		callTotals := datumsByName(buf, "AgentProviderCallTotal")
		if len(callTotals) != n {
			rt.Fatalf("AgentProviderCallTotal count: got %d, want %d", len(callTotals), n)
		}

		// Verify each call total data point has correct ModelId and Status dimensions.
		for i, d := range callTotals {
			gotModelID := dimValue(d, "ModelId")
			wantModelID := records[i].effectiveModelID
			if gotModelID != wantModelID {
				rt.Fatalf("AgentProviderCallTotal[%d] ModelId: got %q, want %q", i, gotModelID, wantModelID)
			}

			gotStatus := dimValue(d, "Status")
			wantStatus := records[i].status
			if gotStatus != wantStatus {
				rt.Fatalf("AgentProviderCallTotal[%d] Status: got %q, want %q", i, gotStatus, wantStatus)
			}

			if aws.ToFloat64(d.Value) != 1 {
				rt.Fatalf("AgentProviderCallTotal[%d] Value: got %v, want 1", i, aws.ToFloat64(d.Value))
			}
		}

		// Verify aggregate call counts per (ModelId, Status) pair.
		gotCalls := make(map[callKey]int)
		for _, d := range callTotals {
			key := callKey{dimValue(d, "ModelId"), dimValue(d, "Status")}
			gotCalls[key]++
		}
		for key, want := range expectedCalls {
			if gotCalls[key] != want {
				rt.Fatalf("AgentProviderCallTotal (ModelId=%q, Status=%q): got %d, want %d",
					key.modelID, key.status, gotCalls[key], want)
			}
		}

		// --- Verify AgentProviderTokensTotal data points ---
		tokenTotals := datumsByName(buf, "AgentProviderTokensTotal")

		// Count expected token data points: 2 per successful call (input + output).
		expectedTokenCount := 0
		for _, r := range records {
			if !r.isErr {
				expectedTokenCount += 2
			}
		}
		if len(tokenTotals) != expectedTokenCount {
			rt.Fatalf("AgentProviderTokensTotal count: got %d, want %d", len(tokenTotals), expectedTokenCount)
		}

		// Verify token data points appear in order and have correct dimensions.
		tokenIdx := 0
		for _, r := range records {
			if r.isErr {
				continue
			}
			// Input token data point.
			if tokenIdx >= len(tokenTotals) {
				rt.Fatalf("ran out of AgentProviderTokensTotal data points at token index %d", tokenIdx)
			}
			d := tokenTotals[tokenIdx]
			if dimValue(d, "ModelId") != r.effectiveModelID {
				rt.Fatalf("AgentProviderTokensTotal[%d] ModelId: got %q, want %q",
					tokenIdx, dimValue(d, "ModelId"), r.effectiveModelID)
			}
			if dimValue(d, "Direction") != "input" {
				rt.Fatalf("AgentProviderTokensTotal[%d] Direction: got %q, want %q",
					tokenIdx, dimValue(d, "Direction"), "input")
			}
			if aws.ToFloat64(d.Value) != float64(r.inputTokens) {
				rt.Fatalf("AgentProviderTokensTotal[%d] Value: got %v, want %v",
					tokenIdx, aws.ToFloat64(d.Value), float64(r.inputTokens))
			}
			tokenIdx++

			// Output token data point.
			d = tokenTotals[tokenIdx]
			if dimValue(d, "ModelId") != r.effectiveModelID {
				rt.Fatalf("AgentProviderTokensTotal[%d] ModelId: got %q, want %q",
					tokenIdx, dimValue(d, "ModelId"), r.effectiveModelID)
			}
			if dimValue(d, "Direction") != "output" {
				rt.Fatalf("AgentProviderTokensTotal[%d] Direction: got %q, want %q",
					tokenIdx, dimValue(d, "Direction"), "output")
			}
			if aws.ToFloat64(d.Value) != float64(r.outputTokens) {
				rt.Fatalf("AgentProviderTokensTotal[%d] Value: got %v, want %v",
					tokenIdx, aws.ToFloat64(d.Value), float64(r.outputTokens))
			}
			tokenIdx++
		}

		// Verify aggregate token sums per (ModelId, Direction) pair.
		gotTokens := make(map[tokenKey]float64)
		for _, d := range tokenTotals {
			key := tokenKey{dimValue(d, "ModelId"), dimValue(d, "Direction")}
			gotTokens[key] += aws.ToFloat64(d.Value)
		}
		for key, want := range expectedTokens {
			if gotTokens[key] != want {
				rt.Fatalf("AgentProviderTokensTotal sum (ModelId=%q, Direction=%q): got %v, want %v",
					key.modelID, key.direction, gotTokens[key], want)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Property 8: CloudWatch tool call counter correctness
// ---------------------------------------------------------------------------

// Feature: metrics-exporters, Property 8: CloudWatch tool call counter correctness
//
// TestProperty_CWToolCallCounterCorrectness verifies that for any sequence of
// tool executions with random tool names and random success/error outcomes,
// the CloudWatch buffer contains one AgentToolCallTotal data point per call
// with the correct ToolName and Status dimensions.
//
// **Validates: Requirements 10.4**
func TestProperty_CWToolCallCounterCorrectness(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		mock := &mockCWClient{}
		hook := newTestHook(mock)

		n := rapid.IntRange(1, 50).Draw(rt, "n")

		// Track expected counts per (ToolName, Status) pair.
		type toolKey struct {
			toolName string
			status   string
		}
		expectedCounts := make(map[toolKey]int)

		// Track the ordered sequence of expected tool names and statuses
		// so we can verify each data point in order.
		type callRecord struct {
			toolName string
			status   string
		}
		records := make([]callRecord, 0, n)

		for i := range n {
			toolName := rapid.StringMatching("[a-zA-Z][a-zA-Z0-9_-]*").Draw(rt, fmt.Sprintf("tool_%d", i))
			isErr := rapid.Bool().Draw(rt, fmt.Sprintf("err_%d", i))

			finish := hook.OnToolStart(toolName)

			var err error
			status := "success"
			if isErr {
				err = errors.New("fail")
				status = "error"
			}
			finish(err)

			expectedCounts[toolKey{toolName, status}]++
			records = append(records, callRecord{toolName: toolName, status: status})
		}

		// Inspect the buffer directly (no flush).
		buf := bufferSnapshot(hook)

		// Filter for AgentToolCallTotal data points.
		toolTotals := datumsByName(buf, "AgentToolCallTotal")

		// Verify count: exactly N AgentToolCallTotal data points.
		if len(toolTotals) != n {
			rt.Fatalf("AgentToolCallTotal count: got %d, want %d", len(toolTotals), n)
		}

		// Verify each data point has the correct ToolName and Status dimensions.
		for i, d := range toolTotals {
			gotToolName := dimValue(d, "ToolName")
			wantToolName := records[i].toolName
			if gotToolName != wantToolName {
				rt.Fatalf("AgentToolCallTotal[%d] ToolName: got %q, want %q", i, gotToolName, wantToolName)
			}

			gotStatus := dimValue(d, "Status")
			wantStatus := records[i].status
			if gotStatus != wantStatus {
				rt.Fatalf("AgentToolCallTotal[%d] Status: got %q, want %q", i, gotStatus, wantStatus)
			}

			// Each counter datum should have Value == 1.
			if aws.ToFloat64(d.Value) != 1 {
				rt.Fatalf("AgentToolCallTotal[%d] Value: got %v, want 1", i, aws.ToFloat64(d.Value))
			}
		}

		// Verify aggregate counts per (ToolName, Status) pair.
		gotCounts := make(map[toolKey]int)
		for _, d := range toolTotals {
			key := toolKey{dimValue(d, "ToolName"), dimValue(d, "Status")}
			gotCounts[key]++
		}
		for key, want := range expectedCounts {
			if gotCounts[key] != want {
				rt.Fatalf("AgentToolCallTotal (ToolName=%q, Status=%q): got %d, want %d",
					key.toolName, key.status, gotCounts[key], want)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Property 9: CloudWatch guardrail block counter selectivity
// ---------------------------------------------------------------------------

// Feature: metrics-exporters, Property 9: CloudWatch guardrail block counter selectivity
//
// TestProperty_CWGuardrailBlockSelectivity verifies that for any sequence of
// guardrail completions with random direction ("input"/"output") and random
// blocked (true/false) values, the CloudWatch buffer contains
// AgentGuardrailBlockTotal data points only for calls where blocked was true,
// with the correct Direction dimension.
//
// **Validates: Requirements 10.5**
func TestProperty_CWGuardrailBlockSelectivity(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		mock := &mockCWClient{}
		hook := newTestHook(mock)

		n := rapid.IntRange(1, 50).Draw(rt, "n")

		// Track expected counts per direction (only for blocked=true).
		expectedCounts := make(map[string]int)

		// Track the ordered sequence of expected directions for blocked calls
		// so we can verify each data point in order.
		var expectedDirections []string

		for i := range n {
			direction := rapid.SampledFrom([]string{"input", "output"}).Draw(rt, fmt.Sprintf("dir_%d", i))
			blocked := rapid.Bool().Draw(rt, fmt.Sprintf("blocked_%d", i))

			hook.OnGuardrailComplete(direction, blocked)

			if blocked {
				expectedCounts[direction]++
				expectedDirections = append(expectedDirections, direction)
			}
		}

		// Inspect the buffer directly (no flush).
		buf := bufferSnapshot(hook)

		// Filter for AgentGuardrailBlockTotal data points.
		blockTotals := datumsByName(buf, "AgentGuardrailBlockTotal")

		// The buffer should contain exactly len(expectedDirections) data points
		// (one per blocked=true call, zero for blocked=false calls).
		if len(blockTotals) != len(expectedDirections) {
			rt.Fatalf("AgentGuardrailBlockTotal count: got %d, want %d",
				len(blockTotals), len(expectedDirections))
		}

		// Verify each data point has the correct Direction dimension and value.
		for i, d := range blockTotals {
			gotDirection := dimValue(d, "Direction")
			wantDirection := expectedDirections[i]
			if gotDirection != wantDirection {
				rt.Fatalf("AgentGuardrailBlockTotal[%d] Direction: got %q, want %q",
					i, gotDirection, wantDirection)
			}

			// Each counter datum should have Value == 1.
			if aws.ToFloat64(d.Value) != 1 {
				rt.Fatalf("AgentGuardrailBlockTotal[%d] Value: got %v, want 1",
					i, aws.ToFloat64(d.Value))
			}
		}

		// Verify aggregate counts per direction match expected.
		gotCounts := make(map[string]int)
		for _, d := range blockTotals {
			key := dimValue(d, "Direction")
			gotCounts[key]++
		}
		for dir, want := range expectedCounts {
			if gotCounts[dir] != want {
				rt.Fatalf("AgentGuardrailBlockTotal (Direction=%q): got %d, want %d",
					dir, gotCounts[dir], want)
			}
		}

		// Verify no unexpected directions exist.
		for dir, got := range gotCounts {
			if _, ok := expectedCounts[dir]; !ok {
				rt.Fatalf("AgentGuardrailBlockTotal unexpected Direction=%q with count %d",
					dir, got)
			}
		}

		// Verify the buffer contains NO other metric types (only guardrail data
		// points should be present since we only called OnGuardrailComplete).
		if len(buf) != len(blockTotals) {
			rt.Fatalf("buffer contains unexpected data points: total %d, AgentGuardrailBlockTotal %d",
				len(buf), len(blockTotals))
		}
	})
}

// ---------------------------------------------------------------------------
// Property 10: CloudWatch iteration counter monotonicity
// ---------------------------------------------------------------------------

// Feature: metrics-exporters, Property 10: CloudWatch iteration counter monotonicity
//
// TestProperty_CWIterationCounterMonotonicity verifies that for any
// non-negative integer N, calling OnIterationStart exactly N times results
// in exactly N AgentIterationTotal data points in the CloudWatch buffer.
//
// **Validates: Requirements 10.6**
func TestProperty_CWIterationCounterMonotonicity(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		mock := &mockCWClient{}
		hook := newTestHook(mock)

		n := rapid.IntRange(0, 100).Draw(rt, "n")

		for range n {
			hook.OnIterationStart()
		}

		// Inspect the buffer directly (no flush).
		buf := bufferSnapshot(hook)

		// Filter for AgentIterationTotal data points.
		iterationTotals := datumsByName(buf, "AgentIterationTotal")

		// Verify count: exactly N AgentIterationTotal data points.
		if len(iterationTotals) != n {
			rt.Fatalf("AgentIterationTotal count: got %d, want %d", len(iterationTotals), n)
		}

		// Verify each data point has Value == 1.
		for i, d := range iterationTotals {
			if aws.ToFloat64(d.Value) != 1 {
				rt.Fatalf("AgentIterationTotal[%d] Value: got %v, want 1", i, aws.ToFloat64(d.Value))
			}

			// Verify unit is Count.
			if d.Unit != cwtypes.StandardUnitCount {
				rt.Fatalf("AgentIterationTotal[%d] Unit: got %v, want Count", i, d.Unit)
			}
		}

		// The buffer should contain ONLY AgentIterationTotal data points
		// since we only called OnIterationStart.
		if len(buf) != n {
			rt.Fatalf("buffer contains unexpected data points: total %d, AgentIterationTotal %d",
				len(buf), n)
		}
	})
}

// ---------------------------------------------------------------------------
// Property 11: CloudWatch batch splitting respects PutMetricData limit
// ---------------------------------------------------------------------------

// Feature: metrics-exporters, Property 11: CloudWatch batch splitting respects PutMetricData limit
//
// TestProperty_CWBatchSplitting verifies that for any buffer containing
// between 1 and 3000 metric data points, when flushed, each PutMetricData
// API call contains at most 1000 data points, and the total number of data
// points across all calls equals the original buffer size.
//
// **Validates: Requirements 13.4**
func TestProperty_CWBatchSplitting(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		mock := &mockCWClient{}
		hook := newTestHook(mock)

		n := rapid.IntRange(1, 3000).Draw(rt, "bufferSize")

		// Directly populate the hook's buffer with n counter data points.
		hook.mu.Lock()
		for i := range n {
			hook.buffer = append(hook.buffer, counterDatum(
				fmt.Sprintf("TestMetric_%d", i), 1,
			))
		}
		hook.mu.Unlock()

		// Flush the buffer through the mock client.
		hook.flush(context.Background())

		// Inspect the mock's recorded PutMetricData calls.
		mock.mu.Lock()
		defer mock.mu.Unlock()

		totalDataPoints := 0
		for i, call := range mock.calls {
			batchSize := len(call.MetricData)

			// Each PutMetricData call must have ≤ maxDataPerPut (1000) data points.
			if batchSize > maxDataPerPut {
				rt.Fatalf("PutMetricData call %d has %d data points, exceeds limit of %d",
					i, batchSize, maxDataPerPut)
			}

			// Each batch must be non-empty.
			if batchSize == 0 {
				rt.Fatalf("PutMetricData call %d has 0 data points", i)
			}

			totalDataPoints += batchSize
		}

		// Total data points across all calls must equal the original buffer size.
		if totalDataPoints != n {
			rt.Fatalf("total data points across all calls: got %d, want %d",
				totalDataPoints, n)
		}

		// Verify the expected number of API calls: ceil(n / maxDataPerPut).
		expectedCalls := (n + maxDataPerPut - 1) / maxDataPerPut
		if len(mock.calls) != expectedCalls {
			rt.Fatalf("PutMetricData call count: got %d, want %d",
				len(mock.calls), expectedCalls)
		}
	})
}
