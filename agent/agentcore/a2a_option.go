package agentcore

import (
	"github.com/a2aproject/a2a-go/v2/a2a"
)

// WithA2A configures the Runtime to mount an A2A server alongside the
// /invocations endpoint on the same HTTP port. The provided AgentCard
// advertises the agent's capabilities to other A2A-compatible agents.
func WithA2A(card a2a.AgentCard) RuntimeOption {
	return func(c *runtimeConfig) error {
		c.a2aCard = &card
		return nil
	}
}

// WithA2AAddr sets the listen address for the HTTP server that serves both
// A2A protocol endpoints and AgentCore invocations. The default is ":8080".
func WithA2AAddr(addr string) RuntimeOption {
	return func(c *runtimeConfig) error {
		c.a2aAddr = addr
		return nil
	}
}
