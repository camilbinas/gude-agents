package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// --- helpers ---

func adminTool() tool.Tool {
	return tool.NewRaw("admin_op", "Admin operation",
		map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (string, error) { return "done", nil },
		tool.AllowRoles("admin"),
	)
}

func publicTool() tool.Tool {
	return tool.NewRaw("public_op", "Public operation",
		map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (string, error) { return "public", nil },
	)
}

func guestDeniedTool() tool.Tool {
	return tool.NewRaw("view_logs", "View logs",
		map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (string, error) { return "logs", nil },
		tool.DenyRoles("guest"),
	)
}

// --- Principal ---

func TestPrincipal_HasRole(t *testing.T) {
	p := Principal{Roles: []string{"admin", "support"}}
	if !p.HasRole("admin") {
		t.Error("expected HasRole(admin) = true")
	}
	if p.HasRole("guest") {
		t.Error("expected HasRole(guest) = false")
	}
}

func TestPrincipal_HasAnyRole(t *testing.T) {
	p := Principal{Roles: []string{"support"}}
	if !p.HasAnyRole("admin", "support") {
		t.Error("expected HasAnyRole(admin,support) = true for support principal")
	}
	if p.HasAnyRole("admin", "manager") {
		t.Error("expected HasAnyRole(admin,manager) = false for support principal")
	}
}

func TestPrincipal_Attr(t *testing.T) {
	p := Principal{Attrs: map[string]string{"org": "acme"}}
	if p.Attr("org") != "acme" {
		t.Errorf("Attr(org) = %q, want %q", p.Attr("org"), "acme")
	}
	if p.Attr("missing") != "" {
		t.Error("expected empty string for missing attr")
	}
}

// --- WithPrincipal / PrincipalFrom ---

func TestWithPrincipal_RoundTrip(t *testing.T) {
	c := Background().WithPrincipal(Principal{ID: "u1", Roles: []string{"admin"}})
	p, ok := PrincipalFrom(c)
	if !ok {
		t.Fatal("expected principal on context")
	}
	if p.ID != "u1" || !p.HasRole("admin") {
		t.Errorf("unexpected principal: %+v", p)
	}
}

func TestPrincipalFrom_NoPrincipal(t *testing.T) {
	c := Background()
	_, ok := PrincipalFrom(c)
	if ok {
		t.Error("expected no principal on fresh context")
	}
}

func TestPrincipalFrom_NonAgentContext(t *testing.T) {
	_, ok := PrincipalFrom(context.Background())
	if ok {
		t.Error("expected no principal on plain context.Background()")
	}
}

func TestWithPrincipal_SurvivedClone(t *testing.T) {
	c := Background().WithPrincipal(Principal{ID: "u2", Roles: []string{"support"}})
	clone := c.Clone()
	p, ok := PrincipalFrom(clone)
	if !ok {
		t.Fatal("expected principal to survive Clone()")
	}
	if p.ID != "u2" {
		t.Errorf("cloned principal ID = %q, want u2", p.ID)
	}
}

// --- tool.AllowRoles / DenyRoles ---

func TestTool_AllowRoles_Allows(t *testing.T) {
	at := adminTool()
	if !at.RolesAllowed([]string{"admin"}) {
		t.Error("admin should be allowed for admin tool")
	}
}

func TestTool_AllowRoles_Denies(t *testing.T) {
	at := adminTool()
	if at.RolesAllowed([]string{"guest"}) {
		t.Error("guest should not be allowed for admin tool")
	}
}

func TestTool_NoPolicy_AlwaysAllowed(t *testing.T) {
	pt := publicTool()
	if !pt.RolesAllowed(nil) {
		t.Error("tool with no policy should always be allowed")
	}
	if !pt.RolesAllowed([]string{"guest"}) {
		t.Error("tool with no policy should allow any role")
	}
}

func TestTool_DenyRoles_Blocks(t *testing.T) {
	gt := guestDeniedTool()
	if gt.RolesAllowed([]string{"guest"}) {
		t.Error("guest should be denied by DenyRoles")
	}
	if !gt.RolesAllowed([]string{"admin"}) {
		t.Error("admin should be allowed for DenyRoles(guest) tool")
	}
}

// --- RoleFilter + WithRoleEnforcement ---

func TestWithRoleEnforcement_AdminSeesAdminTool(t *testing.T) {
	// Admin calls a tool that requires "admin" role — should succeed.
	prov := newScriptedProvider(
		&ProviderResponse{
			ToolCalls: []tool.Call{{ToolUseID: "t1", Name: "admin_op", Input: json.RawMessage(`{}`)}},
		},
		&ProviderResponse{Text: "done"},
	)
	a, err := New(prov, prompt.Text("test"), []tool.Tool{adminTool()}, WithRoleEnforcement())
	if err != nil {
		t.Fatal(err)
	}
	c := Background().WithPrincipal(Principal{ID: "u1", Roles: []string{"admin"}})
	result, err := a.Invoke(c, "do it")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "done" {
		t.Errorf("result = %q", result)
	}
}

func TestWithRoleEnforcement_GuestCannotSeeAdminTool(t *testing.T) {
	// Guest invokes agent — admin_op should be filtered out of the tool spec
	// sent to the provider, so the LLM never sees it.
	prov := newCapturingProvider(&ProviderResponse{Text: "ok"})
	a, err := New(prov, prompt.Text("test"), []tool.Tool{adminTool(), publicTool()},
		WithRoleEnforcement())
	if err != nil {
		t.Fatal(err)
	}
	c := Background().WithPrincipal(Principal{ID: "u2", Roles: []string{"guest"}})
	_, err = a.Invoke(c, "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, call := range prov.captured {
		for _, s := range call.ToolConfig {
			if s.Name == "admin_op" {
				t.Error("admin_op should not be visible to guest role")
			}
		}
	}
}

func TestWithRequirePrincipal_NoPrincipal_ToolsHidden(t *testing.T) {
	// No principal set — all tools should be filtered out.
	prov := newCapturingProvider(&ProviderResponse{Text: "ok"})
	a, err := New(prov, prompt.Text("test"), []tool.Tool{publicTool()},
		WithRequirePrincipal())
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Invoke(Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(prov.captured) > 0 && len(prov.captured[0].ToolConfig) != 0 {
		t.Errorf("expected 0 tool specs without principal, got %d", len(prov.captured[0].ToolConfig))
	}
}

func TestWithPolicy_CustomFunc(t *testing.T) {
	// Custom policy: only allow tools whose name starts with "pub".
	prov := newCapturingProvider(&ProviderResponse{Text: "ok"})
	a, err := New(prov, prompt.Text("test"),
		[]tool.Tool{adminTool(), publicTool()},
		WithPolicy(func(c *Context, t tool.Tool) bool {
			return len(t.Spec.Name) >= 3 && t.Spec.Name[:3] == "pub"
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Invoke(Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range prov.captured {
		for _, s := range call.ToolConfig {
			if s.Name != "public_op" {
				t.Errorf("expected only public_op, got %q", s.Name)
			}
		}
	}
}

// TestRoleEnforcement_ExecutionTime verifies that even when a tool bypasses
// filterTools and ends up in availableTools, the execution-time role check
// still blocks the handler with a denial result.
func TestRoleEnforcement_ExecutionTime(t *testing.T) {
	handlerCalled := false
	restricted := tool.NewRaw("restricted", "admin only",
		map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (string, error) {
			handlerCalled = true
			return "secret", nil
		},
		tool.AllowRoles("admin"),
	)

	// No WithRoleEnforcement — filterTools passes everything.
	// The execution-time check inside executeToolsWithMiddleware is what we're testing.
	prov := newScriptedProvider(
		&ProviderResponse{
			ToolCalls: []tool.Call{{ToolUseID: "t1", Name: "restricted", Input: json.RawMessage(`{}`)}},
		},
		&ProviderResponse{Text: "access denied response"},
	)

	a, err := New(prov, prompt.Text("test"), []tool.Tool{restricted})
	if err != nil {
		t.Fatal(err)
	}

	c := Background().WithPrincipal(Principal{ID: "u1", Roles: []string{"guest"}})
	_, err = a.Invoke(c, "do it")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if handlerCalled {
		t.Error("handler should not have been called — execution-time role check should have blocked it")
	}
}
