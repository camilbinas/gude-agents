package agent

import (
	"github.com/camilbinas/gude-agents/agent/tool"
)

// ToolFilter is a function that determines whether a tool should be available
// for a given invocation. It is evaluated before each provider call in the
// agent loop, allowing tools to be dynamically enabled or disabled based on
// context (e.g., user roles, workflow state, Context fields).
//
// Return true to include the tool, false to exclude it.
type ToolFilter func(c *Context, t tool.Tool) bool

// filterTools applies the agent's tool filters to produce the set of tools
// available for the current provider call. A tool must pass all filters to be
// included (AND semantics). Returns all tools if no filters are set.
func (a *Agent) filterTools(c *Context) ([]tool.Spec, map[string]tool.Tool) {
	a.toolsMu.RLock()
	defer a.toolsMu.RUnlock()

	if len(a.toolFilters) == 0 {
		specs := make([]tool.Spec, len(a.toolSpecs))
		copy(specs, a.toolSpecs)
		tools := make(map[string]tool.Tool, len(a.tools))
		for k, v := range a.tools {
			tools[k] = v
		}
		return specs, tools
	}

	var specs []tool.Spec
	tools := make(map[string]tool.Tool)
	for name, t := range a.tools {
		if a.passesAllFilters(c, t) {
			specs = append(specs, t.Spec)
			tools[name] = t
		}
	}
	return specs, tools
}

// passesAllFilters returns true if the tool passes all registered filters.
func (a *Agent) passesAllFilters(c *Context, t tool.Tool) bool {
	for _, f := range a.toolFilters {
		if !f(c, t) {
			return false
		}
	}
	return true
}
