package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// multiConvStore is a multi-conversation, append-capable ConversationStore for
// nested pause/resume tests. Each conversation ID is an independent transcript
// with its own revision and last sequence.
type multiConvStore struct {
	mu    sync.Mutex
	convs map[string]*convState
}

type convState struct {
	messages     []Message
	revision     uint64
	lastSequence uint64
}

func newMultiConvStore() *multiConvStore { return &multiConvStore{convs: map[string]*convState{}} }

func (s *multiConvStore) get(id string) *convState {
	c, ok := s.convs[id]
	if !ok {
		c = &convState{}
		s.convs[id] = c
	}
	return c
}

func (s *multiConvStore) Load(_ context.Context, id string) (ConversationSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.get(id)
	return ConversationSnapshot{Messages: append([]Message(nil), c.messages...), Revision: c.revision, LastSequence: c.lastSequence}, nil
}

func (s *multiConvStore) LoadAfter(_ context.Context, id string, after uint64) (ConversationSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.get(id)
	start := int(after)
	if start > len(c.messages) {
		start = len(c.messages)
	}
	return ConversationSnapshot{Messages: append([]Message(nil), c.messages[start:]...), Revision: c.revision, LastSequence: c.lastSequence}, nil
}

func (s *multiConvStore) Append(_ context.Context, id string, messages []Message, expected uint64) (ConversationCursor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.get(id)
	if expected != c.revision {
		return ConversationCursor{}, ErrConversationConflict
	}
	if len(messages) == 0 {
		return ConversationCursor{Revision: c.revision, LastSequence: c.lastSequence}, nil
	}
	c.messages = append(c.messages, messages...)
	c.revision++
	c.lastSequence += uint64(len(messages))
	return ConversationCursor{Revision: c.revision, LastSequence: c.lastSequence}, nil
}

// newApprovalChild builds a child whose only tool requires approval, so it
// pauses on an InterruptApproval. calls counts handler executions.
func newApprovalChild(t *testing.T, convs ConversationStore, execs ExecutionStore, calls *int) *Runtime {
	t.Helper()
	approve := tool.NewRaw("approve", "approve", nil, func(context.Context, json.RawMessage) (string, error) {
		*calls++
		return "approved work", nil
	}, tool.RequiresApproval())
	child, err := New(newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "child-approval", Name: "approve", Input: json.RawMessage(`{}`)}}},
		&ModelResponse{Text: "child final answer"},
	), "child", WithConversationStore(convs), WithExecutionStore(execs), WithTools(approve))
	if err != nil {
		t.Fatal(err)
	}
	return child
}

func newPropagatingParent(t *testing.T, convs ConversationStore, execs ExecutionStore, child *Runtime) (*Runtime, *capturingProvider) {
	t.Helper()
	parentProvider := newCapturingProvider(
		&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "delegate", Name: "child", Input: json.RawMessage(`{"message":"go"}`)}}},
		&ModelResponse{Text: "parent done"},
	)
	parent, err := New(parentProvider, "parent", WithConversationStore(convs), WithExecutionStore(execs),
		WithTools(AgentAsTool("child", "child", child)))
	if err != nil {
		t.Fatal(err)
	}
	return parent, parentProvider
}

// TestAgentAsTool_ChildApprovalPausesParent verifies the full approval flow:
// the parent pauses with the child's approval interrupt, Approve resumes, the
// child tool runs once, and the parent finishes with the child's answer in its
// transcript.
func TestAgentAsTool_ChildApprovalPausesParent(t *testing.T) {
	convs, execs := newMultiConvStore(), newTestExecutionStore()
	var childCalls int
	child := newApprovalChild(t, convs, execs, &childCalls)
	parent, parentProvider := newPropagatingParent(t, convs, execs, child)

	ctx := Background().WithConversationID("parent")
	res, err := parent.Invoke(ctx, "delegate")
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if res.StopReason != StopInterrupt || res.Interrupt == nil {
		t.Fatalf("parent did not pause: %+v", res)
	}
	in := res.Interrupt
	if in.Type != InterruptNested || in.Nested == nil || in.Nested.Child == nil {
		t.Fatalf("parent interrupt = %+v, want nested with child", in)
	}
	if in.Nested.CallID != "delegate" {
		t.Fatalf("nested call ID = %q, want delegate", in.Nested.CallID)
	}
	if in.Nested.Child.Type != InterruptApproval {
		t.Fatalf("child interrupt type = %q, want approval", in.Nested.Child.Type)
	}
	if childCalls != 0 {
		t.Fatalf("child approval ran before approval: %d", childCalls)
	}

	res, err = parent.Resume(ctx, in, Approve())
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if res.StopReason == StopInterrupt {
		t.Fatalf("parent still paused: %+v", res)
	}
	if res.Text != "parent done" {
		t.Fatalf("parent text = %q, want parent done", res.Text)
	}
	if childCalls != 1 {
		t.Fatalf("child approval ran %d times, want 1", childCalls)
	}

	// The child's final answer must be the tool result the parent saw.
	got := childToolResultText(t, parentProvider)
	if got != "child final answer" {
		t.Fatalf("parent tool result = %q, want child final answer", got)
	}
}

// TestAgentAsTool_ChildDenyPausesParent verifies Deny flows through to the
// child: the child approval tool does not run and the parent still completes.
func TestAgentAsTool_ChildDenyPausesParent(t *testing.T) {
	convs, execs := newMultiConvStore(), newTestExecutionStore()
	var childCalls int
	child := newApprovalChild(t, convs, execs, &childCalls)
	parent, _ := newPropagatingParent(t, convs, execs, child)

	ctx := Background().WithConversationID("parent")
	res, err := parent.Invoke(ctx, "delegate")
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	res, err = parent.Resume(ctx, res.Interrupt, Deny("not allowed"))
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if res.Text != "parent done" {
		t.Fatalf("parent text = %q", res.Text)
	}
	if childCalls != 0 {
		t.Fatalf("denied child approval ran %d times, want 0", childCalls)
	}
}

// TestAgentAsTool_ChildHumanInputPausesParent verifies a child human-input
// pause surfaces to the parent and Respond resumes the child.
func TestAgentAsTool_ChildHumanInputPausesParent(t *testing.T) {
	convs, execs := newMultiConvStore(), newTestExecutionStore()
	ask := NewHumanInputTool("ask_human", "ask when data is missing")
	child, err := New(newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "child-ask", Name: "ask_human", Input: json.RawMessage(`{"reason":"need","question":"which account?"}`)}}},
		&ModelResponse{Text: "child used the answer"},
	), "child", WithConversationStore(convs), WithExecutionStore(execs), WithTools(ask))
	if err != nil {
		t.Fatal(err)
	}
	parent, parentProvider := newPropagatingParent(t, convs, execs, child)

	ctx := Background().WithConversationID("parent")
	res, err := parent.Invoke(ctx, "delegate")
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if res.Interrupt == nil || res.Interrupt.Type != InterruptNested || res.Interrupt.Nested.Child.Type != InterruptHumanInput {
		t.Fatalf("parent interrupt = %+v, want nested human_input", res.Interrupt)
	}
	if q := res.Interrupt.Nested.Child.Input.Question; q != "which account?" {
		t.Fatalf("child question = %q", q)
	}
	res, err = parent.Resume(ctx, res.Interrupt, Respond("Use ACME-42"))
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if res.Text != "parent done" {
		t.Fatalf("parent text = %q", res.Text)
	}
	if got := childToolResultText(t, parentProvider); got != "child used the answer" {
		t.Fatalf("parent tool result = %q", got)
	}
}

// TestAgentAsTool_StaleNestedInterruptConflicts verifies a stale parent
// interrupt (already resumed) returns ErrExecutionConflict on a second resume.
func TestAgentAsTool_StaleNestedInterruptConflicts(t *testing.T) {
	convs, execs := newMultiConvStore(), newTestExecutionStore()
	var childCalls int
	child := newApprovalChild(t, convs, execs, &childCalls)
	parent, _ := newPropagatingParent(t, convs, execs, child)

	ctx := Background().WithConversationID("parent")
	res, err := parent.Invoke(ctx, "delegate")
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	stale := res.Interrupt
	if _, err := parent.Resume(ctx, stale, Approve()); err != nil {
		t.Fatalf("first resume: %v", err)
	}
	if _, err := parent.Resume(ctx, stale, Approve()); !errors.Is(err, ErrExecutionConflict) {
		t.Fatalf("second resume err = %v, want ErrExecutionConflict", err)
	}
}

// TestAgentAsTool_NestedPauseSurvivesRestart verifies new parent and child
// Runtimes on the same durable stores can resume a nested pause.
func TestAgentAsTool_NestedPauseSurvivesRestart(t *testing.T) {
	convs, execs := newMultiConvStore(), newTestExecutionStore()
	var childCalls int
	child := newApprovalChild(t, convs, execs, &childCalls)
	parent, _ := newPropagatingParent(t, convs, execs, child)

	ctx := Background().WithConversationID("parent")
	res, err := parent.Invoke(ctx, "delegate")
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	in := res.Interrupt

	// Fresh Runtimes on the same stores (process restart).
	var restartCalls int
	child2 := newApprovalChild(t, convs, execs, &restartCalls)
	parent2Provider := newCapturingProvider(&ModelResponse{Text: "parent done after restart"})
	parent2, err := New(parent2Provider, "parent", WithConversationStore(convs), WithExecutionStore(execs),
		WithTools(AgentAsTool("child", "child", child2)))
	if err != nil {
		t.Fatal(err)
	}
	// Reload the interrupt from the durable store, as a restarted process would.
	reloaded, err := parent2.LoadInterrupt(ctx, in.ExecutionID)
	if err != nil {
		t.Fatalf("load interrupt: %v", err)
	}
	if reloaded.Type != InterruptNested || reloaded.Nested == nil || reloaded.Nested.Child == nil {
		t.Fatalf("reloaded interrupt = %+v", reloaded)
	}
	res, err = parent2.Resume(ctx, reloaded, Approve())
	if err != nil {
		t.Fatalf("resume after restart: %v", err)
	}
	if res.Text != "parent done after restart" {
		t.Fatalf("parent text = %q", res.Text)
	}
	if restartCalls != 1 {
		t.Fatalf("child approval ran %d times after restart, want 1", restartCalls)
	}
}

// TestAgentAsTool_NestedPauseKeepsBatchSiblingResults verifies that when the
// parent batches a normal tool and the paused sub-agent together, the normal
// tool's result is kept and not re-run on resume.
func TestAgentAsTool_NestedPauseKeepsBatchSiblingResults(t *testing.T) {
	convs, execs := newMultiConvStore(), newTestExecutionStore()
	var childCalls int
	child := newApprovalChild(t, convs, execs, &childCalls)

	var normalCalls int
	normal := tool.NewRaw("note", "note", nil, func(context.Context, json.RawMessage) (string, error) {
		normalCalls++
		return "noted", nil
	})
	parentProvider := newCapturingProvider(
		&ModelResponse{ToolCalls: []tool.Call{
			{ToolUseID: "note-1", Name: "note", Input: json.RawMessage(`{}`)},
			{ToolUseID: "delegate", Name: "child", Input: json.RawMessage(`{"message":"go"}`)},
		}},
		&ModelResponse{Text: "parent done"},
	)
	parent, err := New(parentProvider, "parent", WithConversationStore(convs), WithExecutionStore(execs),
		WithTools(normal, AgentAsTool("child", "child", child)))
	if err != nil {
		t.Fatal(err)
	}

	ctx := Background().WithConversationID("parent")
	res, err := parent.Invoke(ctx, "delegate")
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if res.Interrupt == nil || res.Interrupt.Type != InterruptNested {
		t.Fatalf("parent interrupt = %+v", res.Interrupt)
	}
	if normalCalls != 1 {
		t.Fatalf("normal tool ran %d times before pause, want 1", normalCalls)
	}
	res, err = parent.Resume(ctx, res.Interrupt, Approve())
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if res.Text != "parent done" {
		t.Fatalf("parent text = %q", res.Text)
	}
	if normalCalls != 1 {
		t.Fatalf("normal tool re-ran on resume: %d calls, want 1", normalCalls)
	}
	if childCalls != 1 {
		t.Fatalf("child approval ran %d times, want 1", childCalls)
	}
	// The parent's second provider request must carry the normal tool result.
	var sawNote bool
	for _, msg := range parentProvider.captured[len(parentProvider.captured)-1].Messages {
		for _, block := range msg.Content {
			if tr, ok := block.(ToolResultBlock); ok && tr.ToolUseID == "note-1" && tr.Content == "noted" {
				sawNote = true
			}
		}
	}
	if !sawNote {
		t.Fatal("normal tool result missing from final provider request")
	}
}

// TestAgentAsTool_GrandchildPausePropagatesUp verifies the same mechanism
// works across two levels: a grandchild pause bubbles up through the child to
// the top parent, and a single Approve resumes the whole chain.
func TestAgentAsTool_GrandchildPausePropagatesUp(t *testing.T) {
	convs, execs := newMultiConvStore(), newTestExecutionStore()
	var grandCalls int
	grandchild := newApprovalChild(t, convs, execs, &grandCalls)

	// Middle child delegates to the grandchild and propagates its pause.
	middleProvider := newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "to-grand", Name: "grand", Input: json.RawMessage(`{"message":"go"}`)}}},
		&ModelResponse{Text: "middle answer"},
	)
	middle, err := New(middleProvider, "middle", WithConversationStore(convs), WithExecutionStore(execs),
		WithTools(AgentAsTool("grand", "grand", grandchild)))
	if err != nil {
		t.Fatal(err)
	}

	parent, parentProvider := newPropagatingParent(t, convs, execs, middle)

	ctx := Background().WithConversationID("parent")
	res, err := parent.Invoke(ctx, "delegate")
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	// Parent sees a nested interrupt whose child is itself a nested interrupt.
	if res.Interrupt == nil || res.Interrupt.Type != InterruptNested {
		t.Fatalf("parent interrupt = %+v", res.Interrupt)
	}
	child := res.Interrupt.Nested.Child
	if child.Type != InterruptNested || child.Nested == nil || child.Nested.Child.Type != InterruptApproval {
		t.Fatalf("expected a nested-of-nested approval, got %+v", child)
	}

	res, err = parent.Resume(ctx, res.Interrupt, Approve())
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if res.Text != "parent done" {
		t.Fatalf("parent text = %q", res.Text)
	}
	if grandCalls != 1 {
		t.Fatalf("grandchild approval ran %d times, want 1", grandCalls)
	}
	if got := childToolResultText(t, parentProvider); got != "middle answer" {
		t.Fatalf("parent tool result = %q, want middle answer", got)
	}
}

// TestAgentAsTool_NestedPauseSnapshotRestore verifies Snapshot/Restore of a
// parent paused on a child: the blob round-trips through a second Runtime that
// then resumes the nested pause to completion.
func TestAgentAsTool_NestedPauseSnapshotRestore(t *testing.T) {
	convs, execs := newMultiConvStore(), newTestExecutionStore()
	var childCalls int
	child := newApprovalChild(t, convs, execs, &childCalls)
	parent, _ := newPropagatingParent(t, convs, execs, child)

	ctx := Background().WithConversationID("parent")
	res, err := parent.Invoke(ctx, "delegate")
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	blob, err := parent.Snapshot(ctx, res.Interrupt)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	// Restore into fresh stores + runtimes, mimicking another process.
	convs2, execs2 := newMultiConvStore(), newTestExecutionStore()
	var restoreCalls int
	child2 := newApprovalChild(t, convs2, execs2, &restoreCalls)
	parent2Provider := newCapturingProvider(&ModelResponse{Text: "parent restored done"})
	parent2, err := New(parent2Provider, "parent", WithConversationStore(convs2), WithExecutionStore(execs2),
		WithTools(AgentAsTool("child", "child", child2)))
	if err != nil {
		t.Fatal(err)
	}

	// The child pause lives in the child stores; Snapshot only carries the
	// parent. Copy the child's paused execution + conversation across so the
	// restored child can be resumed (a real deployment shares the child stores).
	execs.mu.Lock()
	for id, e := range execs.items {
		if id != res.Interrupt.ExecutionID {
			execs2.items[id] = cloneExecution(e)
		}
	}
	execs.mu.Unlock()
	convs.mu.Lock()
	for id, c := range convs.convs {
		if id != "parent" {
			cp := *c
			cp.messages = append([]Message(nil), c.messages...)
			convs2.convs[id] = &cp
		}
	}
	convs.mu.Unlock()

	in, err := parent2.Restore(ctx, blob)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if in.Type != InterruptNested || in.Nested == nil || in.Nested.Child == nil {
		t.Fatalf("restored interrupt = %+v", in)
	}
	res, err = parent2.Resume(ctx, in, Approve())
	if err != nil {
		t.Fatalf("resume restored: %v", err)
	}
	if res.Text != "parent restored done" {
		t.Fatalf("parent text = %q", res.Text)
	}
	if restoreCalls != 1 {
		t.Fatalf("restored child approval ran %d times, want 1", restoreCalls)
	}
}

// A parent without an ExecutionStore cannot pause, so a child pause is still
// canceled and returned as a tool error, and the parent run finishes.
func TestAgentAsTool_ParentWithoutStoresFallsBack(t *testing.T) {
	convs, execs := newMultiConvStore(), newTestExecutionStore()
	var childCalls int
	child := newApprovalChild(t, convs, execs, &childCalls)
	parentProvider := newCapturingProvider(
		&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "delegate", Name: "child", Input: json.RawMessage(`{"message":"go"}`)}}},
		&ModelResponse{Text: "recovered"},
	)
	parent, err := New(parentProvider, "parent", WithConversationStore(convs),
		WithTools(AgentAsTool("child", "child", child)))
	if err != nil {
		t.Fatal(err)
	}
	res, err := parent.Invoke(Background().WithConversationID("parent"), "go")
	if err != nil || res.Text != "recovered" {
		t.Fatalf("parent result = %+v, %v", res, err)
	}
	if got := childToolResultText(t, parentProvider); !strings.Contains(got, "interrupts cannot propagate through AgentAsTool") {
		t.Fatalf("child tool result = %q", got)
	}
}
