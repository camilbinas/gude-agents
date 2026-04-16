// Package swarm provides multi-agent coordination with handoff support.
//
// This package re-exports the swarm types and functions from the parent agent
// package, allowing callers to import "agent/swarm" for a cleaner namespace.
//
// Usage:
//
//	import "github.com/camilbinas/gude-agents/agent/swarm"
//
//	sw, err := swarm.New([]swarm.Member{...}, swarm.WithMaxHandoffs(5))
package swarm

import "github.com/camilbinas/gude-agents/agent"

// Type aliases — these are identical to the agent package types.
type Member = agent.SwarmMember
type Result = agent.SwarmResult
type Handoff = agent.Handoff
type Option = agent.SwarmOption

// Swarm is the multi-agent coordinator.
type Swarm = agent.Swarm

// Constructor and option functions.
var New = agent.NewSwarm
var WithMaxHandoffs = agent.WithSwarmMaxHandoffs
var WithLogger = agent.WithSwarmLogger
var WithMiddleware = agent.WithSwarmMiddleware
var WithMemory = agent.WithSwarmMemory
