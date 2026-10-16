// Package conformance is the behavior every agent.ExecutionStore in the
// agent/execution family must share. Each backend's tests call Run against a
// real or faithfully faked store.
//
// It is internal so it cannot become a public second contract: the contract is
// agent.ExecutionStore, and this package only verifies implementations of it.
package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
)

// Factory returns an empty store. Subtests use distinct execution IDs, so a
// shared backing service is acceptable as long as the IDs are unused.
type Factory func(t *testing.T) agent.ExecutionStore

// Full returns an execution with every persisted field populated, so a
// round trip proves nothing is dropped by serialization.
func Full(id string) agent.Execution {
	pause := &agent.ExecutionPause{
		Type: agent.InterruptApproval,
		Approval: &agent.ApprovalInterrupt{Calls: []agent.ApprovalCall{
			{CallID: "call-approve", Name: "delete_order", Input: json.RawMessage(`{"order_id":"42"}`)},
		}},
	}
	return agent.Execution{
		ID:                   id,
		ConversationID:       "conversation-1",
		Revision:             7,
		LastSequence:         21,
		StartRevision:        5,
		StartLastSequence:    17,
		StartCursorPersisted: true,
		Status:               agent.ExecutionPaused,
		Phase:                agent.ExecutionPhasePaused,
		Iteration:            3,
		Pause:                pause,
		PendingPause: &agent.ExecutionPause{
			Type:  agent.InterruptHumanInput,
			Input: &agent.InputInterrupt{Reason: "missing detail", Question: "Which order?"},
		},
		ToolBatch: &agent.ToolBatchExecution{
			Iteration:           3,
			ToolUseRevision:     6,
			ToolUseLastSequence: 19,
			Calls: []agent.ToolExecution{
				{
					CallID: "call-1", Name: "charge", Status: agent.ToolExecutionInFlight,
					IdempotencyKey: "key-1", InputHash: "hash-1", ReplaySafe: true,
					Approval: &agent.ToolApprovalDecision{Allow: false, Reason: "too risky"},
				},
				{
					CallID: "call-2", Name: "lookup", Status: agent.ToolExecutionReady,
					IdempotencyKey: "key-2", InputHash: "hash-2", Input: json.RawMessage(`{"q":"x"}`),
				},
				{CallID: "call-3", Name: "done", Status: agent.ToolExecutionCompleted, IdempotencyKey: "key-3", InputHash: "hash-3"},
			},
			Resolution: &agent.ToolResolutionClaim{
				CallID: "call-1", Outcome: agent.ToolResolutionFailed, Output: "out", ErrorMessage: "boom",
			},
		},
		Usage: agent.TokenUsage{InputTokens: 10, OutputTokens: 3, CacheReadTokens: 2, CacheWriteTokens: 1},
	}
}

// Run exercises the ExecutionStore contract.
func Run(t *testing.T, newStore Factory) {
	t.Helper()
	ctx := context.Background()

	t.Run("CreateAssignsVersionOneAndLoadReturnsIt", func(t *testing.T) {
		s := newStore(t)
		created, err := s.Create(ctx, Full("conf-create"))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if created.Version != 1 {
			t.Fatalf("Create version = %d, want 1", created.Version)
		}
		loaded, err := s.Load(ctx, "conf-create")
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if loaded.Version != 1 {
			t.Fatalf("Load version = %d, want 1", loaded.Version)
		}
	})

	t.Run("CreateRequiresAbsentID", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.Create(ctx, Full("conf-dup")); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if _, err := s.Create(ctx, Full("conf-dup")); !errors.Is(err, agent.ErrExecutionConflict) {
			t.Fatalf("duplicate Create = %v, want ErrExecutionConflict", err)
		}
		// The failed create must not have disturbed the stored record.
		loaded, err := s.Load(ctx, "conf-dup")
		if err != nil || loaded.Version != 1 {
			t.Fatalf("Load after duplicate Create = %+v, %v", loaded, err)
		}
	})

	t.Run("EmptyIDIsRejected", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.Create(ctx, agent.Execution{}); err == nil {
			t.Error("Create accepted an empty ID")
		}
		if _, err := s.Load(ctx, ""); err == nil {
			t.Error("Load accepted an empty ID")
		}
		if _, err := s.Save(ctx, agent.Execution{}, 1); err == nil {
			t.Error("Save accepted an empty ID")
		}
	})

	t.Run("LoadMissingIsNotFound", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.Load(ctx, "conf-absent"); !errors.Is(err, agent.ErrExecutionNotFound) {
			t.Fatalf("Load = %v, want ErrExecutionNotFound", err)
		}
	})

	t.Run("SaveRequiresTheExactCurrentVersion", func(t *testing.T) {
		s := newStore(t)
		created, err := s.Create(ctx, Full("conf-cas"))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		next := created
		next.Status = agent.ExecutionRunning
		next.Pause = nil
		saved, err := s.Save(ctx, next, created.Version)
		if err != nil {
			t.Fatalf("Save: %v", err)
		}
		if saved.Version != 2 || saved.Status != agent.ExecutionRunning {
			t.Fatalf("Save = version %d status %q, want 2 running", saved.Version, saved.Status)
		}
		for _, expected := range []uint64{0, 1, 3, 99} {
			if _, err := s.Save(ctx, next, expected); !errors.Is(err, agent.ErrExecutionConflict) {
				t.Errorf("Save at expected %d = %v, want ErrExecutionConflict", expected, err)
			}
		}
		loaded, err := s.Load(ctx, "conf-cas")
		if err != nil || loaded.Version != 2 {
			t.Fatalf("Load after conflicts = %+v, %v; the version must still be 2", loaded, err)
		}
	})

	t.Run("SaveOfMissingExecutionIsConflict", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.Save(ctx, Full("conf-save-missing"), 1); !errors.Is(err, agent.ErrExecutionConflict) {
			t.Fatalf("Save of a missing execution = %v, want ErrExecutionConflict", err)
		}
		if _, err := s.Load(ctx, "conf-save-missing"); !errors.Is(err, agent.ErrExecutionNotFound) {
			t.Fatalf("a failed Save must not create the record: Load = %v", err)
		}
	})

	t.Run("BackendOwnsTheVersion", func(t *testing.T) {
		s := newStore(t)
		in := Full("conf-version-owner")
		in.Version = 999
		created, err := s.Create(ctx, in)
		if err != nil || created.Version != 1 {
			t.Fatalf("Create = version %d, %v; a caller-supplied version must be ignored", created.Version, err)
		}
		in.Version = 12345
		saved, err := s.Save(ctx, in, 1)
		if err != nil || saved.Version != 2 {
			t.Fatalf("Save = version %d, %v; the backend assigns the next version", saved.Version, err)
		}
		loaded, err := s.Load(ctx, "conf-version-owner")
		if err != nil || loaded.Version != saved.Version {
			t.Fatalf("Load = version %d, %v; want the Save result %d", loaded.Version, err, saved.Version)
		}
	})

	t.Run("SaveHasExactlyOneConcurrentWinner", func(t *testing.T) {
		s := newStore(t)
		created, err := s.Create(ctx, Full("conf-race"))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		const contenders = 16
		var winners atomic.Int32
		var wg sync.WaitGroup
		wg.Add(contenders)
		for range contenders {
			go func() {
				defer wg.Done()
				next := created
				next.Status = agent.ExecutionRunning
				switch _, err := s.Save(ctx, next, created.Version); {
				case err == nil:
					winners.Add(1)
				case !errors.Is(err, agent.ErrExecutionConflict):
					t.Errorf("Save: %v", err)
				}
			}()
		}
		wg.Wait()
		if got := winners.Load(); got != 1 {
			t.Fatalf("winners = %d, want 1", got)
		}
		loaded, err := s.Load(ctx, "conf-race")
		if err != nil || loaded.Version != 2 {
			t.Fatalf("Load = %+v, %v; want version 2", loaded, err)
		}
	})

	t.Run("ConcurrentCreateHasExactlyOneWinner", func(t *testing.T) {
		s := newStore(t)
		const contenders = 16
		var winners atomic.Int32
		var wg sync.WaitGroup
		wg.Add(contenders)
		for range contenders {
			go func() {
				defer wg.Done()
				switch _, err := s.Create(ctx, Full("conf-create-race")); {
				case err == nil:
					winners.Add(1)
				case !errors.Is(err, agent.ErrExecutionConflict):
					t.Errorf("Create: %v", err)
				}
			}()
		}
		wg.Wait()
		if got := winners.Load(); got != 1 {
			t.Fatalf("winners = %d, want 1", got)
		}
	})

	t.Run("EveryFieldSurvivesSerialization", func(t *testing.T) {
		s := newStore(t)
		in := Full("conf-roundtrip")
		created, err := s.Create(ctx, in)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		want := in
		want.Version = 1
		if !reflect.DeepEqual(created, want) {
			t.Fatalf("Create returned a different execution:\n got %+v\nwant %+v", created, want)
		}
		loaded, err := s.Load(ctx, "conf-roundtrip")
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if !reflect.DeepEqual(loaded, want) {
			t.Fatalf("Load returned a different execution:\n got %+v\nwant %+v", loaded, want)
		}

		// Clearing optional state must persist as cleared, not as the old value.
		cleared := loaded
		cleared.Pause, cleared.PendingPause, cleared.ToolBatch = nil, nil, nil
		cleared.Status, cleared.Phase = agent.ExecutionRunning, agent.ExecutionPhaseModel
		saved, err := s.Save(ctx, cleared, loaded.Version)
		if err != nil {
			t.Fatalf("Save: %v", err)
		}
		reloaded, err := s.Load(ctx, "conf-roundtrip")
		if err != nil {
			t.Fatalf("Load after Save: %v", err)
		}
		cleared.Version = 2
		if !reflect.DeepEqual(saved, cleared) || !reflect.DeepEqual(reloaded, cleared) {
			t.Fatalf("cleared execution did not round trip:\n saved  %+v\n loaded %+v\n want   %+v", saved, reloaded, cleared)
		}
	})

	t.Run("PayloadContainsNoTranscript", func(t *testing.T) {
		data, err := json.Marshal(Full("conf-json"))
		if err != nil {
			t.Fatal(err)
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(data, &object); err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"Messages", "messages", "history", "transcript"} {
			if _, exists := object[forbidden]; exists {
				t.Fatalf("execution payload has a %q field: %s", forbidden, data)
			}
		}
	})

	t.Run("ReturnedValuesAreIsolatedFromTheStore", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.Create(ctx, Full("conf-isolation")); err != nil {
			t.Fatalf("Create: %v", err)
		}
		first, err := s.Load(ctx, "conf-isolation")
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		first.ToolBatch.Calls[0].Name = "mutated"
		first.Pause.Approval.Calls[0].Name = "mutated"
		second, err := s.Load(ctx, "conf-isolation")
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if second.ToolBatch.Calls[0].Name != "charge" || second.Pause.Approval.Calls[0].Name != "delete_order" {
			t.Fatalf("mutating a loaded execution changed stored state: %+v", second)
		}
	})

	t.Run("ExecutionsHaveIndependentVersions", func(t *testing.T) {
		s := newStore(t)
		a, err := s.Create(ctx, Full("conf-iso-a"))
		if err != nil {
			t.Fatalf("Create a: %v", err)
		}
		if _, err := s.Save(ctx, a, a.Version); err != nil {
			t.Fatalf("Save a: %v", err)
		}
		b, err := s.Create(ctx, Full("conf-iso-b"))
		if err != nil || b.Version != 1 {
			t.Fatalf("Create b = version %d, %v; versions are per execution", b.Version, err)
		}
	})
}
