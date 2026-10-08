package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/camilbinas/gude-agents/agent/tool"
)

type recordingTransparentContextManager struct {
	recent []Message
}

func (m *recordingTransparentContextManager) HistoryBoundary(context.Context, string) (uint64, error) {
	return 0, nil
}

func (m *recordingTransparentContextManager) Prepare(_ context.Context, in ContextManagerInput) (ContextManagerOutput, error) {
	m.recent = append([]Message(nil), in.Recent...)
	messages := append([]Message(nil), in.Recent...)
	messages = append(messages, in.Current...)
	return ContextManagerOutput{Messages: messages}, nil
}

func toolResultIDs(messages []Message) ([]string, int) {
	var ids []string
	resultMessages := 0
	for _, message := range messages {
		containsResult := false
		for _, block := range message.Content {
			if result, ok := block.(ToolResultBlock); ok {
				ids = append(ids, result.ToolUseID)
				containsResult = true
			}
		}
		if containsResult {
			resultMessages++
		}
	}
	return ids, resultMessages
}

func assertToolResultIDs(t *testing.T, messages []Message, want ...string) {
	t.Helper()
	got, resultMessages := toolResultIDs(messages)
	if resultMessages != 1 {
		t.Fatalf("ToolResult message count = %d, want 1; messages=%#v", resultMessages, messages)
	}
	if len(got) != len(want) {
		t.Fatalf("ToolResult IDs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ToolResult IDs = %v, want %v", got, want)
		}
	}
}

func TestProviderProjectionOrdersOutOfOrderCanonicalToolResults(t *testing.T) {
	for _, withContextManager := range []bool{false, true} {
		t.Run(map[bool]string{false: "without_context_manager", true: "transparent_context_manager"}[withContextManager], func(t *testing.T) {
			conversations := newTestMemoryStore()
			_, err := conversations.Append(context.Background(), "conversation", []Message{
				{Role: RoleAssistant, Content: []ContentBlock{
					ToolUseBlock{ToolUseID: "A", Name: "alpha", Input: json.RawMessage(`{}`)},
					ToolUseBlock{ToolUseID: "B", Name: "beta", Input: json.RawMessage(`{}`)},
				}},
				{Role: RoleUser, Content: []ContentBlock{ToolResultBlock{ToolUseID: "B", Content: "B completed first"}}},
				{Role: RoleUser, Content: []ContentBlock{ToolResultBlock{ToolUseID: "A", Content: "A completed second"}}},
			}, 0)
			if err != nil {
				t.Fatal(err)
			}

			provider := newCapturingProvider(&ModelResponse{Text: "done"})
			options := []Option{
				WithTools(dummyTool("alpha", "alpha"), dummyTool("beta", "beta")),
				WithConversationStore(conversations),
			}
			var manager *recordingTransparentContextManager
			if withContextManager {
				manager = &recordingTransparentContextManager{}
				options = append(options, WithContextManager(manager))
			}
			a, err := New(provider, "sys", options...)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := a.Invoke(Background().WithConversationID("conversation"), "continue"); err != nil {
				t.Fatal(err)
			}

			if len(provider.captured) != 1 {
				t.Fatalf("provider calls = %d, want 1", len(provider.captured))
			}
			assertToolResultIDs(t, provider.captured[0].Messages, "A", "B")
			if manager != nil {
				assertToolResultIDs(t, manager.recent, "A", "B")
			}

			snapshot, err := conversations.Load(context.Background(), "conversation")
			if err != nil {
				t.Fatal(err)
			}
			canonicalIDs, _ := toolResultIDs(snapshot.Messages[:3])
			if len(canonicalIDs) != 2 || canonicalIDs[0] != "B" || canonicalIDs[1] != "A" {
				t.Fatalf("canonical ToolResult IDs = %v, want [B A]", canonicalIDs)
			}
		})
	}
}

func TestReconcileRetryRecoversOnlyUnknownSiblingWithOriginalKey(t *testing.T) {
	conversations, executions := newTestMemoryStore(), newTestExecutionStore()
	var aCalls, bCalls int
	var bKeys []string
	alpha := tool.NewRaw("alpha", "alpha", nil, func(context.Context, json.RawMessage) (string, error) {
		aCalls++
		return "A completed", nil
	})
	beta := tool.NewRaw("beta", "beta", nil, func(ctx context.Context, _ json.RawMessage) (string, error) {
		key, _ := tool.IdempotencyKey(ctx)
		bKeys = append(bKeys, key)
		bCalls++
		if bCalls == 1 {
			return "", tool.OutcomeUnknown(errors.New("lost beta response"))
		}
		return "B recovered", nil
	})
	provider := newScriptedProvider(&ModelResponse{ToolCalls: []tool.Call{
		{ToolUseID: "A", Name: "alpha", Input: json.RawMessage(`{}`)},
		{ToolUseID: "B", Name: "beta", Input: json.RawMessage(`{}`)},
	}})
	a, err := New(provider, "sys", WithTools(alpha, beta), WithConversationStore(conversations), WithExecutionStore(executions))
	if err != nil {
		t.Fatal(err)
	}

	result, err := a.Invoke(Background().WithConversationID("conversation"), "run both")
	if !errors.Is(err, ErrToolExecutionUncertain) {
		t.Fatalf("Invoke error = %v, want uncertainty", err)
	}
	execution, err := executions.Load(context.Background(), result.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	if execution.ToolBatch == nil || execution.ToolBatch.Calls[0].Status != ToolExecutionCompleted || execution.ToolBatch.Calls[1].Status != ToolExecutionInFlight {
		t.Fatalf("initial execution = %+v", execution)
	}
	originalKey := execution.ToolBatch.Calls[1].IdempotencyKey

	retried, err := a.ReconcileToolExecution(Background(), execution.ID, execution.Version, "B", ToolResolution{Outcome: ToolResolutionRetry})
	if err != nil {
		t.Fatal(err)
	}
	if retried.ToolBatch == nil || retried.ToolBatch.Calls[1].Status != ToolExecutionReady || retried.ToolBatch.Calls[1].IdempotencyKey != originalKey {
		t.Fatalf("retry execution = %+v, original B key=%q", retried, originalKey)
	}
	if _, err := a.RecoverExecution(Background(), execution.ID); err != nil {
		t.Fatal(err)
	}
	if aCalls != 1 || bCalls != 2 || len(bKeys) != 2 || bKeys[0] != originalKey || bKeys[1] != originalKey {
		t.Fatalf("alpha calls=%d beta calls=%d beta keys=%v, want alpha once and beta twice with %q", aCalls, bCalls, bKeys, originalKey)
	}
	if provider.callIndex != 1 {
		t.Fatalf("RecoverExecution made %d model calls, want 0", provider.callIndex-1)
	}

	snapshot, err := conversations.Load(context.Background(), "conversation")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Messages) != 4 {
		t.Fatalf("canonical messages = %#v", snapshot.Messages)
	}
	uses := snapshot.Messages[1].Content
	if len(uses) != 2 || uses[0].(ToolUseBlock).ToolUseID != "A" || uses[1].(ToolUseBlock).ToolUseID != "B" {
		t.Fatalf("canonical ToolUse IDs = %#v", uses)
	}
	canonicalIDs, resultMessages := toolResultIDs(snapshot.Messages)
	if resultMessages != 2 || len(canonicalIDs) != 2 || canonicalIDs[0] != "A" || canonicalIDs[1] != "B" {
		t.Fatalf("canonical ToolResult IDs = %v in %#v, want [A B]", canonicalIDs, snapshot.Messages)
	}
}
