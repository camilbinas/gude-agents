package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// RunAgent drives real Agents through pause, resume, recovery, and
// reconciliation on top of the store. It proves the engine's durable protocol
// works with this backend, not just that the store honors its own contract, and
// it uses only the public agent API.
func RunAgent(t *testing.T, newStore Factory) {
	t.Helper()

	t.Run("PauseIsResumableByAnotherAgentAndReplayLosesTheCAS", func(t *testing.T) {
		store, conversations := newStore(t), NewConversations()
		first := newGatedAgent(t, store, conversations, gatedCall())
		ctx := agent.Background().WithConversationID("agent-pause")
		paused, err := first.agent.Invoke(ctx, "go")
		if err != nil || paused.Interrupt == nil || paused.Interrupt.Type != agent.InterruptApproval {
			t.Fatalf("Invoke = %+v, %v; want an approval interrupt", paused, err)
		}
		if first.ran.Load() != 0 {
			t.Fatal("the gated tool ran before approval")
		}

		second := newGatedAgent(t, store, conversations, &agent.ModelResponse{Text: "resumed elsewhere"})
		loaded, err := second.agent.LoadInterrupt(context.Background(), paused.ExecutionID)
		if err != nil {
			t.Fatalf("LoadInterrupt: %v", err)
		}
		if loaded.ExecutionVersion != paused.Interrupt.ExecutionVersion || loaded.ConversationID != "agent-pause" {
			t.Fatalf("loaded interrupt = %+v, want the paused version and conversation", loaded)
		}
		result, err := second.agent.Resume(agent.Background(), loaded, agent.Approve())
		if err != nil || result.Text != "resumed elsewhere" {
			t.Fatalf("Resume = %+v, %v", result, err)
		}
		if first.ran.Load() != 0 || second.ran.Load() != 1 {
			t.Fatalf("handler runs: first=%d second=%d, want 0 and 1", first.ran.Load(), second.ran.Load())
		}
		if _, err := first.agent.Resume(ctx, paused.Interrupt, agent.Approve()); !errors.Is(err, agent.ErrExecutionConflict) {
			t.Fatalf("replayed Resume = %v, want ErrExecutionConflict", err)
		}
	})

	t.Run("ConcurrentResumesHaveExactlyOneWinner", func(t *testing.T) {
		store, conversations := newStore(t), NewConversations()
		ctx := agent.Background().WithConversationID("agent-race")
		paused, err := newGatedAgent(t, store, conversations, gatedCall()).agent.Invoke(ctx, "go")
		if err != nil || paused.Interrupt == nil {
			t.Fatalf("Invoke = %+v, %v", paused, err)
		}

		const contenders = 8
		contender := make([]*pausingAgent, contenders)
		for i := range contender {
			contender[i] = newGatedAgent(t, store, conversations, &agent.ModelResponse{Text: "winner"})
		}
		var winners atomic.Int32
		var wg sync.WaitGroup
		wg.Add(contenders)
		for i := range contenders {
			go func() {
				defer wg.Done()
				switch _, err := contender[i].agent.Resume(agent.Background(), paused.Interrupt, agent.Approve()); {
				case err == nil:
					winners.Add(1)
				case !errors.Is(err, agent.ErrExecutionConflict):
					t.Errorf("Resume: %v", err)
				}
			}()
		}
		wg.Wait()
		if got := winners.Load(); got != 1 {
			t.Fatalf("winners = %d, want 1", got)
		}
		var ran int32
		for _, c := range contender {
			ran += c.ran.Load()
		}
		if ran != 1 {
			t.Fatalf("the approved tool ran %d times, want exactly once", ran)
		}
	})

	t.Run("ReplaySafeInFlightCallIsRecoveredOnceWithTheSameKey", func(t *testing.T) {
		store, conversations := newStore(t), NewConversations()
		var mu sync.Mutex
		var keys []string
		var calls int
		charge := tool.NewRaw("charge", "charge", nil, func(ctx context.Context, _ json.RawMessage) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			calls++
			key, _ := tool.IdempotencyKey(ctx)
			keys = append(keys, key)
			if calls == 1 {
				// The first attempt dies with an unknown outcome and leaves the
				// call in flight, as a crash after dispatch would.
				return "", tool.OutcomeUnknown(errors.New("connection lost"))
			}
			return "charged", nil
		}, tool.WithReplaySafe())
		a := newAgentWith(t, store, conversations, &scriptedProvider{responses: []*agent.ModelResponse{
			{ToolCalls: []tool.Call{{ToolUseID: "charge-1", Name: "charge", Input: json.RawMessage(`{"amount":7}`)}}},
		}}, charge)

		res, err := a.Invoke(agent.Background().WithConversationID("agent-recover"), "pay")
		if !errors.Is(err, agent.ErrToolExecutionUncertain) {
			t.Fatalf("Invoke error = %v, want ErrToolExecutionUncertain", err)
		}
		stored, err := store.Load(context.Background(), res.ExecutionID)
		if err != nil || stored.ToolBatch == nil || stored.ToolBatch.Calls[0].Status != agent.ToolExecutionInFlight || !stored.ToolBatch.Calls[0].ReplaySafe {
			t.Fatalf("stored execution = %+v, %v; want a replay-safe call left in flight", stored, err)
		}

		recovered, err := a.RecoverExecution(agent.Background(), res.ExecutionID)
		if err != nil || recovered.ToolBatch != nil {
			t.Fatalf("RecoverExecution = %+v, %v; want the batch cleared", recovered, err)
		}
		mu.Lock()
		defer mu.Unlock()
		if calls != 2 || keys[0] == "" || keys[0] != keys[1] {
			t.Fatalf("handler calls=%d keys=%v; want one replay under the original idempotency key", calls, keys)
		}
	})

	t.Run("UnsafeInFlightCallNeedsCASProtectedReconciliation", func(t *testing.T) {
		store, conversations := newStore(t), NewConversations()
		var calls atomic.Int32
		charge := tool.NewRaw("charge", "charge", nil, func(context.Context, json.RawMessage) (string, error) {
			calls.Add(1)
			return "", tool.OutcomeUnknown(errors.New("connection lost"))
		})
		a := newAgentWith(t, store, conversations, &scriptedProvider{responses: []*agent.ModelResponse{
			{ToolCalls: []tool.Call{{ToolUseID: "charge-1", Name: "charge", Input: json.RawMessage(`{}`)}}},
		}}, charge)

		res, err := a.Invoke(agent.Background().WithConversationID("agent-reconcile"), "pay")
		if !errors.Is(err, agent.ErrToolExecutionUncertain) {
			t.Fatalf("Invoke error = %v, want ErrToolExecutionUncertain", err)
		}
		if _, err := a.RecoverExecution(agent.Background(), res.ExecutionID); !errors.Is(err, agent.ErrToolExecutionUncertain) {
			t.Fatalf("RecoverExecution error = %v, want ErrToolExecutionUncertain", err)
		}
		if calls.Load() != 1 {
			t.Fatalf("handler calls = %d; an unsafe call must never be replayed", calls.Load())
		}

		stored, err := store.Load(context.Background(), res.ExecutionID)
		if err != nil {
			t.Fatal(err)
		}
		resolution := agent.ToolResolution{Outcome: agent.ToolResolutionSucceeded, Output: "charged out of band"}
		if _, err := a.ReconcileToolExecution(agent.Background(), res.ExecutionID, stored.Version+1, "charge-1", resolution); !errors.Is(err, agent.ErrExecutionConflict) {
			t.Fatalf("Reconcile with a stale version = %v, want ErrExecutionConflict", err)
		}
		reconciled, err := a.ReconcileToolExecution(agent.Background(), res.ExecutionID, stored.Version, "charge-1", resolution)
		if err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		if reconciled.ToolBatch != nil {
			t.Fatalf("reconciled execution still has a tool batch: %+v", reconciled.ToolBatch)
		}
		snapshot, err := conversations.Load(context.Background(), "agent-reconcile")
		if err != nil || len(snapshot.Messages) != 3 {
			t.Fatalf("transcript = %d messages, %v; want user, tool use, and the reconciled result", len(snapshot.Messages), err)
		}
	})
}

type scriptedProvider struct {
	mu        sync.Mutex
	responses []*agent.ModelResponse
	next      int
}

func (p *scriptedProvider) Name() string { return "scripted" }

func (p *scriptedProvider) Stream(context.Context, agent.ModelRequest, func(agent.ModelEvent)) (*agent.ModelResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.next >= len(p.responses) {
		return nil, errors.New("scripted provider: no more responses")
	}
	response := p.responses[p.next]
	p.next++
	return response, nil
}

type pausingAgent struct {
	agent *agent.Agent
	ran   *atomic.Int32
}

// gatedCall is a model turn that calls the approval-gated tool.
func gatedCall() *agent.ModelResponse {
	return &agent.ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "call-1", Name: "approve", Input: json.RawMessage(`{}`)}}}
}

// newGatedAgent builds an agent that has an approval-gated tool and answers
// with the given scripted model turns. ran counts executions of the tool.
func newGatedAgent(t *testing.T, store agent.ExecutionStore, conversations agent.ConversationStore, responses ...*agent.ModelResponse) *pausingAgent {
	t.Helper()
	ran := new(atomic.Int32)
	gated := tool.NewRaw("approve", "needs approval", nil, func(context.Context, json.RawMessage) (string, error) {
		ran.Add(1)
		return "ok", nil
	}, tool.RequiresApproval())
	// A pause leaves the run waiting; the engine asks the model again only
	// after approval, so a pausing agent needs just the first scripted turn.
	return &pausingAgent{agent: newAgentWith(t, store, conversations, &scriptedProvider{responses: responses}, gated), ran: ran}
}

func newAgentWith(t *testing.T, store agent.ExecutionStore, conversations agent.ConversationStore, provider agent.Provider, tools ...tool.Tool) *agent.Agent {
	t.Helper()
	a, err := agent.New(provider, "sys",
		agent.WithTools(tools...),
		agent.WithConversationStore(conversations),
		agent.WithExecutionStore(store),
	)
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	return a
}
