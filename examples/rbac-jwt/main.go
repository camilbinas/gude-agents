// Example: RBAC + ABAC with JWT-derived identity.
//
// Demonstrates building an agent.Principal from JWT claims and using
// tool.AllowWhen for attribute-based access control (ABAC).
//
// Three users are simulated with different JWT claims:
//   - alice (admin, org=acme)    — can call all tools including acme-only tool
//   - bob   (support, org=acme)  — can call support tools + acme-only tool
//   - guest (guest, org=other)   — can only call the public tool
//
// Run:
//
//	go run ./rbac-jwt
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/golang-jwt/jwt/v5"
	"github.com/joho/godotenv"
)

// signingKey is a demo-only HMAC key. Never hard-code real keys.
var signingKey = []byte("demo-secret-key-not-for-production")

// agentClaims holds the JWT claims used to build a Principal.
type agentClaims struct {
	jwt.RegisteredClaims
	Roles []string `json:"roles"`
	Org   string   `json:"org"`
}

// signToken creates a signed JWT for the given user.
func signToken(sub string, roles []string, org string) (string, error) {
	claims := agentClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   sub,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
		Roles: roles,
		Org:   org,
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString(signingKey)
}

// parsePrincipal verifies a JWT token and returns an agent.Principal from its claims.
func parsePrincipal(tokenStr string) (agent.Principal, error) {
	var claims agentClaims
	_, err := jwt.ParseWithClaims(tokenStr, &claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return signingKey, nil
	})
	if err != nil {
		return agent.Principal{}, fmt.Errorf("invalid token: %w", err)
	}
	return agent.Principal{
		ID:    claims.Subject,
		Roles: claims.Roles,
		Attrs: map[string]string{"org": claims.Org},
	}, nil
}

func main() {
	godotenv.Load() //nolint

	provider := bedrock.Must(bedrock.Standard())

	a, err := agent.New(provider, prompt.Text(
		"You are a customer support assistant. "+
			"When asked to perform an action, use the appropriate tool immediately. "+
			"If a tool is not available, say so briefly.",
	), []tool.Tool{
		publicTool(),
		supportTool(),
		acmeOnlyTool(),
	},
		agent.WithRoleEnforcement(),
	)
	if err != nil {
		log.Fatal(err)
	}

	// Build tokens for each user scenario.
	aliceToken, _ := signToken("alice", []string{"admin"}, "acme")
	bobToken, _ := signToken("bob", []string{"support"}, "acme")
	guestToken, _ := signToken("guest-42", []string{"guest"}, "other")

	fmt.Println("=== alice (admin, org=acme) — lookup + acme report ===")
	runWithJWT(a, aliceToken, "Look up order #1234 and generate the acme report.")

	fmt.Println("\n=== bob (support, org=acme) — acme report only ===")
	runWithJWT(a, bobToken, "Generate the acme report.")

	fmt.Println("\n=== guest (guest, org=other) — public only ===")
	runWithJWT(a, guestToken, "Look up order #1234 and generate the acme report.")
}

func runWithJWT(a *agent.Agent, tokenStr string, message string) {
	p, err := parsePrincipal(tokenStr)
	if err != nil {
		log.Printf("token error: %v", err)
		return
	}
	fmt.Printf("[principal] id=%s roles=%v org=%s\n", p.ID, p.Roles, p.Attr("org"))
	c := agent.Background().WithPrincipal(p)
	err = a.InvokeStream(c, message, func(chunk string) {
		fmt.Print(chunk)
	})
	if errors.Is(err, agent.ErrToolApprovalRequired) {
		fmt.Println("\n[approval required — skipping in demo]")
		return
	}
	if err != nil {
		log.Printf("error: %v", err)
		return
	}
	fmt.Println()
}

// publicTool is available to all roles.
func publicTool() tool.Tool {
	return tool.NewRaw("lookup_order", "Look up an order by ID",
		map[string]any{
			"type":       "object",
			"properties": map[string]any{"order_id": map[string]any{"type": "string"}},
			"required":   []string{"order_id"},
		},
		func(_ context.Context, input json.RawMessage) (string, error) {
			var p struct {
				OrderID string `json:"order_id"`
			}
			json.Unmarshal(input, &p)
			return fmt.Sprintf(`{"order_id":%q,"status":"delivered"}`, p.OrderID), nil
		},
	)
}

// supportTool requires support or admin role.
func supportTool() tool.Tool {
	return tool.NewRaw("process_refund", "Process a refund",
		map[string]any{
			"type":       "object",
			"properties": map[string]any{"order_id": map[string]any{"type": "string"}},
			"required":   []string{"order_id"},
		},
		func(_ context.Context, input json.RawMessage) (string, error) {
			var p struct {
				OrderID string `json:"order_id"`
			}
			json.Unmarshal(input, &p)
			return fmt.Sprintf(`{"refunded":true,"order_id":%q}`, p.OrderID), nil
		},
		tool.AllowRoles("support", "admin"),
	)
}

// acmeOnlyTool requires support or admin role AND org == "acme" (ABAC).
func acmeOnlyTool() tool.Tool {
	return tool.NewRaw("acme_report", "Generate an Acme-exclusive report",
		map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (string, error) {
			return `{"report":"acme-q1","status":"generated"}`, nil
		},
		tool.AllowRoles("support", "admin"),
		tool.AllowWhen(func(attrs map[string]string) bool {
			return attrs["org"] == "acme"
		}),
	)
}
