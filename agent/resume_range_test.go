package agent

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/camilbinas/gude-agents/agent/tool"
)

type rangeResumeStore struct {
	mu             sync.Mutex
	messages       []Message
	revision       uint64
	lastSequence   uint64
	loadCalls      int
	loadAfterCalls []uint64
}

func (s *rangeResumeStore) Load(context.Context, string) (ConversationSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadCalls++
	return ConversationSnapshot{Messages: append([]Message(nil), s.messages...), Revision: s.revision, LastSequence: s.lastSequence}, nil
}

func (s *rangeResumeStore) LoadAfter(_ context.Context, _ string, after uint64) (ConversationSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadAfterCalls = append(s.loadAfterCalls, after)
	start := int(after)
	if start > len(s.messages) {
		start = len(s.messages)
	}
	return ConversationSnapshot{Messages: append([]Message(nil), s.messages[start:]...), Revision: s.revision, LastSequence: s.lastSequence}, nil
}

func (s *rangeResumeStore) Append(_ context.Context, _ string, messages []Message, expected uint64) (ConversationCursor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if expected != s.revision {
		return ConversationCursor{}, ErrConversationConflict
	}
	if len(messages) == 0 {
		return ConversationCursor{Revision: s.revision, LastSequence: s.lastSequence}, nil
	}
	s.messages = append(s.messages, messages...)
	s.revision++
	s.lastSequence += uint64(len(messages))
	return ConversationCursor{Revision: s.revision, LastSequence: s.lastSequence}, nil
}

type rangeResumeManager struct {
	boundary uint64
	calls    int
}

func (m *rangeResumeManager) HistoryBoundary(context.Context, string) (uint64, error) {
	m.calls++
	return m.boundary, nil
}
func (m *rangeResumeManager) Prepare(_ context.Context, in ContextManagerInput) (ContextManagerOutput, error) {
	return ContextManagerOutput{Messages: append(append([]Message(nil), in.Recent...), in.Current...)}, nil
}

func TestResumeUsesContextManagerRangeHistory(t *testing.T) {
	store := &rangeResumeStore{
		messages: []Message{
			{Role: RoleUser, Content: []ContentBlock{TextBlock{Text: "old user"}}},
			{Role: RoleAssistant, Content: []ContentBlock{TextBlock{Text: "old assistant"}}},
			{Role: RoleAssistant, Content: []ContentBlock{ToolUseBlock{ToolUseID: "old-tool", Name: "old", Input: json.RawMessage(`{}`)}}},
			{Role: RoleUser, Content: []ContentBlock{ToolResultBlock{ToolUseID: "old-tool", Content: "old result"}}},
		},
		revision:     1,
		lastSequence: 4,
	}
	manager := &rangeResumeManager{boundary: 2}
	provider := &approvalBatchProvider{responses: []*ModelResponse{
		{ToolCalls: []tool.Call{{ToolUseID: "approve-1", Name: "approve", Input: json.RawMessage(`{}`)}}},
		{Text: "resumed"},
	}}
	called := false
	approval := tool.NewRaw("approve", "approve", nil, func(context.Context, json.RawMessage) (string, error) {
		called = true
		return "approved", nil
	}, tool.RequiresApproval())
	a, err := New(provider, "sys", WithTools(approval), WithConversationStore(store), WithContextManager(manager))
	if err != nil {
		t.Fatal(err)
	}
	ctx := Background().WithConversationID("conversation")
	in := mustInterrupt(t, a, ctx, "pause")
	if in.LastSequence != 6 {
		t.Fatalf("interrupt LastSequence = %d, want 6", in.LastSequence)
	}

	result, err := a.Resume(ctx, in, Approve())
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "resumed" || !called {
		t.Fatalf("result=%+v called=%v", result, called)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.loadCalls != 0 {
		t.Fatalf("resume used Load %d time(s)", store.loadCalls)
	}
	if len(store.loadAfterCalls) < 2 || store.loadAfterCalls[len(store.loadAfterCalls)-1] != 2 {
		t.Fatalf("LoadAfter boundaries = %v, want resume boundary 2", store.loadAfterCalls)
	}
	if manager.calls < 2 {
		t.Fatalf("HistoryBoundary calls = %d, want fresh + resume", manager.calls)
	}
}

func TestResumeRangeHistoryRetainsLastSequenceConflict(t *testing.T) {
	store := &rangeResumeStore{revision: 1, lastSequence: 3}
	manager := &rangeResumeManager{boundary: 2}
	provider := newScriptedProvider()
	a, err := New(provider, "sys", WithConversationStore(store), WithContextManager(manager))
	if err != nil {
		t.Fatal(err)
	}
	r := &run{a: a, c: Background().WithConversationID("conversation"), convID: "conversation"}
	in := &Interrupt{Type: InterruptHumanInput, Revision: 1, LastSequence: 2}
	_, err = r.resumeTurn(in, Respond("answer"))
	if err == nil || !errors.Is(err, ErrConversationConflict) {
		t.Fatalf("resume error = %v, want ErrConversationConflict", err)
	}
}
