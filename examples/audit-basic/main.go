// Example: Audit logging for role denials and human-input interrupts.
//
// Run:
//
//	go run ./audit-basic
package main

import (
	"context"
	"encoding/json"
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

type jsonAuditSink struct{ enc *json.Encoder }

func newJSONAuditSink() *jsonAuditSink {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return &jsonAuditSink{enc: enc}
}

func (s *jsonAuditSink) WriteAudit(_ context.Context, record any) {
	_ = s.enc.Encode(record)
}

func main() {
	godotenv.Load() //nolint

	a, err := agent.New(
		bedrock.Must(bedrock.Standard()),
		prompt.Text("You are a customer support assistant. When asked to perform an action, call the appropriate tool immediately. Do not ask for confirmation.").String(),
		agent.WithTools(
			utils.LookupOrderTool(),
			adminRefundTool(),
			agent.NewHumanInputTool("request_manager", "Escalate to a human manager"),
		),
		agent.WithName("audit-demo"),
		agent.WithRoleEnforcement(),
		agent.WithAudit(newJSONAuditSink(), agent.WithAuditContent()),
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("\n--- Scenario 1: guest tries an admin-only tool ---")
	guestCtx := agent.Background().
		WithPrincipal(agent.Principal{ID: "guest-99", Roles: []string{"guest"}}).
		WithConversationID("conv-guest")
	if _, err := a.Invoke(guestCtx, "Process a refund for order #1234 amount $49.99"); err != nil {
		log.Printf("invoke error: %v", err)
	}

	fmt.Println("\n--- Scenario 2: agent escalates to a human manager ---")
	adminCtx := agent.Background().
		WithPrincipal(agent.Principal{ID: "alice", Roles: []string{"admin"}}).
		WithConversationID("conv-admin")
	result, err := a.Invoke(adminCtx, "Escalate order #5678 to a manager now.")
	if err != nil {
		log.Printf("invoke error: %v", err)
		return
	}
	if result.StopReason == agent.StopInterrupt && result.Interrupt != nil {
		fmt.Printf("[human input required] %s\n", result.Interrupt.Input.Question)
	}
}

func adminRefundTool() tool.Tool {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"order_id": map[string]any{"type": "string"},
			"amount":   map[string]any{"type": "string"},
		},
		"required": []string{"order_id", "amount"},
	}
	return tool.NewRaw(
		"process_refund",
		"Process a refund for an order (admin only)",
		func(context.Context, json.RawMessage) (string, error) {
			return `{"status":"refunded","amount":"$49.99"}`, nil
		},
		tool.WithSchema(schema),
		tool.AllowRoles("admin"),
	)
}
