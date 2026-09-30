package agent

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent/tool"
)

type recordingAuditSink struct {
	mu      sync.Mutex
	records []any
}

func (s *recordingAuditSink) WriteAudit(_ context.Context, record any) {
	s.mu.Lock()
	s.records = append(s.records, record)
	s.mu.Unlock()
}

func auditRecordsOf[T any](s *recordingAuditSink) []T {
	s.mu.Lock()
	defer s.mu.Unlock()
	var records []T
	for _, record := range s.records {
		if typed, ok := record.(T); ok {
			records = append(records, typed)
		}
	}
	return records
}

func TestWithAudit_RejectsNilSink(t *testing.T) {
	if _, err := New(mockProvider{}, "sys", WithAudit(nil)); err == nil {
		t.Fatal("expected nil AuditSink error")
	}
}

func TestWithObserver_RequiresCapability(t *testing.T) {
	if _, err := New(mockProvider{}, "sys", WithObserver(struct{}{})); err == nil {
		t.Fatal("expected observer capability validation error")
	}
	var observer *recordingInvokeObserver
	if _, err := New(mockProvider{}, "sys", WithObserver(observer)); err == nil {
		t.Fatal("expected typed-nil observer validation error")
	}
}

func TestDenialReasonConstants(t *testing.T) {
	got := []string{DenialReasonRolePolicy, DenialReasonAttrCondition, DenialReasonGuard, DenialReasonToolApprovalDenied}
	want := []string{"role_policy", "attr_condition", "guard", "tool_approval_denied"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("denial constant %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestAudit_DefaultRedactionAndStableJSON(t *testing.T) {
	sink := &recordingAuditSink{}
	provider := newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "call-1", Name: "echo", Input: json.RawMessage(`{"secret":"input"}`)}}},
		&ModelResponse{Text: "final response", Usage: TokenUsage{InputTokens: 3, OutputTokens: 4}},
	)
	echo := tool.NewRaw("echo", "echo", func(context.Context, json.RawMessage) (string, error) { return "secret output", nil })
	a, err := New(provider, "sys", WithName("audited"), WithTools(echo), WithAudit(sink))
	if err != nil {
		t.Fatal(err)
	}
	ctx := Background().WithConversationID("conv-1").WithPrincipal(Principal{ID: "user-1"})
	if _, err := a.Invoke(ctx, "secret message"); err != nil {
		t.Fatal(err)
	}

	invokes := auditRecordsOf[InvokeAuditRecord](sink)
	if len(invokes) != 2 || invokes[0].Event != AuditEventInvokeStart || invokes[1].Event != AuditEventInvokeEnd {
		t.Fatalf("unexpected invocation audit records: %#v", invokes)
	}
	if invokes[0].UserMessage != "" || invokes[1].Response != "" {
		t.Fatalf("default audit content was not redacted: %#v", invokes)
	}
	if invokes[1].Usage.Total() != 7 || invokes[1].Duration <= 0 || invokes[1].Principal.ID != "user-1" {
		t.Fatalf("incomplete invocation end audit record: %#v", invokes[1])
	}
	tools := auditRecordsOf[AuditRecord](sink)
	if len(tools) != 1 {
		t.Fatalf("got %d tool audit records, want 1", len(tools))
	}
	if tools[0].CallID != "call-1" || tools[0].ToolInput != nil || tools[0].ToolOutput != "" || !tools[0].Allowed {
		t.Fatalf("unexpected redacted tool audit record: %#v", tools[0])
	}
	encoded, err := json.Marshal(tools[0])
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	if wire["event"] != AuditEventToolCall || wire["call_id"] != "call-1" || wire["duration_ms"] == nil {
		t.Fatalf("unstable audit JSON: %s", encoded)
	}
	if _, exists := wire["tool_input"]; exists {
		t.Fatalf("redacted tool input present in JSON: %s", encoded)
	}
}

func TestAudit_WithAuditContentCapturesFinalResponseAndToolContent(t *testing.T) {
	sink := &recordingAuditSink{}
	input := json.RawMessage(`{"value":42}`)
	provider := newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "call-2", Name: "echo", Input: input}}},
		&ModelResponse{Text: "complete"},
	)
	echo := tool.NewRaw("echo", "echo", func(context.Context, json.RawMessage) (string, error) { return "output", nil })
	a, err := New(provider, "sys", WithTools(echo), WithAudit(sink, WithAuditContent()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Invoke(Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	invokes := auditRecordsOf[InvokeAuditRecord](sink)
	if len(invokes) != 2 || invokes[0].UserMessage != "hello" || invokes[1].Response != "complete" {
		t.Fatalf("audit invocation content missing: %#v", invokes)
	}
	tools := auditRecordsOf[AuditRecord](sink)
	if len(tools) != 1 || string(tools[0].ToolInput) != string(input) || tools[0].ToolOutput != "output" {
		t.Fatalf("audit tool content missing: %#v", tools)
	}
}

func TestAudit_RecordsDenialAndErrorJSON(t *testing.T) {
	sink := &recordingAuditSink{}
	restricted := tool.NewRaw("secret", "secret", func(context.Context, json.RawMessage) (string, error) { return "", nil }, tool.AllowRoles("admin"))
	provider := newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "denied", Name: "secret", Input: json.RawMessage(`{}`)}}},
		&ModelResponse{Text: "done"},
	)
	a, err := New(provider, "sys", WithTools(restricted), WithAudit(sink))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Invoke(Background().WithPrincipal(Principal{ID: "guest", Roles: []string{"guest"}}), "go"); err != nil {
		t.Fatal(err)
	}
	records := auditRecordsOf[AuditRecord](sink)
	if len(records) != 1 || records[0].Allowed || records[0].DenialReason != DenialReasonRolePolicy || records[0].Err == nil {
		t.Fatalf("unexpected denial record: %#v", records)
	}
	encoded, err := json.Marshal(records[0])
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	if wire["error"] == "" || wire["denial_reason"] != DenialReasonRolePolicy {
		t.Fatalf("denial JSON lost fields: %s", encoded)
	}
}

func TestAudit_InterruptRecordsAndApprovalRedaction(t *testing.T) {
	t.Run("human input", func(t *testing.T) {
		sink := &recordingAuditSink{}
		provider := newScriptedProvider(&ModelResponse{ToolCalls: []tool.Call{{
			ToolUseID: "ask-1", Name: "ask", Input: json.RawMessage(`{"reason":"r","question":"q"}`),
		}}})
		a, err := New(provider, "sys", WithTools(NewHumanInputTool("ask", "ask")), WithAudit(sink))
		if err != nil {
			t.Fatal(err)
		}
		result, err := a.Invoke(Background().WithConversationID("conv-h"), "help")
		if err != nil || result.Interrupt == nil {
			t.Fatalf("Invoke = %#v, %v", result, err)
		}
		records := auditRecordsOf[HandoffAuditRecord](sink)
		if len(records) != 1 || records[0].Reason != "r" || records[0].Question != "q" || records[0].InterruptID == "" {
			t.Fatalf("unexpected handoff audit: %#v", records)
		}
	})

	t.Run("approval", func(t *testing.T) {
		sink := &recordingAuditSink{}
		provider := newScriptedProvider(&ModelResponse{ToolCalls: []tool.Call{{
			ToolUseID: "approve-1", Name: "approve", Input: json.RawMessage(`{"secret":true}`),
		}}})
		approval := tool.NewRaw("approve", "approve", func(context.Context, json.RawMessage) (string, error) { return "ok", nil }, tool.RequiresApproval())
		a, err := New(provider, "sys", WithTools(approval), WithAudit(sink))
		if err != nil {
			t.Fatal(err)
		}
		result, err := a.Invoke(Background().WithConversationID("conv-a"), "go")
		if err != nil || result.Interrupt == nil {
			t.Fatalf("Invoke = %#v, %v", result, err)
		}
		records := auditRecordsOf[ApprovalAuditRecord](sink)
		if len(records) != 1 || records[0].CallID != "approve-1" || records[0].ToolInput != nil {
			t.Fatalf("unexpected approval audit: %#v", records)
		}
	})
}

type recordingInvokeObserver struct {
	mu      sync.Mutex
	records []InvokeRecord
}

func (o *recordingInvokeObserver) ObserveInvoke(ctx context.Context, record InvokeRecord) context.Context {
	o.mu.Lock()
	o.records = append(o.records, record)
	o.mu.Unlock()
	return ctx
}

type concurrentToolObserver struct {
	mu      sync.Mutex
	records []ToolCallRecord
}

func (o *concurrentToolObserver) ObserveTool(ctx context.Context, record ToolCallRecord) context.Context {
	o.mu.Lock()
	o.records = append(o.records, record)
	o.mu.Unlock()
	return ctx
}

func TestToolObserver_ConcurrentCallsAreComplete(t *testing.T) {
	observer := &concurrentToolObserver{}
	calls := []tool.Call{
		{ToolUseID: "one", Name: "echo", Input: json.RawMessage(`{"n":1}`)},
		{ToolUseID: "two", Name: "echo", Input: json.RawMessage(`{"n":2}`)},
	}
	provider := newScriptedProvider(&ModelResponse{ToolCalls: calls}, &ModelResponse{Text: "done"})
	echo := tool.NewRaw("echo", "echo", func(context.Context, json.RawMessage) (string, error) {
		time.Sleep(time.Millisecond)
		return "ok", nil
	})
	a, err := New(provider, "sys", WithTools(echo), WithObserver(observer))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Invoke(Background().WithConversationID("parallel"), "go"); err != nil {
		t.Fatal(err)
	}
	observer.mu.Lock()
	defer observer.mu.Unlock()
	if len(observer.records) != 4 {
		t.Fatalf("got %d tool records, want two starts and two ends", len(observer.records))
	}
	ends := map[string]ToolCallRecord{}
	for _, record := range observer.records {
		if record.Phase == End {
			ends[record.CallID] = record
		}
	}
	for _, id := range []string{"one", "two"} {
		record, ok := ends[id]
		if !ok || record.Name != "echo" || record.Output != "ok" || record.ConversationID != "parallel" || !record.Allowed || record.ResultIsError || record.Err != nil {
			t.Fatalf("incomplete end record for %s: %#v", id, record)
		}
	}
}

func TestAudit_InvokeEndCarriesProviderError(t *testing.T) {
	sink := &recordingAuditSink{}
	a, err := New(errorProvider{err: errors.New("down")}, "sys", WithAudit(sink))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = a.Invoke(Background(), "go")
	ends := auditRecordsOf[InvokeAuditRecord](sink)
	if len(ends) != 2 || ends[1].Err == nil {
		t.Fatalf("invoke error not audited: %#v", ends)
	}
}
