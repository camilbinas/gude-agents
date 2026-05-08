package graph

import (
	"github.com/camilbinas/gude-agents/agent"
)

// bridgeMetricsHook implements agent.MetricsHook and delegates to GraphMetricsHook.
// It bridges the graph-level metrics to the agent-level metrics system, enriching
// each metric with the originating node name context.
//
// When inner is non-nil, both the inner hook and the graph hook are called (composition).
// When graphHook is nil, the bridge should not be created at all (zero overhead).
type bridgeMetricsHook struct {
	graphHook GraphMetricsHook
	nodeName  string
	inner     agent.MetricsHook // agent's own hook, may be nil
}

// OnInvokeStart is called at the beginning of InvokeStream/Invoke.
// Delegates to the inner hook only. Node-level duration tracking is handled
// exclusively by the engine's execution path (metricsHook.OnNodeStart) to
// avoid double-counting.
func (b *bridgeMetricsHook) OnInvokeStart() func(err error, usage agent.TokenUsage) {
	var innerFinish func(err error, usage agent.TokenUsage)
	if b.inner != nil {
		innerFinish = b.inner.OnInvokeStart()
	}

	return func(err error, usage agent.TokenUsage) {
		if innerFinish != nil {
			innerFinish(err, usage)
		}
	}
}

// OnIterationStart is called at the beginning of each agent loop iteration.
func (b *bridgeMetricsHook) OnIterationStart() {
	if b.inner != nil {
		b.inner.OnIterationStart()
	}
}

// OnProviderCallStart is called before each Provider.ConverseStream call.
func (b *bridgeMetricsHook) OnProviderCallStart(modelID string) func(err error, usage agent.TokenUsage) {
	var innerFinish func(err error, usage agent.TokenUsage)
	if b.inner != nil {
		innerFinish = b.inner.OnProviderCallStart(modelID)
	}

	return func(err error, usage agent.TokenUsage) {
		if innerFinish != nil {
			innerFinish(err, usage)
		}
	}
}

// OnToolStart is called before each tool execution.
func (b *bridgeMetricsHook) OnToolStart(toolName string) func(err error) {
	var innerFinish func(err error)
	if b.inner != nil {
		innerFinish = b.inner.OnToolStart(toolName)
	}

	return func(err error) {
		if innerFinish != nil {
			innerFinish(err)
		}
	}
}

// OnGuardrailComplete is called after a guardrail evaluation.
func (b *bridgeMetricsHook) OnGuardrailComplete(direction string, blocked bool) {
	if b.inner != nil {
		b.inner.OnGuardrailComplete(direction, blocked)
	}
}

// OnImagesAttached is called when images are attached to the invocation.
func (b *bridgeMetricsHook) OnImagesAttached(imageCount int) {
	if b.inner != nil {
		b.inner.OnImagesAttached(imageCount)
	}
}

// OnDocumentsAttached is called when documents are attached to the invocation.
func (b *bridgeMetricsHook) OnDocumentsAttached(docCount int) {
	if b.inner != nil {
		b.inner.OnDocumentsAttached(docCount)
	}
}

// newBridgeMetricsHook creates a bridgeMetricsHook that delegates to the graph's
// metrics hook. Returns nil if graphHook is nil (zero overhead when no graph metrics
// hook is configured).
// The inner parameter is the agent's own MetricsHook (may be nil).
func newBridgeMetricsHook(graphHook GraphMetricsHook, nodeName string, inner agent.MetricsHook) *bridgeMetricsHook {
	if graphHook == nil {
		return nil
	}
	return &bridgeMetricsHook{
		graphHook: graphHook,
		nodeName:  nodeName,
		inner:     inner,
	}
}
