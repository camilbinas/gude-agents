package agent

import "context"

// SwarmTracingHook is an optional interface for tracing swarm execution.
// The tracing submodule provides the concrete implementation.
// The swarm calls these methods at key lifecycle points when the hook is non-nil.
type SwarmTracingHook interface {
	// OnSwarmRunStart is called at the beginning of Swarm.Run.
	// Returns a context with the root span and a finish function.
	OnSwarmRunStart(ctx context.Context, params SwarmRunSpanParams) (context.Context, func(err error, result SwarmResult))

	// OnSwarmAgentStart is called when a swarm agent begins its turn.
	// Returns a context with the agent span and a finish function.
	OnSwarmAgentStart(ctx context.Context, agentName string) (context.Context, func(err error))

	// OnSwarmHandoff is called when a handoff occurs between agents.
	OnSwarmHandoff(ctx context.Context, from, to string)
}

// SwarmRunSpanParams carries data for the swarm.run root span.
type SwarmRunSpanParams struct {
	InitialAgent   string
	MemberCount    int
	MaxHandoffs    int
	ConversationID string
	UserMessage    string
}

// SetSwarmTracingHook sets the tracing hook on the swarm.
// Called by the tracing submodule's SwarmOption.
func (s *Swarm) SetSwarmTracingHook(h SwarmTracingHook) {
	s.tracingHook = h
}
