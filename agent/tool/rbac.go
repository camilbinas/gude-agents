package tool

// rolePolicy holds the allow/deny role sets and optional ABAC conditions.
type rolePolicy struct {
	allowRoles     map[string]bool
	denyRoles      map[string]bool
	attrConditions []func(attrs map[string]string) bool
	denyConditions []func(attrs map[string]string) bool
}

// allowedRoles checks role-level allow/deny rules only.
func (p *rolePolicy) allowedRoles(roles []string) bool {
	for _, r := range roles {
		if p.denyRoles[r] {
			return false
		}
	}
	if len(p.allowRoles) == 0 {
		return true
	}
	for _, r := range roles {
		if p.allowRoles[r] {
			return true
		}
	}
	return false
}

// allowedAttrs checks all ABAC conditions (AND semantics).
// Returns false if any denyCondition matches, then checks allowConditions.
func (p *rolePolicy) allowedAttrs(attrs map[string]string) bool {
	for _, cond := range p.denyConditions {
		if cond(attrs) {
			return false
		}
	}
	for _, cond := range p.attrConditions {
		if !cond(attrs) {
			return false
		}
	}
	return true
}

// allowed checks roles only; kept for backwards compatibility.
func (p *rolePolicy) allowed(roles []string) bool {
	return p.allowedRoles(roles)
}

// allowedWithAttrs checks roles first, then ABAC conditions.
func (p *rolePolicy) allowedWithAttrs(roles []string, attrs map[string]string) bool {
	if !p.allowedRoles(roles) {
		return false
	}
	return p.allowedAttrs(attrs)
}

// AllowRoles restricts tool execution to callers that hold at least one of the
// given roles. Evaluated before Guard and RequiresApproval; denied calls return
// a structured denial result to the LLM without invoking the handler.
//
//	tool.NewRaw("delete_order", "...", schema, handler, tool.AllowRoles("admin", "manager"))
func AllowRoles(roles ...string) func(*Tool) {
	return func(t *Tool) {
		if t.rolePolicy == nil {
			t.rolePolicy = &rolePolicy{}
		}
		if t.rolePolicy.allowRoles == nil {
			t.rolePolicy.allowRoles = make(map[string]bool, len(roles))
		}
		for _, r := range roles {
			t.rolePolicy.allowRoles[r] = true
		}
	}
}

// DenyRoles blocks callers that hold any of the given roles, regardless of
// other allow rules. Evaluated before Guard and RequiresApproval.
//
//	tool.NewRaw("view_logs", "...", schema, handler, tool.DenyRoles("guest"))
func DenyRoles(roles ...string) func(*Tool) {
	return func(t *Tool) {
		if t.rolePolicy == nil {
			t.rolePolicy = &rolePolicy{}
		}
		if t.rolePolicy.denyRoles == nil {
			t.rolePolicy.denyRoles = make(map[string]bool, len(roles))
		}
		for _, r := range roles {
			t.rolePolicy.denyRoles[r] = true
		}
	}
}

// AllowWhen adds an attribute-based condition. The condition receives the
// principal's Attrs map and returns true to allow. Multiple AllowWhen
// conditions are ANDed. Evaluated after role checks pass.
func AllowWhen(cond func(attrs map[string]string) bool) func(*Tool) {
	return func(t *Tool) {
		if t.rolePolicy == nil {
			t.rolePolicy = &rolePolicy{}
		}
		t.rolePolicy.attrConditions = append(t.rolePolicy.attrConditions, cond)
	}
}

// DenyWhen adds an attribute-based deny condition. If the condition returns
// true, the call is denied regardless of role or AllowWhen checks.
// Symmetric counterpart to AllowWhen; multiple DenyWhen conditions are ORed
// (any match denies).
//
//	tool.NewRaw("restricted_api", "...", schema, handler,
//	    tool.DenyWhen(func(attrs map[string]string) bool {
//	        return attrs["region"] == "restricted"
//	    }),
//	)
func DenyWhen(cond func(attrs map[string]string) bool) func(*Tool) {
	return func(t *Tool) {
		if t.rolePolicy == nil {
			t.rolePolicy = &rolePolicy{}
		}
		t.rolePolicy.denyConditions = append(t.rolePolicy.denyConditions, cond)
	}
}

// RolesAllowed reports whether the given roles satisfy the tool's role policy.
// Returns true when no policy is set (open by default). Does not evaluate ABAC conditions.
func (t Tool) RolesAllowed(roles []string) bool {
	if t.rolePolicy == nil {
		return true
	}
	return t.rolePolicy.allowed(roles)
}

// AllowedWithAttrs reports whether the given roles and attribute map satisfy
// the tool's full policy (role allowlist/denylist + ABAC conditions).
// Returns true when no policy is set. Called by agent.Tool.AllowedFor.
func (t Tool) AllowedWithAttrs(roles []string, attrs map[string]string) bool {
	if t.rolePolicy == nil {
		return true
	}
	return t.rolePolicy.allowedWithAttrs(roles, attrs)
}
