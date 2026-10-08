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

type testExecutionStore struct {
	mu    sync.Mutex
	items map[string]Execution
}

func newTestExecutionStore() *testExecutionStore {
	return &testExecutionStore{items: make(map[string]Execution)}
}
func (s *testExecutionStore) Create(_ context.Context, e Execution) (Execution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[e.ID]; ok {
		return Execution{}, ErrExecutionConflict
	}
	e.Version = 1
	s.items[e.ID] = cloneExecution(e)
	return cloneExecution(e), nil
}
func (s *testExecutionStore) Load(_ context.Context, id string) (Execution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.items[id]
	if !ok {
		return Execution{}, ErrExecutionNotFound
	}
	return cloneExecution(e), nil
}
func (s *testExecutionStore) Save(_ context.Context, e Execution, expected uint64) (Execution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.items[e.ID]
	if !ok {
		return Execution{}, ErrExecutionNotFound
	}
	if current.Version != expected {
		return Execution{}, ErrExecutionConflict
	}
	e.Version = expected + 1
	s.items[e.ID] = cloneExecution(e)
	return cloneExecution(e), nil
}

func TestWithExecutionStoreRequiresConversationStore(t *testing.T) {
	if _, err := New(newScriptedProvider(), "x", WithExecutionStore(newTestExecutionStore())); err == nil {
		t.Fatal("WithExecutionStore without ConversationStore succeeded")
	}
}

func TestExecutionLifecycleCompletes(t *testing.T) {
	conversations, executions := newTestMemoryStore(), newTestExecutionStore()
	a, err := New(newScriptedProvider(&ModelResponse{Text: "done"}), "x", WithConversationStore(conversations), WithExecutionStore(executions))
	if err != nil {
		t.Fatal(err)
	}
	result, err := a.Invoke(Background().WithConversationID("conversation"), "hello")
	if err != nil {
		t.Fatal(err)
	}
	if result.ExecutionID == "" {
		t.Fatal("result has no execution ID")
	}
	execution, err := executions.Load(context.Background(), result.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	if execution.Status != ExecutionCompleted || execution.Phase != ExecutionPhaseDone || execution.Iteration != 1 || execution.ConversationID != "conversation" || execution.Revision == 0 || execution.LastSequence == 0 {
		t.Fatalf("execution = %+v", execution)
	}
	if execution.Usage != result.Usage {
		t.Fatalf("usage = %+v, want %+v", execution.Usage, result.Usage)
	}
}

func TestExecutionPauseLoadInterruptAndResume(t *testing.T) {
	var calls atomic.Int32
	approve := tool.NewRaw("approve", "approve", nil, func(context.Context, json.RawMessage) (string, error) { calls.Add(1); return "ok", nil }, tool.RequiresApproval())
	conversations, executions := newTestMemoryStore(), newTestExecutionStore()
	a, err := New(newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "call", Name: "approve", Input: json.RawMessage(`{}`)}}},
		&ModelResponse{Text: "continued"},
	), "x", WithTools(approve), WithConversationStore(conversations), WithExecutionStore(executions))
	if err != nil {
		t.Fatal(err)
	}
	ctx := Background().WithConversationID("conversation")
	paused, err := a.Invoke(ctx, "go")
	if err != nil {
		t.Fatal(err)
	}
	if paused.StopReason != StopInterrupt || paused.Interrupt == nil || paused.ExecutionID != paused.Interrupt.ExecutionID {
		t.Fatalf("pause = %+v", paused)
	}
	if len(paused.Interrupt.Messages) != 0 || paused.Interrupt.ExecutionVersion == 0 {
		t.Fatalf("interrupt = %+v", paused.Interrupt)
	}
	execution, err := executions.Load(context.Background(), paused.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	if execution.Status != ExecutionPaused || execution.Pause == nil || execution.Revision != paused.Interrupt.Revision || execution.LastSequence != paused.Interrupt.LastSequence {
		t.Fatalf("execution = %+v", execution)
	}
	loaded, err := a.LoadInterrupt(context.Background(), paused.ExecutionID)
	if err != nil || loaded.ExecutionVersion != paused.Interrupt.ExecutionVersion {
		t.Fatalf("LoadInterrupt = %+v, %v", loaded, err)
	}
	resumed, err := a.Resume(ctx, loaded, Approve())
	if err != nil || resumed.Text != "continued" {
		t.Fatalf("Resume = %+v, %v", resumed, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d", calls.Load())
	}
	if _, err := a.LoadInterrupt(context.Background(), paused.ExecutionID); !errors.Is(err, ErrInterruptNotFound) {
		t.Fatalf("LoadInterrupt after resume = %v", err)
	}
}

func TestExecutionConcurrentResumeHasOneWinner(t *testing.T) {
	var handlerCalls atomic.Int32
	approve := tool.NewRaw("approve", "approve", nil, func(context.Context, json.RawMessage) (string, error) { handlerCalls.Add(1); return "ok", nil }, tool.RequiresApproval())
	conversations, executions := newTestMemoryStore(), newTestExecutionStore()
	a, err := New(newScriptedProvider(&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "call", Name: "approve", Input: json.RawMessage(`{}`)}}}, &ModelResponse{Text: "done"}), "x", WithTools(approve), WithConversationStore(conversations), WithExecutionStore(executions))
	if err != nil {
		t.Fatal(err)
	}
	ctx := Background().WithConversationID("c")
	paused, err := a.Invoke(ctx, "go")
	if err != nil {
		t.Fatal(err)
	}
	const n = 16
	var winners atomic.Int32
	var wg sync.WaitGroup
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			_, err := a.Resume(ctx, paused.Interrupt, Approve())
			if err == nil {
				winners.Add(1)
			} else if !errors.Is(err, ErrExecutionConflict) {
				t.Errorf("Resume: %v", err)
			}
		}()
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("winners = %d", winners.Load())
	}
	if handlerCalls.Load() != 1 {
		t.Fatalf("handler calls = %d", handlerCalls.Load())
	}
}

func TestExecutionConversationConflictPreventsSideEffects(t *testing.T) {
	var handlerCalls atomic.Int32
	approve := tool.NewRaw("approve", "approve", nil, func(context.Context, json.RawMessage) (string, error) { handlerCalls.Add(1); return "ok", nil }, tool.RequiresApproval())
	conversations, executions := newTestMemoryStore(), newTestExecutionStore()
	a, err := New(newScriptedProvider(&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "call", Name: "approve", Input: json.RawMessage(`{}`)}}}), "x", WithTools(approve), WithConversationStore(conversations), WithExecutionStore(executions))
	if err != nil {
		t.Fatal(err)
	}
	ctx := Background().WithConversationID("c")
	paused, err := a.Invoke(ctx, "go")
	if err != nil {
		t.Fatal(err)
	}
	// Advance the canonical cursor after pause without changing execution.
	snapshot, _ := conversations.Load(ctx, "c")
	_, err = conversations.Append(ctx, "c", []Message{{Role: RoleUser, Content: []ContentBlock{TextBlock{Text: "other writer"}}}}, snapshot.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Resume(ctx, paused.Interrupt, Approve()); !errors.Is(err, ErrConversationConflict) {
		t.Fatalf("Resume = %v", err)
	}
	if handlerCalls.Load() != 0 {
		t.Fatalf("handler calls = %d", handlerCalls.Load())
	}
}

func TestStatelessPauseResumeStillWorks(t *testing.T) {
	approve := tool.NewRaw("approve", "approve", nil, func(context.Context, json.RawMessage) (string, error) { return "ok", nil }, tool.RequiresApproval())
	a, err := New(newScriptedProvider(&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "call", Name: "approve", Input: json.RawMessage(`{}`)}}}, &ModelResponse{Text: "done"}), "x", WithTools(approve))
	if err != nil {
		t.Fatal(err)
	}
	paused, err := a.Invoke(Background(), "go")
	if err != nil {
		t.Fatal(err)
	}
	if len(paused.Interrupt.Messages) == 0 {
		t.Fatal("stateless pause lost local snapshot")
	}
	if _, err := a.Resume(Background(), paused.Interrupt, Approve()); err != nil {
		t.Fatal(err)
	}
}

func TestLoadInterruptRejectsNonPausedExecution(t *testing.T) {
	conversations, executions := newTestMemoryStore(), newTestExecutionStore()
	a, err := New(newScriptedProvider(&ModelResponse{Text: "done"}), "x", WithConversationStore(conversations), WithExecutionStore(executions))
	if err != nil {
		t.Fatal(err)
	}
	result, err := a.Invoke(Background().WithConversationID("c"), "go")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.LoadInterrupt(context.Background(), result.ExecutionID); !errors.Is(err, ErrInterruptNotFound) {
		t.Fatalf("LoadInterrupt completed execution = %v", err)
	}
}

func TestExecutionStalePauseCannotResumeLaterPause(t *testing.T) {
	approve := tool.NewRaw("approve", "approve", nil, func(context.Context, json.RawMessage) (string, error) { return "ok", nil }, tool.RequiresApproval())
	// The first resume reaches another approval pause in the same execution.
	provider := newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "first", Name: "approve", Input: json.RawMessage(`{}`)}}},
		&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "second", Name: "approve", Input: json.RawMessage(`{}`)}}},
	)
	conversations, executions := newTestMemoryStore(), newTestExecutionStore()
	a, err := New(provider, "x", WithTools(approve), WithConversationStore(conversations), WithExecutionStore(executions))
	if err != nil {
		t.Fatal(err)
	}
	ctx := Background().WithConversationID("c")
	first, err := a.Invoke(ctx, "go")
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.Resume(ctx, first.Interrupt, Approve())
	if err != nil {
		t.Fatal(err)
	}
	if second.Interrupt == nil || second.Interrupt.ExecutionVersion <= first.Interrupt.ExecutionVersion {
		t.Fatalf("second pause = %+v", second.Interrupt)
	}
	if _, err := a.Resume(ctx, first.Interrupt, Approve()); !errors.Is(err, ErrExecutionConflict) {
		t.Fatalf("old pause resume = %v", err)
	}
}

func TestExecutionFailureAndCancellationReachTerminalState(t *testing.T) {
	t.Run("failure", func(t *testing.T) {
		conversations, executions := newTestMemoryStore(), newTestExecutionStore()
		provider := &errorProvider{err: errors.New("provider down")}
		a, err := New(provider, "x", WithConversationStore(conversations), WithExecutionStore(executions))
		if err != nil {
			t.Fatal(err)
		}
		result, err := a.Invoke(Background().WithConversationID("c"), "go")
		if err == nil {
			t.Fatal("Invoke succeeded")
		}
		execution, loadErr := executions.Load(context.Background(), result.ExecutionID)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		if execution.Status != ExecutionFailed {
			t.Fatalf("status = %s", execution.Status)
		}
	})
	t.Run("canceled", func(t *testing.T) {
		conversations, executions := newTestMemoryStore(), newTestExecutionStore()
		provider := &errorProvider{err: context.Canceled}
		a, err := New(provider, "x", WithConversationStore(conversations), WithExecutionStore(executions))
		if err != nil {
			t.Fatal(err)
		}
		result, _ := a.Invoke(Background().WithConversationID("c"), "go")
		execution, loadErr := executions.Load(context.Background(), result.ExecutionID)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		if execution.Status != ExecutionCanceled {
			t.Fatalf("status = %s", execution.Status)
		}
	})
}

func TestWithExecutionIDIsStableAndCollisionConflicts(t *testing.T) {
	conversations, executions := newTestMemoryStore(), newTestExecutionStore()
	a, err := New(newScriptedProvider(&ModelResponse{Text: "one"}, &ModelResponse{Text: "two"}), "x", WithConversationStore(conversations), WithExecutionStore(executions))
	if err != nil {
		t.Fatal(err)
	}
	ctx := Background().WithConversationID("c").WithExecutionID("external-task")
	result, err := a.Invoke(ctx, "go")
	if err != nil {
		t.Fatal(err)
	}
	if result.ExecutionID != "external-task" {
		t.Fatalf("execution ID = %q", result.ExecutionID)
	}
	if _, err := a.Invoke(ctx, "again"); !errors.Is(err, ErrExecutionConflict) {
		t.Fatalf("collision = %v", err)
	}
}

func TestExecutionTerminalIterationTracksToolLoop(t *testing.T) {
	lookup := tool.NewRaw("lookup", "lookup", nil, func(context.Context, json.RawMessage) (string, error) { return "ok", nil })
	conversations, executions := newTestMemoryStore(), newTestExecutionStore()
	a, err := New(newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "lookup-1", Name: "lookup", Input: json.RawMessage(`{}`)}}},
		&ModelResponse{Text: "done"},
	), "x", WithTools(lookup), WithConversationStore(conversations), WithExecutionStore(executions))
	if err != nil {
		t.Fatal(err)
	}
	result, err := a.Invoke(Background().WithConversationID("c"), "go")
	if err != nil {
		t.Fatal(err)
	}
	execution, err := executions.Load(context.Background(), result.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	if execution.Iteration != 2 {
		t.Fatalf("iteration = %d, want 2", execution.Iteration)
	}
}

func TestLoadInterruptMissingPreservesInterruptAndExecutionErrors(t *testing.T) {
	conversations, executions := newTestMemoryStore(), newTestExecutionStore()
	a, err := New(newScriptedProvider(), "x", WithConversationStore(conversations), WithExecutionStore(executions))
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.LoadInterrupt(context.Background(), "missing")
	if !errors.Is(err, ErrInterruptNotFound) || !errors.Is(err, ErrExecutionNotFound) {
		t.Fatalf("LoadInterrupt missing = %v", err)
	}
}

func TestExecutionPauseProjectionDoesNotAliasStoreState(t *testing.T) {
	execution := Execution{
		ID: "exec", Version: 2,
		Pause: &ExecutionPause{Type: InterruptApproval, Approval: &ApprovalInterrupt{Calls: []ApprovalCall{{CallID: "call", Name: "approve", Input: json.RawMessage(`{"amount":1}`)}}}},
	}
	interrupt := interruptFromExecution(execution)
	interrupt.Approval.Calls[0].Input[10] = '9'
	if got := string(execution.Pause.Approval.Calls[0].Input); got != `{"amount":1}` {
		t.Fatalf("interrupt mutation leaked into execution: %s", got)
	}
	in := &Interrupt{Type: InterruptApproval, Approval: &ApprovalInterrupt{Calls: []ApprovalCall{{CallID: "call", Name: "approve", Input: json.RawMessage(`{"amount":1}`)}}}}
	pause := pauseFromInterrupt(in)
	pause.Approval.Calls[0].Input[10] = '9'
	if got := string(in.Approval.Calls[0].Input); got != `{"amount":1}` {
		t.Fatalf("pause mutation leaked into interrupt: %s", got)
	}
}

func TestResumeUsesCanonicalConversationIdentity(t *testing.T) {
	for _, durable := range []bool{false, true} {
		t.Run(map[bool]string{false: "local", true: "durable"}[durable], func(t *testing.T) {
			var seen string
			approve := tool.NewRaw("approve", "approve", nil, func(ctx context.Context, _ json.RawMessage) (string, error) {
				seen = FromContext(ctx).ConversationID()
				return "ok", nil
			}, tool.RequiresApproval())
			conversations := newTestMemoryStore()
			opts := []Option{WithTools(approve), WithConversationStore(conversations)}
			if durable {
				opts = append(opts, WithExecutionStore(newTestExecutionStore()))
			}
			a, err := New(newScriptedProvider(
				&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "call", Name: "approve", Input: json.RawMessage(`{}`)}}},
				&ModelResponse{Text: "done"},
			), "x", opts...)
			if err != nil {
				t.Fatal(err)
			}
			paused, err := a.Invoke(Background().WithConversationID("canonical"), "go")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := a.Resume(Background().WithConversationID("other"), paused.Interrupt, Approve()); err != nil {
				t.Fatal(err)
			}
			if seen != "canonical" {
				t.Fatalf("handler conversation = %q, want canonical", seen)
			}
			canonical, err := conversations.Load(context.Background(), "canonical")
			if err != nil || len(canonical.Messages) == 0 {
				t.Fatalf("canonical transcript = %#v, %v", canonical, err)
			}
			other, err := conversations.Load(context.Background(), "other")
			if err != nil || len(other.Messages) != 0 {
				t.Fatalf("other transcript = %#v, %v", other, err)
			}
		})
	}
}

func TestDurableResumeAccountingIsCumulative(t *testing.T) {
	approve := tool.NewRaw("approve", "approve", nil, func(context.Context, json.RawMessage) (string, error) { return "ok", nil }, tool.RequiresApproval())
	conversations, executions := newTestMemoryStore(), newTestExecutionStore()
	a, err := New(newScriptedProvider(
		&ModelResponse{Usage: TokenUsage{InputTokens: 60}, ToolCalls: []tool.Call{{ToolUseID: "first", Name: "approve", Input: json.RawMessage(`{}`)}}},
		&ModelResponse{Usage: TokenUsage{InputTokens: 60}, Text: "should exceed budget"},
	), "x", WithTools(approve), WithConversationStore(conversations), WithExecutionStore(executions), WithTokenBudget(100))
	if err != nil {
		t.Fatal(err)
	}
	paused, err := a.Invoke(Background().WithConversationID("c"), "go")
	if err != nil {
		t.Fatal(err)
	}
	if paused.Usage.InputTokens != 60 {
		t.Fatalf("first usage = %+v", paused.Usage)
	}
	res, err := a.Resume(Background().WithConversationID("other"), paused.Interrupt, Approve())
	if !errors.Is(err, ErrTokenBudgetExceeded) {
		t.Fatalf("Resume err = %v, want token budget", err)
	}
	if res.Usage.InputTokens != 120 {
		t.Fatalf("result usage = %+v, want 120 input", res.Usage)
	}
	execution, err := executions.Load(context.Background(), paused.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	if execution.Usage != res.Usage || execution.Iteration != 2 {
		t.Fatalf("execution = %+v, result = %+v", execution, res)
	}
}

func TestDurableResumeMaxIterationsIsExecutionWide(t *testing.T) {
	approve := tool.NewRaw("approve", "approve", nil, func(context.Context, json.RawMessage) (string, error) { return "ok", nil }, tool.RequiresApproval())
	conversations, executions := newTestMemoryStore(), newTestExecutionStore()
	a, err := New(newScriptedProvider(&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "call", Name: "approve", Input: json.RawMessage(`{}`)}}}), "x", WithTools(approve), WithConversationStore(conversations), WithExecutionStore(executions), WithMaxIterations(1))
	if err != nil {
		t.Fatal(err)
	}
	paused, err := a.Invoke(Background().WithConversationID("c"), "go")
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Resume(Background().WithConversationID("other"), paused.Interrupt, Approve())
	var limit *MaxIterationsError
	if !errors.As(err, &limit) {
		t.Fatalf("Resume err = %v, want MaxIterationsError", err)
	}
	execution, err := executions.Load(context.Background(), paused.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	if execution.Iteration != 1 {
		t.Fatalf("execution iteration = %d, want 1", execution.Iteration)
	}
}

type failFinalPauseExecutionStore struct {
	*testExecutionStore
	failed atomic.Bool
}

func (s *failFinalPauseExecutionStore) Save(ctx context.Context, e Execution, expected uint64) (Execution, error) {
	if e.Status == ExecutionPaused && s.failed.CompareAndSwap(false, true) {
		return Execution{}, errors.New("injected final pause save failure")
	}
	return s.testExecutionStore.Save(ctx, e, expected)
}

func TestRecoverHumanInputPauseAfterFinalPauseSaveFailure(t *testing.T) {
	conversations := newTestMemoryStore()
	executions := &failFinalPauseExecutionStore{testExecutionStore: newTestExecutionStore()}
	human := NewHumanInputTool("ask", "ask a question")
	a, err := New(newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "ask-1", Name: "ask", Input: json.RawMessage(`{"reason":"need input","question":"continue?"}`)}}},
		&ModelResponse{Text: "done"},
	), "x", WithTools(human), WithConversationStore(conversations), WithExecutionStore(executions))
	if err != nil {
		t.Fatal(err)
	}
	paused, err := a.Invoke(Background().WithConversationID("c"), "go")
	if err == nil || paused.Interrupt == nil || paused.Interrupt.Type != InterruptHumanInput {
		t.Fatalf("Invoke = %+v, %v", paused, err)
	}
	recovered, err := a.RecoverExecution(Background(), paused.ExecutionID)
	if err != nil || recovered.Status != ExecutionPaused || recovered.Pause == nil || recovered.Pause.Input == nil {
		t.Fatalf("RecoverExecution = %+v, %v", recovered, err)
	}
	loaded, err := a.LoadInterrupt(Background(), paused.ExecutionID)
	if err != nil || loaded.Input.Question != "continue?" {
		t.Fatalf("LoadInterrupt = %+v, %v", loaded, err)
	}
	res, err := a.Resume(Background().WithConversationID("other"), loaded, Respond("yes"))
	if err != nil || res.Text != "done" {
		t.Fatalf("Resume = %+v, %v", res, err)
	}
	snapshot, err := conversations.Load(context.Background(), "c")
	if err != nil {
		t.Fatal(err)
	}
	results := 0
	for _, message := range snapshot.Messages {
		for _, block := range message.Content {
			if _, ok := block.(ToolResultBlock); ok {
				results++
			}
		}
	}
	if results != 1 {
		t.Fatalf("tool results = %d, want exactly one", results)
	}
	if _, err := a.Resume(Background(), loaded, Respond("again")); !errors.Is(err, ErrExecutionConflict) {
		t.Fatalf("second Resume = %v, want execution conflict", err)
	}
}

func TestDurableMultipleResumesAccumulateUsageAndIterations(t *testing.T) {
	approve := tool.NewRaw("approve", "approve", nil, func(context.Context, json.RawMessage) (string, error) { return "ok", nil }, tool.RequiresApproval())
	conversations, executions := newTestMemoryStore(), newTestExecutionStore()
	a, err := New(newScriptedProvider(
		&ModelResponse{Usage: TokenUsage{InputTokens: 10}, ToolCalls: []tool.Call{{ToolUseID: "first", Name: "approve", Input: json.RawMessage(`{}`)}}},
		&ModelResponse{Usage: TokenUsage{InputTokens: 20}, ToolCalls: []tool.Call{{ToolUseID: "second", Name: "approve", Input: json.RawMessage(`{}`)}}},
		&ModelResponse{Usage: TokenUsage{InputTokens: 30}, Text: "done"},
	), "x", WithTools(approve), WithConversationStore(conversations), WithExecutionStore(executions))
	if err != nil {
		t.Fatal(err)
	}
	first, err := a.Invoke(Background().WithConversationID("c"), "go")
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.Resume(Background().WithConversationID("other"), first.Interrupt, Approve())
	if err != nil {
		t.Fatal(err)
	}
	if second.Interrupt == nil || second.Usage.InputTokens != 30 {
		t.Fatalf("second pause = %+v", second)
	}
	final, err := a.Resume(Background().WithConversationID("another"), second.Interrupt, Approve())
	if err != nil || final.Text != "done" || final.Usage.InputTokens != 60 {
		t.Fatalf("final Resume = %+v, %v", final, err)
	}
	execution, err := executions.Load(context.Background(), first.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	if execution.Usage != final.Usage || execution.Iteration != 3 {
		t.Fatalf("execution = %+v, final = %+v", execution, final)
	}
}
