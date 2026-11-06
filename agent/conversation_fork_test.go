package agent

import (
	"context"
	"strings"
	"testing"
)

func conversationTexts(t *testing.T, store ConversationStore, id string) []string {
	t.Helper()
	snapshot, err := store.Load(context.Background(), id)
	if err != nil {
		t.Fatalf("Load %s: %v", id, err)
	}
	texts := make([]string, 0, len(snapshot.Messages))
	for _, m := range snapshot.Messages {
		var b strings.Builder
		for _, block := range m.Content {
			if tb, ok := block.(TextBlock); ok {
				b.WriteString(tb.Text)
			}
		}
		texts = append(texts, b.String())
	}
	return texts
}

func TestForkConversation_BranchesDivergeIndependently(t *testing.T) {
	provider := newScriptedProvider(
		&ModelResponse{Text: "noted blue"},
		&ModelResponse{Text: "noted dogs"},
		&ModelResponse{Text: "noted sailing"},
	)
	store := newTestMemoryStore()
	a, err := New(provider, "sys", WithConversationStore(store))
	if err != nil {
		t.Fatal(err)
	}
	main := Background().WithConversationID("main")
	branch := Background().WithConversationID("branch")

	if _, err := a.Invoke(main, "favorite color is blue"); err != nil {
		t.Fatalf("main turn 1: %v", err)
	}
	if err := ForkConversation(context.Background(), store, "main", "branch"); err != nil {
		t.Fatalf("ForkConversation: %v", err)
	}
	if _, err := a.Invoke(main, "I like dogs"); err != nil {
		t.Fatalf("main turn 2: %v", err)
	}
	if _, err := a.Invoke(branch, "I like sailing"); err != nil {
		t.Fatalf("branch turn 2: %v", err)
	}

	gotMain := strings.Join(conversationTexts(t, store, "main"), "|")
	wantMain := "favorite color is blue|noted blue|I like dogs|noted dogs"
	if gotMain != wantMain {
		t.Errorf("main = %q, want %q", gotMain, wantMain)
	}
	gotBranch := strings.Join(conversationTexts(t, store, "branch"), "|")
	wantBranch := "favorite color is blue|noted blue|I like sailing|noted sailing"
	if gotBranch != wantBranch {
		t.Errorf("branch = %q, want %q", gotBranch, wantBranch)
	}
}

func TestForkConversation_FromEmptySource(t *testing.T) {
	store := newTestMemoryStore()
	if err := ForkConversation(context.Background(), store, "never-existed", "fresh-branch"); err != nil {
		t.Fatalf("ForkConversation on missing source: %v", err)
	}
	snapshot, err := store.Load(context.Background(), "fresh-branch")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Messages) != 0 {
		t.Errorf("expected empty branch, got %d messages", len(snapshot.Messages))
	}
}
