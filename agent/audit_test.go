package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// recordingHook collects AuditRecords for inspection in tests.
type recordingHook struct {
	mu                 sync.Mutex
	records            []AuditRecord
	invokeStartRecords []InvokeAuditRecord
	invokeEndRecords   []InvokeAuditRecord
	handoffRecords     []HandoffAuditRecord
	approvalRecords    []ApprovalAuditRecord
}

func (h *recordingHook) OnToolCall(r AuditRecord) {
	h.mu.Lock()
	h.records = append(h.records, r)
	h.mu.Unlock()
}

func (h *recordingHook) OnInvokeStart(r InvokeAuditRecord) {
	h.mu.Lock()
	h.invokeStartRecords = append(h.invokeStartRecords, r)
	h.mu.Unlock()
}

func (h *recordingHook) OnInvokeEnd(r InvokeAuditRecord) {
	h.mu.Lock()
	h.invokeEndRecords = append(h.invokeEndRecords, r)
	h.mu.Unlock()
}

func (h *recordingHook) OnHandoff(r HandoffAuditRecord) {
	h.mu.Lock()
	h.handoffRecords = append(h.handoffRecords, r)
	h.mu.Unlock()
}

func (h *recordingHook) OnApprovalRequest(r ApprovalAuditRecord) {
	h.mu.Lock()
	h.approvalRecords = append(h.approvalRecords, r)
	h.mu.Unlock()
}

func (h *recordingHook) all() []AuditRecord {
	h.mu.Lock()
	defer h.mu.Unlock()
	cp := make([]AuditRecord, len(h.records))
	copy(cp, h.records)
	return cp
}

// TestAuditHook_CalledOnSuccess verifies the hook fires after a successful tool call.
func TestAuditHook_CalledOnSuccess(t *testing.T) {
	hook := &recordingHook{}
	prov := newScriptedProvider(
		&ProviderResponse{ToolCalls: []tool.Call{{ToolUseID: "t1", Name: "echo", Input: json.RawMessage(`{}`)}}},
		&ProviderResponse{Text: "done"},
	)
	echoTool := tool.NewRaw("echo", "echoes", map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (string, error) { return "echoed", nil },
	)
	a, err := New(prov, prompt.Text("sys"), []tool.Tool{echoTool}, WithAuditHook(AuditConfig{Hook: hook, CaptureContent: true}))
	if err != nil {
		t.Fatal(err)
	}

	c := Background().WithPrincipal(Principal{ID: "u1", Roles: []string{"admin"}})
	if _, err := a.Invoke(c, "go"); err != nil {
		t.Fatal(err)
	}

	records := hook.all()
	if len(records) != 1 {
		t.Fatalf("expected 1 audit record, got %d", len(records))
	}
	r := records[0]
	if r.ToolName != "echo" {
		t.Errorf("ToolName = %q, want echo", r.ToolName)
	}
	if r.ToolOutput != "echoed" {
		t.Errorf("ToolOutput = %q, want echoed", r.ToolOutput)
	}
	if r.Err != nil {
		t.Errorf("Err = %v, want nil", r.Err)
	}
	if !r.Allowed {
		t.Error("Allowed = false, want true")
	}
	if r.Principal.ID != "u1" {
		t.Errorf("Principal.ID = %q, want u1", r.Principal.ID)
	}
	if r.Timestamp.IsZero() {
		t.Error("Timestamp should not be zero")
	}
}

// TestAuditHook_CalledOnDenial verifies the hook fires with Allowed=false when a role check denies.
func TestAuditHook_CalledOnDenial(t *testing.T) {
	hook := &recordingHook{}
	restricted := tool.NewRaw("secret", "admin only", map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (string, error) { return "secret", nil },
		tool.AllowRoles("admin"),
	)
	// No WithRoleEnforcement — the execution-time check handles the denial.
	prov := newScriptedProvider(
		&ProviderResponse{ToolCalls: []tool.Call{{ToolUseID: "t1", Name: "secret", Input: json.RawMessage(`{}`)}}},
		&ProviderResponse{Text: "denied response"},
	)
	a, err := New(prov, prompt.Text("sys"), []tool.Tool{restricted}, WithAuditHook(AuditConfig{Hook: hook}))
	if err != nil {
		t.Fatal(err)
	}

	c := Background().WithPrincipal(Principal{ID: "u2", Roles: []string{"guest"}})
	if _, err := a.Invoke(c, "try it"); err != nil {
		t.Fatal(err)
	}

	records := hook.all()
	if len(records) != 1 {
		t.Fatalf("expected 1 audit record, got %d", len(records))
	}
	r := records[0]
	if r.ToolName != "secret" {
		t.Errorf("ToolName = %q, want secret", r.ToolName)
	}
	if r.Allowed {
		t.Error("Allowed = true, want false for denied call")
	}
	if r.Principal.ID != "u2" {
		t.Errorf("Principal.ID = %q, want u2", r.Principal.ID)
	}
}

// TestAuditHook_CarriesCorrectPrincipal verifies the principal in the record matches what was set.
func TestAuditHook_CarriesCorrectPrincipal(t *testing.T) {
	hook := &recordingHook{}
	tl := tool.NewRaw("work", "work", map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (string, error) { return "done", nil },
	)
	prov := newScriptedProvider(
		&ProviderResponse{ToolCalls: []tool.Call{{ToolUseID: "t1", Name: "work", Input: json.RawMessage(`{}`)}}},
		&ProviderResponse{Text: "ok"},
	)
	a, err := New(prov, prompt.Text("sys"), []tool.Tool{tl}, WithAuditHook(AuditConfig{Hook: hook}))
	if err != nil {
		t.Fatal(err)
	}

	p := Principal{ID: "alice", Roles: []string{"editor"}, Attrs: map[string]string{"org": "acme"}}
	c := Background().WithPrincipal(p)
	if _, err := a.Invoke(c, "do it"); err != nil {
		t.Fatal(err)
	}

	records := hook.all()
	if len(records) == 0 {
		t.Fatal("expected at least 1 audit record")
	}
	got := records[0].Principal
	if got.ID != "alice" {
		t.Errorf("Principal.ID = %q, want alice", got.ID)
	}
	if got.Attr("org") != "acme" {
		t.Errorf("Principal.Attr(org) = %q, want acme", got.Attr("org"))
	}
}

// TestAuditHook_NoPrincipal verifies the hook fires with a zero Principal when none is set.
func TestAuditHook_NoPrincipal(t *testing.T) {
	hook := &recordingHook{}
	tl := tool.NewRaw("open", "open tool", map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (string, error) { return "result", nil },
	)
	prov := newScriptedProvider(
		&ProviderResponse{ToolCalls: []tool.Call{{ToolUseID: "t1", Name: "open", Input: json.RawMessage(`{}`)}}},
		&ProviderResponse{Text: "ok"},
	)
	a, err := New(prov, prompt.Text("sys"), []tool.Tool{tl}, WithAuditHook(AuditConfig{Hook: hook}))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := a.Invoke(Background(), "go"); err != nil {
		t.Fatal(err)
	}

	records := hook.all()
	if len(records) == 0 {
		t.Fatal("expected audit record")
	}
	if records[0].Principal.ID != "" {
		t.Errorf("expected zero-value principal, got ID=%q", records[0].Principal.ID)
	}
}

// =============================================================================
// Task 9.1 — WithAuditHook nil-hook error and constant values
// =============================================================================

// TestWithAuditHook_NilHookError verifies that passing AuditConfig{} (nil Hook)
// causes agent.New to return an error.
func TestWithAuditHook_NilHookError(t *testing.T) {
	_, err := New(mockProvider{}, prompt.Text("sys"), nil, WithAuditHook(AuditConfig{}))
	if err == nil {
		t.Fatal("expected error when Hook is nil, got nil")
	}
}

// TestDenialReasonConstants verifies that each exported DenialReason* constant
// equals its documented string value.
func TestDenialReasonConstants(t *testing.T) {
	cases := []struct {
		name     string
		got      string
		expected string
	}{
		{"DenialReasonRolePolicy", DenialReasonRolePolicy, "role_policy"},
		{"DenialReasonAttrCondition", DenialReasonAttrCondition, "attr_condition"},
		{"DenialReasonGuard", DenialReasonGuard, "guard"},
		{"DenialReasonToolApprovalDenied", DenialReasonToolApprovalDenied, "tool_approval_denied"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.expected {
				t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.expected)
			}
		})
	}
}

// =============================================================================
// Task 9.2 — Invocation audit (OnInvokeStart / OnInvokeEnd)
// =============================================================================

// TestInvokeAudit_DurationPositive verifies OnInvokeEnd.Duration > 0.
func TestInvokeAudit_DurationPositive(t *testing.T) {
	hook := &recordingHook{}
	sp := newScriptedProvider(&ProviderResponse{Text: "hello"})
	a, err := New(sp, prompt.Text("sys"), nil, WithAuditHook(AuditConfig{Hook: hook}))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := a.Invoke(Background(), "hi"); err != nil {
		t.Fatal(err)
	}

	hook.mu.Lock()
	ends := hook.invokeEndRecords
	hook.mu.Unlock()

	if len(ends) != 1 {
		t.Fatalf("expected 1 OnInvokeEnd record, got %d", len(ends))
	}
	if ends[0].Duration <= 0 {
		t.Errorf("expected Duration > 0, got %v", ends[0].Duration)
	}
}

// usageCapturingProvider returns a response with specific token counts so tests
// can assert that OnInvokeEnd.Usage matches what the provider reported.
type usageCapturingProvider struct {
	usage TokenUsage
}

func (p *usageCapturingProvider) Name() string { return "usage-mock" }

func (p *usageCapturingProvider) Converse(_ context.Context, _ ConverseParams) (*ProviderResponse, error) {
	return &ProviderResponse{Text: "ok", Usage: p.usage}, nil
}

func (p *usageCapturingProvider) ConverseStream(_ context.Context, _ ConverseParams, cb StreamCallback) (*ProviderResponse, error) {
	if cb != nil {
		cb("ok")
	}
	return &ProviderResponse{Text: "ok", Usage: p.usage}, nil
}

// TestInvokeAudit_UsageMatchesProvider verifies OnInvokeEnd.Usage matches
// the token counts reported by the provider.
func TestInvokeAudit_UsageMatchesProvider(t *testing.T) {
	hook := &recordingHook{}
	prov := &usageCapturingProvider{usage: TokenUsage{InputTokens: 42, OutputTokens: 17}}
	a, err := New(prov, prompt.Text("sys"), nil, WithAuditHook(AuditConfig{Hook: hook}))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := a.Invoke(Background(), "count tokens"); err != nil {
		t.Fatal(err)
	}

	hook.mu.Lock()
	ends := hook.invokeEndRecords
	hook.mu.Unlock()

	if len(ends) != 1 {
		t.Fatalf("expected 1 OnInvokeEnd record, got %d", len(ends))
	}
	u := ends[0].Usage
	if u.InputTokens != 42 || u.OutputTokens != 17 {
		t.Errorf("Usage = {%d, %d}, want {42, 17}", u.InputTokens, u.OutputTokens)
	}
}

// TestInvokeAudit_ErrPopulatedOnProviderError verifies OnInvokeEnd.Err is
// non-nil when the provider returns an error.
func TestInvokeAudit_ErrPopulatedOnProviderError(t *testing.T) {
	hook := &recordingHook{}
	cause := fmt.Errorf("provider down")
	a, err := New(errorProvider{err: cause}, prompt.Text("sys"), nil, WithAuditHook(AuditConfig{Hook: hook}))
	if err != nil {
		t.Fatal(err)
	}

	_, invokeErr := a.Invoke(Background(), "hello")
	if invokeErr == nil {
		t.Fatal("expected error from provider, got nil")
	}

	hook.mu.Lock()
	ends := hook.invokeEndRecords
	hook.mu.Unlock()

	if len(ends) != 1 {
		t.Fatalf("expected 1 OnInvokeEnd record, got %d", len(ends))
	}
	if ends[0].Err == nil {
		t.Error("expected OnInvokeEnd.Err to be non-nil, got nil")
	}
}

// TestInvokeAudit_CaptureContentFalse_OmitsContent verifies that when
// CaptureContent=false, UserMessage and Response are empty.
func TestInvokeAudit_CaptureContentFalse_OmitsContent(t *testing.T) {
	hook := &recordingHook{}
	sp := newScriptedProvider(&ProviderResponse{Text: "my response"})
	a, err := New(sp, prompt.Text("sys"), nil, WithAuditHook(AuditConfig{Hook: hook, CaptureContent: false}))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := a.Invoke(Background(), "my message"); err != nil {
		t.Fatal(err)
	}

	hook.mu.Lock()
	starts := hook.invokeStartRecords
	ends := hook.invokeEndRecords
	hook.mu.Unlock()

	if len(starts) != 1 || len(ends) != 1 {
		t.Fatalf("expected 1 start and 1 end record, got %d/%d", len(starts), len(ends))
	}
	if starts[0].UserMessage != "" {
		t.Errorf("expected empty UserMessage when CaptureContent=false, got %q", starts[0].UserMessage)
	}
	if ends[0].Response != "" {
		t.Errorf("expected empty Response when CaptureContent=false, got %q", ends[0].Response)
	}
}

// TestInvokeAudit_CaptureContentTrue_PopulatesContent verifies that when
// CaptureContent=true, UserMessage and Response are populated.
func TestInvokeAudit_CaptureContentTrue_PopulatesContent(t *testing.T) {
	hook := &recordingHook{}
	sp := newScriptedProvider(&ProviderResponse{Text: "my response"})
	a, err := New(sp, prompt.Text("sys"), nil, WithAuditHook(AuditConfig{Hook: hook, CaptureContent: true}))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := a.Invoke(Background(), "my message"); err != nil {
		t.Fatal(err)
	}

	hook.mu.Lock()
	starts := hook.invokeStartRecords
	ends := hook.invokeEndRecords
	hook.mu.Unlock()

	if len(starts) != 1 || len(ends) != 1 {
		t.Fatalf("expected 1 start and 1 end record, got %d/%d", len(starts), len(ends))
	}
	if starts[0].UserMessage != "my message" {
		t.Errorf("expected UserMessage=%q, got %q", "my message", starts[0].UserMessage)
	}
	if ends[0].Response != "my response" {
		t.Errorf("expected Response=%q, got %q", "my response", ends[0].Response)
	}
}

// =============================================================================
// Task 9.3 — DenialReason on AuditRecord
// =============================================================================

// TestAuditRecord_DenialReason_RolePolicy verifies that a role-policy denial
// sets DenialReasonRolePolicy on the AuditRecord.
func TestAuditRecord_DenialReason_RolePolicy(t *testing.T) {
	hook := &recordingHook{}
	restricted := tool.NewRaw("admin_tool", "admin only", map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (string, error) { return "ok", nil },
		tool.AllowRoles("admin"),
	)
	prov := newScriptedProvider(
		&ProviderResponse{ToolCalls: []tool.Call{{ToolUseID: "t1", Name: "admin_tool", Input: json.RawMessage(`{}`)}}},
		&ProviderResponse{Text: "done"},
	)
	a, err := New(prov, prompt.Text("sys"), []tool.Tool{restricted}, WithAuditHook(AuditConfig{Hook: hook}))
	if err != nil {
		t.Fatal(err)
	}

	c := Background().WithPrincipal(Principal{ID: "guest", Roles: []string{"guest"}})
	if _, err := a.Invoke(c, "call admin"); err != nil {
		t.Fatal(err)
	}

	records := hook.all()
	if len(records) != 1 {
		t.Fatalf("expected 1 audit record, got %d", len(records))
	}
	r := records[0]
	if r.Allowed {
		t.Error("expected Allowed=false")
	}
	if r.DenialReason != DenialReasonRolePolicy {
		t.Errorf("DenialReason = %q, want %q", r.DenialReason, DenialReasonRolePolicy)
	}
}

// TestAuditRecord_DenialReason_Guard verifies that a guard denial sets
// DenialReasonGuard on the AuditRecord.
func TestAuditRecord_DenialReason_Guard(t *testing.T) {
	hook := &recordingHook{}
	guardedTool := tool.NewRaw("guarded", "guarded tool", map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (string, error) { return "ok", nil },
		func(t *tool.Tool) {
			t.Guard = func(_ context.Context, _ json.RawMessage) (tool.Decision, error) {
				return tool.Deny("not allowed by guard"), nil
			}
		},
	)
	prov := newScriptedProvider(
		&ProviderResponse{ToolCalls: []tool.Call{{ToolUseID: "t1", Name: "guarded", Input: json.RawMessage(`{}`)}}},
		&ProviderResponse{Text: "done"},
	)
	a, err := New(prov, prompt.Text("sys"), []tool.Tool{guardedTool}, WithAuditHook(AuditConfig{Hook: hook}))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := a.Invoke(Background(), "call guarded"); err != nil {
		t.Fatal(err)
	}

	records := hook.all()
	if len(records) != 1 {
		t.Fatalf("expected 1 audit record, got %d", len(records))
	}
	r := records[0]
	if r.Allowed {
		t.Error("expected Allowed=false for guard denial")
	}
	if r.DenialReason != DenialReasonGuard {
		t.Errorf("DenialReason = %q, want %q", r.DenialReason, DenialReasonGuard)
	}
}

// TestAuditRecord_DenialReason_ToolApprovalDenied verifies that
// ResumeWithApproval with Allow=false sets DenialReasonToolApprovalDenied.
func TestAuditRecord_DenialReason_ToolApprovalDenied(t *testing.T) {
	hook := &recordingHook{}
	approvalTool := tool.NewRaw("needs_ok", "needs approval", map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (string, error) { return "approved result", nil },
		tool.RequiresApproval(),
	)
	prov := newScriptedProvider(
		&ProviderResponse{ToolCalls: []tool.Call{{ToolUseID: "t1", Name: "needs_ok", Input: json.RawMessage(`{}`)}}},
		&ProviderResponse{Text: "done after denial"},
	)
	a, err := New(prov, prompt.Text("sys"), []tool.Tool{approvalTool}, WithAuditHook(AuditConfig{Hook: hook}))
	if err != nil {
		t.Fatal(err)
	}

	c := Background().WithConversationID("conv-denial")
	invokeErr := a.InvokeStream(c, "do it", nil)
	if invokeErr != ErrToolApprovalRequired {
		t.Fatalf("expected ErrToolApprovalRequired, got %v", invokeErr)
	}

	ar, ok := GetApprovalRequest(c)
	if !ok {
		t.Fatal("expected ApprovalRequest to be set on context")
	}

	// Deny the approval — this should emit OnToolCall with DenialReasonToolApprovalDenied.
	if err := a.ResumeWithApproval(c, ar, tool.Decision{Allow: false, Reason: "denied by test"}, nil); err != nil {
		t.Fatalf("ResumeWithApproval: %v", err)
	}

	records := hook.all()
	var denialRecord *AuditRecord
	for i := range records {
		if records[i].DenialReason == DenialReasonToolApprovalDenied {
			denialRecord = &records[i]
			break
		}
	}
	if denialRecord == nil {
		t.Fatalf("expected audit record with DenialReasonToolApprovalDenied, records: %v", records)
	}
	if denialRecord.Allowed {
		t.Error("expected Allowed=false for approval denial")
	}
}

// TestAuditRecord_DenialReason_EmptyOnSuccess verifies that a successful tool
// call has an empty DenialReason.
func TestAuditRecord_DenialReason_EmptyOnSuccess(t *testing.T) {
	hook := &recordingHook{}
	tl := tool.NewRaw("ok_tool", "always succeeds", map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (string, error) { return "result", nil },
	)
	prov := newScriptedProvider(
		&ProviderResponse{ToolCalls: []tool.Call{{ToolUseID: "t1", Name: "ok_tool", Input: json.RawMessage(`{}`)}}},
		&ProviderResponse{Text: "done"},
	)
	a, err := New(prov, prompt.Text("sys"), []tool.Tool{tl}, WithAuditHook(AuditConfig{Hook: hook}))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := a.Invoke(Background(), "call it"); err != nil {
		t.Fatal(err)
	}

	records := hook.all()
	if len(records) != 1 {
		t.Fatalf("expected 1 audit record, got %d", len(records))
	}
	if records[0].DenialReason != "" {
		t.Errorf("expected empty DenialReason on success, got %q", records[0].DenialReason)
	}
	if !records[0].Allowed {
		t.Error("expected Allowed=true on success")
	}
}

// =============================================================================
// Task 9.4 — ConversationID threading and CaptureContent on AuditRecord
// =============================================================================

// TestAuditRecord_ConversationID_Threaded verifies that AuditRecord.ConversationID
// matches the conversation ID resolved for the invocation.
func TestAuditRecord_ConversationID_Threaded(t *testing.T) {
	hook := &recordingHook{}
	tl := tool.NewRaw("work", "worker", map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (string, error) { return "done", nil },
	)
	prov := newScriptedProvider(
		&ProviderResponse{ToolCalls: []tool.Call{{ToolUseID: "t1", Name: "work", Input: json.RawMessage(`{}`)}}},
		&ProviderResponse{Text: "finished"},
	)
	a, err := New(prov, prompt.Text("sys"), []tool.Tool{tl}, WithAuditHook(AuditConfig{Hook: hook}))
	if err != nil {
		t.Fatal(err)
	}

	c := Background().WithConversationID("test-conv-id")
	if _, err := a.Invoke(c, "go"); err != nil {
		t.Fatal(err)
	}

	records := hook.all()
	if len(records) != 1 {
		t.Fatalf("expected 1 audit record, got %d", len(records))
	}
	if records[0].ConversationID != "test-conv-id" {
		t.Errorf("ConversationID = %q, want %q", records[0].ConversationID, "test-conv-id")
	}
}

// TestAuditRecord_CaptureContentFalse_OmitsToolContent verifies that when
// CaptureContent=false, ToolInput is nil and ToolOutput is empty.
func TestAuditRecord_CaptureContentFalse_OmitsToolContent(t *testing.T) {
	hook := &recordingHook{}
	tl := tool.NewRaw("data_tool", "returns data", map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (string, error) { return "sensitive output", nil },
	)
	prov := newScriptedProvider(
		&ProviderResponse{ToolCalls: []tool.Call{{ToolUseID: "t1", Name: "data_tool", Input: json.RawMessage(`{"key":"val"}`)}}},
		&ProviderResponse{Text: "done"},
	)
	a, err := New(prov, prompt.Text("sys"), []tool.Tool{tl}, WithAuditHook(AuditConfig{Hook: hook, CaptureContent: false}))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := a.Invoke(Background(), "get data"); err != nil {
		t.Fatal(err)
	}

	records := hook.all()
	if len(records) != 1 {
		t.Fatalf("expected 1 audit record, got %d", len(records))
	}
	r := records[0]
	if r.ToolInput != nil {
		t.Errorf("expected ToolInput=nil when CaptureContent=false, got %s", r.ToolInput)
	}
	if r.ToolOutput != "" {
		t.Errorf("expected ToolOutput=%q when CaptureContent=false, got %q", "", r.ToolOutput)
	}
}

// TestAuditRecord_CaptureContentTrue_PopulatesToolContent verifies that when
// CaptureContent=true, ToolInput and ToolOutput are populated.
func TestAuditRecord_CaptureContentTrue_PopulatesToolContent(t *testing.T) {
	hook := &recordingHook{}
	tl := tool.NewRaw("data_tool", "returns data", map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (string, error) { return "sensitive output", nil },
	)
	input := json.RawMessage(`{"key":"val"}`)
	prov := newScriptedProvider(
		&ProviderResponse{ToolCalls: []tool.Call{{ToolUseID: "t1", Name: "data_tool", Input: input}}},
		&ProviderResponse{Text: "done"},
	)
	a, err := New(prov, prompt.Text("sys"), []tool.Tool{tl}, WithAuditHook(AuditConfig{Hook: hook, CaptureContent: true}))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := a.Invoke(Background(), "get data"); err != nil {
		t.Fatal(err)
	}

	records := hook.all()
	if len(records) != 1 {
		t.Fatalf("expected 1 audit record, got %d", len(records))
	}
	r := records[0]
	if string(r.ToolInput) != string(input) {
		t.Errorf("ToolInput = %s, want %s", r.ToolInput, input)
	}
	if r.ToolOutput != "sensitive output" {
		t.Errorf("ToolOutput = %q, want %q", r.ToolOutput, "sensitive output")
	}
}

// =============================================================================
// Task 9.5 — Handoff and approval audit events
// =============================================================================

// TestOnHandoff_CalledOnceWithMatchingConversationID verifies OnHandoff is called
// exactly once with the correct ConversationID when a handoff tool is triggered.
func TestOnHandoff_CalledOnceWithMatchingConversationID(t *testing.T) {
	hook := &recordingHook{}
	handoffTool := NewHandoffTool("ask_human", "ask when stuck")
	handoffInput := json.RawMessage(`{"reason":"need help","question":"what to do?"}`)
	prov := newScriptedProvider(
		&ProviderResponse{ToolCalls: []tool.Call{{ToolUseID: "t1", Name: "ask_human", Input: handoffInput}}},
	)
	a, err := New(prov, prompt.Text("sys"), []tool.Tool{handoffTool}, WithAuditHook(AuditConfig{Hook: hook}))
	if err != nil {
		t.Fatal(err)
	}

	c := Background().WithConversationID("handoff-conv-id")
	invokeErr := a.InvokeStream(c, "help me", nil)
	if invokeErr != ErrHandoffRequested {
		t.Fatalf("expected ErrHandoffRequested, got %v", invokeErr)
	}

	hook.mu.Lock()
	recs := hook.handoffRecords
	hook.mu.Unlock()

	if len(recs) != 1 {
		t.Fatalf("expected OnHandoff called once, got %d times", len(recs))
	}
	if recs[0].ConversationID != "handoff-conv-id" {
		t.Errorf("HandoffAuditRecord.ConversationID = %q, want %q", recs[0].ConversationID, "handoff-conv-id")
	}
}

// TestOnApprovalRequest_CalledOnceWithMatchingFields verifies OnApprovalRequest
// is called exactly once with the correct ConversationID and ToolName when a
// RequiresApproval tool is triggered.
func TestOnApprovalRequest_CalledOnceWithMatchingFields(t *testing.T) {
	hook := &recordingHook{}
	approvalTool := tool.NewRaw("sensitive_op", "sensitive operation", map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (string, error) { return "result", nil },
		tool.RequiresApproval(),
	)
	prov := newScriptedProvider(
		&ProviderResponse{ToolCalls: []tool.Call{{ToolUseID: "t1", Name: "sensitive_op", Input: json.RawMessage(`{}`)}}},
	)
	a, err := New(prov, prompt.Text("sys"), []tool.Tool{approvalTool}, WithAuditHook(AuditConfig{Hook: hook}))
	if err != nil {
		t.Fatal(err)
	}

	c := Background().WithConversationID("approval-conv-id")
	invokeErr := a.InvokeStream(c, "do sensitive op", nil)
	if invokeErr != ErrToolApprovalRequired {
		t.Fatalf("expected ErrToolApprovalRequired, got %v", invokeErr)
	}

	hook.mu.Lock()
	recs := hook.approvalRecords
	hook.mu.Unlock()

	if len(recs) != 1 {
		t.Fatalf("expected OnApprovalRequest called once, got %d times", len(recs))
	}
	if recs[0].ConversationID != "approval-conv-id" {
		t.Errorf("ApprovalAuditRecord.ConversationID = %q, want %q", recs[0].ConversationID, "approval-conv-id")
	}
	if recs[0].ToolName != "sensitive_op" {
		t.Errorf("ApprovalAuditRecord.ToolName = %q, want %q", recs[0].ToolName, "sensitive_op")
	}
}

// TestApprovalAuditRecord_CaptureContentFalse_NilToolInput verifies that when
// CaptureContent=false, ApprovalAuditRecord.ToolInput is nil.
func TestApprovalAuditRecord_CaptureContentFalse_NilToolInput(t *testing.T) {
	hook := &recordingHook{}
	approvalTool := tool.NewRaw("secret_op", "needs approval", map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (string, error) { return "result", nil },
		tool.RequiresApproval(),
	)
	prov := newScriptedProvider(
		&ProviderResponse{ToolCalls: []tool.Call{{ToolUseID: "t1", Name: "secret_op", Input: json.RawMessage(`{"secret":"data"}`)}}},
	)
	a, err := New(prov, prompt.Text("sys"), []tool.Tool{approvalTool},
		WithAuditHook(AuditConfig{Hook: hook, CaptureContent: false}))
	if err != nil {
		t.Fatal(err)
	}

	c := Background().WithConversationID("no-capture-conv")
	invokeErr := a.InvokeStream(c, "do secret op", nil)
	if invokeErr != ErrToolApprovalRequired {
		t.Fatalf("expected ErrToolApprovalRequired, got %v", invokeErr)
	}

	hook.mu.Lock()
	recs := hook.approvalRecords
	hook.mu.Unlock()

	if len(recs) != 1 {
		t.Fatalf("expected 1 OnApprovalRequest record, got %d", len(recs))
	}
	if recs[0].ToolInput != nil {
		t.Errorf("expected ToolInput=nil when CaptureContent=false, got %s", recs[0].ToolInput)
	}
}
