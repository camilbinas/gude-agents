package agent

import (
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// Default creates an agent with standard defaults: 5 max iterations.
// Additional options can be appended to override or extend.
func Default(prov Provider, inst prompt.Instructions, tools []tool.Tool, opts ...Option) (*Agent, error) {
	defaults := []Option{
		WithMaxIterations(5),
	}
	return New(prov, inst, tools, append(defaults, opts...)...)
}

// Worker creates a lightweight agent optimized for tool execution: 3 max iterations.
// Ideal for child agents in a multi-agent setup.
func Worker(prov Provider, inst prompt.Instructions, tools []tool.Tool, opts ...Option) (*Agent, error) {
	defaults := []Option{
		WithMaxIterations(3),
	}
	return New(prov, inst, tools, append(defaults, opts...)...)
}

// Orchestrator creates an agent optimized for routing to other agents:
// 5 max iterations, parallel tool execution.
func Orchestrator(prov Provider, inst prompt.Instructions, tools []tool.Tool, opts ...Option) (*Agent, error) {
	defaults := []Option{
		WithMaxIterations(5),
		WithParallelToolExecution(),
	}
	return New(prov, inst, tools, append(defaults, opts...)...)
}

// SwarmAgent creates an agent optimized for participation in a Swarm:
// 5 max iterations. Handoff tools are injected automatically by NewSwarm.
func SwarmAgent(prov Provider, inst prompt.Instructions, tools []tool.Tool, opts ...Option) (*Agent, error) {
	defaults := []Option{
		WithMaxIterations(5),
	}
	return New(prov, inst, tools, append(defaults, opts...)...)
}

// RAGAgent creates an agent with a retriever for retrieval-augmented generation:
// 5 max iterations. The Retriever is a required parameter to enforce correct setup.
func RAGAgent(prov Provider, inst prompt.Instructions, r Retriever, tools []tool.Tool, opts ...Option) (*Agent, error) {
	defaults := []Option{
		WithMaxIterations(5),
		WithRetriever(r),
	}
	return New(prov, inst, tools, append(defaults, opts...)...)
}
