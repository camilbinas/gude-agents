package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/camilbinas/gude-agents/agent/tool"
)

func TestEngineAppendsOnlyNewTurnMessages(t *testing.T) {
	store := newTestMemoryStore()
	provider := newScriptedProvider(&ModelResponse{Text: "one"}, &ModelResponse{Text: "two"})
	a, err := New(provider, "system", WithConversationStore(store))
	if err != nil {
		t.Fatal(err)
	}
	ctx := Background().WithConversationID("c")
	if _, err := a.Invoke(ctx, "first"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Invoke(ctx, "second"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Load(ctx, "c")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Revision != 2 || len(snapshot.Messages) != 4 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	for i, want := range []string{"first", "one", "second", "two"} {
		if got := snapshot.Messages[i].Content[0].(TextBlock).Text; got != want {
			t.Fatalf("message %d=%q,want %q", i, got, want)
		}
	}
}

func TestPersistentExecutionStoresCursorNotTranscript(t *testing.T) {
	approval := tool.NewRaw("approve", "approval", nil, func(context.Context, json.RawMessage) (string, error) { return "ok", nil }, tool.RequiresApproval())
	provider := newScriptedProvider(&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "call", Name: "approve", Input: json.RawMessage(`{}`)}}}, &ModelResponse{Text: "done"})
	store := newTestMemoryStore()
	executions := newTestExecutionStore()
	a, err := New(provider, "system", WithConversationStore(store), WithExecutionStore(executions), WithTools(approval))
	if err != nil {
		t.Fatal(err)
	}
	ctx := Background().WithConversationID("c")
	paused, err := a.Invoke(ctx, "go")
	if err != nil {
		t.Fatal(err)
	}
	if paused.Interrupt == nil || paused.Interrupt.LastSequence == 0 || len(paused.Interrupt.Messages) != 0 {
		t.Fatalf("interrupt=%+v", paused.Interrupt)
	}
	if _, err := a.Resume(ctx, paused.Interrupt, Approve()); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := store.Load(ctx, "c")
	if len(snapshot.Messages) < 4 {
		t.Fatalf("resume did not append canonical events: %+v", snapshot)
	}
}
