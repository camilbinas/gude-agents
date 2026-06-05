package tool

import (
	"context"
	"encoding/json"
	"testing"
)

func nopHandler(_ context.Context, _ json.RawMessage) (string, error) { return "ok", nil }

// TestAllowWhen_AttrCheckPasses verifies that AllowWhen permits when the condition returns true.
func TestAllowWhen_AttrCheckPasses(t *testing.T) {
	tl := NewRaw("t", "desc", nil, nopHandler,
		AllowWhen(func(attrs map[string]string) bool { return attrs["org"] == "acme" }),
	)
	if !tl.AllowedWithAttrs(nil, map[string]string{"org": "acme"}) {
		t.Error("expected AllowedWithAttrs = true when attr condition passes")
	}
}

// TestAllowWhen_AttrCheckFails verifies that AllowWhen denies when the condition returns false.
func TestAllowWhen_AttrCheckFails(t *testing.T) {
	tl := NewRaw("t", "desc", nil, nopHandler,
		AllowWhen(func(attrs map[string]string) bool { return attrs["org"] == "acme" }),
	)
	if tl.AllowedWithAttrs(nil, map[string]string{"org": "other"}) {
		t.Error("expected AllowedWithAttrs = false when attr condition fails")
	}
}

// TestAllowWhen_CombinedRoleAndAttr verifies that both role and attr must pass.
func TestAllowWhen_CombinedRoleAndAttr(t *testing.T) {
	tl := NewRaw("t", "desc", nil, nopHandler,
		AllowRoles("admin"),
		AllowWhen(func(attrs map[string]string) bool { return attrs["org"] == "acme" }),
	)

	// Role passes, attr passes → allowed.
	if !tl.AllowedWithAttrs([]string{"admin"}, map[string]string{"org": "acme"}) {
		t.Error("expected true for admin + org=acme")
	}
	// Role passes, attr fails → denied.
	if tl.AllowedWithAttrs([]string{"admin"}, map[string]string{"org": "other"}) {
		t.Error("expected false for admin + org=other")
	}
	// Role fails, attr passes → denied.
	if tl.AllowedWithAttrs([]string{"guest"}, map[string]string{"org": "acme"}) {
		t.Error("expected false for guest + org=acme")
	}
}

// TestAllowWhen_MultipleConditionsANDed verifies that multiple AllowWhen conditions are ANDed.
func TestAllowWhen_MultipleConditionsANDed(t *testing.T) {
	tl := NewRaw("t", "desc", nil, nopHandler,
		AllowWhen(func(attrs map[string]string) bool { return attrs["org"] == "acme" }),
		AllowWhen(func(attrs map[string]string) bool { return attrs["tier"] == "premium" }),
	)
	// Both pass.
	if !tl.AllowedWithAttrs(nil, map[string]string{"org": "acme", "tier": "premium"}) {
		t.Error("expected true when both conditions pass")
	}
	// One fails.
	if tl.AllowedWithAttrs(nil, map[string]string{"org": "acme", "tier": "free"}) {
		t.Error("expected false when second condition fails")
	}
}

// TestAllowWhen_NoPolicyAlwaysAllowed verifies no-policy tools are always allowed.
func TestAllowWhen_NoPolicyAlwaysAllowed(t *testing.T) {
	tl := NewRaw("t", "desc", nil, nopHandler)
	if !tl.AllowedWithAttrs(nil, nil) {
		t.Error("tool with no policy should always be allowed")
	}
	if !tl.AllowedWithAttrs([]string{"guest"}, map[string]string{"org": "evil"}) {
		t.Error("tool with no policy should always be allowed regardless of attrs")
	}
}

func TestDenyWhen_BlocksOnMatch(t *testing.T) {
	restricted := NewRaw("api", "api", map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (string, error) { return "ok", nil },
		DenyWhen(func(attrs map[string]string) bool { return attrs["region"] == "restricted" }),
	)
	if restricted.AllowedWithAttrs(nil, map[string]string{"region": "restricted"}) {
		t.Error("DenyWhen should block when condition matches")
	}
	if !restricted.AllowedWithAttrs(nil, map[string]string{"region": "eu"}) {
		t.Error("DenyWhen should allow when condition does not match")
	}
}

func TestDenyWhen_TakesPrecedenceOverAllowWhen(t *testing.T) {
	t1 := NewRaw("api", "api", map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (string, error) { return "ok", nil },
		AllowWhen(func(attrs map[string]string) bool { return attrs["plan"] == "enterprise" }),
		DenyWhen(func(attrs map[string]string) bool { return attrs["suspended"] == "true" }),
	)
	// enterprise but suspended → denied
	if t1.AllowedWithAttrs(nil, map[string]string{"plan": "enterprise", "suspended": "true"}) {
		t.Error("DenyWhen should take precedence over AllowWhen")
	}
	// enterprise and not suspended → allowed
	if !t1.AllowedWithAttrs(nil, map[string]string{"plan": "enterprise", "suspended": "false"}) {
		t.Error("should allow enterprise non-suspended user")
	}
}

// TestDenyWhen_ORSemantics_AnyMatchDenies explicitly documents that multiple
// DenyWhen conditions use OR semantics — any single match is enough to deny.
func TestDenyWhen_ORSemantics_AnyMatchDenies(t *testing.T) {
	tl := NewRaw("api", "api", map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (string, error) { return "ok", nil },
		DenyWhen(func(attrs map[string]string) bool { return attrs["x"] == "1" }),
		DenyWhen(func(attrs map[string]string) bool { return attrs["y"] == "1" }),
	)

	// First condition matches → denied.
	if tl.AllowedWithAttrs(nil, map[string]string{"x": "1", "y": "0"}) {
		t.Error("first DenyWhen matching should deny")
	}
	// Second condition matches → denied.
	if tl.AllowedWithAttrs(nil, map[string]string{"x": "0", "y": "1"}) {
		t.Error("second DenyWhen matching should deny")
	}
	// Neither matches → allowed.
	if !tl.AllowedWithAttrs(nil, map[string]string{"x": "0", "y": "0"}) {
		t.Error("should allow when no deny condition matches")
	}
	// Both match → denied (still OR, just both happen to match).
	if tl.AllowedWithAttrs(nil, map[string]string{"x": "1", "y": "1"}) {
		t.Error("both DenyWhen matching should deny")
	}
}
