package agent

// SwarmMetricsHook is an optional interface for metrics instrumentation of swarm execution.
// The metrics submodule provides the concrete implementation.
// The swarm calls these methods at key lifecycle points when the hook is non-nil.
type SwarmMetricsHook interface {
	// OnSwarmRunStart is called at the beginning of Swarm.Run.
	// Returns a finish function called with the outcome.
	OnSwarmRunStart() func(err error, result SwarmResult)

	// OnSwarmAgentStart is called when a swarm agent begins its turn.
	// Returns a finish function called with the outcome.
	OnSwarmAgentStart(agentName string) func(err error)

	// OnSwarmHandoff is called when a handoff occurs between agents.
	OnSwarmHandoff(from, to string)
}

// SetSwarmMetricsHook sets the metrics hook on the swarm.
// Called by the metrics submodule's SwarmOption.
func (s *Swarm) SetSwarmMetricsHook(h SwarmMetricsHook) {
	s.metricsHook = h
}

// SwarmMetricsHook returns the swarm's metrics hook, or nil if none is set.
func (s *Swarm) SwarmMetricsHook() SwarmMetricsHook {
	return s.metricsHook
}
