package agent

import (
	"context"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// Principal represents the caller's identity for an invocation.
type Principal struct {
	// ID is the unique identifier of the caller (user ID, service account, etc.).
	ID string
	// Roles is the set of roles assigned to this principal.
	Roles []string
	// Attrs carries arbitrary key-value attributes (org_id, tier, region, etc.)
	// that policies can inspect.
	Attrs map[string]string
	// Credentials holds opaque credential values (tokens, API keys, etc.)
	// keyed by name. Never logged or transmitted by the framework.
	Credentials map[string]string
}

// HasRole reports whether the principal holds the given role.
func (p Principal) HasRole(role string) bool {
	for _, r := range p.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// HasAnyRole reports whether the principal holds at least one of the given roles.
func (p Principal) HasAnyRole(roles ...string) bool {
	for _, want := range roles {
		if p.HasRole(want) {
			return true
		}
	}
	return false
}

// Attr returns an attribute value, or empty string if not set.
func (p Principal) Attr(key string) string {
	return p.Attrs[key]
}

// Credential returns a credential value by key, or empty string if not set.
func (p Principal) Credential(key string) string {
	return p.Credentials[key]
}

// principalKey is the *Context KV key for the Principal.
type principalKey struct{}

// WithPrincipal attaches a Principal to the context for the duration of the
// invocation. Tool role policies, ToolFilters, and middleware can retrieve it
// via PrincipalFrom.
func (c *Context) WithPrincipal(p Principal) *Context {
	c.Set(principalKey{}, p)
	return c
}

// PrincipalFrom extracts the Principal from a context.
// Returns a zero Principal and false if none was set.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	c := FromContext(ctx)
	if c == nil {
		return Principal{}, false
	}
	return GetTyped[Principal](c, principalKey{})
}

// --- Agent-level RBAC helpers ---

// RoleFilter returns a ToolFilter that enforces tool.AllowRoles / tool.DenyRoles
// declarations against the Principal on the context. Tools without a role policy
// are always allowed. Invocations without a Principal are denied for tools that
// declare an allowlist.
//
// Attach it at construction time alongside WithToolFilter, or use
// WithRoleEnforcement which installs it automatically.
//
//	agent.New(provider, instructions, tools, agent.WithToolFilter(agent.RoleFilter()))
func RoleFilter() ToolFilter {
	return func(c *Context, t tool.Tool) bool {
		p, ok := GetTyped[Principal](c, principalKey{})
		if !ok {
			return t.RolesAllowed(nil)
		}
		return t.AllowedWithAttrs(p.Roles, p.Attrs)
	}
}

// WithRoleEnforcement adds RoleFilter to the agent. Tools declare their required
// roles via tool.AllowRoles / tool.DenyRoles; this option enforces those
// declarations at call time using the Principal on the invocation context.
//
//	agent.New(provider, instructions, tools, agent.WithRoleEnforcement())
//	// then per-invocation:
//	a.Invoke(ctx.WithPrincipal(agent.Principal{ID: "u1", Roles: []string{"admin"}}), msg)
func WithRoleEnforcement() Option {
	return WithToolFilter(RoleFilter())
}

// RequirePrincipal returns a ToolFilter that denies all tool calls when no
// Principal is set on the context. Use it when every invocation must be
// authenticated before tools can run.
func RequirePrincipal() ToolFilter {
	return func(c *Context, _ tool.Tool) bool {
		_, ok := GetTyped[Principal](c, principalKey{})
		return ok
	}
}

// WithRequirePrincipal installs RequirePrincipal as a ToolFilter.
// Any invocation that omits WithPrincipal on the context will receive
// "unknown tool" errors for every tool call.
func WithRequirePrincipal() Option {
	return WithToolFilter(RequirePrincipal())
}

// PolicyFunc is the signature for a custom authorization policy.
// Return true to allow the tool call, false to deny.
type PolicyFunc func(c *Context, t tool.Tool) bool

// WithPolicy installs a custom authorization policy as a ToolFilter.
// Use this for complex rules (external authz services, OPA, Casbin, etc.)
// that go beyond simple role checks.
//
//	agent.New(provider, instructions, tools, agent.WithPolicy(func(c *agent.Context, t tool.Tool) bool {
//	    p, _ := agent.PrincipalFrom(c)
//	    return myOPA.Allow(p.ID, t.Spec.Name)
//	}))
func WithPolicy(fn PolicyFunc) Option {
	return WithToolFilter(ToolFilter(fn))
}

// WithNarrowedRoles returns a new *Context with the principal's roles replaced
// by the intersection of the current roles and the allowed set. Use this to
// reduce permissions for a sub-task without creating a new principal.
// If no principal is set, returns c unchanged.
func (c *Context) WithNarrowedRoles(allowed ...string) *Context {
	p, ok := GetTyped[Principal](c, principalKey{})
	if !ok {
		return c
	}
	allowSet := make(map[string]struct{}, len(allowed))
	for _, r := range allowed {
		allowSet[r] = struct{}{}
	}
	narrowed := make([]string, 0, len(p.Roles))
	for _, r := range p.Roles {
		if _, ok := allowSet[r]; ok {
			narrowed = append(narrowed, r)
		}
	}
	p.Roles = narrowed
	clone := c.Clone()
	clone.Set(principalKey{}, p)
	return clone
}
