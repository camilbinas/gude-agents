package a2a

import (
	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// CardOption applies overrides to an auto-derived AgentCard.
type CardOption func(*a2a.AgentCard)

// WithCardDescription overrides the auto-derived description.
func WithCardDescription(desc string) CardOption {
	return func(c *a2a.AgentCard) {
		c.Description = desc
	}
}

// WithCardVersion sets the version field.
func WithCardVersion(version string) CardOption {
	return func(c *a2a.AgentCard) {
		c.Version = version
	}
}

// WithCardURL sets the agent's endpoint URL and adds a JSON-RPC interface.
func WithCardURL(url string) CardOption {
	return func(c *a2a.AgentCard) {
		c.SupportedInterfaces = []*a2a.AgentInterface{
			a2a.NewAgentInterface(url, a2a.TransportProtocolJSONRPC),
		}
	}
}

// WithCardSkills overrides the auto-derived skills.
func WithCardSkills(skills []a2a.AgentSkill) CardOption {
	return func(c *a2a.AgentCard) {
		c.Skills = skills
	}
}

// WithCardCapabilities overrides the capabilities.
func WithCardCapabilities(caps a2a.AgentCapabilities) CardOption {
	return func(c *a2a.AgentCard) {
		c.Capabilities = caps
	}
}

// DeriveCard creates an a2a.AgentCard from an *agent.Agent's exported metadata.
//   - Name from Agent.Name()
//   - Description from Agent.Instructions() truncated to 200 characters
//   - Skills from Agent.ToolSpecs()
//   - Streaming capability enabled by default
func DeriveCard(a *agent.Agent, opts ...CardOption) *a2a.AgentCard {
	skills := make([]a2a.AgentSkill, 0, len(a.ToolSpecs()))
	for _, spec := range a.ToolSpecs() {
		skills = append(skills, toolSpecToSkill(spec))
	}

	card := &a2a.AgentCard{
		Name:        a.Name(),
		Description: truncate(a.Instructions(), 200),
		Version:     "1.0.0",
		Skills:      skills,
		Capabilities: a2a.AgentCapabilities{
			Streaming: true,
		},
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"text/plain"},
	}

	for _, opt := range opts {
		opt(card)
	}

	return card
}

// toolSpecToSkill maps a tool.Spec to an a2a.AgentSkill.
func toolSpecToSkill(spec tool.Spec) a2a.AgentSkill {
	return a2a.AgentSkill{
		ID:          spec.Name,
		Name:        spec.Name,
		Description: spec.Description,
		Tags:        []string{},
	}
}

// truncate returns s if len(s) <= maxLen, otherwise s[:maxLen].
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
