package executionstore

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/checkpoint"
)

func sample(id string) agent.Execution {
	return agent.Execution{
		ID:             id,
		ConversationID: "conversation-1",
		Revision:       7,
		LastSequence:   21,
		Status:         agent.ExecutionPaused,
		Phase:          agent.ExecutionPhasePaused,
		Iteration:      2,
		Usage:          agent.TokenUsage{InputTokens: 10, OutputTokens: 3},
		Pause: &agent.ExecutionPause{Type: agent.InterruptHumanInput, Input: &agent.InputInterrupt{
			Reason: "missing detail", Question: "Which order?",
		}},
	}
}

func TestExecutionStoreCreateLoadSaveCAS(t *testing.T) {
	store := New(checkpoint.NewMemory())
	ctx := context.Background()
	created, err := store.Create(ctx, sample("exec-1"))
	if err != nil || created.Version != 1 {
		t.Fatalf("Create = %+v, %v", created, err)
	}
	if _, err := store.Create(ctx, sample("exec-1")); !errors.Is(err, agent.ErrExecutionConflict) {
		t.Fatalf("duplicate Create = %v", err)
	}
	loaded, err := store.Load(ctx, "exec-1")
	if err != nil || loaded.Version != 1 || loaded.Pause == nil || loaded.Pause.Input.Question != "Which order?" {
		t.Fatalf("Load = %+v, %v", loaded, err)
	}
	loaded.Status = agent.ExecutionRunning
	loaded.Phase = agent.ExecutionPhaseTools
	loaded.Pause = nil
	saved, err := store.Save(ctx, loaded, 1)
	if err != nil || saved.Version != 2 || saved.Status != agent.ExecutionRunning {
		t.Fatalf("Save = %+v, %v", saved, err)
	}
	if _, err := store.Save(ctx, loaded, 1); !errors.Is(err, agent.ErrExecutionConflict) {
		t.Fatalf("stale Save = %v", err)
	}
	if _, err := store.Load(ctx, "missing"); !errors.Is(err, agent.ErrExecutionNotFound) {
		t.Fatalf("missing Load = %v", err)
	}
}

func TestExecutionStoreConcurrentSaveOneWinner(t *testing.T) {
	store := New(checkpoint.NewMemory())
	ctx := context.Background()
	created, err := store.Create(ctx, sample("exec-race"))
	if err != nil {
		t.Fatal(err)
	}
	const contenders = 32
	var winners atomic.Int32
	var wg sync.WaitGroup
	wg.Add(contenders)
	for range contenders {
		go func() {
			defer wg.Done()
			execution := created
			execution.Status = agent.ExecutionRunning
			if _, err := store.Save(ctx, execution, created.Version); err == nil {
				winners.Add(1)
			} else if !errors.Is(err, agent.ErrExecutionConflict) {
				t.Errorf("Save: %v", err)
			}
		}()
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("winners = %d, want 1", winners.Load())
	}
}

func TestExecutionStorePayloadHasNoTranscript(t *testing.T) {
	cp := checkpoint.NewMemory()
	store := New(cp)
	if _, err := store.Create(context.Background(), sample("exec-json")); err != nil {
		t.Fatal(err)
	}
	stored, err := cp.Load(context.Background(), DefaultThreadPrefix+"exec-json")
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(stored.Extra, &object); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"Messages", "messages", "history", "transcript"} {
		if _, exists := object[forbidden]; exists {
			t.Fatalf("payload contains forbidden field %q: %s", forbidden, stored.Extra)
		}
	}
}

func TestExecutionStorePrefixAndBadPayload(t *testing.T) {
	cp := checkpoint.NewMemory()
	store := New(cp, WithThreadPrefix("exec/"))
	if _, err := store.Create(context.Background(), sample("one")); err != nil {
		t.Fatal(err)
	}
	ids, err := cp.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "exec/one" {
		t.Fatalf("ids = %v", ids)
	}
	if _, err := cp.Save(context.Background(), "exec/bad", checkpoint.Checkpoint{Label: checkpointLabel, Extra: []byte(`{broken`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(context.Background(), "bad"); err == nil {
		t.Fatal("Load accepted invalid execution payload")
	}
}
