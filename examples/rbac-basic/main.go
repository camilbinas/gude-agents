// Example: Role-based access control for tools.
//
// A single agent is configured with three tools:
//
//   - lookup_order   — available to everyone
//   - process_refund — available to support and admin roles only
//   - delete_account — available to admin only, also requires explicit approval
//
// Three simulated users invoke the same agent:
//   - alice (admin)   — sees all tools, delete_account pauses for approval
//   - bob   (support) — sees lookup and refund, cannot trigger delete_account
//   - guest           — sees lookup only
//
// Run:
//
//	go run ./rbac-basic
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load() //nolint

	provider := bedrock.Must(bedrock.Standard())

	a, err := agent.New(provider, prompt.Text(
		"You are a customer support backend assistant. "+
			"When the user asks you to perform an action, use the appropriate tool immediately. "+
			"Do not ask for confirmation — the caller has already confirmed. "+
			"If a tool you need is not available, say so briefly.",
	).String(),
		agent.WithTools(
			lookupOrderTool(),
			processRefundTool(),
			deleteAccountTool(),
		),
		agent.WithRoleEnforcement(),
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("=== alice (admin) — delete account ===")
	runAs(a, agent.Principal{ID: "alice", Roles: []string{"admin"}},
		"Delete account A-999 now.")

	fmt.Println("\n=== bob (support) — process refund ===")
	runAs(a, agent.Principal{ID: "bob", Roles: []string{"support"}},
		"Process a refund for order #1234")

	fmt.Println("\n=== guest — tries to delete account ===")
	runAs(a, agent.Principal{ID: "guest-42", Roles: []string{"guest"}},
		"Delete account A-999")
}

func runAs(a *agent.Agent, p agent.Principal, message string) {
	c := agent.Background().WithPrincipal(p)

	result, err := a.Invoke(c, message)
	if err != nil {
		log.Printf("error: %v", err)
		return
	}
	if result.StopReason == agent.StopInterrupt && result.Interrupt != nil && result.Interrupt.Approval != nil {
		for _, call := range result.Interrupt.Approval.Calls {
			fmt.Printf("[approval required] tool=%s input=%s\n", call.Name, call.Input)
		}
		fmt.Println("[auto-approving for demo]")
		result, err = a.Resume(c, result.Interrupt, agent.Approve())
		if err != nil {
			log.Printf("resume error: %v", err)
			return
		}
	}
	fmt.Println(result.Text)
}

func lookupOrderTool() tool.Tool {
	type input struct {
		OrderID string `json:"order_id" required:"true"`
	}
	return tool.New("lookup_order", "Look up an order by ID",
		func(_ context.Context, in input) (string, error) {
			return fmt.Sprintf(`{"order_id":%q,"total":"$89.99","status":"delivered"}`, in.OrderID), nil
		},
	)
}

func processRefundTool() tool.Tool {
	type input struct {
		OrderID string `json:"order_id" required:"true"`
	}
	return tool.New("process_refund", "Process a refund for an order",
		func(_ context.Context, in input) (string, error) {
			return fmt.Sprintf(`{"refunded":true,"order_id":%q}`, in.OrderID), nil
		},
		tool.AllowRoles("support", "admin"),
	)
}

func deleteAccountTool() tool.Tool {
	type input struct {
		AccountID string `json:"account_id" required:"true"`
	}
	return tool.New("delete_account", "Permanently delete a customer account",
		func(_ context.Context, in input) (string, error) {
			return fmt.Sprintf(`{"deleted":true,"account_id":%q}`, in.AccountID), nil
		},
		tool.AllowRoles("admin"),
		tool.RequiresApproval(),
	)
}
