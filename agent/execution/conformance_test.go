package execution_test

import (
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/execution"
	"github.com/camilbinas/gude-agents/agent/execution/internal/conformance"
)

func newMemory(*testing.T) agent.ExecutionStore { return execution.NewMemory() }

func TestMemoryConformance(t *testing.T) { conformance.Run(t, newMemory) }

func TestMemoryAgentProtocol(t *testing.T) { conformance.RunAgent(t, newMemory) }
