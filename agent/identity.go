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

// clonePrincipal returns a deep copy of p: Roles, Attrs, and Credentials are
// copied into new backing storage so the clone shares no mutable state with
// p. Use this at every boundary where a Principal enters or leaves
// framework-owned state (WithPrincipal, Principal, background dispatch,
// etc.) so callers can't observe or cause mutation through aliased
// slices/maps, including across concurrent goroutines.
func clonePrincipal(p Principal) Principal {
	clone := Principal{ID: p.ID}
	if p.Roles != nil {
		clone.Roles = append([]string(nil), p.Roles...)
	}
	if p.Attrs != nil {
		clone.Attrs = make(map[string]string, len(p.Attrs))
		for k, v := range p.Attrs {
			clone.Attrs[k] = v
		}
	}
	if p.Credentials != nil {
		clone.Credentials = make(map[string]string, len(p.Credentials))
		for k, v := range p.Credentials {
			clone.Credentials[k] = v
		}
	}
	return clone
}

// WithPrincipal attaches a Principal to the invocation. Tool role policies,
// ToolFilters, and middleware can retrieve it via PrincipalFrom. The
// Principal is deep-copied on the way in: later mutation of p's Roles,
// Attrs, or Credentials by the caller does not affect the invocation's
// stored principal.
func (c *Context) WithPrincipal(p Principal) *Context {
	cloned := clonePrincipal(p)
	c.cfg.principal = &cloned
	return c
}

// Principal returns the invocation's Principal and whether one was set. The
// returned Principal is a deep copy: mutating its Roles, Attrs, or
// Credentials does not affect the invocation's stored principal or any other
// caller holding a reference to the same context/config.
func (c *Context) Principal() (Principal, bool) {
	if c.cfg.principal == nil {
		return Principal{}, false
	}
	return clonePrincipal(*c.cfg.principal), true
}

// PrincipalFrom extracts the Principal from a context.
// Returns a zero Principal and false if none was set.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	c := FromContext(ctx)
	if c == nil {
		return Principal{}, false
	}
	return c.Principal()
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
		p, ok := c.Principal()
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
		_, ok := c.Principal()
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

// WithNarrowedRoles returns a new *Context (see Clone) whose principal's roles
// are the intersection of the current roles and the allowed set. Use this to
// reduce permissions for a sub-task without creating a new principal.
// If no principal is set, returns c unchanged.
func (c *Context) WithNarrowedRoles(allowed ...string) *Context {
	p, ok := c.Principal()
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
	// c.Principal() above already returned a deep copy, and WithPrincipal
	// deep-copies again on the way in, so Attrs/Credentials are fully
	// independent of the original principal here too.
	return c.Clone().WithPrincipal(p)
}
