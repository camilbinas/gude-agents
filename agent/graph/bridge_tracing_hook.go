package graph

import (
	"context"
	"encoding/json"

	"github.com/camilbinas/gude-agents/agent"
)

// bridgeTracingHook implements agent.TracingHook and creates child spans under
// the graph's node span context. It bridges the graph-level tracing to the agent-level
// tracing system, enriching each span with the originating node name.
//
// When inner is non-nil, both the inner hook and the graph hook are called (composition).
// When graphHook is nil, the bridge should not be created at all (zero overhead).
type bridgeTracingHook struct {
	graphHook GraphTracingHook
	nodeName  string
	inner     agent.TracingHook // agent's own hook, may be nil
}

// OnInvokeStart creates a child span for the agent invocation under the graph's node span.
func (b *bridgeTracingHook) OnInvokeStart(ctx context.Context, params agent.InvokeSpanParams) (context.Context, func(err error, usage agent.TokenUsage, response string)) {
	// Create a child span under the graph node context using OnNodeStart.
	ctx, finishNode := b.graphHook.OnNodeStart(ctx, b.nodeName+"/invoke")

	var innerFinish func(err error, usage agent.TokenUsage, response string)
	if b.inner != nil {
		ctx, innerFinish = b.inner.OnInvokeStart(ctx, params)
	}

	return ctx, func(err error, usage agent.TokenUsage, response string) {
		if innerFinish != nil {
			innerFinish(err, usage, response)
		}
		if finishNode != nil {
			finishNode(err)
		}
	}
}

// OnIterationStart creates a child span for the agent iteration.
func (b *bridgeTracingHook) OnIterationStart(ctx context.Context, iteration int) (context.Context, func(toolCount int, isFinal bool)) {
	var innerFinish func(toolCount int, isFinal bool)
	if b.inner != nil {
		ctx, innerFinish = b.inner.OnIterationStart(ctx, iteration)
	}

	return ctx, func(toolCount int, isFinal bool) {
		if innerFinish != nil {
			innerFinish(toolCount, isFinal)
		}
	}
}

// OnProviderCallStart creates a child span for the provider call.
func (b *bridgeTracingHook) OnProviderCallStart(ctx context.Context, params agent.ProviderCallParams) (context.Context, func(err error, usage agent.TokenUsage, toolCallCount int, responseText string)) {
	var innerFinish func(err error, usage agent.TokenUsage, toolCallCount int, responseText string)
	if b.inner != nil {
		ctx, innerFinish = b.inner.OnProviderCallStart(ctx, params)
	}

	return ctx, func(err error, usage agent.TokenUsage, toolCallCount int, responseText string) {
		if innerFinish != nil {
			innerFinish(err, usage, toolCallCount, responseText)
		}
	}
}

// OnToolStart creates a child span for the tool execution.
func (b *bridgeTracingHook) OnToolStart(ctx context.Context, toolName string, input json.RawMessage) (context.Context, func(err error, output string)) {
	var innerFinish func(err error, output string)
	if b.inner != nil {
		ctx, innerFinish = b.inner.OnToolStart(ctx, toolName, input)
	}

	return ctx, func(err error, output string) {
		if innerFinish != nil {
			innerFinish(err, output)
		}
	}
}

// OnGuardrailStart creates a child span for the guardrail execution.
func (b *bridgeTracingHook) OnGuardrailStart(ctx context.Context, direction string, input string) (context.Context, func(err error, output string)) {
	var innerFinish func(err error, output string)
	if b.inner != nil {
		ctx, innerFinish = b.inner.OnGuardrailStart(ctx, direction, input)
	}

	return ctx, func(err error, output string) {
		if innerFinish != nil {
			innerFinish(err, output)
		}
	}
}

// OnConversationStart creates a child span for the conversation operation.
func (b *bridgeTracingHook) OnConversationStart(ctx context.Context, operation string, conversationID string) (context.Context, func(err error)) {
	var innerFinish func(err error)
	if b.inner != nil {
		ctx, innerFinish = b.inner.OnConversationStart(ctx, operation, conversationID)
	}

	return ctx, func(err error) {
		if innerFinish != nil {
			innerFinish(err)
		}
	}
}

// OnRetrieverStart creates a child span for the retriever operation.
func (b *bridgeTracingHook) OnRetrieverStart(ctx context.Context, query string) (context.Context, func(err error, docCount int)) {
	var innerFinish func(err error, docCount int)
	if b.inner != nil {
		ctx, innerFinish = b.inner.OnRetrieverStart(ctx, query)
	}

	return ctx, func(err error, docCount int) {
		if innerFinish != nil {
			innerFinish(err, docCount)
		}
	}
}

// OnMaxIterationsExceeded records the max-iterations-exceeded event.
func (b *bridgeTracingHook) OnMaxIterationsExceeded(ctx context.Context, limit int) {
	if b.inner != nil {
		b.inner.OnMaxIterationsExceeded(ctx, limit)
	}
}

// newBridgeTracingHook creates a bridgeTracingHook that creates child spans under the
// graph's node span context. Returns nil if graphHook is nil (zero overhead when no
// graph tracing hook is configured).
// The inner parameter is the agent's own TracingHook (may be nil).
func newBridgeTracingHook(graphHook GraphTracingHook, nodeName string, inner agent.TracingHook) *bridgeTracingHook {
	if graphHook == nil {
		return nil
	}
	return &bridgeTracingHook{
		graphHook: graphHook,
		nodeName:  nodeName,
		inner:     inner,
	}
}
