package agent

import (
	"github.com/camilbinas/gude-agents/agent/tool"
)

// ToolVisibility decides whether the model may see a tool for the current
// provider call. It is evaluated before every provider call of an invocation,
// so visibility can depend on the Principal, Context values, or workflow state
// that changed during the loop. Return true to show the tool, false to hide it.
//
// Visibility shapes what the model is offered; it is not authorization. A
// hidden tool is omitted from the provider's tool list and from that loop's
// execution map, so a call the model makes to it anyway fails as an unknown
// tool. Visibility does not replace the checks that run when a tool executes:
// role and attribute policies (tool.AllowRoles, tool.DenyRoles, tool.WithAttrs),
// tool guards, approval, and middleware remain the enforcement boundary.
type ToolVisibility func(c *Context, t tool.Tool) bool

// visibleTools applies the agent's visibility rules to produce the tools the
// model may see for the current provider call. A tool must pass every rule
// (AND semantics). Every registered tool is visible when no rule is set.
func (a *Agent) visibleTools(c *Context) ([]tool.Spec, map[string]tool.Tool) {
	registered := a.toolRegistry.List()
	var specs []tool.Spec
	tools := make(map[string]tool.Tool, len(registered))
	for _, t := range registered {
		if len(a.toolVisibility) > 0 && !a.isVisible(c, t) {
			continue
		}
		specs = append(specs, t.Spec)
		tools[t.Spec.Name] = t
	}
	return specs, tools
}

// isVisible reports whether the tool passes every visibility rule.
func (a *Agent) isVisible(c *Context, t tool.Tool) bool {
	for _, rule := range a.toolVisibility {
		if !rule(c, t) {
			return false
		}
	}
	return true
}
