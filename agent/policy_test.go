package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// --- helpers ---

func adminTool() tool.Tool {
	return newTestRaw("admin_op", "Admin operation",
		map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (string, error) { return "done", nil },
		tool.AllowRoles("admin"),
	)
}

func publicTool() tool.Tool {
	return newTestRaw("public_op", "Public operation",
		map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (string, error) { return "public", nil },
	)
}

func guestDeniedTool() tool.Tool {
	return newTestRaw("view_logs", "View logs",
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
		&ModelResponse{
			ToolCalls: []tool.Call{{ToolUseID: "t1", Name: "admin_op", Input: json.RawMessage(`{}`)}},
		},
		&ModelResponse{Text: "done"},
	)
	a, err := New(prov, "test", WithTools(adminTool()), WithRoleEnforcement())
	if err != nil {
		t.Fatal(err)
	}
	c := Background().WithPrincipal(Principal{ID: "u1", Roles: []string{"admin"}})
	result, err := a.Invoke(c, "do it")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Text != "done" {
		t.Errorf("result = %q", result.Text)
	}
}

func TestWithRoleEnforcement_GuestCannotSeeAdminTool(t *testing.T) {
	// Guest invokes agent — admin_op should be filtered out of the tool spec
	// sent to the provider, so the LLM never sees it.
	prov := newCapturingProvider(&ModelResponse{Text: "ok"})
	a, err := New(prov, "test", WithTools(adminTool(), publicTool()),
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
		for _, s := range call.Tools {
			if s.Name == "admin_op" {
				t.Error("admin_op should not be visible to guest role")
			}
		}
	}
}

func TestWithRequirePrincipal_NoPrincipal_ToolsHidden(t *testing.T) {
	// No principal set — all tools should be filtered out.
	prov := newCapturingProvider(&ModelResponse{Text: "ok"})
	a, err := New(prov, "test", WithTools(publicTool()),
		WithRequirePrincipal())
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Invoke(Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(prov.captured) > 0 && len(prov.captured[0].Tools) != 0 {
		t.Errorf("expected 0 tool specs without principal, got %d", len(prov.captured[0].Tools))
	}
}

func TestWithPolicy_CustomFunc(t *testing.T) {
	// Custom policy: only allow tools whose name starts with "pub".
	prov := newCapturingProvider(&ModelResponse{Text: "ok"})
	a, err := New(prov, "test",
		WithTools(adminTool(), publicTool()),
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
		for _, s := range call.Tools {
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
	restricted := newTestRaw("restricted", "admin only",
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
		&ModelResponse{
			ToolCalls: []tool.Call{{ToolUseID: "t1", Name: "restricted", Input: json.RawMessage(`{}`)}},
		},
		&ModelResponse{Text: "access denied response"},
	)

	a, err := New(prov, "test", WithTools(restricted))
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

// --- Principal.Credentials ---

func TestPrincipal_Credential(t *testing.T) {
	p := Principal{Credentials: map[string]string{"api_key": "secret123"}}
	if got := p.Credential("api_key"); got != "secret123" {
		t.Errorf("Credential(api_key) = %q, want secret123", got)
	}
	if got := p.Credential("missing"); got != "" {
		t.Errorf("Credential(missing) = %q, want empty string", got)
	}
}

func TestPrincipal_Credential_NilMap(t *testing.T) {
	p := Principal{}
	if got := p.Credential("key"); got != "" {
		t.Errorf("Credential on nil map = %q, want empty", got)
	}
}

// --- WithNarrowedRoles ---

func TestWithNarrowedRoles_Intersection(t *testing.T) {
	c := Background().WithPrincipal(Principal{ID: "u1", Roles: []string{"admin", "support", "editor"}})
	narrowed := c.WithNarrowedRoles("support", "editor")

	p, ok := PrincipalFrom(narrowed)
	if !ok {
		t.Fatal("expected principal after narrowing")
	}
	if len(p.Roles) != 2 {
		t.Errorf("expected 2 roles after narrowing, got %v", p.Roles)
	}
	for _, r := range p.Roles {
		if r != "support" && r != "editor" {
			t.Errorf("unexpected role %q after narrowing", r)
		}
	}
	// Original context should be unchanged.
	orig, _ := PrincipalFrom(c)
	if len(orig.Roles) != 3 {
		t.Errorf("original context roles modified: %v", orig.Roles)
	}
}

func TestWithNarrowedRoles_EmptyResult(t *testing.T) {
	c := Background().WithPrincipal(Principal{ID: "u1", Roles: []string{"admin"}})
	narrowed := c.WithNarrowedRoles("guest")

	p, ok := PrincipalFrom(narrowed)
	if !ok {
		t.Fatal("expected principal after narrowing")
	}
	if len(p.Roles) != 0 {
		t.Errorf("expected 0 roles after narrowing to non-overlapping set, got %v", p.Roles)
	}
}

func TestWithNarrowedRoles_NoPrincipal_NoOp(t *testing.T) {
	c := Background().WithConversationID("conv-narrow")
	narrowed := c.WithNarrowedRoles("admin")

	if _, ok := PrincipalFrom(narrowed); ok {
		t.Error("narrowing must not create a principal")
	}
	if got := narrowed.ConversationID(); got != "conv-narrow" {
		t.Errorf("ConversationID() = %q, want conv-narrow", got)
	}
}

func TestWithNarrowedRoles_PreservesOtherFields(t *testing.T) {
	p := Principal{
		ID:    "u1",
		Roles: []string{"admin", "support"},
		Attrs: map[string]string{"org": "acme"},
	}
	c := Background().WithPrincipal(p)
	narrowed := c.WithNarrowedRoles("support")

	got, ok := PrincipalFrom(narrowed)
	if !ok {
		t.Fatal("expected principal")
	}
	if got.ID != "u1" {
		t.Errorf("ID = %q, want u1", got.ID)
	}
	if got.Attr("org") != "acme" {
		t.Errorf("org attr = %q, want acme", got.Attr("org"))
	}
	if len(got.Roles) != 1 || got.Roles[0] != "support" {
		t.Errorf("roles = %v, want [support]", got.Roles)
	}
}

// --- Principal deep-copy semantics ---

// TestWithPrincipal_DeepCopiesCallerSlicesAndMaps verifies that mutating the
// caller's Principal after calling WithPrincipal does not affect the
// principal stored on the context.
func TestWithPrincipal_DeepCopiesCallerSlicesAndMaps(t *testing.T) {
	p := Principal{
		ID:          "u1",
		Roles:       []string{"user"},
		Attrs:       map[string]string{"org": "a"},
		Credentials: map[string]string{"token": "t1"},
	}
	ctx := Background().WithPrincipal(p)

	// Mutate the caller's copy after attaching it.
	p.Roles[0] = "admin"
	p.Attrs["org"] = "b"
	p.Credentials["token"] = "t2"

	stored, ok := PrincipalFrom(ctx)
	if !ok {
		t.Fatal("expected principal on context")
	}
	if stored.Roles[0] != "user" {
		t.Errorf("stored Roles[0] = %q, want %q (caller mutation leaked in)", stored.Roles[0], "user")
	}
	if stored.Attrs["org"] != "a" {
		t.Errorf("stored Attrs[org] = %q, want %q (caller mutation leaked in)", stored.Attrs["org"], "a")
	}
	if stored.Credentials["token"] != "t1" {
		t.Errorf("stored Credentials[token] = %q, want %q (caller mutation leaked in)", stored.Credentials["token"], "t1")
	}
}

// TestPrincipalFrom_ReturnedCopyIsIndependent verifies that mutating the
// Principal returned by PrincipalFrom/Context.Principal does not affect the
// context's stored principal.
func TestPrincipalFrom_ReturnedCopyIsIndependent(t *testing.T) {
	ctx := Background().WithPrincipal(Principal{
		ID:          "u1",
		Roles:       []string{"user"},
		Attrs:       map[string]string{"org": "a"},
		Credentials: map[string]string{"token": "t1"},
	})

	p2, ok := PrincipalFrom(ctx)
	if !ok {
		t.Fatal("expected principal")
	}
	p2.Roles[0] = "admin"
	p2.Attrs["org"] = "b"
	p2.Credentials["token"] = "t2"

	stored, _ := PrincipalFrom(ctx)
	if stored.Roles[0] != "user" {
		t.Errorf("stored Roles[0] = %q, want %q (mutation of returned copy leaked in)", stored.Roles[0], "user")
	}
	if stored.Attrs["org"] != "a" {
		t.Errorf("stored Attrs[org] = %q, want %q (mutation of returned copy leaked in)", stored.Attrs["org"], "a")
	}
	if stored.Credentials["token"] != "t1" {
		t.Errorf("stored Credentials[token] = %q, want %q (mutation of returned copy leaked in)", stored.Credentials["token"], "t1")
	}
}

// TestContextClone_PrincipalIsIndependent verifies that Clone-derived
// contexts do not share mutable Principal state: mutating the principal
// retrieved from a clone must not affect the original context's principal,
// and vice versa.
func TestContextClone_PrincipalIsIndependent(t *testing.T) {
	orig := Background().WithPrincipal(Principal{
		ID:    "u1",
		Roles: []string{"user"},
		Attrs: map[string]string{"org": "a"},
	})
	clone := orig.Clone()

	// Mutate a principal fetched from the clone.
	cp, ok := clone.Principal()
	if !ok {
		t.Fatal("expected principal on clone")
	}
	cp.Roles[0] = "admin"
	cp.Attrs["org"] = "b"

	origP, _ := orig.Principal()
	if origP.Roles[0] != "user" {
		t.Errorf("original Roles[0] = %q, want %q", origP.Roles[0], "user")
	}
	if origP.Attrs["org"] != "a" {
		t.Errorf("original Attrs[org] = %q, want %q", origP.Attrs["org"], "a")
	}
}

// TestWithNarrowedRoles_AttrsAndCredentialsIndependent verifies that
// WithNarrowedRoles produces a principal whose Attrs/Credentials are fully
// independent of the source principal, not just Roles.
func TestWithNarrowedRoles_AttrsAndCredentialsIndependent(t *testing.T) {
	c := Background().WithPrincipal(Principal{
		ID:          "u1",
		Roles:       []string{"admin", "support"},
		Attrs:       map[string]string{"org": "acme"},
		Credentials: map[string]string{"token": "t1"},
	})
	narrowed := c.WithNarrowedRoles("support")

	np, ok := narrowed.Principal()
	if !ok {
		t.Fatal("expected principal on narrowed context")
	}
	np.Attrs["org"] = "other"
	np.Credentials["token"] = "t2"

	origP, _ := c.Principal()
	if origP.Attrs["org"] != "acme" {
		t.Errorf("original Attrs[org] = %q, want %q (narrowed mutation leaked in)", origP.Attrs["org"], "acme")
	}
	if origP.Credentials["token"] != "t1" {
		t.Errorf("original Credentials[token] = %q, want %q (narrowed mutation leaked in)", origP.Credentials["token"], "t1")
	}
}

func principalPolicyTool(name string, calls *atomic.Int32, opts ...tool.Option) tool.Tool {
	return tool.NewRaw(name, name, nil, func(context.Context, json.RawMessage) (string, error) {
		calls.Add(1)
		return "executed", nil
	}, opts...)
}

func TestRoleFilterRequiresPrincipalForDeclaredPolicy(t *testing.T) {
	filter := RoleFilter()
	var calls atomic.Int32
	unrestricted := principalPolicyTool("open", &calls)
	cases := []struct {
		name string
		tool tool.Tool
	}{
		{name: "allow roles", tool: principalPolicyTool("roles", &calls, tool.AllowRoles("admin"))},
		{name: "deny roles", tool: principalPolicyTool("deny-roles", &calls, tool.DenyRoles("guest"))},
		{name: "allow attributes", tool: principalPolicyTool("attrs", &calls, tool.AllowWhen(func(attrs map[string]string) bool { return attrs["tenant"] == "acme" }))},
		{name: "deny attributes", tool: principalPolicyTool("deny-attrs", &calls, tool.DenyWhen(func(attrs map[string]string) bool { return attrs["suspended"] == "true" }))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if filter(Background(), tc.tool) {
				t.Fatal("declared policy must not be visible without a principal")
			}
		})
	}
	if !filter(Background(), unrestricted) {
		t.Fatal("unrestricted tool must remain visible without a principal")
	}
}

func TestProtectedToolsDenyMissingPrincipalBeforeHandler(t *testing.T) {
	cases := []struct {
		name string
		opts []tool.Option
	}{
		{name: "role policy", opts: []tool.Option{tool.AllowRoles("admin")}},
		{name: "attribute policy", opts: []tool.Option{tool.AllowWhen(func(attrs map[string]string) bool { return attrs["tenant"] == "acme" })}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			protected := principalPolicyTool("protected", &calls, tc.opts...)
			provider := newScriptedProvider(
				&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "call-1", Name: "protected", Input: json.RawMessage(`{}`)}}},
				&ModelResponse{Text: "denied"},
			)
			a, err := New(provider, "test", WithTools(protected))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := a.Invoke(Background(), "run"); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 0 {
				t.Fatalf("handler calls = %d, want 0", calls.Load())
			}
		})
	}
}

func TestApprovalResumeDeniesProtectedToolWithoutPrincipal(t *testing.T) {
	var calls atomic.Int32
	protected := principalPolicyTool("protected", &calls,
		tool.AllowWhen(func(attrs map[string]string) bool { return attrs["tenant"] == "acme" }),
		tool.RequiresApproval(),
	)
	provider := newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "call-1", Name: "protected", Input: json.RawMessage(`{}`)}}},
		&ModelResponse{Text: "denied"},
	)
	a, err := New(provider, "test", WithTools(protected))
	if err != nil {
		t.Fatal(err)
	}
	initial := Background().WithPrincipal(Principal{ID: "u1", Attrs: map[string]string{"tenant": "acme"}})
	paused, err := a.Invoke(initial, "run")
	if err != nil || paused.Interrupt == nil || paused.Interrupt.Type != InterruptApproval {
		t.Fatalf("Invoke = %+v, %v; want approval interrupt", paused, err)
	}
	if _, err := a.Resume(Background(), paused.Interrupt, Approve()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatalf("handler calls = %d, want 0 after principal-less resume", calls.Load())
	}
}

func TestRecoveryDeniesProtectedReplayWithoutPrincipal(t *testing.T) {
	conversations, executions := newTestMemoryStore(), newTestExecutionStore()
	seedInFlightToolExecution(t, conversations, executions, true)
	var calls atomic.Int32
	protected := principalPolicyTool("charge", &calls,
		tool.AllowRoles("admin"),
		tool.WithReplaySafe(),
	)
	a, err := New(newScriptedProvider(), "test", WithTools(protected), WithConversationStore(conversations), WithExecutionStore(executions))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.RecoverExecution(Background(), "execution-1"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatalf("handler calls = %d, want 0 after principal-less recovery", calls.Load())
	}
}

// capturingProvider records the ModelRequest it receives and returns scripted responses.
type capturingProvider struct {
	responses []*ModelResponse
	callIndex int
	captured  []ModelRequest
}

func newCapturingProvider(responses ...*ModelResponse) *capturingProvider {
	return &capturingProvider{responses: responses}
}

func (cp *capturingProvider) Name() string { return "mock" }

func (cp *capturingProvider) Stream(ctx context.Context, params ModelRequest, cb func(ModelEvent)) (*ModelResponse, error) {
	cp.captured = append(cp.captured, params)
	if cp.callIndex >= len(cp.responses) {
		return nil, fmt.Errorf("capturingProvider: no more responses")
	}
	resp := cp.responses[cp.callIndex]
	cp.callIndex++

	if len(resp.ToolCalls) == 0 && resp.Text != "" && cb != nil {
		cb(ModelEvent{Type: ModelEventText, Text: resp.Text})
	}
	return resp, nil
}

// ---------------------------------------------------------------------------
// Guardrail tests
// ---------------------------------------------------------------------------

func TestInputGuardrail_TransformsMessage(t *testing.T) {
	cp := newCapturingProvider(&ModelResponse{Text: "reply"})

	upperGuardrail := func(_ *Context, msg string) (string, error) {
		return strings.ToUpper(msg), nil
	}

	a, err := New(cp, "sys", WithInputGuardrail(upperGuardrail))
	if err != nil {
		t.Fatal(err)
	}

	_, err = a.Invoke(Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The provider should have received the uppercased message.
	if len(cp.captured) != 1 {
		t.Fatalf("expected 1 provider call, got %d", len(cp.captured))
	}
	msgs := cp.captured[0].Messages
	if len(msgs) == 0 {
		t.Fatal("expected at least one message sent to provider")
	}
	lastMsg := msgs[len(msgs)-1]
	if lastMsg.Role != RoleUser {
		t.Fatalf("expected last message role=user, got %s", lastMsg.Role)
	}
	if len(lastMsg.Content) == 0 {
		t.Fatal("expected content in user message")
	}
	tb, ok := lastMsg.Content[0].(TextBlock)
	if !ok {
		t.Fatalf("expected TextBlock, got %T", lastMsg.Content[0])
	}
	if tb.Text != "HELLO" {
		t.Errorf("expected provider to receive %q, got %q", "HELLO", tb.Text)
	}
}

func TestOutputGuardrail_TransformsResponse(t *testing.T) {
	sp := newScriptedProvider(&ModelResponse{Text: "raw response"})

	filterGuardrail := func(_ *Context, resp string) (string, error) {
		return resp + " [filtered]", nil
	}

	a, err := New(sp, "sys", WithOutputGuardrail(filterGuardrail))
	if err != nil {
		t.Fatal(err)
	}

	result, err := a.Invoke(Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Text != "raw response [filtered]" {
		t.Errorf("expected %q, got %q", "raw response [filtered]", result.Text)
	}
}

func TestInputGuardrail_ErrorAbortsInvocation(t *testing.T) {
	sp := newScriptedProvider(&ModelResponse{Text: "should not reach"})

	blockGuardrail := func(_ *Context, msg string) (string, error) {
		return "", fmt.Errorf("blocked content")
	}

	a, err := New(sp, "sys", WithInputGuardrail(blockGuardrail))
	if err != nil {
		t.Fatal(err)
	}

	_, err = a.Invoke(Background(), "bad input")
	if err == nil {
		t.Fatal("expected error from input guardrail, got nil")
	}
	if !strings.Contains(err.Error(), "input guardrail") {
		t.Errorf("expected error to contain 'input guardrail', got: %v", err)
	}
}

func TestOutputGuardrail_ErrorAbortsInvocation(t *testing.T) {
	sp := newScriptedProvider(&ModelResponse{Text: "some response"})

	blockGuardrail := func(_ *Context, resp string) (string, error) {
		return "", fmt.Errorf("response policy violation")
	}

	a, err := New(sp, "sys", WithOutputGuardrail(blockGuardrail))
	if err != nil {
		t.Fatal(err)
	}

	_, err = a.Invoke(Background(), "hi")
	if err == nil {
		t.Fatal("expected error from output guardrail, got nil")
	}
	if !strings.Contains(err.Error(), "output guardrail") {
		t.Errorf("expected error to contain 'output guardrail', got: %v", err)
	}
}

func TestMultipleInputGuardrails_AppliedInOrder(t *testing.T) {
	cp := newCapturingProvider(&ModelResponse{Text: "done"})

	appendA := func(_ *Context, msg string) (string, error) {
		return msg + "-A", nil
	}
	appendB := func(_ *Context, msg string) (string, error) {
		return msg + "-B", nil
	}

	a, err := New(cp, "sys",
		WithInputGuardrail(appendA),
		WithInputGuardrail(appendB),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = a.Invoke(Background(), "start")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cp.captured) != 1 {
		t.Fatalf("expected 1 provider call, got %d", len(cp.captured))
	}
	msgs := cp.captured[0].Messages
	lastMsg := msgs[len(msgs)-1]
	tb, ok := lastMsg.Content[0].(TextBlock)
	if !ok {
		t.Fatalf("expected TextBlock, got %T", lastMsg.Content[0])
	}
	if tb.Text != "start-A-B" {
		t.Errorf("expected %q, got %q", "start-A-B", tb.Text)
	}
}
