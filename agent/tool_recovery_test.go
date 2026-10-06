package agent

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/camilbinas/gude-agents/agent/tool"
)

func seedInFlightToolExecution(t *testing.T, conversations *testMemoryStore, executions *testExecutionStore, replaySafe bool) Execution {
	t.Helper()
	cursor, err := conversations.Append(context.Background(), "conversation", []Message{{
		Role:    RoleAssistant,
		Content: []ContentBlock{ToolUseBlock{ToolUseID: "call-1", Name: "charge", Input: json.RawMessage(`{"amount":7}`)}},
	}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	execution, err := executions.Create(context.Background(), Execution{
		ID: "execution-1", ConversationID: "conversation", Revision: cursor.Revision, LastSequence: cursor.LastSequence,
		Status: ExecutionRunning, Phase: ExecutionPhaseTools, Iteration: 1,
		ToolBatch: &ToolBatchExecution{Iteration: 1, ToolUseRevision: cursor.Revision, ToolUseLastSequence: cursor.LastSequence, Calls: []ToolExecution{{
			CallID: "call-1", Name: "charge", Status: ToolExecutionInFlight,
			IdempotencyKey: toolExecutionKey("execution-1", "call-1"), InputHash: toolInputHash(json.RawMessage(`{"amount":7}`)), ReplaySafe: replaySafe,
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return execution
}

func TestRecoverExecutionUnsafeInFlightDoesNotCallHandler(t *testing.T) {
	conversations, executions := newTestMemoryStore(), newTestExecutionStore()
	seedInFlightToolExecution(t, conversations, executions, false)
	var calls atomic.Int32
	charge := tool.NewRaw("charge", "charge", nil, func(context.Context, json.RawMessage) (string, error) {
		calls.Add(1)
		return "charged", nil
	})
	a, err := New(newScriptedProvider(), "x", WithTools(charge), WithConversationStore(conversations), WithExecutionStore(executions))
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.RecoverExecution(Background(), "execution-1")
	var uncertain *ToolExecutionUncertainError
	if !errors.As(err, &uncertain) {
		t.Fatalf("RecoverExecution error = %v, want ToolExecutionUncertainError", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("handler calls = %d, want 0", calls.Load())
	}
}

func TestRecoverExecutionReplaySafeReusesIdempotencyKey(t *testing.T) {
	conversations, executions := newTestMemoryStore(), newTestExecutionStore()
	seedInFlightToolExecution(t, conversations, executions, true)
	var calls atomic.Int32
	var gotKey string
	var gotReplay bool
	charge := tool.NewRaw("charge", "charge", nil, func(ctx context.Context, _ json.RawMessage) (string, error) {
		calls.Add(1)
		gotKey, _ = tool.IdempotencyKey(ctx)
		gotReplay = tool.IsRecoveryReplay(ctx)
		return "charged", nil
	}, tool.WithReplaySafe())
	a, err := New(newScriptedProvider(), "x", WithTools(charge), WithConversationStore(conversations), WithExecutionStore(executions))
	if err != nil {
		t.Fatal(err)
	}
	execution, err := a.RecoverExecution(Background(), "execution-1")
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || !gotReplay || gotKey != toolExecutionKey("execution-1", "call-1") {
		t.Fatalf("calls=%d replay=%v key=%q", calls.Load(), gotReplay, gotKey)
	}
	if execution.ToolBatch != nil || execution.Phase != ExecutionPhaseModel {
		t.Fatalf("execution = %+v", execution)
	}
	snapshot, err := conversations.Load(context.Background(), "conversation")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Messages) != 2 {
		t.Fatalf("messages = %d, want tool use and result", len(snapshot.Messages))
	}
	result, ok := snapshot.Messages[1].Content[0].(ToolResultBlock)
	if !ok || result.ToolUseID != "call-1" || result.Content != "charged" {
		t.Fatalf("result = %#v", snapshot.Messages[1].Content)
	}
}

func TestReconcileToolExecutionAppendsConfirmedResult(t *testing.T) {
	conversations, executions := newTestMemoryStore(), newTestExecutionStore()
	seeded := seedInFlightToolExecution(t, conversations, executions, false)
	a, err := New(newScriptedProvider(), "x", WithConversationStore(conversations), WithExecutionStore(executions))
	if err != nil {
		t.Fatal(err)
	}
	ctx := Background()
	execution, err := a.ReconcileToolExecution(ctx, seeded.ID, seeded.Version, "call-1", ToolResolution{Outcome: ToolResolutionSucceeded, Output: "already charged"})
	if err != nil {
		t.Fatal(err)
	}
	if execution.ToolBatch != nil || execution.Phase != ExecutionPhaseModel {
		t.Fatalf("execution = %+v", execution)
	}
	snapshot, err := conversations.Load(context.Background(), "conversation")
	if err != nil {
		t.Fatal(err)
	}
	result, ok := snapshot.Messages[1].Content[0].(ToolResultBlock)
	if !ok || result.Content != "already charged" || result.IsError {
		t.Fatalf("result = %#v", snapshot.Messages[1].Content)
	}
}

// recordingExecutionStore captures each committed execution boundary while
// delegating CAS semantics to the normal in-memory test store.
type recordingExecutionStore struct {
	*testExecutionStore
	saves []Execution
}

func newRecordingExecutionStore() *recordingExecutionStore {
	return &recordingExecutionStore{testExecutionStore: newTestExecutionStore()}
}

func (s *recordingExecutionStore) Create(ctx context.Context, execution Execution) (Execution, error) {
	created, err := s.testExecutionStore.Create(ctx, execution)
	if err == nil {
		s.mu.Lock()
		s.saves = append(s.saves, cloneExecution(created))
		s.mu.Unlock()
	}
	return created, err
}

func (s *recordingExecutionStore) Save(ctx context.Context, execution Execution, expected uint64) (Execution, error) {
	saved, err := s.testExecutionStore.Save(ctx, execution, expected)
	if err == nil {
		s.mu.Lock()
		s.saves = append(s.saves, cloneExecution(saved))
		s.mu.Unlock()
	}
	return saved, err
}

func TestDurableToolBoundaryPersistsIntentBeforeHandlerAndClearsAfterResult(t *testing.T) {
	conversations, executions := newTestMemoryStore(), newRecordingExecutionStore()
	var handlerCalls atomic.Int32
	var gotKey string
	lookup := tool.NewRaw("lookup", "lookup", nil, func(ctx context.Context, _ json.RawMessage) (string, error) {
		handlerCalls.Add(1)
		gotKey, _ = tool.IdempotencyKey(ctx)
		return "found", nil
	})
	a, err := New(newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "call-1", Name: "lookup", Input: json.RawMessage(`{"query":"x"}`)}}},
		&ModelResponse{Text: "done"},
	), "x", WithTools(lookup), WithConversationStore(conversations), WithExecutionStore(executions))
	if err != nil {
		t.Fatal(err)
	}
	result, err := a.Invoke(Background().WithConversationID("conversation"), "find")
	if err != nil {
		t.Fatal(err)
	}
	if handlerCalls.Load() != 1 || gotKey == "" || gotKey[:len("gude/tool/v1/")] != "gude/tool/v1/" {
		t.Fatalf("handler calls=%d idempotency key=%q", handlerCalls.Load(), gotKey)
	}

	var planned, ready, inFlight, cleared bool
	for _, saved := range executions.saves {
		if saved.ToolBatch == nil {
			if saved.Phase == ExecutionPhaseModel {
				cleared = true
			}
			continue
		}
		call := saved.ToolBatch.Calls[0]
		switch call.Status {
		case ToolExecutionPlanned:
			planned = call.Input != nil && saved.ToolBatch.ToolUseRevision == 0
		case ToolExecutionReady:
			ready = call.Input == nil && saved.ToolBatch.ToolUseRevision > 0
		case ToolExecutionInFlight:
			inFlight = true
		}
	}
	if !planned || !ready || !inFlight || !cleared {
		t.Fatalf("planned=%v ready=%v in_flight=%v cleared=%v saves=%+v", planned, ready, inFlight, cleared, executions.saves)
	}
	snapshot, err := conversations.Load(context.Background(), "conversation")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Messages) != 4 { // input, ToolUse, ToolResult, final answer
		t.Fatalf("canonical messages = %d", len(snapshot.Messages))
	}
	if _, ok := snapshot.Messages[1].Content[0].(ToolUseBlock); !ok {
		t.Fatalf("tool use message = %#v", snapshot.Messages[1])
	}
	if _, ok := snapshot.Messages[2].Content[0].(ToolResultBlock); !ok {
		t.Fatalf("tool result message = %#v", snapshot.Messages[2])
	}
	if result.ExecutionID == "" {
		t.Fatal("result has no execution ID")
	}
}

func TestReplaySafeBackgroundDispatchRemainsUncertain(t *testing.T) {
	conversations, executions := newTestMemoryStore(), newTestExecutionStore()
	seedInFlightToolExecution(t, conversations, executions, true)
	// The seeded call is represented as a plain tool so this test verifies the
	// recovery policy from the registered background definition instead.
	background := tool.NewBackground("charge", "charge", "accepted", func(context.Context, json.RawMessage) (string, error) {
		t.Fatal("background handler must not be replayed")
		return "", nil
	}, tool.WithReplaySafe())
	a, err := New(newScriptedProvider(), "x", WithTools(background), WithConversationStore(conversations), WithExecutionStore(executions))
	if err != nil {
		t.Fatal(err)
	}
	// Persisted replay policy is the source of truth. A legacy/inconsistent
	// record marked replay-safe is still not dispatched as a background tool.
	_, err = a.RecoverExecution(Background(), "execution-1")
	if err == nil || !errors.Is(err, ErrToolExecutionUncertain) {
		t.Fatalf("RecoverExecution error = %v, want uncertainty", err)
	}
}

func TestConcurrentRecoveryHasOneSameAgentWinner(t *testing.T) {
	conversations, executions := newTestMemoryStore(), newTestExecutionStore()
	seedInFlightToolExecution(t, conversations, executions, true)
	var calls atomic.Int32
	charge := tool.NewRaw("charge", "charge", nil, func(context.Context, json.RawMessage) (string, error) {
		calls.Add(1)
		return "charged", nil
	}, tool.WithReplaySafe())
	a, err := New(newScriptedProvider(), "x", WithTools(charge), WithConversationStore(conversations), WithExecutionStore(executions))
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	var successes atomic.Int32
	wg.Add(2)
	for range 2 {
		go func() {
			defer wg.Done()
			if _, err := a.RecoverExecution(Background(), "execution-1"); err == nil {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 || calls.Load() != 1 {
		t.Fatalf("successful recoveries=%d handler calls=%d", successes.Load(), calls.Load())
	}
}

func TestReconcileToolExecutionRetryPreservesKey(t *testing.T) {
	conversations, executions := newTestMemoryStore(), newTestExecutionStore()
	seeded := seedInFlightToolExecution(t, conversations, executions, false)
	var gotKey string
	charge := tool.NewRaw("charge", "charge", nil, func(ctx context.Context, _ json.RawMessage) (string, error) {
		gotKey, _ = tool.IdempotencyKey(ctx)
		return "charged", nil
	})
	a, err := New(newScriptedProvider(), "x", WithTools(charge), WithConversationStore(conversations), WithExecutionStore(executions))
	if err != nil {
		t.Fatal(err)
	}
	retried, err := a.ReconcileToolExecution(Background(), seeded.ID, seeded.Version, "call-1", ToolResolution{Outcome: ToolResolutionRetry})
	if err != nil {
		t.Fatal(err)
	}
	if retried.ToolBatch == nil || retried.ToolBatch.Calls[0].Status != ToolExecutionReady {
		t.Fatalf("retry execution = %+v", retried)
	}
	if _, err := a.RecoverExecution(Background(), seeded.ID); err != nil {
		t.Fatal(err)
	}
	if gotKey != toolExecutionKey(seeded.ID, "call-1") {
		t.Fatalf("idempotency key = %q", gotKey)
	}
}

func TestOutcomeUnknownLeavesToolExecutionForReconciliation(t *testing.T) {
	conversations, executions := newTestMemoryStore(), newTestExecutionStore()
	charge := tool.NewRaw("charge", "charge", nil, func(context.Context, json.RawMessage) (string, error) {
		return "", tool.OutcomeUnknown(errors.New("gateway response lost"))
	})
	a, err := New(newScriptedProvider(&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "call-1", Name: "charge", Input: json.RawMessage(`{"amount":7}`)}}}), "x", WithTools(charge), WithConversationStore(conversations), WithExecutionStore(executions))
	if err != nil {
		t.Fatal(err)
	}
	result, err := a.Invoke(Background().WithConversationID("conversation"), "charge")
	if !errors.Is(err, ErrToolExecutionUncertain) {
		t.Fatalf("Invoke error = %v, want uncertainty", err)
	}
	execution, loadErr := executions.Load(context.Background(), result.ExecutionID)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if execution.Status != ExecutionRunning || execution.ToolBatch == nil || execution.ToolBatch.Calls[0].Status != ToolExecutionInFlight {
		t.Fatalf("execution = %+v", execution)
	}
	snapshot, loadErr := conversations.Load(context.Background(), "conversation")
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if len(snapshot.Messages) != 2 { // input and canonical ToolUse; no result yet
		t.Fatalf("canonical messages = %d", len(snapshot.Messages))
	}
}

func TestOutcomeUnknownCanBeReconciledAfterCanonicalToolUse(t *testing.T) {
	conversations, executions := newTestMemoryStore(), newTestExecutionStore()
	charge := tool.NewRaw("charge", "charge", nil, func(context.Context, json.RawMessage) (string, error) {
		return "", tool.OutcomeUnknown(errors.New("external timeout"))
	})
	a, err := New(newScriptedProvider(&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "call-1", Name: "charge", Input: json.RawMessage(`{"amount":7}`)}}}), "x", WithTools(charge), WithConversationStore(conversations), WithExecutionStore(executions))
	if err != nil {
		t.Fatal(err)
	}
	result, err := a.Invoke(Background().WithConversationID("conversation"), "charge")
	if !errors.Is(err, ErrToolExecutionUncertain) {
		t.Fatalf("Invoke error = %v, want uncertainty", err)
	}
	before, err := executions.Load(context.Background(), result.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	reconciled, err := a.ReconcileToolExecution(Background(), before.ID, before.Version, "call-1", ToolResolution{Outcome: ToolResolutionSucceeded, Output: "captured"})
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.ToolBatch != nil || reconciled.Phase != ExecutionPhaseModel {
		t.Fatalf("reconciled execution = %+v", reconciled)
	}
	snapshot, err := conversations.Load(context.Background(), "conversation")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Messages) != 3 {
		t.Fatalf("canonical messages = %d", len(snapshot.Messages))
	}
	toolResult, ok := snapshot.Messages[2].Content[0].(ToolResultBlock)
	if !ok || toolResult.Content != "captured" {
		t.Fatalf("tool result = %#v", snapshot.Messages[2].Content)
	}
}
