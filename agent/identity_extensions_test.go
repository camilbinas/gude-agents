package agent

import (
	"testing"
)

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
	c := Background()
	narrowed := c.WithNarrowedRoles("admin")
	// Should return the same context unchanged.
	if narrowed != c {
		t.Error("expected same context when no principal is set")
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
