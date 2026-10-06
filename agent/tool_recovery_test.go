package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/camilbinas/gude-agents/agent/rag"
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
	hash, err := toolInputHash(json.RawMessage(`{"amount":7}`))
	if err != nil {
		t.Fatal(err)
	}
	execution, err := executions.Create(context.Background(), Execution{
		ID: "execution-1", ConversationID: "conversation", Revision: cursor.Revision, LastSequence: cursor.LastSequence,
		Status: ExecutionRunning, Phase: ExecutionPhaseTools, Iteration: 1,
		ToolBatch: &ToolBatchExecution{Iteration: 1, ToolUseRevision: cursor.Revision, ToolUseLastSequence: cursor.LastSequence, Calls: []ToolExecution{{
			CallID: "call-1", Name: "charge", Status: ToolExecutionInFlight,
			IdempotencyKey: toolExecutionKey("execution-1", "call-1"), InputHash: hash, ReplaySafe: replaySafe,
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

func TestToolInputHashCanonicalJSON(t *testing.T) {
	a, err := toolInputHash(json.RawMessage(`{"b":"\u003c","a":900719925474099312345}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := toolInputHash(json.RawMessage(` { "a" : 900719925474099312345 , "b" : "<" } `))
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("canonical hashes differ: %q != %q", a, b)
	}
	decimal, err := toolInputHash(json.RawMessage(`1.0`))
	if err != nil {
		t.Fatal(err)
	}
	integer, err := toolInputHash(json.RawMessage(`1`))
	if err != nil {
		t.Fatal(err)
	}
	if decimal == integer {
		t.Fatal("json.Number lexical representation was not retained")
	}
	if _, err := toolInputHash(json.RawMessage(`{} {}`)); err == nil {
		t.Fatal("trailing JSON was accepted")
	}
	if _, err := toolInputHash(json.RawMessage(`{`)); err == nil {
		t.Fatal("invalid JSON was accepted")
	}
}

func TestRecoverExecutionFinishesResolutionClaimIdempotently(t *testing.T) {
	for _, afterAppend := range []bool{false, true} {
		t.Run(map[bool]string{false: "before_append", true: "after_append_before_clear"}[afterAppend], func(t *testing.T) {
			conversations, executions := newTestMemoryStore(), newTestExecutionStore()
			seeded := seedInFlightToolExecution(t, conversations, executions, false)
			seeded.ToolBatch.Resolution = &ToolResolutionClaim{CallID: "call-1", Outcome: ToolResolutionSucceeded, Output: "captured"}
			claimed, err := executions.Save(context.Background(), seeded, seeded.Version)
			if err != nil {
				t.Fatal(err)
			}
			if afterAppend {
				if _, err := conversations.Append(context.Background(), claimed.ConversationID, []Message{{Role: RoleUser, Content: []ContentBlock{ToolResultBlock{ToolUseID: "call-1", Content: "captured"}}}}, claimed.Revision); err != nil {
					t.Fatal(err)
				}
			}
			a, err := New(newScriptedProvider(), "x", WithConversationStore(conversations), WithExecutionStore(executions))
			if err != nil {
				t.Fatal(err)
			}
			recovered, err := a.RecoverExecution(Background(), claimed.ID)
			if err != nil {
				t.Fatal(err)
			}
			if recovered.ToolBatch != nil || recovered.Phase != ExecutionPhaseModel {
				t.Fatalf("execution = %+v", recovered)
			}
			snapshot, err := conversations.Load(context.Background(), claimed.ConversationID)
			if err != nil {
				t.Fatal(err)
			}
			if len(snapshot.Messages) != 2 {
				t.Fatalf("messages = %#v", snapshot.Messages)
			}
		})
	}
}

func TestOutcomeUnknownPersistsDefinitiveSiblingResults(t *testing.T) {
	conversations, executions := newTestMemoryStore(), newTestExecutionStore()
	var knownCalls atomic.Int32
	known := tool.NewRaw("known", "known", nil, func(context.Context, json.RawMessage) (string, error) {
		knownCalls.Add(1)
		return "done", nil
	})
	unknown := tool.NewRaw("unknown", "unknown", nil, func(context.Context, json.RawMessage) (string, error) {
		return "", tool.OutcomeUnknown(errors.New("transport lost"))
	})
	a, err := New(newScriptedProvider(&ModelResponse{ToolCalls: []tool.Call{
		{ToolUseID: "known", Name: "known", Input: json.RawMessage(`{}`)},
		{ToolUseID: "unknown", Name: "unknown", Input: json.RawMessage(`{}`)},
	}}), "x", WithTools(known, unknown), WithConversationStore(conversations), WithExecutionStore(executions))
	if err != nil {
		t.Fatal(err)
	}
	result, err := a.Invoke(Background().WithConversationID("conversation"), "go")
	if !errors.Is(err, ErrToolExecutionUncertain) {
		t.Fatalf("Invoke error = %v", err)
	}
	snapshot, err := conversations.Load(context.Background(), "conversation")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Messages) != 3 {
		t.Fatalf("canonical messages = %#v", snapshot.Messages)
	}
	resultBlock, ok := snapshot.Messages[2].Content[0].(ToolResultBlock)
	if !ok || resultBlock.ToolUseID != "known" || resultBlock.Content != "done" {
		t.Fatalf("definitive sibling result = %#v", snapshot.Messages[2].Content)
	}
	execution, err := executions.Load(context.Background(), result.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	if execution.ToolBatch == nil || execution.ToolBatch.Calls[0].Status != ToolExecutionCompleted || execution.ToolBatch.Calls[1].Status != ToolExecutionInFlight {
		t.Fatalf("execution = %+v", execution)
	}
	if knownCalls.Load() != 1 {
		t.Fatalf("known calls = %d", knownCalls.Load())
	}
}

func TestRecoverExecutionReadyApprovalRemainsDurablyPaused(t *testing.T) {
	conversations, executions := newTestMemoryStore(), newTestExecutionStore()
	cursor, err := conversations.Append(context.Background(), "conversation", []Message{{
		Role:    RoleAssistant,
		Content: []ContentBlock{ToolUseBlock{ToolUseID: "approve", Name: "approve", Input: json.RawMessage(`{}`)}, ToolUseBlock{ToolUseID: "ordinary", Name: "ordinary", Input: json.RawMessage(`{}`)}},
	}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	approveHash, _ := toolInputHash(json.RawMessage(`{}`))
	ordinaryHash, _ := toolInputHash(json.RawMessage(`{}`))
	seeded, err := executions.Create(context.Background(), Execution{ID: "execution-approval", ConversationID: "conversation", Revision: cursor.Revision, LastSequence: cursor.LastSequence, Status: ExecutionRunning, Phase: ExecutionPhaseTools, ToolBatch: &ToolBatchExecution{ToolUseRevision: cursor.Revision, ToolUseLastSequence: cursor.LastSequence, Calls: []ToolExecution{
		{CallID: "approve", Name: "approve", Status: ToolExecutionReady, IdempotencyKey: toolExecutionKey("execution-approval", "approve"), InputHash: approveHash},
		{CallID: "ordinary", Name: "ordinary", Status: ToolExecutionReady, IdempotencyKey: toolExecutionKey("execution-approval", "ordinary"), InputHash: ordinaryHash},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	var approvalCalls, ordinaryCalls atomic.Int32
	approval := tool.NewRaw("approve", "approve", nil, func(context.Context, json.RawMessage) (string, error) { approvalCalls.Add(1); return "approved", nil }, tool.RequiresApproval())
	ordinary := tool.NewRaw("ordinary", "ordinary", nil, func(context.Context, json.RawMessage) (string, error) { ordinaryCalls.Add(1); return "ordinary", nil })
	a, err := New(newScriptedProvider(), "x", WithTools(approval, ordinary), WithConversationStore(conversations), WithExecutionStore(executions))
	if err != nil {
		t.Fatal(err)
	}
	paused, err := a.RecoverExecution(Background(), seeded.ID)
	if err != nil {
		t.Fatal(err)
	}
	if paused.Status != ExecutionPaused || paused.Pause == nil || paused.Pause.Approval == nil || approvalCalls.Load() != 0 || ordinaryCalls.Load() != 0 {
		t.Fatalf("paused=%+v approval=%d ordinary=%d", paused, approvalCalls.Load(), ordinaryCalls.Load())
	}
	again, err := a.RecoverExecution(Background(), seeded.ID)
	if err != nil || again.Status != ExecutionPaused || approvalCalls.Load() != 0 || ordinaryCalls.Load() != 0 {
		t.Fatalf("repeat recovery=%+v err=%v approval=%d ordinary=%d", again, err, approvalCalls.Load(), ordinaryCalls.Load())
	}
	snapshot, err := conversations.Load(context.Background(), "conversation")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Messages) != 1 {
		t.Fatalf("fabricated tool result: %#v", snapshot.Messages)
	}
}

// blockResolutionClaimStore pauses only the first resolution-claim CAS so a
// competing Retry can deterministically win before any conversation append.
type blockResolutionClaimStore struct {
	*testExecutionStore
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *blockResolutionClaimStore) Save(ctx context.Context, execution Execution, expected uint64) (Execution, error) {
	if execution.ToolBatch != nil && execution.ToolBatch.Resolution != nil {
		s.once.Do(func() {
			close(s.entered)
			<-s.release
		})
	}
	return s.testExecutionStore.Save(ctx, execution, expected)
}

func TestReconcileClaimPreventsStaleSuccessAfterRetry(t *testing.T) {
	conversations, base := newTestMemoryStore(), newTestExecutionStore()
	seeded := seedInFlightToolExecution(t, conversations, base, false)
	executions := &blockResolutionClaimStore{testExecutionStore: base, entered: make(chan struct{}), release: make(chan struct{})}
	a, err := New(newScriptedProvider(), "x", WithConversationStore(conversations), WithExecutionStore(executions))
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := a.ReconcileToolExecution(Background(), seeded.ID, seeded.Version, "call-1", ToolResolution{Outcome: ToolResolutionSucceeded, Output: "captured"})
		result <- err
	}()
	<-executions.entered
	retried, err := a.ReconcileToolExecution(Background(), seeded.ID, seeded.Version, "call-1", ToolResolution{Outcome: ToolResolutionRetry})
	if err != nil {
		t.Fatal(err)
	}
	if retried.ToolBatch == nil || retried.ToolBatch.Calls[0].Status != ToolExecutionReady {
		t.Fatalf("retry = %+v", retried)
	}
	close(executions.release)
	if err := <-result; !errors.Is(err, ErrExecutionConflict) {
		t.Fatalf("stale success error = %v", err)
	}
	snapshot, err := conversations.Load(context.Background(), seeded.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Messages) != 1 {
		t.Fatalf("stale success appended a result: %#v", snapshot.Messages)
	}
}

func TestReconcileClaimPreventsStaleFailureAfterRetry(t *testing.T) {
	conversations, base := newTestMemoryStore(), newTestExecutionStore()
	seeded := seedInFlightToolExecution(t, conversations, base, false)
	executions := &blockResolutionClaimStore{testExecutionStore: base, entered: make(chan struct{}), release: make(chan struct{})}
	a, err := New(newScriptedProvider(), "x", WithConversationStore(conversations), WithExecutionStore(executions))
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := a.ReconcileToolExecution(Background(), seeded.ID, seeded.Version, "call-1", ToolResolution{Outcome: ToolResolutionFailed, ErrorMessage: "declined"})
		result <- err
	}()
	<-executions.entered
	if _, err := a.ReconcileToolExecution(Background(), seeded.ID, seeded.Version, "call-1", ToolResolution{Outcome: ToolResolutionRetry}); err != nil {
		t.Fatal(err)
	}
	close(executions.release)
	if err := <-result; !errors.Is(err, ErrExecutionConflict) {
		t.Fatalf("stale failure error = %v", err)
	}
	snapshot, err := conversations.Load(context.Background(), seeded.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Messages) != 1 {
		t.Fatalf("stale failure appended a result: %#v", snapshot.Messages)
	}
}

func TestRecoverPlannedBatchKeepsPrecedingCanonicalUserTurn(t *testing.T) {
	conversations, executions := newTestMemoryStore(), newTestExecutionStore()
	cursor, err := conversations.Append(context.Background(), "conversation", []Message{{Role: RoleUser, Content: []ContentBlock{TextBlock{Text: "charge invoice"}}}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := toolInputHash(json.RawMessage(`{"amount":7}`))
	if err != nil {
		t.Fatal(err)
	}
	seeded, err := executions.Create(context.Background(), Execution{ID: "planned", ConversationID: "conversation", Revision: cursor.Revision, LastSequence: cursor.LastSequence, Status: ExecutionRunning, Phase: ExecutionPhaseTools, ToolBatch: &ToolBatchExecution{Iteration: 1, Calls: []ToolExecution{{CallID: "charge", Name: "charge", Status: ToolExecutionPlanned, IdempotencyKey: toolExecutionKey("planned", "charge"), InputHash: hash, Input: json.RawMessage(`{"amount":7}`)}}}})
	if err != nil {
		t.Fatal(err)
	}
	charge := tool.NewRaw("charge", "charge", nil, func(context.Context, json.RawMessage) (string, error) { return "charged", nil })
	a, err := New(newScriptedProvider(), "x", WithTools(charge), WithConversationStore(conversations), WithExecutionStore(executions))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.RecoverExecution(Background(), seeded.ID); err != nil {
		t.Fatal(err)
	}
	snapshot, err := conversations.Load(context.Background(), "conversation")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Messages) != 3 || snapshot.Messages[0].Role != RoleUser {
		t.Fatalf("canonical recovery messages = %#v", snapshot.Messages)
	}
	text, ok := snapshot.Messages[0].Content[0].(TextBlock)
	if !ok || text.Text != "charge invoice" {
		t.Fatalf("preceding user turn = %#v", snapshot.Messages[0])
	}
}

func TestRecoveryRejectsUnrelatedAdvanceWithinActiveBoundary(t *testing.T) {
	conversations, executions := newTestMemoryStore(), newTestExecutionStore()
	seeded := seedInFlightToolExecution(t, conversations, executions, false)
	if _, err := conversations.Append(context.Background(), seeded.ConversationID, []Message{{Role: RoleAssistant, Content: []ContentBlock{TextBlock{Text: "unrelated"}}}}, seeded.Revision); err != nil {
		t.Fatal(err)
	}
	a, err := New(newScriptedProvider(), "x", WithConversationStore(conversations), WithExecutionStore(executions))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.RecoverExecution(Background(), seeded.ID); !errors.Is(err, ErrConversationConflict) {
		t.Fatalf("RecoverExecution error = %v, want conversation conflict", err)
	}
}

type staticToolRecoveryRetriever struct{}

func (staticToolRecoveryRetriever) Retrieve(context.Context, string) ([]rag.Document, error) {
	return []rag.Document{{Content: "transient reference"}}, nil
}

func TestDurableToolCheckpointExcludesTransientRAGMessages(t *testing.T) {
	conversations, executions := newTestMemoryStore(), newTestExecutionStore()
	lookup := tool.NewRaw("lookup", "lookup", nil, func(context.Context, json.RawMessage) (string, error) { return "found", nil })
	a, err := New(newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "lookup", Name: "lookup", Input: json.RawMessage(`{}`)}}},
		&ModelResponse{Text: "done"},
	), "x", WithTools(lookup), WithRetriever(staticToolRecoveryRetriever{}), WithConversationStore(conversations), WithExecutionStore(executions))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Invoke(Background().WithConversationID("conversation"), "question"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := conversations.Load(context.Background(), "conversation")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Messages) != 4 { // user, ToolUse, ToolResult, final
		t.Fatalf("canonical messages = %#v", snapshot.Messages)
	}
	for _, message := range snapshot.Messages {
		for _, block := range message.Content {
			if text, ok := block.(TextBlock); ok && strings.Contains(text.Text, "transient reference") {
				t.Fatalf("transient RAG message persisted: %#v", snapshot.Messages)
			}
		}
	}
}

func TestToolInputHashSurvivesExecutionJSONRoundTrip(t *testing.T) {
	input := json.RawMessage(` { "payload": "\u003c", "n": 900719925474099312345 } `)
	hash, err := toolInputHash(input)
	if err != nil {
		t.Fatal(err)
	}
	execution := Execution{ID: "round-trip", ToolBatch: &ToolBatchExecution{Calls: []ToolExecution{{CallID: "call", Name: "tool", Status: ToolExecutionPlanned, InputHash: hash, Input: input}}}}
	encoded, err := json.Marshal(execution)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Execution
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	got, err := toolInputHash(decoded.ToolBatch.Calls[0].Input)
	if err != nil {
		t.Fatal(err)
	}
	if got != hash {
		t.Fatalf("round-trip hash=%q, want %q; encoded=%s", got, hash, encoded)
	}
}

// failNthAppendStore injects one durable append failure while preserving the
// underlying append-only transcript for a subsequent recovery attempt.
type failNthAppendStore struct {
	ConversationStore
	failAt  int
	appends atomic.Int32
}

func (s *failNthAppendStore) Append(ctx context.Context, conversationID string, messages []Message, expectedRevision uint64) (ConversationCursor, error) {
	if len(messages) > 0 && int(s.appends.Add(1)) == s.failAt {
		return ConversationCursor{}, errors.New("injected ToolUse append failure")
	}
	return s.ConversationStore.Append(ctx, conversationID, messages, expectedRevision)
}

func TestRecoverExecutionRepairsRealPlannedBoundaryWithoutReplacingUserTurn(t *testing.T) {
	base, executions := newTestMemoryStore(), newTestExecutionStore()
	conversations := &failNthAppendStore{ConversationStore: base, failAt: 2}
	var calls atomic.Int32
	charge := tool.NewRaw("charge", "charge", nil, func(context.Context, json.RawMessage) (string, error) {
		calls.Add(1)
		return "charged", nil
	})
	a, err := New(newScriptedProvider(&ModelResponse{ToolCalls: []tool.Call{{
		ToolUseID: "charge", Name: "charge", Input: json.RawMessage(`{"amount":7}`),
	}}}), "x", WithTools(charge), WithConversationStore(conversations), WithExecutionStore(executions))
	if err != nil {
		t.Fatal(err)
	}
	result, err := a.Invoke(Background().WithConversationID("conversation"), "charge invoice")
	if err == nil || !strings.Contains(err.Error(), "injected ToolUse append failure") {
		t.Fatalf("Invoke error = %v, want injected ToolUse append failure", err)
	}
	planned, err := executions.Load(context.Background(), result.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	if planned.ToolBatch == nil || planned.ToolBatch.Calls[0].Status != ToolExecutionPlanned || planned.LastSequence == 0 {
		t.Fatalf("planned execution = %+v", planned)
	}
	if _, err := a.RecoverExecution(Background(), result.ExecutionID); err != nil {
		t.Fatal(err)
	}
	snapshot, err := base.Load(context.Background(), "conversation")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Messages) != 3 || snapshot.Messages[0].Role != RoleUser {
		t.Fatalf("recovered messages = %#v", snapshot.Messages)
	}
	text, ok := snapshot.Messages[0].Content[0].(TextBlock)
	if !ok || text.Text != "charge invoice" {
		t.Fatalf("original user turn was replaced: %#v", snapshot.Messages[0])
	}
	if calls.Load() != 1 {
		t.Fatalf("handler calls = %d, want 1", calls.Load())
	}
}

func TestResumeUnknownOutcomeCheckpointsDefinitiveSibling(t *testing.T) {
	conversations, executions := newTestMemoryStore(), newTestExecutionStore()
	approved := tool.NewRaw("approved", "approved", nil, func(context.Context, json.RawMessage) (string, error) {
		return "approved", nil
	}, tool.RequiresApproval())
	unknown := tool.NewRaw("unknown", "unknown", nil, func(context.Context, json.RawMessage) (string, error) {
		return "", tool.OutcomeUnknown(errors.New("external response lost"))
	})
	a, err := New(newScriptedProvider(&ModelResponse{ToolCalls: []tool.Call{
		{ToolUseID: "approved", Name: "approved", Input: json.RawMessage(`{}`)},
		{ToolUseID: "unknown", Name: "unknown", Input: json.RawMessage(`{}`)},
	}}), "x", WithTools(approved, unknown), WithConversationStore(conversations), WithExecutionStore(executions))
	if err != nil {
		t.Fatal(err)
	}
	ctx := Background().WithConversationID("conversation")
	paused, err := a.Invoke(ctx, "go")
	if err != nil || paused.Interrupt == nil {
		t.Fatalf("Invoke = %+v, %v", paused, err)
	}
	_, err = a.Resume(ctx, paused.Interrupt, Approve())
	if !errors.Is(err, ErrToolExecutionUncertain) {
		t.Fatalf("Resume error = %v, want uncertainty", err)
	}
	snapshot, err := conversations.Load(context.Background(), "conversation")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Messages) != 3 {
		t.Fatalf("messages = %#v", snapshot.Messages)
	}
	resultBlock, ok := snapshot.Messages[2].Content[0].(ToolResultBlock)
	if !ok || resultBlock.ToolUseID != "approved" || resultBlock.Content != "approved" {
		t.Fatalf("definitive resume sibling = %#v", snapshot.Messages[2].Content)
	}
	execution, err := executions.Load(context.Background(), paused.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	if execution.ToolBatch == nil || execution.ToolBatch.Calls[0].Status != ToolExecutionCompleted || execution.ToolBatch.Calls[1].Status != ToolExecutionInFlight {
		t.Fatalf("execution = %+v", execution)
	}
}

func TestExecutionRejectsLaterToolCallIDReuseAndAllowsIndependentExecution(t *testing.T) {
	t.Run("later iteration rejects distinct input before handler", func(t *testing.T) {
		conversations, executions := newTestMemoryStore(), newTestExecutionStore()
		var calls atomic.Int32
		echo := tool.NewRaw("echo", "echo", nil, func(context.Context, json.RawMessage) (string, error) {
			calls.Add(1)
			return "ok", nil
		})
		a, err := New(newScriptedProvider(
			&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "reused", Name: "echo", Input: json.RawMessage(`{"value":1}`)}}},
			&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "reused", Name: "echo", Input: json.RawMessage(`{"value":2}`)}}},
		), "x", WithTools(echo), WithConversationStore(conversations), WithExecutionStore(executions))
		if err != nil {
			t.Fatal(err)
		}
		_, err = a.Invoke(Background().WithConversationID("conversation"), "go")
		if err == nil || !strings.Contains(err.Error(), "reuses call ID \"reused\" with different name or input") {
			t.Fatalf("Invoke error = %v, want call ID reuse rejection", err)
		}
		if calls.Load() != 1 {
			t.Fatalf("handler calls = %d, want only the first iteration", calls.Load())
		}
	})

	t.Run("independent executions may reuse call IDs", func(t *testing.T) {
		conversations, executions := newTestMemoryStore(), newTestExecutionStore()
		var calls atomic.Int32
		echo := tool.NewRaw("echo", "echo", nil, func(context.Context, json.RawMessage) (string, error) {
			calls.Add(1)
			return "ok", nil
		})
		first, err := New(newScriptedProvider(
			&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "shared", Name: "echo", Input: json.RawMessage(`{"value":1}`)}}},
			&ModelResponse{Text: "first done"},
		), "x", WithTools(echo), WithConversationStore(conversations), WithExecutionStore(executions))
		if err != nil {
			t.Fatal(err)
		}
		second, err := New(newScriptedProvider(
			&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "shared", Name: "echo", Input: json.RawMessage(`{"value":2}`)}}},
			&ModelResponse{Text: "second done"},
		), "x", WithTools(echo), WithConversationStore(conversations), WithExecutionStore(executions))
		if err != nil {
			t.Fatal(err)
		}
		ctx := Background().WithConversationID("conversation")
		if _, err := first.Invoke(ctx, "first"); err != nil {
			t.Fatal(err)
		}
		if _, err := second.Invoke(ctx, "second"); err != nil {
			t.Fatal(err)
		}
		if calls.Load() != 2 {
			t.Fatalf("handler calls = %d, want 2 independent executions", calls.Load())
		}
	})
}

// blockFirstReadyClaimStore lets a second worker win the same Ready call's
// durable claim, exercising the loser path without manufacturing a result.
type blockFirstReadyClaimStore struct {
	*testExecutionStore
	entered chan struct{}
	release chan struct{}
	blocked atomic.Bool
}

func (s *blockFirstReadyClaimStore) Save(ctx context.Context, execution Execution, expected uint64) (Execution, error) {
	if execution.ToolBatch != nil && len(execution.ToolBatch.Calls) == 1 && execution.ToolBatch.Calls[0].Status == ToolExecutionInFlight && s.blocked.CompareAndSwap(false, true) {
		close(s.entered)
		<-s.release
	}
	return s.testExecutionStore.Save(ctx, execution, expected)
}

func TestReadyClaimRaceDoesNotCreateFalseToolResult(t *testing.T) {
	conversations, base := newTestMemoryStore(), newTestExecutionStore()
	seeded := seedInFlightToolExecution(t, conversations, base, true)
	seeded.ToolBatch.Calls[0].Status = ToolExecutionReady
	if _, err := base.Save(context.Background(), seeded, seeded.Version); err != nil {
		t.Fatal(err)
	}
	executions := &blockFirstReadyClaimStore{testExecutionStore: base, entered: make(chan struct{}), release: make(chan struct{})}
	var calls atomic.Int32
	charge := tool.NewRaw("charge", "charge", nil, func(context.Context, json.RawMessage) (string, error) {
		calls.Add(1)
		return "charged", nil
	}, tool.WithReplaySafe())
	first, err := New(newScriptedProvider(), "x", WithTools(charge), WithConversationStore(conversations), WithExecutionStore(executions))
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(newScriptedProvider(), "x", WithTools(charge), WithConversationStore(conversations), WithExecutionStore(executions))
	if err != nil {
		t.Fatal(err)
	}
	firstResult := make(chan error, 1)
	go func() {
		_, err := first.RecoverExecution(Background(), seeded.ID)
		firstResult <- err
	}()
	<-executions.entered
	if _, err := second.RecoverExecution(Background(), seeded.ID); err != nil {
		t.Fatal(err)
	}
	close(executions.release)
	if err := <-firstResult; !errors.Is(err, ErrExecutionConflict) {
		t.Fatalf("losing recovery error = %v, want execution conflict", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("handler calls = %d, want 1", calls.Load())
	}
	snapshot, err := conversations.Load(context.Background(), seeded.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Messages) != 2 {
		t.Fatalf("false coordination result appended: %#v", snapshot.Messages)
	}
	result, ok := snapshot.Messages[1].Content[0].(ToolResultBlock)
	if !ok || result.ToolUseID != "call-1" || result.Content != "charged" {
		t.Fatalf("canonical result = %#v", snapshot.Messages[1].Content)
	}
}
