package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestWithConversationID_OverridesDefault(t *testing.T) {
	sp := newScriptedProvider(
		&ModelResponse{Text: "reply for conv-A"},
		&ModelResponse{Text: "reply for conv-B"},
	)

	store := newTestMemoryStore()
	a, err := New(sp, "sys", WithConversationStore(store))
	if err != nil {
		t.Fatal(err)
	}

	// Invoke with per-request conversation ID "conv-A".
	cA := Background().WithConversationID("conv-A")
	result, err := a.Invoke(cA, "hello A")
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "reply for conv-A" {
		t.Errorf("expected %q, got %q", "reply for conv-A", result.Text)
	}

	// Invoke with per-request conversation ID "conv-B".
	cB := Background().WithConversationID("conv-B")
	result, err = a.Invoke(cB, "hello B")
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "reply for conv-B" {
		t.Errorf("expected %q, got %q", "reply for conv-B", result.Text)
	}

	// Verify each conversation was saved separately.
	msgsA, _ := testLoadMessages(context.Background(), store, "conv-A")
	msgsB, _ := testLoadMessages(context.Background(), store, "conv-B")
	msgsDefault, _ := testLoadMessages(context.Background(), store, "default-conv")

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

func TestConversationID_ComesFromContext(t *testing.T) {
	sp := newScriptedProvider(&ModelResponse{Text: "reply"})

	store := newTestMemoryStore()
	a, err := New(sp, "sys", WithConversationStore(store))
	if err != nil {
		t.Fatal(err)
	}

	_, err = a.Invoke(Background().WithConversationID("fallback"), "hello")
	if err != nil {
		t.Fatal(err)
	}

	msgs, _ := testLoadMessages(context.Background(), store, "fallback")
	if len(msgs) != 2 {
		t.Errorf("expected 2 messages in fallback conv, got %d", len(msgs))
	}
}

func TestConversationStore_IsolatesContextConversationIDs(t *testing.T) {
	sp := newScriptedProvider(
		&ModelResponse{Text: "user-1 reply"},
		&ModelResponse{Text: "user-2 reply"},
	)

	store := newTestMemoryStore()
	a, err := New(sp, "sys", WithConversationStore(store))
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

	if r1.Text != "user-1 reply" {
		t.Errorf("user-1: expected %q, got %q", "user-1 reply", r1.Text)
	}
	if r2.Text != "user-2 reply" {
		t.Errorf("user-2: expected %q, got %q", "user-2 reply", r2.Text)
	}

	msgs1, _ := testLoadMessages(context.Background(), store, "user-1")
	msgs2, _ := testLoadMessages(context.Background(), store, "user-2")

	if len(msgs1) != 2 {
		t.Errorf("user-1: expected 2 messages, got %d", len(msgs1))
	}
	if len(msgs2) != 2 {
		t.Errorf("user-2: expected 2 messages, got %d", len(msgs2))
	}
}

func TestConversationID_EmptyStringIsStateless(t *testing.T) {
	sp := newScriptedProvider(&ModelResponse{Text: "reply"})

	store := &failingSaveConversation{loadErr: errors.New("must not load"), flushErr: errors.New("must not flush")}
	a, err := New(sp, "sys", WithConversationStore(store), WithSyncConversation())
	if err != nil {
		t.Fatal(err)
	}

	result, err := a.Invoke(Background().WithConversationID(""), "hello")
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "reply" {
		t.Fatalf("result.Text = %q, want reply", result.Text)
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if store.loads != 0 || store.saves != 0 || store.flushes != 0 {
		t.Fatalf("stateless invocation touched store: loads=%d saves=%d flushes=%d", store.loads, store.saves, store.flushes)
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
	data map[string]ConversationSnapshot
}

func newMemConversation() *memConversation {
	return &memConversation{data: make(map[string]ConversationSnapshot)}
}

func (m *memConversation) Save(_ context.Context, id string, msgs []Message, expectedRevision uint64) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data[id].Revision != expectedRevision {
		return 0, ErrConversationConflict
	}
	cp := make([]Message, len(msgs))
	copy(cp, msgs)
	next := expectedRevision + 1
	m.data[id] = ConversationSnapshot{Messages: cp, Revision: next}
	return next, nil
}

func (m *memConversation) Load(_ context.Context, id string) (ConversationSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	snapshot, ok := m.data[id]
	if !ok {
		return ConversationSnapshot{Messages: []Message{}}, nil
	}
	cp := make([]Message, len(snapshot.Messages))
	copy(cp, snapshot.Messages)
	return ConversationSnapshot{Messages: cp, Revision: snapshot.Revision}, nil
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
	if err := testSaveLatest(ctx, store, "src", original); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := ForkConversation(ctx, store, "src", "fork"); err != nil {
		t.Fatalf("ForkConversation: %v", err)
	}

	forked, err := testLoadMessages(ctx, store, "fork")
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
	if err := testSaveLatest(ctx, store, "main", base); err != nil {
		t.Fatalf("Save base: %v", err)
	}
	if err := ForkConversation(ctx, store, "main", "branch"); err != nil {
		t.Fatalf("Fork: %v", err)
	}

	// Advance "main" with a new turn.
	mainHist, _ := testLoadMessages(ctx, store, "main")
	mainHist = append(mainHist,
		Message{Role: RoleUser, Content: []ContentBlock{TextBlock{Text: "main-only turn"}}},
		Message{Role: RoleAssistant, Content: []ContentBlock{TextBlock{Text: "main-only reply"}}},
	)
	if err := testSaveLatest(ctx, store, "main", mainHist); err != nil {
		t.Fatalf("Save main: %v", err)
	}

	// Advance "branch" with a different turn.
	branchHist, _ := testLoadMessages(ctx, store, "branch")
	branchHist = append(branchHist,
		Message{Role: RoleUser, Content: []ContentBlock{TextBlock{Text: "branch-only turn"}}},
		Message{Role: RoleAssistant, Content: []ContentBlock{TextBlock{Text: "branch-only reply"}}},
	)
	if err := testSaveLatest(ctx, store, "branch", branchHist); err != nil {
		t.Fatalf("Save branch: %v", err)
	}

	finalMain, _ := testLoadMessages(ctx, store, "main")
	finalBranch, _ := testLoadMessages(ctx, store, "branch")

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

	branch, err := testLoadMessages(ctx, store, "branch")
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

func (e *errorConversation) Load(_ context.Context, _ string) (ConversationSnapshot, error) {
	if e.loadErr != nil {
		return ConversationSnapshot{}, e.loadErr
	}
	return ConversationSnapshot{Messages: []Message{}}, nil
}
func (e *errorConversation) Save(_ context.Context, _ string, _ []Message, _ uint64) (uint64, error) {
	return 0, e.saveErr
}
func (e *errorConversation) List(_ context.Context) ([]string, error) { return nil, nil }
func (e *errorConversation) Delete(_ context.Context, _ string) error { return nil }

type blockingCASProvider struct {
	started chan struct{}
	release chan struct{}
}

func (p *blockingCASProvider) Name() string { return "blocking-cas" }
func (p *blockingCASProvider) Stream(context.Context, ModelRequest, func(ModelEvent)) (*ModelResponse, error) {
	close(p.started)
	<-p.release
	return &ModelResponse{Text: "reply"}, nil
}

func TestInvokeReturnsConversationConflictWithoutOverwrite(t *testing.T) {
	store := newTestMemoryStore()
	provider := &blockingCASProvider{started: make(chan struct{}), release: make(chan struct{})}
	a, err := New(provider, "sys", WithConversationStore(store))
	if err != nil {
		t.Fatal(err)
	}
	ctx := Background().WithConversationID("conv")
	done := make(chan error, 1)
	go func() {
		_, err := a.Invoke(ctx, "agent turn")
		done <- err
	}()
	<-provider.started
	external := []Message{{Role: RoleUser, Content: []ContentBlock{TextBlock{Text: "newer turn"}}}}
	if _, err := store.Save(context.Background(), "conv", external, 0); err != nil {
		t.Fatal(err)
	}
	close(provider.release)
	if err := <-done; !errors.Is(err, ErrConversationConflict) {
		t.Fatalf("Invoke error = %v, want ErrConversationConflict", err)
	}
	snapshot, err := store.Load(context.Background(), "conv")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Messages) != 1 || snapshot.Messages[0].Content[0].(TextBlock).Text != "newer turn" {
		t.Fatalf("concurrent turn was overwritten: %#v", snapshot.Messages)
	}
}
