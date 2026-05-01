// Example: Conditional tool availability with WithToolFilter.
//
// Demonstrates two patterns:
//  1. Role-based filtering — admin tools are hidden from regular users.
//  2. Workflow-stage gating — a tool unlocks another tool mid-invocation
//     by writing to the Context.
//
// Run:
//
//	go run ./tool-filter

package main

import (
	"context"
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/logging/auto"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// --- Tools ---

type ValidateCartInput struct {
	CartID string `json:"cart_id" description:"The cart ID to validate" required:"true"`
}

type SubmitOrderInput struct {
	CartID string `json:"cart_id" description:"The cart ID to submit" required:"true"`
}

type DeleteAccountInput struct {
	UserID string `json:"user_id" description:"The user ID to delete" required:"true"`
}

func validateCart(ctx context.Context, in ValidateCartInput) (string, error) {
	// Mark the cart as validated on the Context.
	c := ctx.(*agent.Context)
	c.Set("cart_validated", true)
	return fmt.Sprintf("Cart %s validated successfully. Ready to submit.", in.CartID), nil
}

func submitOrder(_ context.Context, in SubmitOrderInput) (string, error) {
	return fmt.Sprintf("Order submitted for cart %s. Confirmation #ORD-42.", in.CartID), nil
}

func deleteAccount(_ context.Context, in DeleteAccountInput) (string, error) {
	return fmt.Sprintf("Account %s deleted.", in.UserID), nil
}

func main() {
	provider := bedrock.Must(bedrock.Standard())

	tools := []tool.Tool{
		tool.New("validate_cart", "Validate a shopping cart before checkout", validateCart),
		tool.New("submit_order", "Submit a validated cart as an order", submitOrder),
		tool.New("delete_account", "Delete a user account (admin only)", deleteAccount),
	}

	// Each filter is a single concern, evaluated before each provider call.
	// A tool must pass ALL filters to be available (AND semantics).

	// Filter 1: Workflow gating — submit_order requires prior validation.
	workflowFilter := func(c *agent.Context, t tool.Tool) bool {
		if t.Spec.Name == "submit_order" {
			v, ok := c.Get("cart_validated")
			return ok && v.(bool)
		}
		return true
	}

	// Filter 2: Role-based access — delete_account is admin-only.
	roleFilter := func(c *agent.Context, t tool.Tool) bool {
		if t.Spec.Name == "delete_account" {
			role, _ := c.Value(userRoleKey{}).(string)
			return role == "admin"
		}
		return true
	}

	a, err := agent.New(provider,
		prompt.Text("You are a shopping and account management assistant. Help users validate carts, submit orders, and manage accounts. Use available tools. Be concise."),
		tools,
		agent.WithToolFilter(workflowFilter, roleFilter),
		auto.WithLogging(),
	)
	if err != nil {
		log.Fatal(err)
	}

	ctx := agent.Background()

	// --- Scenario 1: Workflow gating ---
	// The user asks to checkout. The agent must validate first (submit_order is hidden),
	// then after validate_cart sets the flag, submit_order becomes available.
	fmt.Println("=== Scenario 1: Workflow gating ===")
	result, err := a.Invoke(ctx, "Please validate and submit my cart ABC-123.")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Response:", result)
	fmt.Println()

	// --- Scenario 2: Role-based access ---
	// Regular user cannot see delete_account.
	fmt.Println("=== Scenario 2: Regular user ===")
	result, err = a.Invoke(ctx, "Delete account user-99.")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Response:", result)
	fmt.Println()

	// Admin user can see delete_account.
	fmt.Println("=== Scenario 3: Admin user ===")
	adminCtx := agent.NewContext(context.WithValue(ctx, userRoleKey{}, "admin"))
	result, err = a.Invoke(adminCtx, "Delete account user-99.")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Response:", result)
}

type userRoleKey struct{}
