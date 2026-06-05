package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
	"pgregory.net/rapid"
)

// ---------------------------------------------------------------------------
// Shared generators
// ---------------------------------------------------------------------------

// auditIDGen generates non-empty alphanumeric+hyphen+underscore strings for use
// as IDs (principal ID, conversation ID, agent name, etc.).
var auditIDGen = rapid.StringMatching(`[a-z][a-z0-9_-]{0,30}`)

// auditMsgGen generates non-empty printable strings for user messages / responses.
var auditMsgGen = rapid.StringMatching(`[a-zA-Z0-9 .,!?]{1,80}`)

// auditRoleGen generates a non-empty role string.
var auditRoleGen = rapid.StringMatching(`[a-z]{3,10}`)

// genPrincipal draws a Principal from rapid.
func genPrincipal(t *rapid.T, label string) Principal {
	id := auditIDGen.Draw(t, label+"_principal_id")
	numRoles := rapid.IntRange(0, 4).Draw(t, label+"_num_roles")
	roles := make([]string, numRoles)
	for i := range numRoles {
		roles[i] = auditRoleGen.Draw(t, label+"_role")
	}
	return Principal{ID: id, Roles: roles}
}

// ---------------------------------------------------------------------------
// Property 1: OnInvokeStart receives correct principal and metadata
//
// For any Principal, agent name, and conversation ID, assert
// InvokeAuditRecord.Principal, AgentName, ConversationID match values
// resolved at invocation start.
//
// **Validates: Requirements 1.4, 1.5**
// ---------------------------------------------------------------------------

func TestAuditPBT_Property1_InvokeStartMetadata(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		principal := genPrincipal(rt, "p1")
		agentName := auditIDGen.Draw(rt, "agentName")
		convID := auditIDGen.Draw(rt, "convID")

		hook := &recordingHook{}
		sp := newScriptedProvider(&ProviderResponse{Text: "ok"})
		a, err := New(sp, prompt.Text("sys"), nil,
			WithName(agentName),
			WithAuditHook(AuditConfig{Hook: hook}),
		)
		if err != nil {
			rt.Fatalf("New: %v", err)
		}

		c := Background().WithConversationID(convID).WithPrincipal(principal)
		if _, err := a.Invoke(c, "hello"); err != nil {
			rt.Fatalf("Invoke: %v", err)
		}

		hook.mu.Lock()
		starts := hook.invokeStartRecords
		hook.mu.Unlock()

		if len(starts) != 1 {
			rt.Fatalf("expected 1 OnInvokeStart record, got %d", len(starts))
		}
		r := starts[0]

		if r.AgentName != agentName {
			rt.Fatalf("AgentName = %q, want %q", r.AgentName, agentName)
		}
		if r.ConversationID != convID {
			rt.Fatalf("ConversationID = %q, want %q", r.ConversationID, convID)
		}
		if r.Principal.ID != principal.ID {
			rt.Fatalf("Principal.ID = %q, want %q", r.Principal.ID, principal.ID)
		}
	})
}

// ---------------------------------------------------------------------------
// Property 2: CaptureContent=false always omits content from InvokeAuditRecord
//
// For any user message, assert OnInvokeStart.UserMessage and
// OnInvokeEnd.Response are always empty when CaptureContent=false.
//
// **Validates: Requirements 1.6, 5.4**
// ---------------------------------------------------------------------------

func TestAuditPBT_Property2_CaptureContentFalse_OmitsInvokeContent(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		userMessage := auditMsgGen.Draw(rt, "userMessage")
		responseText := auditMsgGen.Draw(rt, "responseText")

		hook := &recordingHook{}
		sp := newScriptedProvider(&ProviderResponse{Text: responseText})
		a, err := New(sp, prompt.Text("sys"), nil,
			WithAuditHook(AuditConfig{Hook: hook, CaptureContent: false}),
		)
		if err != nil {
			rt.Fatalf("New: %v", err)
		}

		if _, err := a.Invoke(Background(), userMessage); err != nil {
			rt.Fatalf("Invoke: %v", err)
		}

		hook.mu.Lock()
		starts := hook.invokeStartRecords
		ends := hook.invokeEndRecords
		hook.mu.Unlock()

		if len(starts) != 1 {
			rt.Fatalf("expected 1 OnInvokeStart record, got %d", len(starts))
		}
		if len(ends) != 1 {
			rt.Fatalf("expected 1 OnInvokeEnd record, got %d", len(ends))
		}

		if starts[0].UserMessage != "" {
			rt.Fatalf("OnInvokeStart.UserMessage = %q, want empty with CaptureContent=false", starts[0].UserMessage)
		}
		if ends[0].Response != "" {
			rt.Fatalf("OnInvokeEnd.Response = %q, want empty with CaptureContent=false", ends[0].Response)
		}
	})
}

// ---------------------------------------------------------------------------
// Property 3: CaptureContent=true always populates content in InvokeAuditRecord
//
// For any user message and response, assert OnInvokeStart.UserMessage and
// OnInvokeEnd.Response match actual values when CaptureContent=true.
//
// **Validates: Requirements 1.7, 5.5**
// ---------------------------------------------------------------------------

func TestAuditPBT_Property3_CaptureContentTrue_PopulatesInvokeContent(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		userMessage := auditMsgGen.Draw(rt, "userMessage")
		responseText := auditMsgGen.Draw(rt, "responseText")

		hook := &recordingHook{}
		sp := newScriptedProvider(&ProviderResponse{Text: responseText})
		a, err := New(sp, prompt.Text("sys"), nil,
			WithAuditHook(AuditConfig{Hook: hook, CaptureContent: true}),
		)
		if err != nil {
			rt.Fatalf("New: %v", err)
		}

		if _, err := a.Invoke(Background(), userMessage); err != nil {
			rt.Fatalf("Invoke: %v", err)
		}

		hook.mu.Lock()
		starts := hook.invokeStartRecords
		ends := hook.invokeEndRecords
		hook.mu.Unlock()

		if len(starts) != 1 {
			rt.Fatalf("expected 1 OnInvokeStart record, got %d", len(starts))
		}
		if len(ends) != 1 {
			rt.Fatalf("expected 1 OnInvokeEnd record, got %d", len(ends))
		}

		if starts[0].UserMessage != userMessage {
			rt.Fatalf("OnInvokeStart.UserMessage = %q, want %q", starts[0].UserMessage, userMessage)
		}
		if ends[0].Response != responseText {
			rt.Fatalf("OnInvokeEnd.Response = %q, want %q", ends[0].Response, responseText)
		}
	})
}

// ---------------------------------------------------------------------------
// Property 4: Role-policy denial always sets DenialReasonRolePolicy
//
// For any tool with AllowRoles and any principal whose roles don't satisfy it,
// assert Allowed=false and DenialReason=DenialReasonRolePolicy.
//
// **Validates: Requirements 2.3**
// ---------------------------------------------------------------------------

func TestAuditPBT_Property4_RolePolicyDenial(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// A role the tool requires — guaranteed to be different from the caller's roles.
		requiredRole := "superadmin_" + auditIDGen.Draw(rt, "reqRole")

		// Caller has a different role that never equals requiredRole.
		callerRole := "guest_" + auditIDGen.Draw(rt, "callerRole")

		hook := &recordingHook{}
		restrictedTool := tool.NewRaw(
			"restricted",
			"admin only",
			map[string]any{"type": "object"},
			func(_ context.Context, _ json.RawMessage) (string, error) { return "ok", nil },
			tool.AllowRoles(requiredRole),
		)
		prov := newScriptedProvider(
			&ProviderResponse{ToolCalls: []tool.Call{{ToolUseID: "t1", Name: "restricted", Input: json.RawMessage(`{}`)}}},
			&ProviderResponse{Text: "done"},
		)
		a, err := New(prov, prompt.Text("sys"), []tool.Tool{restrictedTool},
			WithAuditHook(AuditConfig{Hook: hook}),
		)
		if err != nil {
			rt.Fatalf("New: %v", err)
		}

		callerID := auditIDGen.Draw(rt, "callerID")
		c := Background().WithPrincipal(Principal{ID: callerID, Roles: []string{callerRole}})
		if _, err := a.Invoke(c, "call it"); err != nil {
			rt.Fatalf("Invoke: %v", err)
		}

		records := hook.all()
		if len(records) != 1 {
			rt.Fatalf("expected 1 audit record, got %d", len(records))
		}
		r := records[0]
		if r.Allowed {
			rt.Fatalf("expected Allowed=false for role-policy denial")
		}
		if r.DenialReason != DenialReasonRolePolicy {
			rt.Fatalf("DenialReason = %q, want %q", r.DenialReason, DenialReasonRolePolicy)
		}
	})
}

// ---------------------------------------------------------------------------
// Property 5: Guard denial always sets DenialReasonGuard
//
// For any tool whose Guard returns a denial, assert Allowed=false and
// DenialReason=DenialReasonGuard.
//
// **Validates: Requirements 2.5**
// ---------------------------------------------------------------------------

func TestAuditPBT_Property5_GuardDenial(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		denialMsg := "denied: " + auditIDGen.Draw(rt, "denialMsg")

		hook := &recordingHook{}
		guardedTool := tool.NewRaw(
			"guarded",
			"guarded tool",
			map[string]any{"type": "object"},
			func(_ context.Context, _ json.RawMessage) (string, error) { return "ok", nil },
			func(t *tool.Tool) {
				t.Guard = func(_ context.Context, _ json.RawMessage) (tool.Decision, error) {
					return tool.Deny(denialMsg), nil
				}
			},
		)
		prov := newScriptedProvider(
			&ProviderResponse{ToolCalls: []tool.Call{{ToolUseID: "t1", Name: "guarded", Input: json.RawMessage(`{}`)}}},
			&ProviderResponse{Text: "done"},
		)
		a, err := New(prov, prompt.Text("sys"), []tool.Tool{guardedTool},
			WithAuditHook(AuditConfig{Hook: hook}),
		)
		if err != nil {
			rt.Fatalf("New: %v", err)
		}

		if _, err := a.Invoke(Background(), "call it"); err != nil {
			rt.Fatalf("Invoke: %v", err)
		}

		records := hook.all()
		if len(records) != 1 {
			rt.Fatalf("expected 1 audit record, got %d", len(records))
		}
		r := records[0]
		if r.Allowed {
			rt.Fatalf("expected Allowed=false for guard denial")
		}
		if r.DenialReason != DenialReasonGuard {
			rt.Fatalf("DenialReason = %q, want %q", r.DenialReason, DenialReasonGuard)
		}
	})
}

// ---------------------------------------------------------------------------
// Property 6: Allowed=true always produces empty DenialReason
//
// For any successful tool call, assert Allowed=true and DenialReason="".
//
// **Validates: Requirements 2.7**
// ---------------------------------------------------------------------------

func TestAuditPBT_Property6_SuccessHasEmptyDenialReason(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		toolOutput := auditMsgGen.Draw(rt, "toolOutput")

		hook := &recordingHook{}
		successTool := tool.NewRaw(
			"success_tool",
			"always succeeds",
			map[string]any{"type": "object"},
			func(_ context.Context, _ json.RawMessage) (string, error) { return toolOutput, nil },
		)
		prov := newScriptedProvider(
			&ProviderResponse{ToolCalls: []tool.Call{{ToolUseID: "t1", Name: "success_tool", Input: json.RawMessage(`{}`)}}},
			&ProviderResponse{Text: "done"},
		)
		a, err := New(prov, prompt.Text("sys"), []tool.Tool{successTool},
			WithAuditHook(AuditConfig{Hook: hook}),
		)
		if err != nil {
			rt.Fatalf("New: %v", err)
		}

		if _, err := a.Invoke(Background(), "call it"); err != nil {
			rt.Fatalf("Invoke: %v", err)
		}

		records := hook.all()
		if len(records) != 1 {
			rt.Fatalf("expected 1 audit record, got %d", len(records))
		}
		r := records[0]
		if !r.Allowed {
			rt.Fatalf("expected Allowed=true for successful tool call")
		}
		if r.DenialReason != "" {
			rt.Fatalf("DenialReason = %q, want empty for Allowed=true", r.DenialReason)
		}
	})
}

// ---------------------------------------------------------------------------
// Property 7: ConversationID is propagated to every OnToolCall record
//
// For any conversation ID set on the invocation context, assert every
// AuditRecord.ConversationID during that invocation matches exactly.
//
// **Validates: Requirements 3.2**
// ---------------------------------------------------------------------------

func TestAuditPBT_Property7_ConversationIDPropagatedToToolRecords(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		convID := auditIDGen.Draw(rt, "convID")
		numTools := rapid.IntRange(1, 4).Draw(rt, "numTools")

		hook := &recordingHook{}

		// Build N tools and a provider that calls them all.
		tools := make([]tool.Tool, numTools)
		calls := make([]tool.Call, numTools)
		for i := range numTools {
			name := "tool_" + string(rune('a'+i))
			tools[i] = tool.NewRaw(name, name+" desc", map[string]any{"type": "object"},
				func(_ context.Context, _ json.RawMessage) (string, error) { return "ok", nil },
			)
			calls[i] = tool.Call{ToolUseID: "tc" + string(rune('0'+i)), Name: name, Input: json.RawMessage(`{}`)}
		}
		prov := newScriptedProvider(
			&ProviderResponse{ToolCalls: calls},
			&ProviderResponse{Text: "done"},
		)

		a, err := New(prov, prompt.Text("sys"), tools,
			WithAuditHook(AuditConfig{Hook: hook}),
		)
		if err != nil {
			rt.Fatalf("New: %v", err)
		}

		c := Background().WithConversationID(convID)
		if _, err := a.Invoke(c, "call tools"); err != nil {
			rt.Fatalf("Invoke: %v", err)
		}

		records := hook.all()
		if len(records) != numTools {
			rt.Fatalf("expected %d audit records, got %d", numTools, len(records))
		}
		for i, r := range records {
			if r.ConversationID != convID {
				rt.Fatalf("records[%d].ConversationID = %q, want %q", i, r.ConversationID, convID)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Property 8: CaptureContent=false always omits ToolInput and ToolOutput
//
// For any tool call with any input/output, assert ToolInput=nil and
// ToolOutput="" when CaptureContent=false.
//
// **Validates: Requirements 3.4, 5.4**
// ---------------------------------------------------------------------------

func TestAuditPBT_Property8_CaptureContentFalse_OmitsToolContent(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		toolOutput := auditMsgGen.Draw(rt, "toolOutput")

		hook := &recordingHook{}
		dataTool := tool.NewRaw(
			"data_tool",
			"returns data",
			map[string]any{"type": "object"},
			func(_ context.Context, _ json.RawMessage) (string, error) { return toolOutput, nil },
		)
		input := json.RawMessage(`{"key":"secret"}`)
		prov := newScriptedProvider(
			&ProviderResponse{ToolCalls: []tool.Call{{ToolUseID: "t1", Name: "data_tool", Input: input}}},
			&ProviderResponse{Text: "done"},
		)
		a, err := New(prov, prompt.Text("sys"), []tool.Tool{dataTool},
			WithAuditHook(AuditConfig{Hook: hook, CaptureContent: false}),
		)
		if err != nil {
			rt.Fatalf("New: %v", err)
		}

		if _, err := a.Invoke(Background(), "go"); err != nil {
			rt.Fatalf("Invoke: %v", err)
		}

		records := hook.all()
		if len(records) != 1 {
			rt.Fatalf("expected 1 audit record, got %d", len(records))
		}
		r := records[0]
		if r.ToolInput != nil {
			rt.Fatalf("ToolInput = %s, want nil with CaptureContent=false", r.ToolInput)
		}
		if r.ToolOutput != "" {
			rt.Fatalf("ToolOutput = %q, want empty with CaptureContent=false", r.ToolOutput)
		}
	})
}

// ---------------------------------------------------------------------------
// Property 9: CaptureContent=true always populates ToolInput and ToolOutput
//
// For any tool call, assert ToolInput equals raw JSON input and ToolOutput
// equals handler output string when CaptureContent=true.
//
// **Validates: Requirements 3.5, 5.5**
// ---------------------------------------------------------------------------

func TestAuditPBT_Property9_CaptureContentTrue_PopulatesToolContent(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		toolOutput := auditMsgGen.Draw(rt, "toolOutput")

		hook := &recordingHook{}
		dataTool := tool.NewRaw(
			"data_tool",
			"returns data",
			map[string]any{"type": "object"},
			func(_ context.Context, _ json.RawMessage) (string, error) { return toolOutput, nil },
		)
		input := json.RawMessage(`{"key":"value"}`)
		prov := newScriptedProvider(
			&ProviderResponse{ToolCalls: []tool.Call{{ToolUseID: "t1", Name: "data_tool", Input: input}}},
			&ProviderResponse{Text: "done"},
		)
		a, err := New(prov, prompt.Text("sys"), []tool.Tool{dataTool},
			WithAuditHook(AuditConfig{Hook: hook, CaptureContent: true}),
		)
		if err != nil {
			rt.Fatalf("New: %v", err)
		}

		if _, err := a.Invoke(Background(), "go"); err != nil {
			rt.Fatalf("Invoke: %v", err)
		}

		records := hook.all()
		if len(records) != 1 {
			rt.Fatalf("expected 1 audit record, got %d", len(records))
		}
		r := records[0]
		if string(r.ToolInput) != string(input) {
			rt.Fatalf("ToolInput = %s, want %s", r.ToolInput, input)
		}
		if r.ToolOutput != toolOutput {
			rt.Fatalf("ToolOutput = %q, want %q", r.ToolOutput, toolOutput)
		}
	})
}

// ---------------------------------------------------------------------------
// Property 10: Any handoff invocation produces exactly one OnHandoff call
//
// For any invocation triggering the handoff tool, assert OnHandoff called
// exactly once and HandoffAuditRecord.ConversationID matches.
//
// **Validates: Requirements 4.5, 4.11**
// ---------------------------------------------------------------------------

func TestAuditPBT_Property10_HandoffProducesOneOnHandoffCall(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		convID := auditIDGen.Draw(rt, "convID")
		reason := auditMsgGen.Draw(rt, "reason")
		question := auditMsgGen.Draw(rt, "question")

		hook := &recordingHook{}
		handoffTool := NewHandoffTool("ask_human", "ask when stuck")

		handoffInput, _ := json.Marshal(map[string]string{
			"reason":   reason,
			"question": question,
		})
		prov := newScriptedProvider(
			&ProviderResponse{ToolCalls: []tool.Call{{ToolUseID: "t1", Name: "ask_human", Input: handoffInput}}},
		)
		a, err := New(prov, prompt.Text("sys"), []tool.Tool{handoffTool},
			WithAuditHook(AuditConfig{Hook: hook}),
		)
		if err != nil {
			rt.Fatalf("New: %v", err)
		}

		c := Background().WithConversationID(convID)
		invokeErr := a.InvokeStream(c, "help me", nil)
		if invokeErr != ErrHandoffRequested {
			rt.Fatalf("expected ErrHandoffRequested, got %v", invokeErr)
		}

		hook.mu.Lock()
		recs := hook.handoffRecords
		hook.mu.Unlock()

		if len(recs) != 1 {
			rt.Fatalf("expected OnHandoff called exactly once, got %d times", len(recs))
		}
		if recs[0].ConversationID != convID {
			rt.Fatalf("HandoffAuditRecord.ConversationID = %q, want %q", recs[0].ConversationID, convID)
		}
	})
}

// ---------------------------------------------------------------------------
// Property 11: Any tool-approval-required invocation produces exactly one
// OnApprovalRequest call
//
// For any invocation reaching a RequiresApproval tool, assert OnApprovalRequest
// called exactly once with matching ConversationID and ToolName.
//
// **Validates: Requirements 4.6**
// ---------------------------------------------------------------------------

func TestAuditPBT_Property11_ApprovalProducesOneOnApprovalRequestCall(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		convID := auditIDGen.Draw(rt, "convID")
		toolName := "sensitive_" + auditIDGen.Draw(rt, "toolName")

		hook := &recordingHook{}
		approvalTool := tool.NewRaw(
			toolName,
			"sensitive operation requiring approval",
			map[string]any{"type": "object"},
			func(_ context.Context, _ json.RawMessage) (string, error) { return "result", nil },
			tool.RequiresApproval(),
		)
		prov := newScriptedProvider(
			&ProviderResponse{ToolCalls: []tool.Call{{ToolUseID: "t1", Name: toolName, Input: json.RawMessage(`{}`)}}},
		)
		a, err := New(prov, prompt.Text("sys"), []tool.Tool{approvalTool},
			WithAuditHook(AuditConfig{Hook: hook}),
		)
		if err != nil {
			rt.Fatalf("New: %v", err)
		}

		c := Background().WithConversationID(convID)
		invokeErr := a.InvokeStream(c, "do it", nil)
		if invokeErr != ErrToolApprovalRequired {
			rt.Fatalf("expected ErrToolApprovalRequired, got %v", invokeErr)
		}

		hook.mu.Lock()
		recs := hook.approvalRecords
		hook.mu.Unlock()

		if len(recs) != 1 {
			rt.Fatalf("expected OnApprovalRequest called exactly once, got %d times", len(recs))
		}
		if recs[0].ConversationID != convID {
			rt.Fatalf("ApprovalAuditRecord.ConversationID = %q, want %q", recs[0].ConversationID, convID)
		}
		if recs[0].ToolName != toolName {
			rt.Fatalf("ApprovalAuditRecord.ToolName = %q, want %q", recs[0].ToolName, toolName)
		}
	})
}

// ---------------------------------------------------------------------------
// Property 12: CaptureContent=false always produces nil ToolInput in
// ApprovalAuditRecord
//
// For any tool-approval trigger, assert ApprovalAuditRecord.ToolInput=nil
// when CaptureContent=false.
//
// **Validates: Requirements 4.9**
// ---------------------------------------------------------------------------

func TestAuditPBT_Property12_CaptureContentFalse_NilApprovalToolInput(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		convID := auditIDGen.Draw(rt, "convID")
		// Generate non-trivial tool input to ensure it would appear if capture were on.
		keyVal := auditIDGen.Draw(rt, "keyVal")
		toolInput, _ := json.Marshal(map[string]string{"secret": keyVal})

		hook := &recordingHook{}
		approvalTool := tool.NewRaw(
			"secret_op",
			"needs approval",
			map[string]any{"type": "object"},
			func(_ context.Context, _ json.RawMessage) (string, error) { return "result", nil },
			tool.RequiresApproval(),
		)
		prov := newScriptedProvider(
			&ProviderResponse{ToolCalls: []tool.Call{{ToolUseID: "t1", Name: "secret_op", Input: toolInput}}},
		)
		a, err := New(prov, prompt.Text("sys"), []tool.Tool{approvalTool},
			WithAuditHook(AuditConfig{Hook: hook, CaptureContent: false}),
		)
		if err != nil {
			rt.Fatalf("New: %v", err)
		}

		c := Background().WithConversationID(convID)
		invokeErr := a.InvokeStream(c, "do secret op", nil)
		if invokeErr != ErrToolApprovalRequired {
			rt.Fatalf("expected ErrToolApprovalRequired, got %v", invokeErr)
		}

		hook.mu.Lock()
		recs := hook.approvalRecords
		hook.mu.Unlock()

		if len(recs) != 1 {
			rt.Fatalf("expected 1 OnApprovalRequest record, got %d", len(recs))
		}
		if recs[0].ToolInput != nil {
			rt.Fatalf("ApprovalAuditRecord.ToolInput = %s, want nil with CaptureContent=false", recs[0].ToolInput)
		}
	})
}
