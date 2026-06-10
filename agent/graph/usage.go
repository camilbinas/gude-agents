package graph

import (
	"context"
	"sync"

	"github.com/camilbinas/gude-agents/agent"
)

// usageCollector accumulates token usage reported by a single node execution.
// One collector is created per node invocation and seeded into the node's
// context. It is safe for concurrent use so a node may report usage from
// multiple goroutines.
type usageCollector struct {
	mu    sync.Mutex
	usage agent.TokenUsage
}

func (c *usageCollector) add(u agent.TokenUsage) {
	c.mu.Lock()
	c.usage.InputTokens += u.InputTokens
	c.usage.OutputTokens += u.OutputTokens
	c.usage.CacheReadTokens += u.CacheReadTokens
	c.usage.CacheWriteTokens += u.CacheWriteTokens
	c.mu.Unlock()
}

func (c *usageCollector) total() agent.TokenUsage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.usage
}

type usageCollectorKey struct{}

// withUsageCollector returns a context carrying a fresh usage collector.
// The engine calls this once per node execution before invoking the node
// function, then reads the accumulated total back via collectorFromContext.
func withUsageCollector(ctx context.Context) (context.Context, *usageCollector) {
	c := &usageCollector{}
	return context.WithValue(ctx, usageCollectorKey{}, c), c
}

// collectorFromContext returns the usage collector seeded by the engine, if any.
func collectorFromContext(ctx context.Context) (*usageCollector, bool) {
	c, ok := ctx.Value(usageCollectorKey{}).(*usageCollector)
	return c, ok
}

// AddUsage reports token usage consumed by the current graph node. Nodes call
// it to contribute to the graph's accumulated Usage total (surfaced in
// Result.Usage, StepResult.Usage, and checkpoints).
//
// AddUsage works for every state type — map[string]any and custom structs
// alike — because it threads usage through the context rather than the state
// value. It is safe to call multiple times and from multiple goroutines within
// a single node. Calls outside a graph node execution (no collector on the
// context) are a no-op.
//
// Agent-backed nodes created via Agent, AgentNode, or RegisterAgent report
// their usage automatically; manual nodes that call an agent or provider
// directly should call AddUsage with the returned TokenUsage.
func AddUsage(ctx context.Context, u agent.TokenUsage) {
	if c, ok := collectorFromContext(ctx); ok {
		c.add(u)
	}
}
