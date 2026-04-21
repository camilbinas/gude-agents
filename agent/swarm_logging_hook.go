package agent

import "time"

// SwarmLoggingHook is an optional interface for structured logging of swarm execution.
// The logging submodule provides the concrete implementation.
// The swarm calls these methods at key lifecycle points when the hook is non-nil.
type SwarmLoggingHook interface {
	// OnSwarmRunStart is called at the beginning of Swarm.Run.
	OnSwarmRunStart(initialAgent string, memberCount int, maxHandoffs int)

	// OnSwarmRunEnd is called at the end of Swarm.Run with the outcome.
	OnSwarmRunEnd(err error, result SwarmResult, duration time.Duration)

	// OnSwarmAgentStart is called when a swarm agent begins its turn.
	OnSwarmAgentStart(agentName string)

	// OnSwarmAgentEnd is called when a swarm agent finishes its turn.
	OnSwarmAgentEnd(agentName string, err error, duration time.Duration)

	// OnSwarmHandoff is called when a handoff occurs between agents.
	OnSwarmHandoff(from, to string)
}

// SetSwarmLoggingHook sets the logging hook on the swarm.
// Called by the logging submodule's SwarmOption.
func (s *Swarm) SetSwarmLoggingHook(h SwarmLoggingHook) {
	s.loggingHook = h
}

// SwarmLoggingHook returns the swarm's logging hook, or nil if none is set.
func (s *Swarm) SwarmLoggingHook() SwarmLoggingHook {
	return s.loggingHook
}
