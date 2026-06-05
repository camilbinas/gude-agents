// Example: Audit logging with AuditHook.
//
// Demonstrates the full AuditHook interface by logging every audit event as
// a JSON line to stdout. The hook embeds NoopAuditHook so only the events it
// cares about need to be implemented.
//
// Two scenarios are shown back-to-back:
//
//  1. Role-based denial: a guest tries to call an admin-only tool.
//     The audit record shows allowed=false and denial_reason="role_policy".
//
//  2. Handoff: the agent escalates to a human manager.
//     The audit record shows the handoff reason and question.
//
// CaptureContent=true is set so that tool_input, tool_output, user_message,
// and response are included in the JSON output. Set it to false (the default)
// in production to avoid logging sensitive payloads.
//
// Run:
//
//	go run ./audit-basic
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/camilbinas/gude-agents/examples/utils"
	"github.com/joho/godotenv"
)

// ----------------------------------------------------------------------------
// AuditHook — logs every event as a JSON line directly from the record
// ----------------------------------------------------------------------------

type jsonAuditHook struct {
	agent.NoopAuditHook
	enc *json.Encoder
}

func newJSONAuditHook() *jsonAuditHook {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return &jsonAuditHook{enc: enc}
}

func (h *jsonAuditHook) OnInvokeStart(r agent.InvokeAuditRecord)       { h.enc.Encode(r) } //nolint
func (h *jsonAuditHook) OnInvokeEnd(r agent.InvokeAuditRecord)         { h.enc.Encode(r) } //nolint
func (h *jsonAuditHook) OnToolCall(r agent.AuditRecord)                { h.enc.Encode(r) } //nolint
func (h *jsonAuditHook) OnHandoff(r agent.HandoffAuditRecord)          { h.enc.Encode(r) } //nolint
func (h *jsonAuditHook) OnApprovalRequest(r agent.ApprovalAuditRecord) { h.enc.Encode(r) } //nolint

// ----------------------------------------------------------------------------
// Main
// ----------------------------------------------------------------------------

func main() {
	godotenv.Load() //nolint

	provider := bedrock.Must(bedrock.Standard())

	a, err := agent.New(
		provider,
		prompt.Text(
			"You are a customer support assistant. "+
				"When asked to perform an action, call the appropriate tool immediately. "+
				"Do not ask for confirmation.",
		),
		[]tool.Tool{
			utils.LookupOrderTool(),
			adminRefundTool(), // admin-only — demonstrates role-policy denial
			agent.NewHandoffTool("request_manager", "Escalate to a human manager"),
		},
		agent.WithName("audit-demo"),
		agent.WithRoleEnforcement(),
		agent.WithAuditHook(agent.AuditConfig{
			Hook:           newJSONAuditHook(),
			CaptureContent: true,
		}),
	)
	if err != nil {
		log.Fatal(err)
	}

	// -------------------------------------------------------------------------
	// Scenario 1: role-based denial
	// -------------------------------------------------------------------------
	fmt.Println("\n--- Scenario 1: guest tries an admin-only tool ---")
	guestCtx := agent.Background().
		WithPrincipal(agent.Principal{ID: "guest-99", Roles: []string{"guest"}}).
		WithConversationID("conv-guest")

	if _, err := a.Invoke(guestCtx, "Process a refund for order #1234 amount $49.99"); err != nil {
		log.Printf("invoke error: %v", err)
	}

	// -------------------------------------------------------------------------
	// Scenario 2: handoff
	// -------------------------------------------------------------------------
	fmt.Println("\n--- Scenario 2: agent escalates to a human manager ---")
	adminCtx := agent.Background().
		WithPrincipal(agent.Principal{ID: "alice", Roles: []string{"admin"}}).
		WithConversationID("conv-admin")

	err = a.InvokeStream(adminCtx, "Escalate order #5678 to a manager now.", func(chunk string) {
		fmt.Print(chunk)
	})
	if errors.Is(err, agent.ErrHandoffRequested) {
		fmt.Println() // newline after streamed text
	} else if err != nil {
		log.Printf("invoke error: %v", err)
	}
}

// ----------------------------------------------------------------------------
// Tools
// ----------------------------------------------------------------------------

// adminRefundTool is process_refund restricted to the "admin" role.
// utils.ProcessRefundTool doesn't carry a role policy, so we define a local
// variant to demonstrate the role-policy denial audit record.
func adminRefundTool() tool.Tool {
	return tool.NewRaw("process_refund", "Process a refund for an order (admin only)",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"order_id": map[string]any{"type": "string"},
				"amount":   map[string]any{"type": "string"},
			},
			"required": []string{"order_id", "amount"},
		},
		func(_ context.Context, _ json.RawMessage) (string, error) {
			return `{"status":"refunded","amount":"$49.99"}`, nil
		},
		tool.AllowRoles("admin"),
	)
}
