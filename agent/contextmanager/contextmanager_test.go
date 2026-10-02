package contextmanager

import (
	"context"
	"strings"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
)

func message(text string) agent.Message {
	return agent.Message{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: text}}}
}

func TestRollingSummaryPreservesCanonicalHistoryAndUsesRangeBoundary(t *testing.T) {
	store := conversation.NewInMemory()
	ctx := context.Background()
	messages := []agent.Message{message(strings.Repeat("a", 100)), message(strings.Repeat("b", 100)), message(strings.Repeat("c", 100)), message(strings.Repeat("d", 100)), message(strings.Repeat("e", 100)), message(strings.Repeat("f", 100))}
	if _, err := store.Append(ctx, "c", messages, 0); err != nil {
		t.Fatal(err)
	}
	calls := 0
	manager, err := NewRollingSummary(store, func(_ context.Context, existing string, covered []agent.Message) (string, error) {
		calls++
		return existing + " summary-" + string(rune('0'+len(covered))), nil
	}, WithMaxInputTokens(20), WithPreserveRecentTurns(1))
	if err != nil {
		t.Fatal(err)
	}
	boundary, err := manager.HistoryBoundary(ctx, "c")
	if err != nil || boundary != 0 {
		t.Fatalf("boundary=%d,%v", boundary, err)
	}
	tail, err := store.LoadAfter(ctx, "c", boundary)
	if err != nil {
		t.Fatal(err)
	}
	out, err := manager.Prepare(ctx, agent.ContextManagerInput{ConversationID: "c", Recent: tail.Messages, Revision: tail.Revision, LastSequence: tail.LastSequence})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(out.Messages) < 3 {
		t.Fatalf("calls=%d output=%#v", calls, out.Messages)
	}
	// Summary is derived; every original event stays in the canonical log.
	all, _ := store.Load(ctx, "c")
	if len(all.Messages) != 6 || all.Revision != 1 || all.LastSequence != 6 {
		t.Fatalf("canonical=%+v", all)
	}
	state, _ := store.LoadContextState(ctx, "c", "rolling_summary")
	if len(state.Data) == 0 {
		t.Fatal("summary state was not persisted")
	}
	boundary, err = manager.HistoryBoundary(ctx, "c")
	if err != nil || boundary == 0 {
		t.Fatalf("updated boundary=%d,%v", boundary, err)
	}
	recent, _ := store.LoadAfter(ctx, "c", boundary)
	if len(recent.Messages) >= len(all.Messages) {
		t.Fatalf("range read did not reduce history: %d", len(recent.Messages))
	}
}

func TestWindowUsesTailAndKeepsToolPairs(t *testing.T) {
	store := conversation.NewInMemory()
	ctx := context.Background()
	events := []agent.Message{
		message("old"),
		{Role: agent.RoleAssistant, Content: []agent.ContentBlock{agent.ToolUseBlock{ToolUseID: "x", Name: "t", Input: []byte(`{}`)}}},
		{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.ToolResultBlock{ToolUseID: "x", Content: "ok"}}},
		message("new"),
	}
	if _, err := store.Append(ctx, "c", events, 0); err != nil {
		t.Fatal(err)
	}
	w, err := NewWindow(store, 2)
	if err != nil {
		t.Fatal(err)
	}
	boundary, err := w.HistoryBoundary(ctx, "c")
	if err != nil {
		t.Fatal(err)
	}
	tail, _ := store.LoadAfter(ctx, "c", boundary)
	if hasOrphanedToolResult(tail.Messages) {
		t.Fatalf("window orphaned tool result: %#v", tail.Messages)
	}
}
