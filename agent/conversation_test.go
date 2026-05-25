package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/camilbinas/gude-agents/agent/prompt"
)

func TestWithConversationID_OverridesDefault(t *testing.T) {
	sp := newScriptedProvider(
		&ProviderResponse{Text: "reply for conv-A"},
		&ProviderResponse{Text: "reply for conv-B"},
	)

	store := newTestMemoryStore()
	a, err := New(sp, prompt.Text("sys"), nil, WithConversation(store, "default-conv"))
	if err != nil {
		t.Fatal(err)
	}

	// Invoke with per-request conversation ID "conv-A".
	cA := Background().WithConversationID("conv-A")
	result, err := a.Invoke(cA, "hello A")
	if err != nil {
		t.Fatal(err)
	}
	if result != "reply for conv-A" {
		t.Errorf("expected %q, got %q", "reply for conv-A", result)
	}

	// Invoke with per-request conversation ID "conv-B".
	cB := Background().WithConversationID("conv-B")
	result, err = a.Invoke(cB, "hello B")
	if err != nil {
		t.Fatal(err)
	}
	if result != "reply for conv-B" {
		t.Errorf("expected %q, got %q", "reply for conv-B", result)
	}

	// Verify each conversation was saved separately.
	msgsA, _ := store.Load(context.Background(), "conv-A")
	msgsB, _ := store.Load(context.Background(), "conv-B")
	msgsDefault, _ := store.Load(context.Background(), "default-conv")

	if len(msgsA) != 2 {
		t.Errorf("conv-A: expected 2 messages, got %d", len(msgsA))
	}
	if len(msgsB) != 2 {
		t.Errorf("conv-B: expected 2 messages, got %d", len(msgsB))
	}
	if len(msgsDefault) != 0 {
		t.Errorf("default-conv: expected 0 messages, got %d", len(msgsDefault))
	}
}

func TestWithConversationID_FallsBackToDefault(t *testing.T) {
	sp := newScriptedProvider(&ProviderResponse{Text: "reply"})

	store := newTestMemoryStore()
	a, err := New(sp, prompt.Text("sys"), nil, WithConversation(store, "fallback"))
	if err != nil {
		t.Fatal(err)
	}

	// Invoke without per-request override — should use "fallback".
	_, err = a.Invoke(Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}

	msgs, _ := store.Load(context.Background(), "fallback")
	if len(msgs) != 2 {
		t.Errorf("expected 2 messages in fallback conv, got %d", len(msgs))
	}
}

func TestWithSharedConversation_RequiresContextConversationID(t *testing.T) {
	sp := newScriptedProvider(
		&ProviderResponse{Text: "user-1 reply"},
		&ProviderResponse{Text: "user-2 reply"},
	)

	store := newTestMemoryStore()
	a, err := New(sp, prompt.Text("sys"), nil, WithSharedConversation(store))
	if err != nil {
		t.Fatal(err)
	}

	// Two different users, same agent instance.
	c1 := Background().WithConversationID("user-1")
	c2 := Background().WithConversationID("user-2")

	r1, err := a.Invoke(c1, "hi from user 1")
	if err != nil {
		t.Fatal(err)
	}
	r2, err := a.Invoke(c2, "hi from user 2")
	if err != nil {
		t.Fatal(err)
	}

	if r1 != "user-1 reply" {
		t.Errorf("user-1: expected %q, got %q", "user-1 reply", r1)
	}
	if r2 != "user-2 reply" {
		t.Errorf("user-2: expected %q, got %q", "user-2 reply", r2)
	}

	msgs1, _ := store.Load(context.Background(), "user-1")
	msgs2, _ := store.Load(context.Background(), "user-2")

	if len(msgs1) != 2 {
		t.Errorf("user-1: expected 2 messages, got %d", len(msgs1))
	}
	if len(msgs2) != 2 {
		t.Errorf("user-2: expected 2 messages, got %d", len(msgs2))
	}
}

func TestConversationID_EmptyStringFallsBackToDefault(t *testing.T) {
	sp := newScriptedProvider(&ProviderResponse{Text: "reply"})

	store := newTestMemoryStore()
	a, err := New(sp, prompt.Text("sys"), nil, WithConversation(store, "default"))
	if err != nil {
		t.Fatal(err)
	}

	// Empty conversation ID should fall back to the agent's default.
	c := Background().WithConversationID("")
	_, err = a.Invoke(c, "hello")
	if err != nil {
		t.Fatal(err)
	}

	msgs, _ := store.Load(context.Background(), "default")
	if len(msgs) != 2 {
		t.Errorf("expected 2 messages in default conv, got %d", len(msgs))
	}
}

// ---------------------------------------------------------------------------
// ForkConversation (1d9ac49)
// ---------------------------------------------------------------------------

// memConversation is a minimal in-memory Conversation store for testing
// branching and fork independence. It deep-copies messages on Save and
// Load so callers cannot mutate the stored history through aliasing.
type memConversation struct {
	mu   sync.Mutex
	data map[string][]Message
}

func newMemConversation() *memConversation {
	return &memConversation{data: make(map[string][]Message)}
}

func (m *memConversation) Save(_ context.Context, id string, msgs []Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]Message, len(msgs))
	copy(cp, msgs)
	m.data[id] = cp
	return nil
}

func (m *memConversation) Load(_ context.Context, id string) ([]Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	src, ok := m.data[id]
	if !ok {
		return []Message{}, nil
	}
	cp := make([]Message, len(src))
	copy(cp, src)
	return cp, nil
}

func (m *memConversation) List(_ context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.data))
	for k := range m.data {
		out = append(out, k)
	}
	return out, nil
}

func (m *memConversation) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, id)
	return nil
}

func TestForkConversation_CopiesHistoryToNewID(t *testing.T) {
	store := newMemConversation()
	ctx := context.Background()

	original := []Message{
		{Role: RoleUser, Content: []ContentBlock{TextBlock{Text: "hi"}}},
		{Role: RoleAssistant, Content: []ContentBlock{TextBlock{Text: "hello"}}},
		{Role: RoleUser, Content: []ContentBlock{TextBlock{Text: "what is 2+2?"}}},
	}
	if err := store.Save(ctx, "src", original); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := ForkConversation(ctx, store, "src", "fork"); err != nil {
		t.Fatalf("ForkConversation: %v", err)
	}

	forked, err := store.Load(ctx, "fork")
	if err != nil {
		t.Fatalf("Load forked: %v", err)
	}
	if len(forked) != len(original) {
		t.Fatalf("forked length = %d, want %d", len(forked), len(original))
	}
	for i := range original {
		if forked[i].Role != original[i].Role {
			t.Errorf("msg[%d].Role = %v, want %v", i, forked[i].Role, original[i].Role)
		}
	}
}

func TestForkConversation_BranchesAreIndependent(t *testing.T) {
	store := newMemConversation()
	ctx := context.Background()

	base := []Message{
		{Role: RoleUser, Content: []ContentBlock{TextBlock{Text: "shared turn"}}},
		{Role: RoleAssistant, Content: []ContentBlock{TextBlock{Text: "shared reply"}}},
	}
	if err := store.Save(ctx, "main", base); err != nil {
		t.Fatalf("Save base: %v", err)
	}
	if err := ForkConversation(ctx, store, "main", "branch"); err != nil {
		t.Fatalf("Fork: %v", err)
	}

	// Advance "main" with a new turn.
	mainHist, _ := store.Load(ctx, "main")
	mainHist = append(mainHist,
		Message{Role: RoleUser, Content: []ContentBlock{TextBlock{Text: "main-only turn"}}},
		Message{Role: RoleAssistant, Content: []ContentBlock{TextBlock{Text: "main-only reply"}}},
	)
	if err := store.Save(ctx, "main", mainHist); err != nil {
		t.Fatalf("Save main: %v", err)
	}

	// Advance "branch" with a different turn.
	branchHist, _ := store.Load(ctx, "branch")
	branchHist = append(branchHist,
		Message{Role: RoleUser, Content: []ContentBlock{TextBlock{Text: "branch-only turn"}}},
		Message{Role: RoleAssistant, Content: []ContentBlock{TextBlock{Text: "branch-only reply"}}},
	)
	if err := store.Save(ctx, "branch", branchHist); err != nil {
		t.Fatalf("Save branch: %v", err)
	}

	finalMain, _ := store.Load(ctx, "main")
	finalBranch, _ := store.Load(ctx, "branch")

	if len(finalMain) != 4 {
		t.Errorf("main length = %d, want 4", len(finalMain))
	}
	if len(finalBranch) != 4 {
		t.Errorf("branch length = %d, want 4", len(finalBranch))
	}
	// Verify the branches diverged.
	mainText := finalMain[2].Content[0].(TextBlock).Text
	branchText := finalBranch[2].Content[0].(TextBlock).Text
	if mainText == branchText {
		t.Errorf("branches did not diverge: both have %q at position 2", mainText)
	}
	if mainText != "main-only turn" {
		t.Errorf("main[2] = %q, want main-only turn", mainText)
	}
	if branchText != "branch-only turn" {
		t.Errorf("branch[2] = %q, want branch-only turn", branchText)
	}
}

func TestForkConversation_EmptySource(t *testing.T) {
	store := newMemConversation()
	ctx := context.Background()

	// Forking a never-saved conversation should produce an empty branch
	// (Load returns empty slice, Save persists empty slice).
	if err := ForkConversation(ctx, store, "missing", "branch"); err != nil {
		t.Fatalf("ForkConversation on missing source: %v", err)
	}

	branch, err := store.Load(ctx, "branch")
	if err != nil {
		t.Fatalf("Load branch: %v", err)
	}
	if len(branch) != 0 {
		t.Errorf("branch length = %d, want 0", len(branch))
	}
}

func TestForkConversation_LoadErrorPropagates(t *testing.T) {
	store := &errorConversation{loadErr: fmt.Errorf("backend down")}
	err := ForkConversation(context.Background(), store, "src", "fork")
	if err == nil || !strings.Contains(err.Error(), "backend down") {
		t.Errorf("expected load error to propagate, got %v", err)
	}
}

func TestForkConversation_SaveErrorPropagates(t *testing.T) {
	store := &errorConversation{saveErr: fmt.Errorf("disk full")}
	err := ForkConversation(context.Background(), store, "src", "fork")
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Errorf("expected save error to propagate, got %v", err)
	}
}

// errorConversation returns canned errors from Load and/or Save for testing
// error propagation in ForkConversation.
type errorConversation struct {
	loadErr error
	saveErr error
}

func (e *errorConversation) Load(_ context.Context, _ string) ([]Message, error) {
	if e.loadErr != nil {
		return nil, e.loadErr
	}
	return nil, nil
}
func (e *errorConversation) Save(_ context.Context, _ string, _ []Message) error {
	return e.saveErr
}
func (e *errorConversation) List(_ context.Context) ([]string, error) { return nil, nil }
func (e *errorConversation) Delete(_ context.Context, _ string) error { return nil }
