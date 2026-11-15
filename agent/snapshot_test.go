package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// snapshotApprovalAgent builds an agent that pauses on an approval-required
// tool, returns the interrupt, its runtime, and the shared stores.
func snapshotApprovalAgent(t *testing.T, convID string) (*Runtime, *Interrupt, *testMemoryStore, *testExecutionStore) {
	t.Helper()
	provider := newScriptedProvider(&ModelResponse{ToolCalls: []tool.Call{{
		ToolUseID: "tc-1", Name: "delete_order", Input: json.RawMessage(`{"order_id":"ORD-7"}`),
	}}})
	conversations, executions, stores := pauseStores()
	a, err := New(provider, "helpful", append(stores, WithTools(deleteOrderTool()))...)
	if err != nil {
		t.Fatal(err)
	}
	in := mustInterrupt(t, a, Background().WithConversationID(convID), "delete order 7")
	return a, in, conversations, executions
}

// TestSnapshotRestore_ApprovalRoundTripResumes snapshots a paused approval,
// restores it into a brand-new runtime (fresh stores), and resumes there.
func TestSnapshotRestore_ApprovalRoundTripResumes(t *testing.T) {
	src, in, _, _ := snapshotApprovalAgent(t, "snap-approval")

	blob, err := src.Snapshot(context.Background(), in)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(blob) == 0 {
		t.Fatal("empty snapshot blob")
	}

	// A fresh process: new stores, new runtime, able to run the handler and
	// produce a final answer.
	dstConv, dstExec := newTestMemoryStore(), newTestExecutionStore()
	dst, err := New(
		newScriptedProvider(&ModelResponse{Text: "Order ORD-7 deleted."}),
		"helpful",
		WithConversationStore(dstConv), WithExecutionStore(dstExec), WithTools(deleteOrderTool()),
	)
	if err != nil {
		t.Fatal(err)
	}

	restored, err := dst.Restore(context.Background(), blob)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if restored.Type != InterruptApproval || restored.ExecutionID != in.ExecutionID || restored.ConversationID != in.ConversationID {
		t.Fatalf("restored interrupt = %+v, want approval matching source", restored)
	}
	if len(restored.Approval.Calls) != 1 || restored.Approval.Calls[0].CallID != "tc-1" {
		t.Fatalf("restored approval = %+v", restored.Approval)
	}

	// LoadInterrupt on the destination projects the same pause.
	if _, err := dst.LoadInterrupt(context.Background(), in.ExecutionID); err != nil {
		t.Fatalf("LoadInterrupt after restore: %v", err)
	}

	res, err := dst.Resume(Background(), restored, Approve())
	if err != nil {
		t.Fatalf("Resume after restore: %v", err)
	}
	if res.Text != "Order ORD-7 deleted." || res.StopReason != StopEndTurn {
		t.Fatalf("resumed result = %+v", res)
	}
}

// TestSnapshotRestore_HumanInputRoundTrip snapshots a human-input pause and
// resumes it with Respond in a fresh runtime.
func TestSnapshotRestore_HumanInputRoundTrip(t *testing.T) {
	ask := NewHumanInputTool("ask_human", "ask when data is missing")
	provider := newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "h-1", Name: "ask_human", Input: json.RawMessage(`{"reason":"need id","question":"which account?"}`)}}},
		&ModelResponse{Text: "Using account ACME-42."},
	)
	_, _, stores := pauseStores()
	src, err := New(provider, "helpful", append(stores, WithTools(ask))...)
	if err != nil {
		t.Fatal(err)
	}
	in := mustInterrupt(t, src, Background().WithConversationID("snap-human"), "look up balance")
	if in.Type != InterruptHumanInput {
		t.Fatalf("interrupt type = %q, want human_input", in.Type)
	}

	blob, err := src.Snapshot(context.Background(), in)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	dst, err := New(
		newScriptedProvider(&ModelResponse{Text: "Using account ACME-42."}),
		"helpful",
		WithConversationStore(newTestMemoryStore()), WithExecutionStore(newTestExecutionStore()),
		WithTools(NewHumanInputTool("ask_human", "ask when data is missing")),
	)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := dst.Restore(context.Background(), blob)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	res, err := dst.Resume(Background(), restored, Respond("Use account ACME-42"))
	if err != nil {
		t.Fatalf("Resume after restore: %v", err)
	}
	if res.Text != "Using account ACME-42." {
		t.Fatalf("resumed result = %+v", res)
	}
}

// Two runtimes with separate in-memory stores: the blob is the only link.
func TestSnapshotRestore_SeparateMemoryStores(t *testing.T) {
	provider := newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "tc-1", Name: "delete_order", Input: json.RawMessage(`{"order_id":"ORD-1"}`)}}},
		&ModelResponse{Text: "done"},
	)
	src, err := New(provider, "helpful", withTestPauseStores(), WithTools(deleteOrderTool()))
	if err != nil {
		t.Fatal(err)
	}
	in := mustInterrupt(t, src, Background().WithConversationID("separate"), "delete order 1")

	blob, err := src.Snapshot(context.Background(), in)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	dst, err := New(
		newScriptedProvider(&ModelResponse{Text: "done"}),
		"helpful", withTestPauseStores(), WithTools(deleteOrderTool()),
	)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := dst.Restore(context.Background(), blob)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if _, err := dst.Resume(Background(), restored, Approve()); err != nil {
		t.Fatalf("Resume after restore: %v", err)
	}
}

// TestSnapshot_StoreRequired verifies Snapshot and Restore fail without stores.
func TestSnapshot_StoreRequired(t *testing.T) {
	a, err := New(newScriptedProvider(), "helpful")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Snapshot(context.Background(), &Interrupt{ExecutionID: "x"}); !errors.Is(err, ErrSnapshotStoreRequired) {
		t.Fatalf("Snapshot error = %v, want ErrSnapshotStoreRequired", err)
	}
	if _, err := a.Restore(context.Background(), []byte(`{}`)); !errors.Is(err, ErrSnapshotStoreRequired) {
		t.Fatalf("Restore error = %v, want ErrSnapshotStoreRequired", err)
	}
}

// TestRestore_RejectsUnknownFormat verifies the format gate.
func TestRestore_RejectsUnknownFormat(t *testing.T) {
	dst, err := New(newScriptedProvider(), "x", withTestPauseStores())
	if err != nil {
		t.Fatal(err)
	}
	for _, blob := range [][]byte{
		nil,
		[]byte(`not json`),
		[]byte(`{"format":999,"execution":{"id":"a"}}`),
	} {
		if _, err := dst.Restore(context.Background(), blob); !errors.Is(err, ErrSnapshotFormat) {
			t.Fatalf("Restore(%q) error = %v, want ErrSnapshotFormat", blob, err)
		}
	}
}

// TestRestore_RejectsTamperedChecksum flips a byte in the transcript and
// expects an integrity failure.
func TestRestore_RejectsTamperedChecksum(t *testing.T) {
	src, in, _, _ := snapshotApprovalAgent(t, "tamper")
	blob, err := src.Snapshot(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	tampered := bytes.Replace(blob, []byte("ORD-7"), []byte("ORD-9"), 1)
	if bytes.Equal(tampered, blob) {
		t.Fatal("tamper did not change the blob")
	}
	dst, err := New(newScriptedProvider(), "x", withTestPauseStores(), WithTools(deleteOrderTool()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dst.Restore(context.Background(), tampered); !errors.Is(err, ErrSnapshotIntegrity) {
		t.Fatalf("Restore(tampered) error = %v, want ErrSnapshotIntegrity", err)
	}
}

// TestRestore_RejectsNonPause builds a valid blob whose execution is not a
// pause and expects an integrity failure.
func TestRestore_RejectsNonPause(t *testing.T) {
	execution := Execution{ID: "e1", ConversationID: "c1", Status: ExecutionRunning}
	msgs := marshalMessages([]Message{{Role: RoleUser, Content: []ContentBlock{TextBlock{Text: "hi"}}}})
	blob := snapshotBlob{Format: snapshotFormatVersion, Execution: execution, Messages: msgs}
	blob.Checksum = snapshotChecksum(blob)
	raw, _ := json.Marshal(blob)

	dst, err := New(newScriptedProvider(), "x", withTestPauseStores())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dst.Restore(context.Background(), raw); !errors.Is(err, ErrSnapshotIntegrity) {
		t.Fatalf("Restore error = %v, want ErrSnapshotIntegrity", err)
	}
}

// TestRestore_DuplicatePreservesCAS verifies a second Restore of the same blob
// does not overwrite the created execution/conversation.
func TestRestore_DuplicatePreservesCAS(t *testing.T) {
	src, in, _, _ := snapshotApprovalAgent(t, "dup")
	blob, err := src.Snapshot(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	dst, err := New(
		newScriptedProvider(&ModelResponse{Text: "ok"}),
		"x",
		WithConversationStore(newTestMemoryStore()), WithExecutionStore(newTestExecutionStore()),
		WithTools(deleteOrderTool()),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dst.Restore(context.Background(), blob); err != nil {
		t.Fatalf("first Restore: %v", err)
	}
	// Second restore must conflict rather than overwrite: the conversation
	// already exists (revision CAS) and so does the create-only execution.
	_, err = dst.Restore(context.Background(), blob)
	if !errors.Is(err, ErrConversationConflict) && !errors.Is(err, ErrExecutionConflict) {
		t.Fatalf("second Restore error = %v, want a CAS conflict", err)
	}
}

// TestSnapshot_RejectsStaleVersion verifies Snapshot guards the interrupt's
// execution version.
func TestSnapshot_RejectsStaleVersion(t *testing.T) {
	src, in, _, _ := snapshotApprovalAgent(t, "stale")
	stale := *in
	stale.ExecutionVersion = in.ExecutionVersion + 100
	if _, err := src.Snapshot(context.Background(), &stale); !errors.Is(err, ErrExecutionConflict) {
		t.Fatalf("Snapshot(stale) error = %v, want ErrExecutionConflict", err)
	}
}
