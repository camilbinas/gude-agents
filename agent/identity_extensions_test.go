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
